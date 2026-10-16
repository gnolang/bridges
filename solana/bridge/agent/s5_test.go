package agent

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gagliardetto/solana-go/rpc"

	"github.com/gnolang/bridges/solana/bridge/gno"
	"github.com/gnolang/bridges/solana/bridge/gnolight"
	"github.com/gnolang/bridges/solana/bridge/hl"
	"github.com/gnolang/bridges/solana/bridge/sol"
)

// Lane s5 (2026-10-15): the round-4 live-fixes audit's findings on this bridge (E4-SF1…SF6), E4-GT1's Solana sibling
// and E4-RL1's Solana side. The rigs are s4's: a Solana in memory behind JSON-RPC whose two providers answer at their
// own finalized slots — one getMultipleAccounts from one slot, as a node answers it from one bank, an account pinned
// to an older slot answering its whole call from there (a stale backend) — and a gno node in memory.

// s5Fillers puts count routes before the config's, each with its own token, realm and supply, so that BUBBLE's mint
// is the (count+1)th key of the supplies' batched read: past ten, in a second call (sol.MultiMax). It answers the
// first filler's mint, which a test pins to put the first call at an older slot.
func s5Fillers(cfg *Config, g *s4Gno, n *solNet, count int) (firstMint [32]byte) {
	var fill []Route
	for i := 0; i < count; i++ {
		sym := "F" + strconv.Itoa(i)
		realm := "gno.land/r/test/fill" + strconv.Itoa(i)
		tok := s4Key("filler token " + sym)
		fill = append(fill, Route{Symbol: sym, Gno: realm, Token: tok.String()})
		g.set(realm+":json", fmt.Sprintf(`{"address":"g1fill%d","live":true,"symbol":%q,"lock":10,"sentTotal":10,"owed":0,"voided":0,"lost":0,"imported":0,"refunded":0,"migratedTo":""}`, i, sym))
		g.set(realm+":claims/0", "[]")
		n.supply(tok, 1, 10)
		if i == 0 {
			firstMint = sol.Synthetic{Program: tok}.Mint()
		}
	}
	cfg.Routes = append(fill, cfg.Routes...)
	return firstMint
}

func (r *s4Monitor) identitySlot(sym string) any {
	r.m.mu.Lock()
	defer r.m.mu.Unlock()
	if e, ok := r.m.status.Identity[sym].(map[string]any); ok {
		return e["slot"]
	}
	return nil
}

// ------------------------------------------------------------------------------------------- E4-SF1 --

// E4-SF1 past one batch: with eleven routes the supplies are two getMultipleAccounts a provider, and the root may move
// between them. Here the first ten mints are read at 100 and BUBBLE's at 101, where an honest send of 300 was minted
// and processed. BUBBLE's supply, its burns and its sends rest on BUBBLE's own slot (101): the send is minted and out
// of flight, and the identity holds. Holding every route to the lowest slot (100) counted it in flight beside its
// mint — BROKEN, the flags set.
func TestS5MonitorBatchesAtTwoSlots(t *testing.T) {
	r := newS4Monitor(t, false)
	first := s5Fillers(r.cfg, r.g, r.n, 10)
	r.n.slots(100, 100)
	r.poll()
	if v := r.verdict(); v != "holds" {
		t.Fatalf("poll 1: %s (%s)", v, r.cut())
	}
	send := s4Send(0, 300)
	r.g.set(s4Mailbox+":json", s4MailboxJSON(1, 0))
	r.g.set(s4Route+":json", s4RouteJSON(1300, 0, 0, 0, ""))
	r.g.set(s4Mailbox+":msg/0", msgRender(send, 140))
	r.step(10 * time.Second)
	r.poll() // walked, in flight
	if v := r.verdict(); v != "holds" {
		t.Fatalf("poll 2 (in flight): %s (%s)", v, r.cut())
	}
	r.n.supply(s4Token, 101, 1300) // the honest mint …
	r.n.processed(r.cfg, send.ID(), 101)
	r.n.slots(101, 101)
	r.n.pinAt(0, first, 100) // … the first batch (ten fillers) read at 100, BUBBLE's batch at 101
	r.n.pinAt(1, first, 100)
	r.step(10 * time.Second)
	r.poll()
	if v := r.verdict(); v != "holds" || r.alarmed("identity:BUBBLE:flag") {
		t.Fatalf("an honest send minted between the two batches of the supplies' read: %s (%s), alarms %v", v, r.cut(), r.m.Alarm.Standing())
	}
	if s := r.identitySlot("BUBBLE"); s != uint64(101) {
		t.Fatalf("BUBBLE's identity rests on slot %v, not its own read's (101)", s)
	}
	if s := r.identitySlot("F0"); s != uint64(100) {
		t.Fatalf("F0's identity rests on slot %v, not its own read's (100)", s)
	}
}

