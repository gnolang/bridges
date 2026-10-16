package agent

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
)

// Two steps of a deploy of the machines, for the operator (bridge/README.md, "Deploying the machines").

// DropDust removes from a relayer's state the burns it keeps pending as dust left under their realm's fee (E5-GS5) —
// the rollback step of the round-6 live-fixes audit's E6-SF5: a relayer from before 1aea555f has no fee rule, so started
// over a state the new one wrote it would Process every dust burn pending (up to a thousand, ≈0.83 GNOT of the relayers'
// gno key each: a sweep entry's storage deposit and its gas). Run it, with this build, on each relayer's state file
// before the old binary starts. It answers how many it dropped; the file is written only when it drops one.
func DropDust(path string) (int, error) {
	if _, err := os.Stat(path); err != nil {
		return 0, err // a path that names no state file is no state of nothing to drop (the review of lane s7)
	}
	r := &Relayer{State: path}
	if err := r.load(); err != nil {
		return 0, err
	}
	if len(r.state.Left) == 0 {
		return 0, nil
	}
	var keep []solDispatch
	dropped := 0
	for _, d := range r.state.Pending {
		id := d.IDHex()
		if _, left := r.state.Left[id]; !left {
			keep = append(keep, d)
			continue
		}
		dropped++
		key := "process:" + id
		delete(r.state.Ready, key)
		delete(r.state.Attempts, key)
		delete(r.state.Sigs, id)
	}
	r.state.Pending = keep
	r.state.Left = nil
	if dropped == 0 {
		return 0, nil
	}
	return dropped, r.save()
}

// ZeroBurn is a burn of nothing a signer serves a signature for: its nonce, its id, and the signers that signed it.
type ZeroBurn struct {
	Nonce   uint32
	ID      string
	Signers []string
}

// ZeroBurns walks the signers' public signatures (GET <url>/v1/sol/<nonce> of each of cfg.Signers) from nonce from on,
// until gap nonces in a row that no signer serves, and answers the burns of nothing among them and how many nonces it
// read — the deploy step of the round-6 live-fixes audit's E6-SF3: the signers before a63337f0 signed a burn of nothing,
// and a new signer never indexes one (E5-SF1), so each such credit brought after the deploy is a window of no verdict on
// every new signer (and, at a route's fee of 0, an open claim of 0 no index holds). Run it just before the new signers
// start: none found, nothing of the kind can be brought later (a burn of nothing is never signed again).
func ZeroBurns(ctx context.Context, cfg *Config, client *http.Client, from uint32, gap int) ([]ZeroBurn, uint32, error) {
	if len(cfg.Signers) == 0 {
		return nil, 0, errors.New("the config names no signers (signers: url and key)")
	}
	if client == nil {
		client = http.DefaultClient
	}
	var out []ZeroBurn
	empty := 0
	n := from
	for ; empty < gap; n++ {
		byID := map[string]*ZeroBurn{}
		served := false
		for _, ref := range cfg.Signers {
			s, err := servedSignature(ctx, client, cfg, ref.URL, n)
			if err != nil {
				return out, n - from, fmt.Errorf("nonce %d from %s: %w", n, ref.URL, err)
			}
			if s == nil {
				continue
			}
			served = true
			raw, err := hex.DecodeString(s.Message)
			if err != nil {
				return out, n - from, fmt.Errorf("nonce %d from %s: its message is not hex: %w", n, ref.URL, err)
			}
			d := solDispatch{Raw: raw}
			if err := d.fill(); err != nil {
				return out, n - from, fmt.Errorf("nonce %d from %s: its message does not parse: %w", n, ref.URL, err)
			}
			if _, amount, _, ok := d.token(); ok && amount == 0 {
				z := byID[d.IDHex()]
				if z == nil {
					z = &ZeroBurn{Nonce: n, ID: d.IDHex()}
					byID[d.IDHex()] = z
				}
				z.Signers = append(z.Signers, ref.Key)
			}
		}
		if !served {
			empty++
			continue
		}
		empty = 0
		for _, id := range sortedKeys(byID) {
			out = append(out, *byID[id])
		}
	}
	return out, n - from, nil
}

// servedSignature is a signer's signature for a nonce as its public endpoint serves it, nil for none (404).
func servedSignature(ctx context.Context, client *http.Client, cfg *Config, base string, nonce uint32) (*Signed, error) {
	if err := checkSignerURL(base, cfg.AllowHTTP); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, "GET", strings.TrimRight(base, "/")+"/v1/sol/"+strconv.FormatUint(uint64(nonce), 10), nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		return nil, nil
	default:
		return nil, fmt.Errorf("%s", resp.Status)
	}
	var s Signed
	if err := json.NewDecoder(io.LimitReader(resp.Body, signerBodyLimit)).Decode(&s); err != nil {
		return nil, err
	}
	if s.Nonce != nonce {
		return nil, fmt.Errorf("asked nonce %d, served nonce %d", nonce, s.Nonce)
	}
	return &s, nil
}

// String is a burn of nothing as a line for the operator.
func (z ZeroBurn) String() string {
	return fmt.Sprintf("nonce %d (id %s): a burn of nothing signed by %d signer(s) %v", z.Nonce, z.ID, len(z.Signers), z.Signers)
}
