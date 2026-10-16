package agent

import (
	"context"
	"testing"

	"github.com/gnolang/bridges/solana/bridge/gnolight"
	"github.com/gnolang/bridges/solana/bridge/hl"
	"github.com/gnolang/bridges/solana/bridge/sol"
)

// A decision goes only when its number is the next the admin program expects (§5.7).
func TestDecisionGate(t *testing.T) {
	cases := []struct {
		seq, last uint64
		want      string
	}{
		{1, 0, "deliver"}, {2, 0, "wait"}, {1, 1, "past"}, {5, 4, "deliver"}, {3, 7, "past"}, {9, 7, "wait"},
	}
	for _, c := range cases {
		if got := decisionGate(c.seq, c.last); got != c.want {
			t.Errorf("seq %d last %d: %s, want %s", c.seq, c.last, got, c.want)
		}
	}
}

// The leaf of a block's results that carries a message's Dispatch is found by its id, among other transactions
// and other dispatches.
func TestFindLeaf(t *testing.T) {
	path := "gno.land/r/test/hlmailbox"
	mk := func(nonce uint32) ([]byte, [32]byte) {
		m := hl.Message{Version: 3, Nonce: nonce, Origin: hl.DomainGnoTest, Destination: hl.DomainSolanaTestnet, Body: []byte("x")}
		raw := m.Bytes()
		id := hl.ID(raw)
		d := gnolight.Dispatch{ID: id, Nonce: nonce, Origin: m.Origin, Destination: m.Destination, Sender: m.Sender, Recipient: m.Recipient, Hash: gnolight.MessageHash(raw)}
		return gnolight.EncodeResult(nil, nil, [][]byte{gnolight.Any(gnolight.EventTypeURL, gnolight.EncodeEvent(d.ToEvent(path)))}), id
	}
	plain := gnolight.EncodeResult(nil, []byte("data"), nil)
	l1, id1 := mk(1)
	l2, id2 := mk(2)
	leaves := [][]byte{plain, l1, plain, l2}
	if findLeaf(leaves, id1, path) != 1 || findLeaf(leaves, id2, path) != 3 {
		t.Fatal("not found at their positions")
	}
	if findLeaf(leaves, [32]byte{9}, path) != -1 || findLeaf(leaves, id1, "gno.land/r/other") != -1 {
		t.Fatal("found where there is none")
	}
	// anyone may Reemit a message: a stranger's Reemit of nonce 2 beside junk events, ahead of the relayer's own in
	// the block, is a bigger leaf carrying the same Dispatch; the proof stages the smaller (E3-RT2)
	_, d2, _ := dispatchOf(2, path)
	junk := gnolight.Any(gnolight.EventTypeURL, gnolight.EncodeEvent(&gnolight.Event{Type: "Junk", Attributes: []gnolight.Attribute{{Key: "k", Value: string(make([]byte, 9000))}}, PkgPath: "gno.land/r/stranger"}))
	bigger := gnolight.EncodeResult(nil, nil, [][]byte{junk, gnolight.Any(gnolight.EventTypeURL, gnolight.EncodeEvent(d2.ToEvent(path)))})
	if got := findLeaf([][]byte{plain, bigger, l1, l2}, id2, path); got != 3 {
		t.Fatalf("leaf %d (%d bytes), not the smaller leaf 3", got, len(bigger))
	}
	if got := findLeaf([][]byte{l2, bigger, l2}, id2, path); got != 0 {
		t.Fatalf("leaf %d: equals go to the lowest index", got)
	}
}

// dispatchOf is a message of the nonce, its id and its Dispatch.
func dispatchOf(nonce uint32, path string) ([]byte, gnolight.Dispatch, [32]byte) {
	m := hl.Message{Version: 3, Nonce: nonce, Origin: hl.DomainGnoTest, Destination: hl.DomainSolanaTestnet, Body: []byte("x")}
	raw := m.Bytes()
	id := hl.ID(raw)
	return raw, gnolight.Dispatch{ID: id, Nonce: nonce, Origin: m.Origin, Destination: m.Destination, Sender: m.Sender, Recipient: m.Recipient, Hash: gnolight.MessageHash(raw)}, id
}

// The relayer classifies hlmailbox's messages: a route's token message from the route's own realm, r/hlgov's
// decision to the admin program, and nothing else.
func TestClassify(t *testing.T) {
	f := newFake()
	r := testRelayer(f, "primary")
	r.Cfg.Gno.Gov = "gno.land/r/test/hlgov"
	r.Cfg.Solana.Admin = "4RGdtbf8oxCqysLCmVDRKLLZTrTmjPWiLY1xUTiq9HXU"
	token := [32]byte{9}
	r.Cfg.Routes[0].Token = hl.Base58(token[:])
	route, _ := hl.GnoAddressToBytes32(hl.GnoPackageAddress(testRoute))
	gov, _ := r.Cfg.GovSender()
	admin := hl.MustKey32(r.Cfg.Solana.Admin)
	msg := func(sender, rcpt [32]byte, dest uint32) *gnoMsg {
		return &gnoMsg{Msg: hl.Message{Version: 3, Origin: hl.DomainGnoTest, Sender: sender, Destination: dest, Recipient: rcpt, Body: []byte{7, 0, 0, 0, 0, 0, 0, 0, 1}}}
	}
	// the routes' lines are read once a step before anything is classified (K34; G46: unread, a send from anyone
	// but the configured realm is held)
	f.renders[testRoute+":json"] = routeRender(0, 50, 0, "")
	r.resetStep()
	r.readLines(context.Background())
	if k, rt := r.classify(msg(route, token, hl.DomainSolanaTestnet)); k != kindRoute || rt == nil || rt.Symbol != "BUBBLE" {
		t.Fatal("a route message")
	}
	if k, _ := r.classify(msg([32]byte{1}, token, hl.DomainSolanaTestnet)); k != kindOther {
		t.Fatal("a token message from a stranger is not ours")
	}
	if k, _ := r.classify(msg(gov, admin, hl.DomainSolanaTestnet)); k != kindDecision {
		t.Fatal("a decision")
	}
	if k, _ := r.classify(msg([32]byte{1}, admin, hl.DomainSolanaTestnet)); k != kindOther {
		t.Fatal("a message to the admin program from a stranger is not ours")
	}
	if k, _ := r.classify(msg(route, token, 7)); k != kindOther {
		t.Fatal("another destination")
	}
	_ = sol.MaxLeaf
}
