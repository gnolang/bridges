package agent

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/gnolang/bridges/solana/bridge/hl"
	"github.com/gnolang/bridges/solana/bridge/sol"
)

// The round-4 live-fixes audit's tests (audit-r4/solana-fixes-poc/audit4sf_test.go), as the auditor wrote them; each
// failed on a7992734 and passes on the fix that names it (E4-SF1, SF2, SF3, SF5).
//
// Audit round 4 (solana-fixes): two routes, as the live deployment has (GNOT, then BUBBLE). solSupplies reads each
// route's mint on its own getAccountInfo, so the two supplies rest on different slots; snap.Slot is the lowest of
// all four answers. A BUBBLE send minted after the GNOT read and before the BUBBLE read is in BUBBLE's supply, and
// its processed record's slot is above snap.Slot: InFlightAt(snap.Slot) counts it in flight as well.
const a4Gnot = "gno.land/r/test/hlgnot"

var a4GnotToken = s4Key("gnot token")

func newA4Monitor(t *testing.T) *s4Monitor {
	r := &s4Monitor{t: t, cfg: s4Config(false), n: newSolNet(t), g: newS4Gno(), clock: s4ClockZero}
	r.cfg.Routes = append([]Route{{Symbol: "GNOT", Gno: a4Gnot, Token: a4GnotToken.String()}}, r.cfg.Routes...)
	r.g.set(s4Mailbox+":json", s4MailboxJSON(0, 0))
	r.g.set(s4Route+":json", s4RouteJSON(1000, 0, 0, 0, ""))
	r.g.set(a4Gnot+":json", `{"address":"g1gnot","live":true,"symbol":"GNOT","lock":5000,"sentTotal":5000,"owed":0,"voided":0,"lost":0,"imported":0,"refunded":0,"migratedTo":""}`)
	r.g.set(s4Route+":claims/0", "[]")
	r.g.set(a4Gnot+":claims/0", "[]")
	r.n.supply(s4Token, 1, 1000)
	r.n.supply(a4GnotToken, 1, 5000)
	r.m = r.newMonitor(t.TempDir() + "/monitor.json")
	return r
}

func (r *s4Monitor) verdictOf(sym string) string {
	r.m.mu.Lock()
	defer r.m.mu.Unlock()
	if e, ok := r.m.status.Identity[sym].(map[string]any); ok {
		return e["verdict"].(string) + " — " + e["text"].(string)
	}
	return ""
}

// An honest send of 300 BUBBLE, minted at slot 101. The poll reads GNOT's mint at 100 (both providers) and
// BUBBLE's at 101 — the reads are sequential, and the finalized slot moved by one between them. Nothing is forged:
// BUBBLE's lock 1,300 = its supply 1,300.
func TestA4HonestSendBetweenTheSupplyReadsIsNoBreak(t *testing.T) {
	r := newA4Monitor(t)
	r.n.slots(100, 100)
	r.poll()
	if v := r.verdictOf("BUBBLE"); v == "" || v[:5] != "holds" {
		t.Fatalf("poll 1: %s (%s)", v, r.cut())
	}
	send := s4Send(0, 300)
	r.g.set(s4Mailbox+":json", s4MailboxJSON(1, 0))
	r.g.set(s4Route+":json", s4RouteJSON(1300, 0, 0, 0, ""))
	r.g.set(s4Mailbox+":msg/0", msgRender(send, 140))
	r.step(10 * time.Second)
	r.n.slots(100, 100)
	r.poll() // the send walked, in flight, not minted yet
	if v := r.verdictOf("BUBBLE"); v == "" || v[:5] != "holds" {
		t.Fatalf("poll 2 (in flight): %s (%s)", v, r.cut())
	}
	r.n.supply(s4Token, 101, 1300) // the honest mint …
	r.n.processed(r.cfg, send.ID(), 101)
	r.n.slots(101, 101)
	gnotMint := sol.Synthetic{Program: a4GnotToken}.Mint()
	r.n.pinAt(0, gnotMint, 100) // … GNOT read at 100 (the first read), BUBBLE at 101
	r.n.pinAt(1, gnotMint, 100)
	r.step(10 * time.Second)
	r.poll()
	t.Logf("poll 3: BUBBLE %s; cut %s; flags %v", r.verdictOf("BUBBLE"), r.cut(), r.m.Alarm.Standing())
	if s := r.m.state.Sends["0"]; s == nil || !s.Processed {
		t.Fatalf("the mark: %+v", s)
	}
	if v := r.verdictOf("BUBBLE"); v[:5] != "holds" || r.alarmed("identity:BUBBLE:flag") {
		t.Fatalf("an honest send minted between the two routes' supply reads read as a break: %s", v)
	}
}

// burnOn is s4's burn on route ri of cfg (s4's burn takes route 0).
func (n *solNet) burnOn(cfg *Config, ri int, nonce uint32, slot uint64, to [32]byte, amount uint64) [32]byte {
	saved := cfg.Routes
	cfg.Routes = []Route{saved[ri]}
	defer func() { cfg.Routes = saved }()
	return n.burn(cfg, nonce, slot, to, amount)
}

