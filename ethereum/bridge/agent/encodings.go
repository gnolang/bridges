package agent

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/gnolang/bridges/solana/bridge/gnolight"
)

// The client's encodings held to gno.land's (the assumptions audit's E3-AS3). GnoLightClient compiles tm2 v1.2.0's
// encodings for its life — the header's sixteen leaves and their hash (TmHeader.sol), the precommit's sign bytes
// (TmVote.sol) under Go's ed25519 rules (Ed25519.sol), the validator leaf (TmValset.sol), the results leaf and the
// /tm.Event inside it (TmResults.sol) — and gnoland-1 upgrades by halting the chain (v1.2.0 → v1.5.1 in its first
// month). An upgrade that changed one would leave the client refusing every header, or every proof: gno.land → Ethereum
// stops, and HlAdmin, whose only way in is the client, never hears GovDAO again. Nothing changed them so far; nothing
// watched for it. So the monitor (and hlwatch) rebuilds, as the client does, what it reads from its own node — every
// header it reads hashed from its fields against the node's block hash, and once a minute the block below the head
// whole: its header and the next one, its validator set against the header's ValidatorsHash, every precommit of its
// commit verified over the sign bytes the client builds, its results leaves against the next header's
// LastResultsHash — and alarms gno:encoding on a difference: a gno upgrade changed what the client verifies, and a kind
// 8 to a client that accepts the new encoding must be executed before the upgrade's height (≥ 7 days' notice). It
// records the node's version (/status node_info.version and its version set) and alarms gno:version for a day when it
// changes.

// encodingEvery is how often the block below the head is rebuilt whole (each header read is held every poll).
const encodingEvery = time.Minute

// versionAlarmFor is how long gno:version stands after the node's version changed.
const versionAlarmFor = 24 * time.Hour

// encodingNode is the own node as the client's feeders read it (gno.Client).
type encodingNode interface {
	keyReader
	CommitAt(ctx context.Context, h int64) (*gnolight.Commit, []*gnolight.Vote, error)
	BlockResults(ctx context.Context, h int64) ([]*gnolight.TxResult, error)
}

// statusRequester reads a node's /status whole (gno.Client.Request).
type statusRequester interface {
	Request(ctx context.Context, method string, params map[string]any, result any) error
}

// noteHeader holds a header read from the own node to the client's encoding: its sixteen fields hashed as the client
// hashes them against the node's own hash of the block.
func (m *Monitor) noteHeader(blk *gnolight.Block) {
	if blk == nil || blk.Header == nil {
		return
	}
	if got := blk.Header.Hash(); got != blk.Hash {
		m.encodingFindings = append(m.encodingFindings, fmt.Sprintf("header %d's fields hash to %s as the client hashes them, and the node's hash of the block is %s (the header's encoding)", blk.Header.Height, short(hex.EncodeToString(got[:])), short(hex.EncodeToString(blk.Hash[:]))))
	}
}

