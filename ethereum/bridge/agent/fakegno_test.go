package agent

import (
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gnolang/bridges/solana/bridge/gno"
	"github.com/gnolang/bridges/solana/bridge/gnolight"
	"github.com/gnolang/bridges/solana/bridge/hl"
	"github.com/gnolang/bridges/ethereum/bridge/gnoside"
)

// fakeGno is a gno.land chain in memory, consistent the way the light client needs it: every header's
// ValidatorsHash and NextValidatorsHash are its sets', its LastResultsHash the block before's results, its commit
// signed by its set's keys over tm2's sign bytes; the realms' renders are canned, calls logged, MsgRuns become
// blocks of their own.
type fakeGno struct {
	mu       sync.Mutex
	chainID  string
	head     int64
	genesis  time.Time
	sets     map[int64]*fakeSet // a set signs from its height on
	results  map[int64][]*gnolight.TxResult
	renders  map[string]string
	calls    []string
	callFail map[string]error
	runs     []string
	runFail  error
	runOpen  bool // MsgRun taken from any key (gnoland-1's run_submitters refuses it: E3-AS1)
	mailbox  string
	domain   uint32
	count    uint32
	msgs     map[uint32]string
	headers  map[int64]*gnolight.Block
}

type fakeSet struct {
	set  *gnolight.ValidatorSet
	keys map[[32]byte]ed25519.PrivateKey
}

func newFakeSet(seed byte, powers ...int64) *fakeSet {
	fs := &fakeSet{set: &gnolight.ValidatorSet{}, keys: map[[32]byte]ed25519.PrivateKey{}}
	for i, p := range powers {
		s := make([]byte, 32)
		s[0], s[1] = seed, byte(i)
		k := ed25519.NewKeyFromSeed(s)
		var pk [32]byte
		copy(pk[:], k.Public().(ed25519.PublicKey))
		fs.set.Validators = append(fs.set.Validators, gnolight.Validator{PubKey: pk, Power: p})
		fs.keys[pk] = k
	}
	fs.set.Sort()
	return fs
}

func newFakeGno(mailbox string, head int64) *fakeGno {
	f := &fakeGno{chainID: "dev", head: head, genesis: time.Unix(1_800_000_000, 0), sets: map[int64]*fakeSet{1: newFakeSet(1, 60, 60, 60, 60, 60, 60)},
		results: map[int64][]*gnolight.TxResult{}, renders: map[string]string{}, callFail: map[string]error{}, mailbox: mailbox, domain: hl.DomainGnoTest,
		msgs: map[uint32]string{}, headers: map[int64]*gnolight.Block{}}
	f.renderMailbox()
	return f
}

func (f *fakeGno) setAt(h int64) *fakeSet {
	var best int64
	for from := range f.sets {
		if from <= h && from > best {
			best = from
		}
	}
	return f.sets[best]
}

func (f *fakeGno) timeAt(h int64) time.Time { return f.genesis.Add(time.Duration(h) * 3 * time.Second) }

// header builds block h's header (cached once the chain is past it).
func (f *fakeGno) header(h int64) *gnolight.Block {
	if b, ok := f.headers[h]; ok {
		return b
	}
	cur, _ := f.setAt(h).set.Hash()
	next, _ := f.setAt(h + 1).set.Hash()
	hdr := &gnolight.Header{Version: "v1", ChainID: f.chainID, Height: h, Time: gnolight.Time{Secs: f.timeAt(h).Unix()}, NumTxs: int64(len(f.results[h])),
		ValidatorsHash: cur[:], NextValidatorsHash: next[:], AppHash: make([]byte, 32), ConsensusHash: make([]byte, 32), ProposerAddress: "g1proposer"}
	if prev := f.results[h-1]; len(prev) > 0 {
		leaves, err := gnolight.ResultLeaves(prev)
		if err == nil {
			root, _ := gnolight.SimpleHash(leaves)
			hdr.LastResultsHash = root[:]
		} else {
			hdr.LastResultsHash = make([]byte, 32) // the chain hashes what this side cannot rebuild
		}
	}
	b := &gnolight.Block{Header: hdr, Hash: hdr.Hash(), NumTxs: hdr.NumTxs}
	if h < f.head {
		f.headers[h] = b
	}
	return b
}

