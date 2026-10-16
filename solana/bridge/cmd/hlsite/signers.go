package main

import (
	"context"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// The signers' lamps (M7): each signer machine serves the signatures it made at a public endpoint, GET
// <url>/v1/<lane>/<nonce> — "sol" for the Solana deployment's, "eth" for the Ethereum one's (E-M4) — what the
// relayers read to deliver a burn (PLAN.md §5.6). The site asks each for the
// newest burn: a signature served is the signer vouching for it, a 404 is the signer up and not (yet) vouching, no
// answer is the signer down. The answers are read by the backend, never by the page, and kept fifteen seconds.

// signerProber asks one signer's endpoint for one burn's signature: the HTTP status, or an error when nothing
// answered.
type signerProber interface {
	Probe(ctx context.Context, base, lane string, nonce uint64) (int, error)
}

type httpProber struct{ c *http.Client }

func (p httpProber) Probe(ctx context.Context, base, lane string, nonce uint64) (int, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", strings.TrimRight(base, "/")+"/v1/"+lane+"/"+strconv.FormatUint(nonce, 10), nil)
	if err != nil {
		return 0, err
	}
	resp, err := p.c.Do(req)
	if err != nil {
		return 0, err
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	resp.Body.Close()
	return resp.StatusCode, nil
}

// signerLamp is one signer as the page draws it.
type signerLamp struct {
	N     int    `json:"n"`     // its place in the config, from 1
	State string `json:"state"` // "vouching" (it serves the burn's signature), "up" (it answers, without one), "down"
	Nonce int64  `json:"nonce"` // the burn asked about, -1 when there was none to ask about
}

// lampState reads one answer: a signature is vouching, any other answer from the machine is up (a 404 is "not
// signed"), a failure to answer or a gateway's 5xx is down.
func lampState(code int, err error) string {
	switch {
	case err != nil || code == 0 || code >= 500:
		return "down"
	case code == 200:
		return "vouching"
	}
	return "up"
}

const lampsTTL = 15 * time.Second

type lampCache struct {
	mu sync.Mutex
	m  map[string]lampEntry // chain/nonce → the lamps
}

type lampEntry struct {
	at    time.Time
	lamps []signerLamp
}

// lamps are a chain's signers asked about one burn (nonce −1: none yet, so nonce 0 is asked and a signature is not
// expected), all at once, each within four seconds.
func (s *server) lamps(ctx context.Context, ch *ChainConfig, nonce int64) []signerLamp {
	if len(ch.Signers) == 0 {
		return []signerLamp{}
	}
	key := ch.ID + "/" + strconv.FormatInt(nonce, 10)
	s.lampc.mu.Lock()
	if e, ok := s.lampc.m[key]; ok && s.now().Sub(e.at) < s.lampsTTL {
		s.lampc.mu.Unlock()
		return e.lamps
	}
	s.lampc.mu.Unlock()
	ask := uint64(0)
	if nonce > 0 {
		ask = uint64(nonce)
	}
	out := make([]signerLamp, len(ch.Signers))
	var wg sync.WaitGroup
	for i, url := range ch.Signers {
		wg.Add(1)
		go func(i int, url string) {
			defer wg.Done()
			c, cancel := context.WithTimeout(ctx, 4*time.Second)
			defer cancel()
			code, err := s.prober.Probe(c, url, kindWords(ch).lane, ask)
			st := lampState(code, err)
			if nonce < 0 && st == "vouching" {
				st = "up" // nothing burned yet: a signature for nonce 0 cannot be one (a stale store), and up is all it says
			}
			out[i] = signerLamp{N: i + 1, State: st, Nonce: nonce}
		}(i, url)
	}
	wg.Wait()
	s.lampc.mu.Lock()
	if len(s.lampc.m) > 200 {
		s.lampc.m = map[string]lampEntry{}
	}
	s.lampc.m[key] = lampEntry{at: s.now(), lamps: out}
	s.lampc.mu.Unlock()
	return out
}

// lampsNow are the lamps the activity carries, at once: the last answers about this burn, stale ones asked again in
// the background; a burn not asked about yet is asked now (four seconds at most).
func (s *server) lampsNow(ctx context.Context, ch *ChainConfig, nonce int64) []signerLamp {
	key := ch.ID + "/" + strconv.FormatInt(nonce, 10)
	s.lampc.mu.Lock()
	e, ok := s.lampc.m[key]
	s.lampc.mu.Unlock()
	if !ok || s.syncRefresh || len(ch.Signers) == 0 {
		return s.lamps(ctx, ch, nonce)
	}
	if s.now().Sub(e.at) >= s.lampsTTL {
		s.bg.run("lamps/"+key, func() {
			bctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			s.lamps(bctx, ch, nonce)
		})
	}
	return e.lamps
}

// vouched are the signers (from 1) whose lamps say they serve the burn's signature.
func vouched(lamps []signerLamp) []int {
	out := []int{}
	for _, l := range lamps {
		if l.State == "vouching" {
			out = append(out, l.N)
		}
	}
	return out
}