// The short side of the same skew, on the signer (the GT9 forgery with no provider lying): the client is fooled,
// 300 BUBBLE minted and burned straight back (the forged burn, nonce 0) — every signer halts. The forger then burns
// 300 BUBBLE of his own (nonce 1) so that it lands between a round's GNOT read and its BUBBLE read: out of BUBBLE's
// supply, and above snap.Slot so not in flight. That round holds, the halt lifts, and the forged burn is signed.
func TestA4SignerSkewHidesAForgedMint(t *testing.T) {
	r := &s4Signer{t: t, cfg: s4Config(false), n: newSolNet(t), g: newS4Gno()}
	r.cfg.Routes = append([]Route{{Symbol: "GNOT", Gno: a4Gnot, Token: a4GnotToken.String()}}, r.cfg.Routes...)
	r.g.set(s4Mailbox+":json", s4MailboxJSON(0, 0))
	r.g.set(s4Route+":json", s4RouteJSON(1000, 0, 0, 0, ""))
	r.g.set(a4Gnot+":json", `{"address":"g1gnot","live":true,"symbol":"GNOT","lock":5000,"sentTotal":5000,"owed":0,"voided":0,"lost":0,"imported":0,"refunded":0,"migratedTo":""}`)
	r.n.supply(s4Token, 1, 1000)
	r.n.supply(a4GnotToken, 1, 5000)
	r.s = r.newSigner(t.TempDir())
	r.n.slots(100, 100)
	r.round()
	if r.s.Status()["signing"] != true {
		t.Fatalf("round 1: %v", r.s.Status())
	}
	r.n.supply(s4Token, 105, 1300)             // the forged mint …
	r.n.burnOn(r.cfg, 1, 0, 106, s4Alice, 300) // … burned straight back: the burn the forger wants signed
	r.n.supply(s4Token, 106, 1000)
	r.n.slots(110, 110)
	r.round()
	if st := r.s.Status(); st["signing"] == true || r.signed(0) {
		t.Fatalf("round 2 did not halt on the forged mint: %v", st)
	}
	r.n.burnOn(r.cfg, 1, 1, 112, s4Alice, 300) // the forger's own 300, burned at 112
	r.n.supply(s4Token, 112, 700)
	r.n.slots(112, 112)
	gnotMint := sol.Synthetic{Program: a4GnotToken}.Mint()
	r.n.pinAt(0, gnotMint, 111) // the round reads GNOT at 111, BUBBLE at 112 (sequential reads, the root moving)
	r.n.pinAt(1, gnotMint, 111)
	r.round()
	t.Logf("round 3: signing %v, halted %q, cut %s, the forged burn signed %v", r.s.Status()["signing"], r.s.Status()["halted"], r.cut(), r.signed(0))
	if r.signed(0) {
		t.Fatalf("the forged burn was signed: a burn of the forger's own between the two routes' supply reads made the round hold")
	}
}

// The same honest send on one route (no skew between routes): minted at 101, both supplies read at 101; but the
// processed record's read through the pair fails this poll — provider 2 answers it from a backend still below 101
// (no record: the pair's accounts differ), or times out. readSends keeps the send unprocessed and returns the error;
// the poll logs it (solErr) and judges on: the mint is in the supply and the send is counted in flight.
func TestA4ProcessedReadFailsTheMintIsCountedTwice(t *testing.T) {
	r := newS4Monitor(t, false)
	r.n.slots(100, 100)
	r.poll()
	send := s4Send(0, 300)
	r.g.set(s4Mailbox+":json", s4MailboxJSON(1, 0))
	r.g.set(s4Route+":json", s4RouteJSON(1300, 0, 0, 0, ""))
	r.g.set(s4Mailbox+":msg/0", msgRender(send, 140))
	r.step(10 * time.Second)
	r.poll() // walked, in flight
	r.n.supply(s4Token, 101, 1300)
	r.n.processed(r.cfg, send.ID(), 101)
	r.n.slots(101, 101)
	rec := sol.Mailbox{Program: r.cfg.SolMailbox()}.ProcessedMessage(send.ID())
	r.n.pinAt(1, rec, 100) // provider 2's backend for this one read is a slot behind
	r.step(10 * time.Second)
	r.poll()
	t.Logf("poll 3: %s; cut %s", r.verdict(), r.cut())
	if r.alarmed("identity:BUBBLE:flag") {
		t.Fatalf("an honest send whose processed record one provider answered a slot late was flagged: %s", r.verdict())
	}
}

