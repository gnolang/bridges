package agent

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/gnolang/bridges/solana/bridge/hl"
)

// unbuildableClient answers every height's leaf as unbuildable unless told the height reads.
type unbuildableClient struct {
	setFrom   int64
	buildable map[int64]bool
}

func (c unbuildableClient) CurrentSetFrom(context.Context) (int64, error) { return c.setFrom, nil }
func (c unbuildableClient) LeafBytes(_ context.Context, h int64, _ [32]byte) (int, bool, error) {
	if c.buildable[h] {
		return 200, true, nil
	}
	return 0, false, nil
}

// E3-RT2's residual: a block whose results cannot be rebuilt here (a string the node's JSON cannot carry, in anyone's
// event attribute or panic message) asks for a Reemit into a fresh block, and someone who puts one into every block
// made the relayer Reemit — each paid in GNOT — once a minute for good. The Reemits into unbuildable blocks now back
// off per message: a minute after the first, doubling, at most an hour; the third in a row alarms; a block that reads
// ends the run, its backoff and its alarm.
func TestReemitsIntoUnbuildableBlocksBackOff(t *testing.T) {
	f := newFake()
	r := testRelayer(f, "primary")
	ctx := context.Background()
	token := [32]byte{9}
	r.Cfg.Routes[0].Token = hl.Base58(token[:])
	route, _ := hl.GnoAddressToBytes32(hl.GnoPackageAddress(testRoute))
	msg := hl.Message{Version: 3, Nonce: 0, Origin: hl.DomainGnoTest, Sender: route, Destination: hl.DomainSolanaTestnet, Recipient: token, Body: hl.TokenMessage([32]byte{1}, 5, nil)}
	f.renders[testMailbox+":json"] = fmt.Sprintf(`{"domain":%d,"hook":"","address":"","count":1,"root":"","delivered":0,"live":true,"launchedAt":1,"launchOrigin":%d,"decisions":0,"signers":{"keys":[],"threshold":0,"groupA":[],"groupB":[]},"verifiers":{},"registered":[],"seats":[],"routes":{}}`, hl.DomainGnoTest, hl.DomainSolanaTestnet)
	f.renders[testMailbox+":msg/0"] = msgRender(msg, 50)
	client := unbuildableClient{setFrom: 10, buildable: map[int64]bool{}}
	r.client = client
	now := time.Unix(1_800_000_000, 0)
	r.now = func() time.Time { return now }
	pass := func(what string, want int) {
		t.Helper()
		_ = r.reemits(ctx)
		if n := len(f.callsOf(testMailbox + ".Reemit")); n != want {
			t.Fatalf("%s: %d Reemits, want %d", what, n, want)
		}
	}
	pass("the dispatch's block is unbuildable", 1)
	pass("the Reemit's block is unbuildable too, within the minute", 1)
	now = now.Add(61 * time.Second)
	pass("a minute on", 2)
	now = now.Add(61 * time.Second)
	pass("within the two minutes", 2)
	now = now.Add(60 * time.Second)
	pass("two minutes on", 3)
	if _, ok := r.Alarm.Standing()["reemit:unbuildable:0"]; !ok {
		t.Fatalf("no alarm after three Reemits into unbuildable blocks: %v", r.Alarm.StandingKeys())
	}
	// however long it lasts, at most one an hour: passes every 31 minutes for 310 minutes send at 31, 62, 93 (the
	// waits 4, 8, 16 minutes), 155 (32), 217 and 279 (an hour) — six, where a Reemit a pass would send ten
	for i := 0; i < 10; i++ {
		now = now.Add(31 * time.Minute)
		_ = r.reemits(ctx)
	}
	if n := len(f.callsOf(testMailbox + ".Reemit")); n != 9 {
		t.Fatalf("five hours of unbuildable blocks: %d Reemits, want 9", n)
	}
	// the last Reemit's block reads: nothing more is sent, but leaves that merely parse end no run (E4-SF5: the root
	// check may still fail); once they are proven against a trusted header's root, the run ends and the alarm clears
	h := r.state.Reemits["0"]
	client.buildable[h] = true
	now = now.Add(2 * time.Hour)
	pass("the block reads", 9)
	if _, ok := r.Alarm.Standing()["reemit:unbuildable:0"]; !ok || r.state.ReemitTries["0"] == 0 {
		t.Fatalf("leaves that parse ended the run: %v, tries %d", r.Alarm.StandingKeys(), r.state.ReemitTries["0"])
	}
	r.leafs[leafKey(h, hl.ID(msg.Bytes()))] = leafInfo{index: 0, bytes: 200, buildable: true, proven: true}
	pass("the block is proven", 9)
	if _, ok := r.Alarm.Standing()["reemit:unbuildable:0"]; ok {
		t.Fatalf("the run did not end: %v", r.Alarm.StandingKeys())
	}
	// a later unbuildable block starts a fresh run: the first Reemit goes at once
	client.buildable[r.state.Reemits["0"]] = false
	pass("a fresh run", 10)
}