// checkEncodings rebuilds the block below the head as the client would, once every encodingEvery, records the node's
// version, and raises or clears gno:encoding with this poll's findings (the headers noted by checkKeys among them). A
// read that fails is said (blind: the page is not green over a probe that did not run, E4-GT5) and the probe is made
// again at the next poll; gno:encoding is cleared only by a probe that judged every part.
func (m *Monitor) checkEncodings(ctx context.Context, H int64) {
	probed := false
	defer func() {
		switch {
		case len(m.encodingFindings) > 0:
			m.Alarm.Alarm("gno:encoding", "gno.land's encodings differ from the client's — a gno upgrade changed what the client verifies, and it will refuse gno.land's headers or proofs until a kind 8 to a client that accepts them is executed (§6.5; E3-AS3): "+strings.Join(m.encodingFindings, "; "))
		case probed:
			m.Alarm.Clear("gno:encoding")
		}
		m.encodingFindings = nil
	}()
	en, ok := m.own.(encodingNode)
	if !ok || H < 2 || m.now().Before(m.encodingNext) {
		return
	}
	m.noteVersion(ctx)
	h := H - 1
	skip := func(what string, err error) {
		m.blind(fmt.Sprintf("the encodings probe (E3-AS3): %s could not be read from this node (%v): gno.land's encodings are not held to the client's this poll", what, err))
	}
	bh, err := en.BlockAt(ctx, h)
	if err != nil {
		skip(fmt.Sprintf("block %d", h), err)
		return
	}
	bH, err := en.BlockAt(ctx, H)
	if err != nil {
		skip(fmt.Sprintf("block %d", H), err)
		return
	}
	m.noteHeader(bh)
	m.noteHeader(bH)
	set, err := en.ValidatorsAt(ctx, h)
	if err != nil {
		skip(fmt.Sprintf("the validator set at %d", h), err)
		return
	}
	if sh, _ := set.Hash(); sh != bh.Header.ValidatorsHashArray() {
		m.encodingFindings = append(m.encodingFindings, fmt.Sprintf("the validator set at %d hashes to %s by the client's validator leaf, and header %d's ValidatorsHash is %s (the validator leaf's encoding)", h, short(hex.EncodeToString(sh[:])), h, short(hex.EncodeToString(bh.Header.ValidatorsHash))))
	}
	commit, _, err := en.CommitAt(ctx, h)
	if err != nil {
		skip(fmt.Sprintf("the commit of %d", h), err)
		return
	}
	if !bytes.Equal(commit.BlockID.Hash, bh.Hash[:]) {
		m.encodingFindings = append(m.encodingFindings, fmt.Sprintf("the commit for height %d names block %s, the node's block there is %s", h, short(hex.EncodeToString(commit.BlockID.Hash)), short(hex.EncodeToString(bh.Hash[:]))))
	}
	present, bad, verified := 0, 0, map[int]bool{}
	for _, i := range commit.Present() {
		present++
		if i >= len(set.Validators) || i >= len(commit.Signatures) {
			bad++
			continue
		}
		pk := set.Validators[i].PubKey
		if ed25519.Verify(pk[:], commit.SignBytes(m.Cfg.Gno.ChainID, h, i), commit.Signatures[i]) {
			verified[i] = true
		} else {
			bad++
		}
	}
	switch {
	case bad > 0:
		m.encodingFindings = append(m.encodingFindings, fmt.Sprintf("%d of the %d precommits of height %d do not verify over the sign bytes the client builds (the vote's encoding, or ed25519's rules)", bad, present, h))
	case !gnolight.CommitRule(set.Tallied(func(i int) bool { return verified[i] }), set.TotalPower()):
		m.encodingFindings = append(m.encodingFindings, fmt.Sprintf("the precommits of height %d that verify over the client's sign bytes do not hold more than two thirds of the set's power", h))
	}
	// the results, against the next header's LastResultsHash: the block below the head, or — where that block carries a
	// string its JSON cannot carry — the nearest block below it whose results the JSON does carry
	next := bH
	for x := h; x > max(h-lossyProbes, 0); x-- {
		if x < h {
			b, err := en.BlockAt(ctx, x)
			if err != nil {
				skip(fmt.Sprintf("block %d", x), err)
				return
			}
			next, bh = bh, b
		}
		rs, err := en.BlockResults(ctx, x)
		if err != nil {
			skip(fmt.Sprintf("the results of block %d", x), err)
			return
		}
		switch m.judgeResults(x, rs, next) {
		case resultsLossy:
			continue
		case resultsUnread:
			m.blind(fmt.Sprintf("the encodings probe (E3-AS3): block %d's results do not read from the node's JSON: the results encoding is not judged this poll", x))
			m.encodingNext = m.now().Add(encodingEvery)
			return
		}
		if x == h {
			m.Alarm.Clear("results:lossy") // the block below the head reads whole again
		}
		probed = true
		m.encodingNext = m.now().Add(encodingEvery)
		return
	}
	// every block probed carries a string the JSON cannot carry: the results encoding is not judged (and a standing
	// gno:encoding is not cleared); the relayers cannot prove those blocks from this JSON either (§9, results:mismatch)
	m.blind(fmt.Sprintf("the encodings probe (E3-AS3): the results of blocks %d–%d all carry strings the node's JSON cannot carry (U+FFFD where the chain hashed other bytes: §9's non-UTF-8 door, results:lossy), so none could be judged against its header", max(h-lossyProbes, 0)+1, h))
	m.encodingNext = m.now().Add(encodingEvery)
}

// lossyProbes is how many blocks the probe walks down from the block below the head for one whose results the node's
// JSON carries whole.
const lossyProbes = 5

// resultsVerdict is what one block's results said against the next header's LastResultsHash.
type resultsVerdict int

const (
	resultsJudged resultsVerdict = iota // judged: equal, or a finding (an encoding change) noted
	resultsLossy                        // a mismatch in a block whose JSON carries U+FFFD: unprovable, no encoding change
	resultsUnread                       // a leaf the JSON does not give (bad JSON): nothing judged
)