func (f *fakeGno) RenderAt(_ context.Context, pkg, path string, _ int64) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.renders[pkg+":"+path]
	if !ok {
		if strings.HasPrefix(path, "decisions.json/") {
			return "unknown path; see the page", nil // a realm built before the pages (E3-RL4): its whole page is read
		}
		return "", fmt.Errorf("fake: no render %s:%s", pkg, path)
	}
	return s, nil
}

func (f *fakeGno) Status(context.Context) (gno.Head, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return gno.Head{Height: f.head, Time: f.timeAt(f.head)}, nil
}

func (f *fakeGno) Block(_ context.Context, h int64) (gno.Head, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if h > f.head {
		return gno.Head{}, errors.New("fake: no such height")
	}
	b := f.header(h)
	return gno.Head{Height: h, Time: f.timeAt(h), Hash: hex.EncodeToString(b.Hash[:])}, nil
}

func (f *fakeGno) BlockAt(_ context.Context, h int64) (*gnolight.Block, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if h > f.head || h < 1 {
		return nil, errors.New("fake: no such height")
	}
	return f.header(h), nil
}

func (f *fakeGno) CommitAt(_ context.Context, h int64) (*gnolight.Commit, []*gnolight.Vote, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if h > f.head {
		return nil, nil, errors.New("fake: no such height")
	}
	b := f.header(h)
	s := f.setAt(h)
	bid := gnolight.BlockID{Hash: b.Hash[:], Parts: gnolight.PartSetHeader{Total: 1, Hash: make([]byte, 32)}}
	cm := &gnolight.Commit{BlockID: bid, Precommits: make([]*gnolight.Precommit, len(s.set.Validators)), Signatures: make([][]byte, len(s.set.Validators))}
	for i, v := range s.set.Validators {
		p := &gnolight.Precommit{Round: 0, Time: gnolight.Time{Secs: f.timeAt(h).Unix() + 1, Nanos: int32(i)}}
		cm.Precommits[i] = p
		cm.Signatures[i] = ed25519.Sign(s.keys[v.PubKey], gnolight.PrecommitSignBytes(f.chainID, h, 0, bid, p.Time))
	}
	return cm, nil, nil
}

func (f *fakeGno) ValidatorsAt(_ context.Context, h int64) (*gnolight.ValidatorSet, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if h > f.head+1 || h < 1 {
		return nil, errors.New("fake: no such height")
	}
	return f.setAt(h).set, nil
}

func (f *fakeGno) BlockResults(_ context.Context, h int64) ([]*gnolight.TxResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.results[h], nil
}

func (f *fakeGno) NumTxs(_ context.Context, from, to int64) (map[int64]int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := map[int64]int64{}
	for h := from; h <= to; h++ {
		out[h] = int64(len(f.results[h]))
	}
	return out, nil
}

func (f *fakeGno) Call(_ context.Context, pkg, fn string, _ int64, args ...string) (*gno.Result, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, pkg+"."+fn+"("+strings.Join(args, ",")+")")
	if err := f.callFail[pkg+"."+fn]; err != nil {
		return nil, err
	}
	f.head++
	return &gno.Result{Hash: fmt.Sprintf("tx%d", len(f.calls)), Height: f.head}, nil
}

func (f *fakeGno) callsOf(prefix string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, c := range f.calls {
		if strings.HasPrefix(c, prefix) {
			out = append(out, c)
		}
	}
	return out
}

var reemitRE = regexp.MustCompile(`Reemit\(cross, (\d+)\)`)

// Run is a MsgRun, which gnoland-1 refuses from any key but its run_submitters (E3-AS1): this chain refuses it as
// gnoland-1 does unless a test opens it (runOpen); taken, the Reemits it calls become one new block whose one result
// carries their Dispatch events.
func (f *fakeGno) Run(_ context.Context, source string, _ int64) (*gno.Result, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.runs = append(f.runs, source)
	if !f.runOpen {
		return nil, &gno.CallError{Pkg: "run", Fn: "main", Err: errors.New("unauthorized"), Output: "g1relayer is not authorized to send MsgRun; see the vm run_submitters param"}
	}
	var nonces []uint32
	for _, m := range reemitRE.FindAllStringSubmatch(source, -1) {
		n, _ := strconv.Atoi(m[1])
		nonces = append(nonces, uint32(n))
	}
	return f.reemitLocked(nonces)
}