// hlwatch reads the supplies as the monitors do (TakeSnapshot) and judges the same identity: an honest BUBBLE send
// minted between the GNOT read and the BUBBLE read is no break there either.
func TestS5HlwatchHonestSendBetweenTheSupplyReads(t *testing.T) {
	cfg, n, g := s4Config(false), newSolNet(t), newS4Gno()
	cfg.Routes = append([]Route{{Symbol: "GNOT", Gno: a4Gnot, Token: a4GnotToken.String()}}, cfg.Routes...)
	g.set(s4Mailbox+":json", s4MailboxJSON(0, 0))
	g.set(s4Route+":json", s4RouteJSON(1000, 0, 0, 0, ""))
	g.set(a4Gnot+":json", `{"address":"g1gnot","live":true,"symbol":"GNOT","lock":5000,"sentTotal":5000,"owed":0,"voided":0,"lost":0,"imported":0,"refunded":0,"migratedTo":""}`)
	g.set(s4Route+":claims/0", "[]")
	g.set(a4Gnot+":claims/0", "[]")
	n.supply(s4Token, 1, 1000)
	n.supply(a4GnotToken, 1, 5000)
	providers := n.clients()
	st := &WatchState{}
	st.init()
	ctx := context.Background()
	read := func() *SolanaView {
		snap, err := TakeSnapshot(ctx, providers, cfg)
		if err != nil {
			t.Fatal(err)
		}
		v, err := ReadSolanaAt(ctx, providers, g, cfg, st, snap, 200, s4ClockZero)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	n.slots(100, 100)
	send := s4Send(0, 300)
	g.set(s4Mailbox+":json", s4MailboxJSON(1, 0))
	g.set(s4Route+":json", s4RouteJSON(1300, 0, 0, 0, ""))
	g.set(s4Mailbox+":msg/0", msgRender(send, 140))
	if verdict, text := read().Identities["BUBBLE"].Verdict(); verdict != Holds {
		t.Fatalf("in flight: %s %s", verdict, text)
	}
	n.supply(s4Token, 101, 1300)
	n.processed(cfg, send.ID(), 101)
	n.slots(101, 101)
	gnotMint := sol.Synthetic{Program: a4GnotToken}.Mint()
	n.pinAt(0, gnotMint, 100)
	n.pinAt(1, gnotMint, 100)
	v := read()
	id, ok := v.Identities["BUBBLE"]
	if verdict, text := id.Verdict(); !ok || verdict != Holds {
		t.Fatalf("an honest send minted between the supply reads: %v %s %s (errors %v)", ok, verdict, text, v.Err)
	}
}

// ------------------------------------------------------------------------------------------- E4-SF3 --

// E4-SF3 past one batch, on the signer: the first ten mints read at 111, BUBBLE's at 112, where the forger burned 300
// of his own. That burn is out of BUBBLE's supply (700) and must be in flight against it: BUBBLE's own slot (112)
// holds it there, and the forged mint (burned back at 106) still breaks the identity. Against the lowest slot (111)
// it was in neither: the round held, the halt lifted and the forged burn was signed.
func TestS5SignerBatchesAtTwoSlots(t *testing.T) {
	r := newS4Signer(t, false)
	first := s5Fillers(r.cfg, r.g, r.n, 10)
	r.n.slots(100, 100)
	r.round()
	if r.s.Status()["signing"] != true {
		t.Fatalf("round 1: %v (%s)", r.s.Status(), r.cut())
	}
	r.n.supply(s4Token, 105, 1300)              // the forged mint …
	r.n.burnOn(r.cfg, 10, 0, 106, s4Alice, 300) // … burned straight back: the burn the forger wants signed
	r.n.supply(s4Token, 106, 1000)
	r.n.slots(110, 110)
	r.round()
	if st := r.s.Status(); st["signing"] == true || r.signed(0) {
		t.Fatalf("round 2 did not halt on the forged mint: %v", st)
	}
	r.n.burnOn(r.cfg, 10, 1, 112, s4Alice, 300) // the forger's own 300 …
	r.n.supply(s4Token, 112, 700)
	r.n.slots(112, 112)
	r.n.pinAt(0, first, 111) // … burned between the two batches' reads
	r.n.pinAt(1, first, 111)
	r.round()
	if st := r.s.Status(); st["signing"] == true || r.signed(0) || !strings.Contains(fmt.Sprint(st["halted"]), "broken") {
		t.Fatalf("a burn between the supplies' two batches hid the forged mint: signing %v, the forged burn signed %v, the cut %q", st["signing"], r.signed(0), r.cut())
	}
}

// The burn index held to the slot the supply was read at (E4-SF3's other half): a provider's signature listing is a
// read of its own, answered from wherever its backend is. Here both list from four slots behind the supplies' (110
// against 114), so the forger's own burn at 112 — out of the supply — was in no index and out of flight: the round
// held over his forged mint and its burn was signed. Each route's listing must now reach the slot its supply was read
// at (minContextSlot): a listing behind it fails the scan, no verdict, and the halt stands.
func TestS5SignerListingBehindTheSupply(t *testing.T) {
	r := newS4Signer(t, false)
	r.n.slots(100, 100)
	r.round()
	r.n.supply(s4Token, 101, 1300)        // a forged mint of 300 …
	r.n.burn(r.cfg, 0, 102, s4Alice, 300) // … burned straight back
	r.n.supply(s4Token, 102, 1000)
	r.n.slots(110, 110)
	r.round()
	if st := r.s.Status(); st["signing"] == true || r.signed(0) {
		t.Fatalf("round 2 did not halt on the forged mint: %v", st)
	}
	r.n.burn(r.cfg, 1, 112, s4Alice, 300) // the forger's own 300
	r.n.supply(s4Token, 112, 700)
	r.n.slots(114, 114)
	r.n.lagListing(0, 4)
	r.n.lagListing(1, 4)
	r.round()
	if st := r.s.Status(); st["signing"] == true || r.signed(0) {
		t.Fatalf("a listing behind the supply hid the forger's own burn: signing %v, the forged burn signed %v, the cut %q", st["signing"], r.signed(0), r.cut())
	}
	if c := r.cut(); !strings.Contains(c, "Minimum context slot") {
		t.Fatalf("the round over a listing behind the supply: %q", c)
	}
	r.n.lagListing(0, 0)
	r.n.lagListing(1, 0)
	r.round()
	if st := r.s.Status(); st["signing"] == true || r.signed(0) || !strings.Contains(fmt.Sprint(st["halted"]), "broken") {
		t.Fatalf("the listings caught up: %v", st)
	}
}

// The monitor's index the same (indexDispatches, which hlwatch shares): a listing behind the supplies' slot left the
// forger's own burn out of the index and out of flight, and the forged mint read as holding. Now the scan fails —
// the poll is no verdict — until the listing reaches the supply's slot, and then the forged mint shows.
func TestS5MonitorListingBehindTheSupply(t *testing.T) {
	r := newS4Monitor(t, false)
	r.n.supply(s4Token, 101, 1300)        // a forged mint of 300 …
	r.n.burn(r.cfg, 0, 102, s4Alice, 300) // … burned straight back
	r.n.supply(s4Token, 102, 1000)
	r.n.burn(r.cfg, 1, 112, s4Alice, 300) // and 300 of the forger's own
	r.n.supply(s4Token, 112, 700)
	r.n.slots(114, 114)
	r.n.lagListing(0, 4) // listed at 110: the burn at 102 seen, the one at 112 not
	r.n.lagListing(1, 4)
	r.poll()
	if v := r.verdict(); v == "holds" {
		t.Fatalf("a listing behind the supply hid the forger's own burn: %s (%s)", v, r.cut())
	}
	r.n.lagListing(0, 0)
	r.n.lagListing(1, 0)
	r.step(10 * time.Second)
	r.poll()
	if v := r.verdict(); v != "BROKEN" || !r.alarmed("identity:BUBBLE:flag") {
		t.Fatalf("the listings caught up: %s (%s)", v, r.cut())
	}
}

// The processed records are read ten a batch, and two batches are two reads at their own slots. a7992734 held every
// record's slot to the LOWEST batch's: a record in the second batch, processed between the two batches' slots, read as
// "above its read" — no slot, processed at any slot — and its send counted out of flight against a supply read before
// its mint: the reading short by it, a forged mint of its amount hidden. Each record is held to its own batch's slot.
func TestS5MonitorRecordsInTwoBatches(t *testing.T) {
	r := newS4Monitor(t, false)
	// eleven sends of 10 in flight (the lock 1,110), and a forged mint of 10 burned straight back
	r.g.set(s4Mailbox+":json", s4MailboxJSON(11, 0))
	r.g.set(s4Route+":json", s4RouteJSON(1110, 0, 0, 0, ""))
	for i := uint32(0); i < 11; i++ {
		r.g.set(s4Mailbox+":msg/"+strconv.Itoa(int(i)), msgRender(s4Send(i, 10), 140))
	}
	r.n.supply(s4Token, 50, 1010)
	r.n.burn(r.cfg, 0, 51, s4Alice, 10)
	r.n.supply(s4Token, 51, 1000)
	r.n.slots(100, 100)
	r.poll()
	if v := r.verdict(); v != "BROKEN" {
		t.Fatalf("poll 1: %s (%s)", v, r.cut())
	}
	// send #10 delivered at 105; the supplies read at 100, the first batch of records (#0-#9) at 104, the second (#10)
	// at 106
	last := s4Send(10, 10)
	r.n.supply(s4Token, 105, 1010)
	r.n.processed(r.cfg, last.ID(), 105)
	r.n.slots(106, 106)
	mint := sol.Synthetic{Program: s4Token}.Mint()
	firstRecord := sol.Mailbox{Program: r.cfg.SolMailbox()}.ProcessedMessage(s4Send(0, 10).ID())
	for i := range 2 {
		r.n.pinAt(i, mint, 100)
		r.n.pinAt(i, firstRecord, 104)
	}
	r.step(10 * time.Second)
	r.poll()
	if s := r.m.state.Sends["10"]; s == nil || !s.Processed || s.ProcessedSlot != 105 {
		t.Fatalf("#10's mark: %+v", s)
	}
	if v := r.verdict(); v != "BROKEN" {
		t.Fatalf("a record of the second batch held to the first batch's slot hid the forged mint: %s (%s)", v, r.cut())
	}
}

// ------------------------------------------------------------------------------------------- E4-SF2 --

// E4-SF2, the read below the supply: both providers answer the processed record from a backend a slot behind the
// supplies' (the record absent there, alike — no disagreement). The send minted at 101 counted in flight beside its
// mint: BROKEN, the flags set, on two lagging backends. The records are read at or above the supply's slot now
// (minContextSlot) — a provider below it fails the read — and a record read below it anyway (a provider that ignores
// the bound) is no read of the mark: the token has no verdict, never a break.
func TestS5MonitorRecordsBelowTheSupply(t *testing.T) {
	for _, ignore := range []bool{false, true} {
		r := newS4Monitor(t, false)
		r.n.ignoreMin = [2]bool{ignore, ignore}
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
		r.n.pinAt(0, rec, 100) // both providers' records from a slot behind
		r.n.pinAt(1, rec, 100)
		r.step(10 * time.Second)
		r.poll()
		if r.alarmed("identity:BUBBLE:flag") || r.verdict() != "no verdict" {
			t.Fatalf("ignoring the bound %v: a send whose record both providers read below the supply's slot: %s (%s)", ignore, r.verdict(), r.cut())
		}
		// the backends catch up: the mark is read, the send minted, the identity holds
		r.n.pinAt(0, rec, 0)
		r.n.pinAt(1, rec, 0)
		r.step(10 * time.Second)
		r.poll()
		if v := r.verdict(); v != "holds" || r.alarmed("identity:BUBBLE:flag") {
			t.Fatalf("ignoring the bound %v: once the records read: %s (%s)", ignore, v, r.cut())
		}
	}
}

// A token whose sends' marks stay unread is no verdict for good — alarmed (sends:unread:<symbol>) once it has stood two
// minutes, quiet before, cleared once they read.
func TestS5MonitorMarksUnreadAlarm(t *testing.T) {
	r := newS4Monitor(t, false)
	r.n.slots(100, 100)
	send := s4Send(0, 300)
	r.g.set(s4Mailbox+":json", s4MailboxJSON(1, 0))
	r.g.set(s4Route+":json", s4RouteJSON(1300, 0, 0, 0, ""))
	r.g.set(s4Mailbox+":msg/0", msgRender(send, 140))
	rec := sol.Mailbox{Program: r.cfg.SolMailbox()}.ProcessedMessage(send.ID())
	r.n.pinAt(1, rec, 90) // provider 2 refuses the record's read (below the bound) every poll
	for i := 0; i < 13; i++ {
		r.poll()
		if got := r.alarmed("sends:unread:BUBBLE"); got != (i == 12) || r.verdict() != "no verdict" {
			t.Fatalf("poll %d (%s after the first): alarmed %v, %s", i+1, time.Duration(i)*10*time.Second, got, r.verdict())
		}
		r.step(10 * time.Second)
	}
	r.n.pinAt(1, rec, 0)
	r.poll()
	if r.alarmed("sends:unread:BUBBLE") || r.verdict() != "holds" {
		t.Fatalf("the marks read: alarms %v, %s", r.m.Alarm.Standing(), r.verdict())
	}
}

// The walk held at a send of the token's own whose page fails (one transient render error: a send is counted from its
// page alone, E3-RL1) returned before the marks: #0, delivered meanwhile, counted in flight beside its mint. The marks
// are read whatever the walk did.
func TestS5MonitorHeldAtOurSendReadsTheMarks(t *testing.T) {
	r := newS4Monitor(t, false)
	r.n.slots(100, 100)
	send := s4Send(0, 300)
	r.g.set(s4Mailbox+":json", s4MailboxJSON(1, 0))
	r.g.set(s4Route+":json", s4RouteJSON(1300, 0, 0, 0, ""))
	r.g.set(s4Mailbox+":msg/0", msgRender(send, 140))
	r.poll() // #0 walked, in flight
	// #1, a send of the route of 200, whose page fails this poll (and no header reader: the walk holds there)
	r.g.set(s4Mailbox+":json", s4MailboxJSON(2, 0))
	r.g.set(s4Route+":json", s4RouteJSON(1500, 0, 0, 0, ""))
	r.n.supply(s4Token, 101, 1300) // #0 delivered
	r.n.processed(r.cfg, send.ID(), 101)
	r.n.slots(102, 102)
	r.step(10 * time.Second)
	r.poll()
	if r.alarmed("identity:BUBBLE:flag") || r.verdict() == "BROKEN" {
		t.Fatalf("the walk held at #1 and #0's delivery read as a break: %s (%s)", r.verdict(), r.cut())
	}
	if s := r.m.state.Sends["0"]; s == nil || !s.Processed || r.m.state.SendNext != 1 {
		t.Fatalf("#0's mark: %+v, next %d", s, r.m.state.SendNext)
	}
}

// hlwatch the same: a walk held at a message it cannot pass reads the marks all the same, and a mark the providers give
// differently is no identity (never one provider's mark, as the UNCONFIRMED fallback read it).
func TestS5HlwatchMarksWhateverTheWalk(t *testing.T) {
	cfg, n, g := s4Config(false), newSolNet(t), newS4Gno()
	g.set(s4Route+":claims/0", "[]")
	n.supply(s4Token, 1, 1000)
	providers := n.clients()
	st := &WatchState{}
	st.init()
	ctx := context.Background()
	read := func() *SolanaView {
		snap, err := TakeSnapshot(ctx, providers, cfg)
		if err != nil {
			t.Fatal(err)
		}
		v, err := ReadSolanaAt(ctx, providers, g, cfg, st, snap, 200, s4ClockZero)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	n.slots(100, 100)
	send := s4Send(0, 300)
	g.set(s4Mailbox+":json", s4MailboxJSON(1, 0))
	g.set(s4Route+":json", s4RouteJSON(1300, 0, 0, 0, ""))
	g.set(s4Mailbox+":msg/0", msgRender(send, 140))
	if verdict, _ := read().Identities["BUBBLE"].Verdict(); verdict != Holds {
		t.Fatalf("in flight: %s", verdict)
	}
	// a stranger's #1 whose page cannot be read and no header reader: the walk holds there; #0 delivered meanwhile
	g.set(s4Mailbox+":json", s4MailboxJSON(2, 0))
	n.supply(s4Token, 101, 1300)
	n.processed(cfg, send.ID(), 101)
	n.slots(102, 102)
	v := read()
	if verdict, text := v.Identities["BUBBLE"].Verdict(); verdict != Holds {
		t.Fatalf("held at #1: %s %s (errors %v)", verdict, text, v.Err)
	}
	// a second send whose record provider 2 answers from a backend below its mint: no identity this reading
	g.set(s4Mailbox+":json", s4MailboxJSON(2, 0))
	g.set(s4Mailbox+":msg/1", msgRender(s4Send(1, 200), 141))
	g.set(s4Route+":json", s4RouteJSON(1500, 0, 0, 0, ""))
	second := s4Send(1, 200)
	n.supply(s4Token, 103, 1500)
	n.processed(cfg, second.ID(), 103)
	n.slots(104, 104)
	n.pinAt(1, sol.Mailbox{Program: cfg.SolMailbox()}.ProcessedMessage(second.ID()), 102)
	n.ignoreMin = [2]bool{true, true}
	v = read()
	if id, ok := v.Identities["BUBBLE"]; ok {
		verdict, text := id.Verdict()
		t.Fatalf("a mark the providers give differently gave an identity: %s %s", verdict, text)
	}
	if s := st.Sends["1"]; s == nil || s.Processed {
		t.Fatalf("#1's mark read from one provider: %+v", s)
	}
}

// ------------------------------------------------------------------------------------------- E4-GT1 --

// s5Credit credits a burn on the BUBBLE route at a height, as the relayer's Process does: the claim, owed, the
// delivered mark and hlmailbox's delivered count.
func s5Credit(g *s4Gno, id [32]byte, to [32]byte, amount, owed int64, height int64, delivered int) {
	g.set(s4Route+":json", s4RouteJSON(1000, owed, 0, 0, ""))
	g.set(s4Mailbox+":json", s4MailboxJSON(0, delivered))
	g.set(s4Mailbox+":delivered/"+s4IDHex(id), strconv.FormatInt(height, 10))
	g.set(s4Route+":claim/"+s4IDHex(id), s4Claim(id, to, amount, height))
	g.set(s4Route+":claims/0", "["+s4Claim(id, to, amount, height)+"]")
}

// E4-GT1's Solana sibling, verified: the cut's order check (Cut.CreditedAfterSlot) reads only the dispatch index. Bob
// burns 300 at slot 105; the signers' providers see it finalized and the relayer credits it at height 201 (owed 300).
// This monitor's two providers trail: they answer slot 100, below the burn, so the supply still holds his 300 and the
// index has no record of the burn to place the credit by — the identity read BROKEN by 300 (credits and payouts
// flagged, the pausable ISM paused) over an honest credit. The credits hlmailbox counts that no index holds are now
// placed by time: no verdict until the snapshot's slot was produced after the credit's block (by creditMargin), or the
// index holds the burn — here the providers catch up and the identity holds; never a flag.
func TestS5MonitorTrailingProvidersHonestCredit(t *testing.T) {
	r := newS4Monitor(t, false)
	r.n.slots(100, 100)
	r.poll()
	if v := r.verdict(); v != "holds" {
		t.Fatalf("poll 1: %s (%s)", v, r.cut())
	}
	honest := r.n.burn(r.cfg, 0, 105, s4Bob, 300)
	r.n.supply(s4Token, 105, 700)
	s5Credit(r.g, honest, s4Bob, 300, 300, 201, 1)
	r.g.setHead(203, s4ClockZero.Add(12*time.Second)) // the credit's block ≈ 12 s after slot 100 was produced
	for i := 0; i < 3; i++ {
		r.step(10 * time.Second)
		r.poll()
		if r.alarmed("identity:BUBBLE:flag") || r.verdict() == "BROKEN" || !strings.Contains(r.cut(), "in no dispatch index yet") {
			t.Fatalf("poll %d with the providers trailing the credit: %s (%s), alarms %v", i+2, r.verdict(), r.cut(), r.m.Alarm.Standing())
		}
	}
	r.n.slots(110, 110)
	r.step(10 * time.Second)
	r.poll()
	if v := r.verdict(); v != "holds" || r.alarmed("identity:BUBBLE:flag") {
		t.Fatalf("the providers caught up: %s (%s), alarms %v", v, r.cut(), r.m.Alarm.Standing())
	}
}

// The other side of the rule: a credit no index will ever hold (a forged one: no burn anywhere) is judged once the
// snapshot passes its time — here 12 s after its block (slot 131 against the block's s4ClockZero + 2 s) — and flagged.
func TestS5MonitorForgedCreditJudgedOnceTheSnapshotPassesIt(t *testing.T) {
	r := newS4Monitor(t, false)
	r.n.slots(100, 100)
	r.poll()
	forged := [32]byte{0xf0, 0x12}
	s5Credit(r.g, forged, s4Alice, 300, 300, 201, 1)
	r.g.setHead(203, s4ClockZero.Add(2*time.Second))
	r.n.slots(110, 110) // produced at s4ClockZero + 4 s: not past the credit's block by the margin yet
	r.step(10 * time.Second)
	r.poll()
	if v := r.verdict(); v == "BROKEN" || !strings.Contains(r.cut(), "in no dispatch index yet") {
		t.Fatalf("a credit made after the snapshot's slot was judged: %s (%s)", v, r.cut())
	}
	r.n.slots(131, 131) // s4ClockZero + 12.4 s
	r.step(10 * time.Second)
	r.poll()
	if v := r.verdict(); v != "BROKEN" || !r.alarmed("identity:BUBBLE:flag") {
		t.Fatalf("the forged credit once the snapshot passed its time: %s (%s), alarms %v", v, r.cut(), r.m.Alarm.Standing())
	}
}

// The signer the same: its providers trailing the credit halted it (signer:halt, a flag-level alarm) over an honest
// credit. Now the round is no verdict, quiet, and the next round past the burn holds.
func TestS5SignerTrailingProvidersHonestCredit(t *testing.T) {
	r := newS4Signer(t, false)
	r.n.slots(100, 100)
	r.round()
	if r.s.Status()["signing"] != true {
		t.Fatalf("round 1: %v (%s)", r.s.Status(), r.cut())
	}
	honest := r.n.burn(r.cfg, 0, 105, s4Bob, 300)
	r.n.supply(s4Token, 105, 700)
	s5Credit(r.g, honest, s4Bob, 300, 300, 201, 1)
	r.g.setHead(203, s4ClockZero.Add(12*time.Second))
	for i := 0; i < 3; i++ {
		r.round()
		if st := r.s.Status(); st["halted"] != "" || r.alarmed("signer:halt") || r.alarmed("signer:no-verdict") || !strings.Contains(r.cut(), "in no dispatch index yet") {
			t.Fatalf("round %d with the providers trailing the credit: %v, alarms %v", i+2, st, r.s.Alarm.Standing())
		}
	}
	r.n.slots(110, 110)
	r.round()
	if st := r.s.Status(); st["halted"] != "" || st["verdictSlot"] != uint64(110) {
		t.Fatalf("the providers caught up: %v", st)
	}
	// a restart keeps the count (state.json): its index scans again from the signing cursor only, so bob's credit is in
	// no index of its own now, and a book started over would hold every credit in one window until the snapshot
	// passed it
	r.s = r.newSigner(r.s.Store)
	r.n.slots(111, 111)
	r.round()
	if st := r.s.Status(); st["verdictSlot"] != uint64(111) {
		t.Fatalf("the round after a restart: %v (%s)", st, r.cut())
	}
}

// hlwatch the same, and its two halves: it never checked the cut's order at all (a burn its index holds, credited and
// above its route's supply slot, read as BROKEN), and a credit no index holds yet read as BROKEN and, on the second
// reading, as a FORGED CREDIT of the honest claim.
func TestS5HlwatchTrailingProvidersHonestCredit(t *testing.T) {
	cfg, n, g := s4Config(false), newSolNet(t), newS4Gno()
	g.set(s4Mailbox+":json", s4MailboxJSON(0, 0))
	g.set(s4Route+":json", s4RouteJSON(1000, 0, 0, 0, ""))
	g.set(s4Route+":claims/0", "[]")
	n.supply(s4Token, 1, 1000)
	providers := n.clients()
	st := &WatchState{}
	st.init()
	ctx := context.Background()
	read := func() *SolanaView {
		snap, err := TakeSnapshot(ctx, providers, cfg)
		if err != nil {
			t.Fatal(err)
		}
		v, err := ReadSolanaAt(ctx, providers, g, cfg, st, snap, 200, s4ClockZero)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	n.slots(100, 100)
	read()
	honest := n.burn(cfg, 0, 105, s4Bob, 300)
	n.supply(s4Token, 105, 700)
	s5Credit(g, honest, s4Bob, 300, 300, 201, 1)
	g.setHead(203, s4ClockZero.Add(12*time.Second))
	for i := 0; i < 2; i++ {
		v := read()
		if _, ok := v.Identities["BUBBLE"]; ok || len(v.Forged) > 0 || !strings.Contains(strings.Join(v.Err, "; "), "in no dispatch index yet") {
			t.Fatalf("reading %d with the providers trailing the credit: identities %v, forged %v, errors %v", i+2, v.Identities, v.Forged, v.Err)
		}
	}
	// a provider's listing ahead of the supplies' read: the burn indexed and credited, above BUBBLE's supply slot
	n.slots(110, 110)
	mint := sol.Synthetic{Program: s4Token}.Mint()
	n.pinAt(0, mint, 100)
	n.pinAt(1, mint, 100)
	v := read()
	if _, ok := v.Identities["BUBBLE"]; ok || !strings.Contains(strings.Join(v.Err, "; "), "dispatched after their route's supply slot") {
		t.Fatalf("a burn credited above the supply's slot: identities %v, errors %v", v.Identities, v.Err)
	}
	n.pinAt(0, mint, 0)
	n.pinAt(1, mint, 0)
	v = read()
	if verdict, text := v.Identities["BUBBLE"].Verdict(); verdict != Holds || len(v.Forged) > 0 {
		t.Fatalf("caught up: %s %s, forged %v, errors %v", verdict, text, v.Forged, v.Err)
	}
}

// creditBook.place: the credits counted between readings in windows of heights, a window closed once the index places
// its credits or the snapshot's slot was produced past its last block by the margin; one window over everything at the
// first reading; a node answering below the last reading starts again.
func TestS5CreditBook(t *testing.T) {
	var b creditBook
	t0 := time.Unix(1_800_000_000, 0)
	if why, _ := b.place(2, 100, t0, []int64{50, 60}, t0); why != "" || len(b.Open) != 0 {
		t.Fatalf("every credit placed: %q %v", why, b.Open)
	}
	// one more credit by height 110, unplaced: open until the snapshot passes t0+20s by the margin
	why, _ := b.place(3, 110, t0.Add(20*time.Second), []int64{50, 60}, t0.Add(25*time.Second))
	if why == "" || len(b.Open) != 1 || b.Open[0].From != 100 || b.Open[0].To != 110 {
		t.Fatalf("an unplaced credit: %q %+v", why, b.Open)
	}
	// placed at 105 — by an index that caught up — closes it
	if why, closed := b.place(3, 115, t0.Add(30*time.Second), []int64{50, 60, 105}, t0.Add(25*time.Second)); why != "" || closed != 0 || len(b.Open) != 0 {
		t.Fatalf("placed: %q %d %+v", why, closed, b.Open)
	}
	// a fourth, never placed (a vote's id, a forgery): open, then closed as no lag once the snapshot passes it
	if why, _ := b.place(4, 120, t0.Add(40*time.Second), []int64{50, 60, 105}, t0.Add(45*time.Second)); why == "" {
		t.Fatal("the fourth credit judged at once")
	}
	// a credit placed elsewhere does not close another window's: two unplaced in (120,130], one placed outside it
	if why, _ := b.place(6, 130, t0.Add(50*time.Second), []int64{50, 60, 105, 125}, t0.Add(49*time.Second)); why == "" || len(b.Open) != 2 {
		t.Fatalf("two windows: %q %+v", why, b.Open)
	}
	if why, closed := b.place(6, 131, t0.Add(51*time.Second), []int64{50, 60, 105, 125}, t0.Add(60*time.Second)); why != "" || closed != 2 || len(b.Open) != 0 {
		t.Fatalf("both windows past their time: %q, closed %d, %+v", why, closed, b.Open)
	}
	// a snapshot without a clock closes nothing by time
	if why, _ := b.place(7, 140, t0.Add(70*time.Second), []int64{50, 60, 105, 125}, time.Time{}); why == "" {
		t.Fatal("no clock, judged")
	}
	// a node answering below the last reading: one window over everything again
	if why, _ := b.place(7, 90, t0.Add(80*time.Second), []int64{50, 60, 105, 125}, time.Time{}); why == "" || len(b.Open) != 1 || b.Open[0].From != 0 || b.Open[0].N != 7 {
		t.Fatalf("a node behind the book: %q %+v", why, b.Open)
	}
}

// ------------------------------------------------------------------------------------------- E4-RL1 --

var doneQueryRE = regexp.MustCompile(`^(.+)\.func\(\) string \{ s := ""; done\.IterateByOffset\((\d+), (\d+), func\(k string, v any\) bool \{ s \+= k \+ ":" \+ u32\(uint32\(v\.\(int64\)\)\) \+ ","; return false \}\); return s \}\(\)$`)

// doneAnswer answers doneExpr as gno v1.5.1 does over hlmailbox's done tree, which here is a fake's renders: its
// "delivered/<id>" entries that name a height, in id order (the avl tree's), count of them from the offset.
func doneAnswer(renders map[string]string, mailbox, path string, data []byte) ([]byte, error) {
	m := doneQueryRE.FindSubmatch(data)
	if path != "vm/qeval" || m == nil || string(m[1]) != mailbox {
		return nil, fmt.Errorf("fake: no answer to %s %.120s", path, data)
	}
	from, _ := strconv.Atoi(string(m[2]))
	count, _ := strconv.Atoi(string(m[3]))
	prefix := mailbox + ":delivered/"
	var ids []string
	for k, v := range renders {
		if strings.HasPrefix(k, prefix) && v != "0" {
			ids = append(ids, k[len(prefix):])
		}
	}
	sort.Strings(ids)
	var b strings.Builder
	for i := from; i < len(ids) && i < from+count; i++ {
		b.WriteString(ids[i] + ":" + renders[prefix+ids[i]] + ",")
	}
	return []byte(`("` + b.String() + `" string)`), nil
}

// doneNode is the plain fake node that also answers the done tree's vm/qeval, counting them; a claims/<from> page past
// gasFrom fails as the node's 3B query gas does (0: none fails).
type doneNode struct {
	*fakeChain
	evals   int
	pages   int
	gasFrom int
}

func (d *doneNode) Query(_ context.Context, path string, data []byte, _ int64) ([]byte, error) {
	d.fakeChain.mu.Lock()
	defer d.fakeChain.mu.Unlock()
	d.evals++
	return doneAnswer(d.fakeChain.renders, testMailbox, path, data)
}

func (d *doneNode) RenderAt(ctx context.Context, pkg, path string, h int64) (string, error) {
	if strings.HasPrefix(path, "claims/") {
		d.pages++
		if from, _ := strconv.Atoi(path[len("claims/"):]); d.gasFrom > 0 && from >= d.gasFrom {
			return "", errors.New(`gno query vm/qrender: map[@type:/abci.StringError value:out of gas in location: CPUCycles] types.OutOfGasError{Descriptor:"CPUCycles"}`)
		}
	}
	return d.fakeChain.RenderAt(ctx, pkg, path, h)
}

// s5ClaimJSON is a route's claim/<id>.
func s5ClaimJSON(id, to string, amount int64, state, by string, height int64) string {
	return fmt.Sprintf(`{"id":%q,"to":%q,"amount":%d,"paid":0,"state":%q,"by":%q,"at":1,"height":%d,"seq":1}`, id, to, amount, state, by, height)
}

// E4-RL1, the Solana side: 2,000 claims wait on BUBBLE, 1,999 of them burns the index holds and the 2,000th a forged
// credit at the end of the route's order. The forged-credit rule walked the claims/<from> pages ten a poll, and p/hlroute
// reaches <from> by walking every waiting claim before it: past ≈1,600 the page is out of the node's query gas (here
// claims/1600 on fails so), the walk's read failed every poll from then on — the poll returning before its later steps
// — and the forged claim was never read. The rule now finds the credits no index holds by id (hlmailbox's delivered
// ids, read by offset through vm/qeval — twenty reads, once — then the claim by id), reads no claims page at all, and
// flags the forged credit on its second poll as before.
func TestS5UnindexedCreditFoundByIDPastABacklog(t *testing.T) {
	f := newFake()
	d := &doneNode{fakeChain: f, gasFrom: 1_600}
	m := testMonitor(f)
	m.own = d
	ctx := context.Background()
	mb := mailboxWith()
	m.seat = 5
	const n = 2_000
	var page []string
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("%064x", i+1)
		f.renders[testMailbox+":delivered/"+id] = "90"
		c := s5ClaimJSON(id, "g1user"+strconv.Itoa(i), 25, "open", "signers", 90)
		f.renders[testRoute+":claim/"+id] = c
		page = append(page, c)
		if i < n-1 {
			m.state.Index[id] = &dispatchRec{Symbol: "BUBBLE", Slot: 10, To: "g1user" + strconv.Itoa(i), Amount: 25, Credited: 90}
		}
		if len(page) == 100 {
			f.renders[testRoute+":claims/"+strconv.Itoa(i-99)] = "[" + strings.Join(page, ",") + "]"
			page = nil
		}
	}
	f.renders[testRoute+":claims/2000"] = "[]"
	forged := fmt.Sprintf("%064x", n)
	mb.Delivered = n
	for poll := 0; poll < 3; poll++ {
		if err := m.unindexedCredits(ctx, mb, 100); err != nil {
			t.Fatalf("poll %d: %v", poll+1, err)
		}
		now := m.now().Add(m.Every)
		m.now = func() time.Time { return now }
	}
	if a, ok := m.Alarm.Standing()["credit:unindexed:"+forged+":flag"]; !ok || !strings.Contains(a, "forged credit "+forged) {
		t.Fatalf("the forged credit behind 1,999 waiting claims was not flagged: %v", m.Alarm.StandingKeys())
	}
	if d.pages != 0 || d.evals != n/donePage+1 {
		t.Fatalf("%d claims pages read, %d qevals (want none, and %d once)", d.pages, d.evals, n/donePage+1)
	}
}

// hlwatch the same: the forged credit behind the backlog printed (on its second reading, G33), no claims page read.
func TestS5HlwatchUnindexedCreditFoundByID(t *testing.T) {
	cfg, n, g := s4Config(false), newSolNet(t), newS4Gno()
	g.set(s4Mailbox+":json", s4MailboxJSON(0, 1))
	g.set(s4Route+":json", s4RouteJSON(1000, 300, 0, 0, ""))
	forged := strings.Repeat("f0", 32)
	g.set(s4Mailbox+":delivered/"+forged, "150")
	g.set(s4Route+":claim/"+forged, s5ClaimJSON(forged, "g1eve", 300, "open", "signers", 150))
	n.supply(s4Token, 1, 1000)
	n.slots(200, 200) // the snapshot past the credit's block (s4ClockZero) by 40 s
	providers := n.clients()
	st := &WatchState{}
	st.init()
	ctx := context.Background()
	var v *SolanaView
	for i := 0; i < 2; i++ {
		snap, err := TakeSnapshot(ctx, providers, cfg)
		if err != nil {
			t.Fatal(err)
		}
		if v, err = ReadSolanaAt(ctx, providers, g, cfg, st, snap, 200, s4ClockZero); err != nil {
			t.Fatal(err)
		}
	}
	if len(v.Forged) != 1 || !strings.Contains(v.Forged[0], "forged credit "+forged) {
		t.Fatalf("the forged credit: %v (errors %v)", v.Forged, v.Err)
	}
	if verdict, _ := v.Identities["BUBBLE"].Verdict(); verdict != Broken {
		t.Fatalf("the identity over the forged credit: %s", verdict)
	}
}

// The done tree's page as the node answers it, and what is no answer.
func TestS5DonePage(t *testing.T) {
	a, b := strings.Repeat("ab", 32), strings.Repeat("cd", 32)
	q := &doneQuery{answer: `("` + a + `:791946,` + b + `:796585," string)`}
	page, err := donePageAt(context.Background(), q, testMailbox, 0)
	if err != nil || len(page) != 2 || page[0] != (doneEntry{a, 791946}) || page[1] != (doneEntry{b, 796585}) {
		t.Fatalf("a page: %v %v", page, err)
	}
	if want := testMailbox + "." + doneExpr(0, donePage); q.data != want {
		t.Fatalf("the query: %q, want %q", q.data, want)
	}
	if page, err := donePageAt(context.Background(), &doneQuery{answer: `("" string)`}, testMailbox, 300); err != nil || len(page) != 0 {
		t.Fatalf("past the end: %v %v", page, err)
	}
	for _, bad := range []string{`(791946 int64)`, `("` + a[:62] + `:5," string)`, `("` + a + `:0," string)`, `("` + a + `:x," string)`, `("` + a + `," string)`} {
		if _, err := donePageAt(context.Background(), &doneQuery{answer: bad}, testMailbox, 0); err == nil {
			t.Fatalf("%q read as a page", bad)
		}
	}
}

type doneQuery struct{ answer, data string }

func (q *doneQuery) Query(_ context.Context, _ string, data []byte, _ int64) ([]byte, error) {
	q.data = string(data)
	return []byte(q.answer), nil
}

// ------------------------------------------------------------------------------------------- E4-SF5 --

// A block's leaves are proven once they match a trusted header's results root, and stay so: the deliver pass's next
// read of the block (leafAt fetches a buildable block again) keeps the mark, and the Reemit rule ends a run of
// unbuildable blocks on it — never on leaves that merely parse.
func TestS5LeafProvenStays(t *testing.T) {
	f := newFake()
	r := testRelayer(f, "primary")
	ctx := context.Background()
	_, d, id := dispatchOf(6, testMailbox)
	r.light = &resultsLight{fakeLight: &fakeLight{fakeChain: f}, results: map[int64][]*gnolight.TxResult{
		70: blockResults(t, txJSON("null", dispatchJSON(d, testMailbox))),
	}}
	li, leaves, err := r.leafAt(ctx, 70, id)
	if err != nil || !li.buildable || li.proven || r.leafProven(70, id) {
		t.Fatalf("leaves that parse: %+v %v", li, err)
	}
	root, _ := gnolight.SimpleHash(leaves)
	if err := r.resultsMatch(70, id, leaves, 71, root); err != nil {
		t.Fatal(err)
	}
	r.markProven(70, id)
	if !r.leafProven(70, id) {
		t.Fatal("not proven after the root matched")
	}
	if li, _, err := r.leafAt(ctx, 70, id); err != nil || !li.proven || !r.leafProven(70, id) {
		t.Fatalf("the next read of the block dropped the mark: %+v %v", li, err)
	}
}

// ------------------------------------------------------------------------------------------- E4-SF4 --

// clientStateFor is the light client's state account (sol.ParseClientState's layout) trusting a height under a
// validator set, on a chain id: an empty record and ring.
func clientStateFor(trustedHeight uint64, lastAccepted int64, chainID string, set *gnolight.ValidatorSet) []byte {
	const bodyCap8 = 128
	sh, _ := set.Hash()
	b := []byte{sol.ClientStateVersion, 1}
	b = binary.LittleEndian.AppendUint16(b, 0) // ring count
	b = binary.LittleEndian.AppendUint16(b, 0) // ring next
	b = binary.LittleEndian.AppendUint16(b, bodyCap8)
	b = binary.LittleEndian.AppendUint64(b, 0) // no quarantine
	b = binary.LittleEndian.AppendUint64(b, uint64(lastAccepted))
	b = binary.LittleEndian.AppendUint64(b, trustedHeight)
	b = append(b, make([]byte, 32)...) // the trusted hash
	b = append(b, s4Key("admin").Bytes()...)
	b = binary.LittleEndian.AppendUint32(b, uint32(len(chainID)))
	b = append(b, chainID...)
	b = binary.LittleEndian.AppendUint32(b, hl.DomainGnoTest)
	b = binary.LittleEndian.AppendUint64(b, trustedHeight) // the trusted header: height, hash, its sets' hashes, time
	b = append(b, make([]byte, 32)...)
	b = append(b, sh[:]...)
	b = append(b, sh[:]...)
	b = binary.LittleEndian.AppendUint64(b, uint64(lastAccepted))
	b = binary.LittleEndian.AppendUint32(b, uint32(len(set.Validators)))
	for _, v := range set.Validators {
		b = append(b, v.PubKey[:]...)
		b = binary.LittleEndian.AppendUint64(b, uint64(v.Power))
	}
	b = append(b, sh[:]...) // the set's hash
	b = append(b, 0)        // no next set
	b = append(b, sh[:]...)
	b = binary.LittleEndian.AppendUint32(b, 0) // no history
	b = binary.LittleEndian.AppendUint64(b, 0) // the record's counter
	b = append(b, make([]byte, 32)...)         // its running hash
	b = binary.LittleEndian.AppendUint64(b, 0) // the ring epoch
	for len(b) < sol.HeadLen+bodyCap8*8 {
		b = append(b, 0)
	}
	return b
}

// E4-SF4: the updater's tick read the newest message's height (a dispatch waits?) before it weighed any reason, and
// returned on its error: while the page and the mailbox's record both failed (a node refusing or limiting vm/qeval, a
// backend without the message, a timeout), no header went at all — the announcing header of a validator change and
// a validator-set event's header included. Here a validator-set event armed the next header and the newest message
// cannot be read: the tick weighs the event (the header is submitted — the submission itself fails in this rig, which
// holds no transactions); updater:newest alarms once the error has stood two minutes.
func TestS5UpdaterNewestUnreadable(t *testing.T) {
	set := setOf(1, 4)
	fl := &fakeLight{fakeChain: newFake(), sets: map[int64]*gnolight.ValidatorSet{1: set}, head: 120}
	fl.fakeChain.head = gno.Head{Height: 120, Time: time.Unix(1_800_000_000, 0)}
	hf := newHeaderFake()
	n := newSolNet(t)
	client := sol.LightClient{Program: s4Key("client")}
	clock := time.Unix(1_800_000_000, 0)
	n.put(client.State(), 1, s4Key("client"), clientStateFor(100, clock.Unix(), "dev", set))
	n.slots(10, 10)
	cfg := &Config{}
	cfg.Gno.Mailbox, cfg.Gno.ChainID = testMailbox, "dev"
	u := &Updater{Cfg: cfg, Alarm: &Alarmer{Source: "test"}, gno: &headerLight{fakeLight: fl, headerFake: hf}, sol: &sol.Client{RPC: rpc.New(n.urls[0])}, client: client, now: func() time.Time { return clock }}
	if err := u.init(); err != nil {
		t.Fatal(err)
	}
	fl.renders[testMailbox+":json"] = fmt.Sprintf(`{"domain":%d,"count":2}`, hl.DomainGnoTest)
	fl.renders[testMailbox+":msg/0"] = msgRender(hl.Message{Version: 3, Nonce: 0, Origin: hl.DomainGnoTest}, 40)
	hf.unreadable(hl.Message{Version: 3, Nonce: 1, Origin: hl.DomainGnoTest, Sender: [32]byte{0xee}, Body: bigBody}, 110)
	hf.evalErr = errors.New("gno rpc abci_query: vm/qeval refused")
	u.state.Armed = true
	ctx := context.Background()
	err := u.tick(ctx)
	if plan := fmt.Sprint(u.status["plan"]); !strings.Contains(plan, "reason: a validator-set event") || err == nil || !strings.Contains(err.Error(), "submitting 120 (a validator-set event)") {
		t.Fatalf("the tick over an unreadable newest message: plan %q, err %v", plan, err)
	}
	if u.Alarm.Standing()["updater:newest"] != "" {
		t.Fatal("alarmed at once")
	}
	clock = clock.Add(cutFailAfter)
	_ = u.tick(ctx)
	if a := u.Alarm.Standing()["updater:newest"]; !strings.Contains(a, "vm/qeval refused") {
		t.Fatalf("two minutes of it: %q", a)
	}
	hf.evalErr = nil
	_ = u.tick(ctx)
	if _, ok := u.Alarm.Standing()["updater:newest"]; ok {
		t.Fatal("the alarm stands after the record read")
	}
}

// ------------------------------------------------------------------------------------------- E4-SF6 --

// qevalNode is the fake node with the record reads (headerFake) and the done tree's (doneAnswer).
type qevalNode struct {
	*fakeChain
	*headerFake
}

func (q qevalNode) Query(ctx context.Context, path string, data []byte, h int64) ([]byte, error) {
	if q.headerFake.evalErr == nil && doneQueryRE.Match(data) {
		q.fakeChain.mu.Lock()
		defer q.fakeChain.mu.Unlock()
		return doneAnswer(q.fakeChain.renders, testMailbox, path, data)
	}
	return q.headerFake.Query(ctx, path, data, h)
}

// E4-SF6: the reads of E3-RL1 (a message's record) and of E4-RL1 (the delivered ids) rest on the own node answering
// vm/qeval of hlmailbox's unexported state, which nothing watched: a machine checks it at its start and hourly, and
// alarms gno:qeval while the node does not answer (tried again each minute), cleared once it does.
func TestS5QevalCheck(t *testing.T) {
	f := newFake()
	hf := newHeaderFake()
	node := qevalNode{fakeChain: f, headerFake: hf}
	f.renders[testMailbox+":json"] = fmt.Sprintf(`{"domain":%d,"count":1,"delivered":1}`, hl.DomainGnoTest)
	hf.records[0] = record{hl.Message{Version: 3, Nonce: 0, Origin: hl.DomainGnoTest}, 40}
	f.renders[testMailbox+":delivered/"+strings.Repeat("ab", 32)] = "41"
	alarm := &Alarmer{Source: "test"}
	ctx := context.Background()
	now := time.Unix(1_800_000_000, 0)
	var qc qevalCheck
	qc.run(ctx, node, testMailbox, alarm, now)
	if _, ok := alarm.Standing()["gno:qeval"]; ok || !qc.next.Equal(now.Add(time.Hour)) {
		t.Fatalf("a node that answers: %v, next %s", alarm.Standing(), qc.next)
	}
	hf.evalErr = errors.New("gno rpc abci_query: unknown query path vm/qeval")
	qc.run(ctx, node, testMailbox, alarm, now.Add(30*time.Minute))
	if _, ok := alarm.Standing()["gno:qeval"]; ok || hf.evals != 1 {
		t.Fatalf("checked before the hour: %v, %d record reads", alarm.Standing(), hf.evals)
	}
	now = now.Add(time.Hour)
	qc.run(ctx, node, testMailbox, alarm, now)
	if a := alarm.Standing()["gno:qeval"]; !strings.Contains(a, "unknown query path vm/qeval") || !qc.next.Equal(now.Add(time.Minute)) {
		t.Fatalf("a node that stopped answering: %q, next %s", a, qc.next)
	}
	hf.evalErr = nil
	now = now.Add(time.Minute)
	qc.run(ctx, node, testMailbox, alarm, now)
	if _, ok := alarm.Standing()["gno:qeval"]; ok {
		t.Fatal("the alarm stands once the node answers again")
	}
	// the delivered ids alone failing: alarmed too
	delete(f.renders, testMailbox+":delivered/"+strings.Repeat("ab", 32))
	now = now.Add(time.Hour)
	qc.run(ctx, node, testMailbox, alarm, now)
	if a := alarm.Standing()["gno:qeval"]; !strings.Contains(a, "none answered, 1 delivered") {
		t.Fatalf("a done tree that answers nothing: %q", a)
	}
	// a node reader with no vm/qeval at all, and a mailbox with nothing to read yet
	var plain qevalCheck
	alarm2 := &Alarmer{Source: "test"}
	plain.run(ctx, f, testMailbox, alarm2, now)
	if _, ok := alarm2.Standing()["gno:qeval"]; !ok {
		t.Fatal("a node with no vm/qeval was not alarmed")
	}
	f.renders[testMailbox+":json"] = fmt.Sprintf(`{"domain":%d,"count":0,"delivered":0}`, hl.DomainGnoTest)
	var empty qevalCheck
	alarm3 := &Alarmer{Source: "test"}
	empty.run(ctx, node, testMailbox, alarm3, now)
	if len(alarm3.Standing()) != 0 {
		t.Fatalf("an empty mailbox: %v", alarm3.Standing())
	}
}

// The snapshot's time is the lowest any of its reads answered: a provider that answers the lowest slot with a clock a
// minute late (a liar, or a backend with a broken clock) cannot close a window of credits the index has not placed —
// the other provider's clock, a slot higher, still reads before the credit. Taking the lowest slot's own clock let the
// lie through, and the honest credit above both providers' slots read as BROKEN.
func TestS5MonitorLyingClockKeepsTheWindowOpen(t *testing.T) {
	r := newS4Monitor(t, false)
	r.n.slots(100, 100)
	r.poll()
	honest := r.n.burn(r.cfg, 0, 105, s4Bob, 300)
	r.n.supply(s4Token, 105, 700)
	s5Credit(r.g, honest, s4Bob, 300, 300, 201, 1)
	r.g.setHead(203, s4ClockZero.Add(12*time.Second))
	r.n.slots(100, 101)                // both below the burn; provider 1 the lower …
	r.n.clockLie[0] = 60 * time.Second // … with its clock a minute late
	r.step(10 * time.Second)
	r.poll()
	if r.alarmed("identity:BUBBLE:flag") || r.verdict() == "BROKEN" || !strings.Contains(r.cut(), "in no dispatch index yet") {
		t.Fatalf("a lying clock closed the window: %s (%s)", r.verdict(), r.cut())
	}
}