// judgeResults holds one block's results leaves, rebuilt from the node's JSON as the client would, against the next
// header's LastResultsHash (E4-AS1, E4-CF1, E4-GT4). A leaf of a type outside the client's closed set (an error or an
// event type: no stranger's transaction can make one — every error path of gno lands in the set, and a realm emits only
// chain events — so a gno upgrade added it) is a finding; a root that differs is a finding only in a block whose JSON
// carries no U+FFFD — a string that is not UTF-8 in anyone's event or panic message reaches the JSON as U+FFFD while
// the chain hashed its bytes (§9: the block is unprovable from this JSON, the relayers' results:mismatch), which anyone
// can buy for a transaction's fee: results:lossy then (an alarm, never a finding), and the probe takes the block below.
func (m *Monitor) judgeResults(h int64, rs []*gnolight.TxResult, next *gnolight.Block) resultsVerdict {
	lrh, has := next.Header.LastResultsHashArray()
	leaves, err := gnolight.ResultLeaves(rs)
	var se *gnolight.SchemaError
	switch {
	case errors.As(err, &se):
		m.encodingFindings = append(m.encodingFindings, fmt.Sprintf("a results leaf of block %d carries a type outside the client's closed set (%v): no message dispatched in such a block can be proven (§13) — a gno upgrade added it", h, err))
		return resultsJudged
	case err != nil:
		return resultsUnread
	case len(leaves) == 0 && has:
		m.encodingFindings = append(m.encodingFindings, fmt.Sprintf("block %d has no results here, and header %d's LastResultsHash commits to some", h, next.Header.Height))
		return resultsJudged
	case len(leaves) == 0:
		return resultsJudged
	}
	root, _ := gnolight.SimpleHash(leaves)
	if has && root == lrh {
		return resultsJudged
	}
	if lossyResults(rs) {
		m.Alarm.Alarm("results:lossy", fmt.Sprintf("block %d's results carry a string the node's JSON cannot carry (U+FFFD where the chain hashed other bytes: anyone's non-UTF-8 event or panic message, §9): rebuilt, they hash to %s, header %d's LastResultsHash is %s — the block is unprovable from this JSON (the relayers' results:mismatch), and the encodings probe takes a block below it; no encoding change", h, short(hex.EncodeToString(root[:])), next.Header.Height, short(hex.EncodeToString(next.Header.LastResultsHash))))
		return resultsLossy
	}
	m.encodingFindings = append(m.encodingFindings, fmt.Sprintf("block %d's %d results leaves hash to %s as the client rebuilds them, and header %d's LastResultsHash is %s (the results leaf's or the event's encoding: the block's JSON carries no string it could not carry)", h, len(leaves), short(hex.EncodeToString(root[:])), next.Header.Height, short(hex.EncodeToString(next.Header.LastResultsHash))))
	return resultsJudged
}

// lossyResults says whether a block's results, as the node's JSON gave them, carry U+FFFD — the mark a string that is
// not UTF-8 leaves in it (Go's encoding/json writes each invalid byte so), and the only loss the JSON makes.
func lossyResults(rs []*gnolight.TxResult) bool {
	for _, r := range rs {
		if bytes.Contains(r.Leaf, []byte("\uFFFD")) {
			return true
		}
	}
	return false
}

// noteVersion records the own node's version (/status: node_info.version and its version set) and alarms gno:version
// when it changes — once, standing for a day: the encodings are rebuilt every minute, and gno:encoding says whether the
// change touched one.
func (m *Monitor) noteVersion(ctx context.Context) {
	sr, ok := m.own.(statusRequester)
	if !ok {
		return
	}
	var st struct {
		NodeInfo struct {
			Version    string `json:"version"`
			VersionSet []struct {
				Name    string `json:"Name"`
				Version string `json:"Version"`
			} `json:"version_set"`
		} `json:"node_info"`
	}
	if err := sr.Request(ctx, "status", map[string]any{}, &st); err != nil || st.NodeInfo.Version == "" {
		return
	}
	parts := []string{}
	for _, x := range st.NodeInfo.VersionSet {
		parts = append(parts, x.Name+"@"+x.Version)
	}
	sort.Strings(parts)
	v := st.NodeInfo.Version
	if len(parts) > 0 {
		v += " (" + strings.Join(parts, ", ") + ")"
	}
	now := m.now()
	switch was := m.state.NodeVersion; {
	case was == "":
		m.state.NodeVersion = v
	case was != v:
		m.state.NodeVersion, m.state.VersionChanged = v, now.Unix()
		m.Alarm.Alarm("gno:version", fmt.Sprintf("the own node's version changed from %q to %q (seen %s): a gno.land upgrade — the client's encodings are rebuilt against it every minute, and gno:encoding stands if one of them changed (E3-AS3)", was, v, now.UTC().Format(time.RFC3339)))
	case m.state.VersionChanged != 0 && now.Sub(time.Unix(m.state.VersionChanged, 0)) > versionAlarmFor:
		m.Alarm.Clear("gno:version")
	}
}