// Reemit is the batch re-emit as gnoland-1 takes it: one transaction of Reemit MsgCalls, which becomes one new block
// whose one result carries their Dispatch events.
func (f *fakeGno) Reemit(_ context.Context, mailbox string, nonces []uint32, _ int64) (*gno.Result, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.runs = append(f.runs, reemitText(nonces))
	if f.runFail != nil {
		return nil, f.runFail
	}
	if mailbox != f.mailbox {
		return nil, fmt.Errorf("fake: Reemit on %s, not the mailbox", mailbox)
	}
	return f.reemitLocked(nonces)
}

func (f *fakeGno) reemitLocked(nonces []uint32) (*gno.Result, error) {
	var raws [][]byte
	for _, n := range nonces {
		raws = append(raws, f.raw(n))
	}
	f.head++
	f.results[f.head] = append(f.results[f.head], dispatchTx(f.mailbox, raws...))
	h := f.head
	f.head++ // the block after it exists, so its header can be proven
	return &gno.Result{Hash: fmt.Sprintf("run%d", len(f.runs)), Height: h}, nil
}

// reemitText writes a batch re-emit as the tests read it (one Reemit(cross, n) per nonce).
func reemitText(nonces []uint32) string {
	var b strings.Builder
	for _, n := range nonces {
		fmt.Fprintf(&b, "Reemit(cross, %d)\n", n)
	}
	return b.String()
}

func (f *fakeGno) raw(n uint32) []byte {
	var j struct {
		Message string `json:"message"`
	}
	_ = gnoside.JSON(f.msgs[n], &j)
	b, _ := hex.DecodeString(j.Message)
	return b
}

// dispatchTx is a transaction's result carrying the Dispatch events of messages (a send's, or a MsgRun's Reemits).
func dispatchTx(mailbox string, raws ...[]byte) *gnolight.TxResult {
	r := &gnolight.TxResult{}
	var anys [][]byte
	for _, raw := range raws {
		m, _ := hl.ParseMessage(raw)
		d := gnolight.Dispatch{ID: hl.ID(raw), Nonce: m.Nonce, Origin: m.Origin, Destination: m.Destination, Sender: m.Sender, Recipient: m.Recipient, Hash: gnolight.MessageHash(raw)}
		e := d.ToEvent(mailbox)
		r.Events = append(r.Events, e)
		anys = append(anys, gnolight.Any(gnolight.EventTypeURL, gnolight.EncodeEvent(e)))
	}
	r.Leaf = gnolight.EncodeResult(nil, []byte("ok"), anys)
	return r
}

// dispatch puts messages into block h (a send's transaction), renders them, and moves the head past h+1.
func (f *fakeGno) dispatch(h int64, ms ...hl.Message) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var raws [][]byte
	for _, m := range ms {
		m.Nonce = f.count
		m.Origin = f.domain
		m.Version = hl.Version
		raw := m.Bytes()
		id := hl.ID(raw)
		f.msgs[f.count] = fmt.Sprintf(`{"nonce":%d,"id":%q,"root":"%s","height":%d,"message":%q}`, f.count, hex.EncodeToString(id[:]), strings.Repeat("00", 32), h, hex.EncodeToString(raw))
		f.renders[f.mailbox+":msg/"+strconv.Itoa(int(f.count))] = f.msgs[f.count]
		raws = append(raws, raw)
		f.count++
	}
	f.results[h] = append(f.results[h], dispatchTx(f.mailbox, raws...))
	if f.head < h+1 {
		f.head = h + 1
	}
	f.renderMailboxLocked()
}

func (f *fakeGno) renderMailbox() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.renderMailboxLocked()
}

func (f *fakeGno) renderMailboxLocked() {
	f.renders[f.mailbox+":json"] = fmt.Sprintf(`{"domain":%d,"count":%d,"live":true,"governor":"","yielded":0,"routes":{},"signers":{"keys":[],"threshold":0},"clocks":{"factor":1,"delay":604800,"deadline":1209600}}`, f.domain, f.count)
}

// idOf is message n's id.
func (f *fakeGno) idOf(n uint32) [32]byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	return hl.ID(f.raw(n))
}