// The walk held at a message it cannot pass (here: a stranger's message whose page does not render and whose header
// this node does not answer — a node refusing vm/qeval, or any read error) returns from readSends before the
// processed marks are read. An earlier honest send, delivered meanwhile, is then counted in flight beside its mint.
func TestA4WalkHeldSkipsTheMarks(t *testing.T) {
	r := newS4Monitor(t, false)
	r.n.slots(100, 100)
	r.poll()
	send := s4Send(0, 300)
	r.g.set(s4Mailbox+":json", s4MailboxJSON(1, 0))
	r.g.set(s4Route+":json", s4RouteJSON(1300, 0, 0, 0, ""))
	r.g.set(s4Mailbox+":msg/0", msgRender(send, 140))
	r.step(10 * time.Second)
	r.poll() // walked, in flight
	// a stranger's message #1 whose page cannot be read (no render here), and no header reader
	r.g.set(s4Mailbox+":json", s4MailboxJSON(2, 0))
	r.step(10 * time.Second)
	r.poll()
	t.Logf("poll 3 (held at #1): %s; next %d", r.verdict(), r.m.state.SendNext)
	r.n.supply(s4Token, 101, 1300) // #0 delivered
	r.n.processed(r.cfg, send.ID(), 101)
	r.n.slots(102, 102)
	r.step(10 * time.Second)
	r.poll()
	t.Logf("poll 4: %s; cut %s", r.verdict(), r.cut())
	if r.alarmed("identity:BUBBLE:flag") {
		t.Fatalf("the walk held at #1 and #0's delivery read as a break: %s", r.verdict())
	}
}

// e8644b44's backoff over the residual it names (a string the node's JSON cannot carry: the leaves rebuild, only the
// root check in deliverOne — resultsMatch — finds them not the chain's). In production the Reemit rule reads the leaf
// facts from the relayer's cache (solClientView). After a Reemit lands at h, the next tick's deliver pass rebuilds h's
// leaves — they parse — and caches the leaf BUILDABLE, then stops at "header h+1 is above the client's trusted height"
// before resultsMatch runs; the Reemit rule then reads h as buildable and clears the run (clearUnbuildable). Once the
// updater brings h+1, resultsMatch marks h unbuildable and the rule re-emits at once, the run back at zero: the
// backoff never grows and reemit:unbuildable never fires.
func TestA4BackoffResetsOnAMismatch(t *testing.T) {
	f := newFake()
	r := testRelayer(f, "primary")
	ctx := context.Background()
	token := [32]byte{9}
	r.Cfg.Routes[0].Token = hl.Base58(token[:])
	route, _ := hl.GnoAddressToBytes32(hl.GnoPackageAddress(testRoute))
	msg := hl.Message{Version: 3, Nonce: 0, Origin: hl.DomainGnoTest, Sender: route, Destination: hl.DomainSolanaTestnet, Recipient: token, Body: hl.TokenMessage([32]byte{1}, 5, nil)}
	f.renders[testMailbox+":json"] = fmt.Sprintf(`{"domain":%d,"hook":"","address":"","count":1,"root":"","delivered":0,"live":true,"launchedAt":1,"launchOrigin":%d,"decisions":0,"signers":{"keys":[],"threshold":0,"groupA":[],"groupB":[]},"verifiers":{},"registered":[],"seats":[],"routes":{}}`, hl.DomainGnoTest, hl.DomainSolanaTestnet)
	f.renders[testMailbox+":msg/0"] = msgRender(msg, 50)
	r.client = a4View{sv: &solClientView{r: r}} // the production view of the leaf cache (the set's height faked)
	now := time.Unix(1_800_000_000, 0)
	r.now = func() time.Time { return now }
	id := hl.ID(msg.Bytes())
	// the dispatch's block 50: its leaves rebuilt, the header came, resultsMatch found them not the chain's
	r.leafs[leafKey(50, id)] = leafInfo{index: 0, bytes: 200, buildable: true}
	_ = r.resultsMatch(50, id, [][]byte{{1}}, 51, [32]byte{})
	_ = r.reemits(ctx)
	maxTries := 0
	for cycle := 0; cycle < 30; cycle++ { // five hours, a header every ten minutes
		h := r.state.Reemits["0"]
		r.leafs[leafKey(h, id)] = leafInfo{index: 0, bytes: 200, buildable: true} // the deliver pass: leaves parse; h+1 not trusted yet
		now = now.Add(time.Minute)
		_ = r.reemits(ctx)
		now = now.Add(9 * time.Minute)
		_ = r.resultsMatch(h, id, [][]byte{{1}}, h+1, [32]byte{}) // the header came: the root check fails
		_ = r.reemits(ctx)
		maxTries = max(maxTries, r.state.ReemitTries["0"])
	}
	n := len(f.callsOf(testMailbox + ".Reemit"))
	_, alarmed := r.Alarm.Standing()["reemit:unbuildable:0"]
	t.Logf("five hours of poisoned blocks: %d Reemits, the run's tries at most %d, reemit:unbuildable raised %v", n, maxTries, alarmed)
	if n > 12 || !alarmed {
		t.Fatalf("the backoff did not hold: %d Reemits in five hours (TestReemitsIntoUnbuildableBlocksBackOff's rule: 9), alarm %v", n, alarmed)
	}
}

// a4View answers the Reemit rule's leaf facts as production does (solClientView over the relayer's leaf cache) and
// the client's current set from a constant.
type a4View struct{ sv *solClientView }

func (a4View) CurrentSetFrom(context.Context) (int64, error) { return 10, nil }
func (v a4View) LeafBytes(ctx context.Context, h int64, id [32]byte) (int, bool, error) {
	return v.sv.LeafBytes(ctx, h, id)
}
