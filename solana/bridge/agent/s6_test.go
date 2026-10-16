package agent

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gagliardetto/solana-go"
	"github.com/gagliardetto/solana-go/rpc"

	"github.com/gnolang/bridges/solana/bridge/gno"
	"github.com/gnolang/bridges/solana/bridge/hl"
	"github.com/gnolang/bridges/solana/bridge/sol"
)

// Lane s6 (2026-10-15): round 5's fixes to the live Solana bridge's Go — the round-5 audit of the live fixes
// (solana-fixes.md: E5-SF1…SF6) and the Ethereum spend audit's E5-GS1 and E5-GS5, whose Solana siblings live here,
// with I2's Claim batch carried over from bridge-eth.

// s6Sweep is a route's claim/<id> for a sweep entry: p/hlroute's sweptJSON, the amount in "swept" and no "state".
func s6Sweep(id [32]byte, amount, height int64) string {
	return fmt.Sprintf(`{"id":%q,"swept":%d,"height":%d,"by":"signers","voided":false}`, s4IDHex(id), amount, height)
}

// s6Claim is a route's claim/<id> in a state, credited by "signers" or "vote".
func s6Claim(id, to [32]byte, amount, paid int64, state, by string, height int64) string {
	addr, _ := hl.Bytes32ToGnoAddress(to)
	return fmt.Sprintf(`{"id":%q,"to":%q,"amount":%d,"paid":%d,"state":%q,"by":%q,"height":%d}`, s4IDHex(id), addr, amount, paid, state, by, height)
}

// s6ID is a message id no burn carries (a credit the dispatch index cannot hold).
func s6ID(name string) [32]byte { return [32]byte(s4Key(name)) }

// ------------------------------------------------------------------------------------------- E5-SF1 --

// E5-SF1, the signers' half: a burn of nothing was indexed and signed like any other, its signature served publicly, so
// a stranger brought it to hlgnot/hlbubble's Process himself — a credit (a sweep of 0) no monitor's index holds, each
// one a window of no verdict on both monitors and hlwatch, for as long as he repeated it. The signers skip it now, as
// the relayer and the watch do: no signature, so hlmailbox's verifier refuses its Process; the burns around it are
// signed as before.
func TestS6SignerSignsNoZeroBurn(t *testing.T) {
	r := newS4Signer(t, false)
	r.n.slots(100, 100)
	r.round()
	r.n.burn(r.cfg, 0, 101, s4Bob, 300)
	r.n.burn(r.cfg, 1, 102, s4Alice, 0) // a burn of nothing
	r.n.burn(r.cfg, 2, 103, s4Bob, 200)
	r.n.supply(s4Token, 103, 500)
	r.n.slots(104, 104)
	r.round()
	r.round()
	t.Logf("signed #0 %v, #1 (zero) %v, #2 %v; indexed %v; cursor %v", r.signed(0), r.signed(1), r.signed(2), r.s.Status()["indexed"], r.s.Status()["solCursors"])
	if !r.signed(0) || !r.signed(2) {
		t.Fatalf("the burns around the zero one were not signed: %v", r.s.Status())
	}
	if r.signed(1) {
		t.Fatalf("the signer signed a burn of nothing: its signature lets anyone credit it, a credit no monitor's index holds (E5-SF1)")
	}
	srv := httptest.NewServer(r.s.Handler())
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/v1/sol/1")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("the zero burn's signature is served: %s", resp.Status)
	}
	if n := r.s.Status()["indexed"]; n != 2 {
		t.Fatalf("the index holds %v records, want the two burns of something", n)
	}
}

// E5-SF1, the monitors' half: an unindexed credit whose claim is a sweep entry (a zero credit is one, "swept":0 with no
// state) or a claim paid in full settled no class, so each was read again by id on every poll for good — a stranger's
// zero credits a render each a poll, growing with every one he ever made. A sweep entry and a paid claim settle a
// class now (never a waiting claim of the verifier's again); a voided claim is read again, since a restore reopens it —
// and a restored one is judged as before.
func TestS6UnindexedSweepAndPaidSettle(t *testing.T) {
	r := newS4Monitor(t, false)
	r.n.slots(100, 100)
	r.poll()
	if v := r.verdict(); v != "holds" {
		t.Fatalf("poll 1: %s (%s)", v, r.cut())
	}
	zero, dust, paid, vote, voided := s6ID("zero"), s6ID("dust"), s6ID("paid"), s6ID("vote"), s6ID("voided")
	claims := map[[32]byte]string{
		zero:   s6Sweep(zero, 0, 201),
		dust:   s6Sweep(dust, 7, 201),
		paid:   s6Claim(paid, s4Bob, 20, 20, "done", "signers", 201),
		vote:   s6Claim(vote, s4Bob, 30, 0, "open", "vote", 201),
		voided: s6Claim(voided, s4Alice, 40, 0, "withheld", "signers", 201),
	}
	for id, c := range claims {
		r.g.set(s4Mailbox+":delivered/"+s4IDHex(id), "201")
		r.g.set(s4Route+":claim/"+s4IDHex(id), c)
	}
	r.g.set(s4Mailbox+":json", s4MailboxJSON(0, len(claims)))
	r.g.setHead(203, s4ClockZero.Add(2*time.Second))
	reads := map[string]int{}
	var mu sync.Mutex
	r.g.hook(func(_, path string) {
		if strings.HasPrefix(path, "claim/") {
			mu.Lock()
			reads[strings.TrimPrefix(path, "claim/")]++
			mu.Unlock()
		}
	})
	slot := uint64(160) // past the credits' block by the margin
	var perPoll []int
	for p := 0; p < 3; p++ {
		mu.Lock()
		reads = map[string]int{}
		mu.Unlock()
		r.n.slots(slot, slot)
		r.step(10 * time.Second)
		r.poll()
		mu.Lock()
		perPoll = append(perPoll, len(reads))
		mu.Unlock()
		slot += 25
	}
	classes := map[string]string{}
	for id, u := range r.m.state.Unindexed {
		classes[id[:6]] = u.Class
	}
	t.Logf("claims read by id per poll %v; classes %v; cut %q", perPoll, classes, r.cut())
	if len(r.m.state.Unindexed) != 5 || perPoll[0] != 5 {
		t.Fatalf("the five credits no index holds were not found and read: unindexed %d, reads %v", len(r.m.state.Unindexed), perPoll)
	}
	for id, want := range map[[32]byte]string{zero: "sweep", dust: "sweep", paid: "paid", vote: "vote", voided: ""} {
		if got := r.m.state.Unindexed[s4IDHex(id)].Class; got != want {
			t.Errorf("%s: class %q, want %q", s4IDHex(id)[:6], got, want)
		}
	}
	if perPoll[1] != 1 || perPoll[2] != 1 {
		t.Fatalf("the settled credits were read again by id every poll: %v (only the voided claim is read again)", perPoll)
	}
	// the voided claim restored: an open claim of the verifier's no index holds, flagged once it has stood a poll
	r.g.set(s4Route+":claim/"+s4IDHex(voided), s6Claim(voided, s4Alice, 40, 0, "open", "signers", 201))
	for p := 0; p < 2; p++ {
		r.n.slots(slot, slot)
		r.step(10 * time.Second)
		r.poll()
		slot += 25
	}
	if !r.alarmed("credit:unindexed:" + s4IDHex(voided) + ":flag") {
		t.Fatalf("a restored claim no index holds was not judged: %v", r.m.Alarm.StandingKeys())
	}
}

// hlwatch keeps the same classes in its state file: a reading reads the settled credits' claims no more.
func TestS6HlwatchUnindexedSettle(t *testing.T) {
	cfg, n, g := s4Config(false), newSolNet(t), newS4Gno()
	g.set(s4Mailbox+":json", s4MailboxJSON(0, 0))
	g.set(s4Route+":json", s4RouteJSON(1000, 0, 0, 0, ""))
	n.supply(s4Token, 1, 1000)
	zero, paid := s6ID("zero"), s6ID("paid")
	for id, c := range map[[32]byte]string{zero: s6Sweep(zero, 0, 201), paid: s6Claim(paid, s4Bob, 20, 20, "done", "signers", 201)} {
		g.set(s4Mailbox+":delivered/"+s4IDHex(id), "201")
		g.set(s4Route+":claim/"+s4IDHex(id), c)
	}
	g.set(s4Mailbox+":json", s4MailboxJSON(0, 2))
	g.setHead(203, s4ClockZero.Add(2*time.Second))
	providers := n.clients()
	st := &WatchState{}
	st.init()
	ctx := context.Background()
	var reads int
	var mu sync.Mutex
	g.hook(func(_, path string) {
		if strings.HasPrefix(path, "claim/") {
			mu.Lock()
			reads++
			mu.Unlock()
		}
	})
	var perReading []int
	for i, slot := range []uint64{160, 185, 210} {
		n.slots(slot, slot)
		snap, err := TakeSnapshot(ctx, providers, cfg)
		if err != nil {
			t.Fatal(err)
		}
		mu.Lock()
		reads = 0
		mu.Unlock()
		v, err := ReadSolanaAt(ctx, providers, g, cfg, st, snap, 203, s4ClockZero.Add(time.Duration(i)*10*time.Second))
		if err != nil {
			t.Fatal(err)
		}
		mu.Lock()
		perReading = append(perReading, reads)
		mu.Unlock()
		_ = v
	}
	t.Logf("claims read by id per reading %v; unindexed %d", perReading, len(st.Unindexed))
	if perReading[0] != 2 || perReading[1] != 0 || perReading[2] != 0 {
		t.Fatalf("hlwatch read the settled credits again: %v", perReading)
	}
}

// ------------------------------------------------------------------------------------------- E5-SF3 --

// E5-SF3: a restarted signer's index began at its signing cursor, so a burn it signed and not credited yet — waiting
// for the relayer's next tick, or for hours while the route's credits are flagged, when an operator restarts — was in
// no index: the reading short by it, and a forged mint of its amount, burned straight back, was signed by every
// restarted signer (a deploy restarts them all). The burns it signed are put back from its store: bob's 300 stays in
// flight until its credit, the forged burn halts the signer and is never signed — before and after bob's credit.
func TestS6SignerRestartKeepsSignedInFlight(t *testing.T) {
	r := newS4Signer(t, false)
	r.n.slots(100, 100)
	r.round()
	bob := r.n.burn(r.cfg, 0, 101, s4Bob, 300) // bob's honest burn: signed, not credited yet
	r.n.supply(s4Token, 101, 700)
	r.n.slots(102, 102)
	r.round()
	r.round()
	if !r.signed(0) {
		t.Fatalf("bob's burn not signed: %v", r.s.Status())
	}
	r.s = r.newSigner(r.s.Store) // a restart
	indexed := len(r.s.index)
	r.n.supply(s4Token, 105, 1000)        // the forged mint …
	r.n.burn(r.cfg, 1, 106, s4Alice, 300) // … burned straight back
	r.n.supply(s4Token, 106, 700)
	for i := 0; i < 4; i++ {
		r.n.slots(110+uint64(i)*30, 110+uint64(i)*30)
		r.round()
	}
	st := r.s.Status()
	t.Logf("after the restart (its index %d record(s) at load): signing %v, halted %q, the forged burn signed %v", indexed, st["signing"], st["halted"], r.signed(1))
	if r.signed(1) || !strings.Contains(fmt.Sprint(st["halted"]), "broken") {
		t.Fatalf("the restart dropped bob's signed, uncredited burn from the in-flight count: the forged burn signed %v, halted %q", r.signed(1), st["halted"])
	}
	// bob's credit lands: owed now, in flight no more — the forged burn still breaks it
	r.g.set(s4Route+":json", s4RouteJSON(1000, 300, 0, 0, ""))
	r.g.set(s4Mailbox+":delivered/"+s4IDHex(bob), "150")
	r.g.set(s4Mailbox+":json", s4MailboxJSON(0, 1))
	r.n.slots(250, 250)
	r.round()
	if st := r.s.Status(); r.signed(1) || !strings.Contains(fmt.Sprint(st["halted"]), "broken") || r.s.index[s4IDHex(bob)].credited != 150 {
		t.Fatalf("after bob's credit: the forged burn signed %v, halted %q, bob's record %+v", r.signed(1), st["halted"], r.s.index[s4IDHex(bob)])
	}
}

// The same restart places the credits behind the signing cursor by the burns it signed: a store from before the book
// of credits (the live 3559a3da's state.json holds the cursors alone) opened one window over every credit, no verdict
// until the snapshot passed the head's block by the margin (≈16 s after the restart, measured by the audit); the
// signer's own credited burns place them now, and the first round after the restart has its verdict.
func TestS6SignerRestartPlacesItsCredits(t *testing.T) {
	r := newS4Signer(t, false)
	r.n.slots(100, 100)
	r.round()
	bob := r.n.burn(r.cfg, 0, 101, s4Bob, 300)
	r.n.supply(s4Token, 101, 700)
	r.n.slots(102, 102)
	r.round()
	r.round()
	r.g.set(s4Route+":json", s4RouteJSON(1000, 300, 0, 0, "")) // credited at 150 by the relayer
	r.g.set(s4Mailbox+":delivered/"+s4IDHex(bob), "150")
	r.g.set(s4Mailbox+":json", s4MailboxJSON(0, 1))
	r.round()
	if !r.signed(0) || r.s.Status()["signing"] != true {
		t.Fatalf("before the restart: signed %v, %v (%s)", r.signed(0), r.s.Status()["signing"], r.cut())
	}
	// the store as the live signers' is: state.json with the cursors alone, no book of credits
	r.s.mu.Lock()
	r.s.state.Credits = creditBook{}
	if err := r.s.save(); err != nil {
		r.s.mu.Unlock()
		t.Fatal(err)
	}
	r.s.mu.Unlock()
	r.s = r.newSigner(r.s.Store)
	r.n.slots(104, 104) // its slot ≈ 1.6 s after the head's block: inside the margin
	r.round()
	t.Logf("the first round after the restart: signing %v, cut %q", r.s.Status()["signing"], r.cut())
	if r.s.Status()["signing"] != true {
		t.Fatalf("the first round after a restart gave no verdict over the signer's own credited burn: %s", r.cut())
	}
}

// The store read back at load: a signature whose message does not hash to its file's id is corruption, named as such
// (a human restores or deletes it knowingly), never a record; a burn of nothing signed before E5-SF1 is no record.
func TestS6SignerReloadReadsTheStore(t *testing.T) {
	r := newS4Signer(t, false)
	r.n.slots(100, 100)
	r.round()
	r.n.burn(r.cfg, 0, 101, s4Bob, 300)
	r.n.supply(s4Token, 101, 700)
	r.n.slots(102, 102)
	r.round()
	r.round()
	sg, err := r.s.readSigned("sol", 0)
	if err != nil {
		t.Fatal(err)
	}
	zero := hl.Message{Version: 3, Nonce: 7, Origin: r.cfg.Solana.Domain, Sender: [32]byte(s4Token), Destination: hl.DomainGnoTest, Body: hl.TokenMessage(s4Alice, 0, nil)}
	z := *sg
	z.Nonce, z.ID, z.Message = 7, fmt.Sprintf("%x", zero.ID()), fmt.Sprintf("%x", zero.Bytes())
	if _, err := r.s.put("sol", z); err != nil {
		t.Fatal(err)
	}
	again := &Signer{Cfg: r.cfg, Store: r.s.Store}
	if err := again.load(); err != nil || len(again.index) != 1 || !again.signed[z.ID] {
		t.Fatalf("the store with a zero burn's signature: %v, %d record(s), signed %v", err, len(again.index), again.signed[z.ID])
	}
	bad := *sg
	bad.Nonce, bad.ID = 8, fmt.Sprintf("%x", s6ID("another"))
	if _, err := r.s.put("sol", bad); err != nil {
		t.Fatal(err)
	}
	again = &Signer{Cfg: r.cfg, Store: r.s.Store}
	if err := again.load(); err == nil || !strings.Contains(err.Error(), "corrupt") {
		t.Fatalf("a signature whose message is another id's loaded: %v", err)
	}
}

// ------------------------------------------------------------------------------- E5-SF5's class: marks --

// s6Reads counts a gno fake's delivered-mark renders and its vm/qeval reads.
type s6Reads struct {
	mu    sync.Mutex
	marks int
}

func (c *s6Reads) hook(g *s4Gno) {
	g.hook(func(_, path string) {
		if strings.HasPrefix(path, "delivered/") {
			c.mu.Lock()
			c.marks++
			c.mu.Unlock()
		}
	})
}

func (c *s6Reads) take(g *s4Gno) (marks, evals int) {
	c.mu.Lock()
	marks, c.marks = c.marks, 0
	c.mu.Unlock()
	g.mu.Lock()
	evals, g.evals = g.evals, 0
	g.mu.Unlock()
	return marks, evals
}

// E5-SF5's class, which the dust below a route's fee the relayer now leaves (E5-GS5) would reopen: a burn nobody credits
// stays in every signer's index uncredited, and each round read its delivered mark again — a stranger's burns at a
// Solana fee each, a render each a round, for good. A mark read unset is read again only once hlmailbox's delivered
// count moved (a mark is set only with its rise), and a pile of them is read as the done tree, a hundred ids a vm/qeval.
func TestS6SignerReadsUncreditedMarksOnce(t *testing.T) {
	r := newS4Signer(t, false)
	r.n.slots(100, 100)
	r.round()
	const n = 300
	var ids [][32]byte
	for i := 0; i < n; i++ { // dust: one base unit each, which the relayer leaves
		ids = append(ids, r.n.burn(r.cfg, uint32(i), 101, s4Alice, 1))
	}
	r.n.supply(s4Token, 101, 1000-n)
	var c s6Reads
	c.hook(r.g)
	var per []string
	for i := 0; i < 4; i++ {
		if i == 3 { // someone credits one of them at his own cost: the count moves, the marks are read once more
			r.g.set(s4Mailbox+":delivered/"+s4IDHex(ids[7]), "150")
			r.g.set(s4Mailbox+":json", s4MailboxJSON(0, 1))
			r.g.set(s4Route+":json", s4RouteJSON(1000, 0, 0, 0, "")) // swept to GovDAO: owed 1 … rounded away here
		}
		r.n.slots(110+uint64(i)*10, 110+uint64(i)*10)
		c.take(r.g)
		r.round()
		marks, evals := c.take(r.g)
		per = append(per, fmt.Sprintf("%d renders + %d qevals", marks, evals))
	}
	t.Logf("%d dust burns: delivered-mark reads a round %v; signing %v; credited %d", n, per, r.s.Status()["signing"], r.s.index[s4IDHex(ids[7])].credited)
	if per[1] != "0 renders + 0 qevals" || per[2] != "0 renders + 0 qevals" {
		t.Fatalf("every round read the marks of the burns nobody credits again: %v", per)
	}
	if r.s.index[s4IDHex(ids[7])].credited != 150 || per[3] == "0 renders + 0 qevals" {
		t.Fatalf("a credit landing was not read: %v, the record %+v", per, r.s.index[s4IDHex(ids[7])])
	}
}

// The monitor's poll the same (and hlwatch's reading, which shares readMarks).
func TestS6MonitorReadsUncreditedMarksOnce(t *testing.T) {
	r := newS4Monitor(t, false)
	r.n.slots(100, 100)
	r.poll()
	const n = 300
	for i := 0; i < n; i++ {
		r.n.burn(r.cfg, uint32(i), 101, s4Alice, 1)
	}
	r.n.supply(s4Token, 101, 1000-n)
	var c s6Reads
	c.hook(r.g)
	var per []string
	for i := 0; i < 3; i++ {
		r.n.slots(110+uint64(i)*25, 110+uint64(i)*25)
		r.step(10 * time.Second)
		c.take(r.g)
		r.poll()
		marks, evals := c.take(r.g)
		per = append(per, fmt.Sprintf("%d renders + %d qevals", marks, evals))
	}
	t.Logf("%d dust burns: delivered-mark reads a poll %v; verdict %s", n, per, r.verdict())
	if r.verdict() != "holds" || per[1] != "0 renders + 0 qevals" || per[2] != "0 renders + 0 qevals" {
		t.Fatalf("every poll read the marks of the burns nobody credits again: %v (%s, %s)", per, r.verdict(), r.cut())
	}
}

// ------------------------------------------------------------------------------------------- E5-SF4 --

// s6Unlisted is a provider in front of upstream whose signature listings leave out one transaction (an honest provider
// beside a liar who invented it, or one whose listing omits a real one); every other answer is upstream's.
func s6Unlisted(t *testing.T, upstream, sig string) string {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		body, _ := io.ReadAll(req.Body)
		resp, err := http.Post(upstream, "application/json", bytes.NewReader(body))
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		out, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if bytes.Contains(body, []byte(`"getSignaturesForAddress"`)) {
			var j map[string]any
			if json.Unmarshal(out, &j) == nil {
				if list, ok := j["result"].([]any); ok {
					kept := []any{}
					for _, e := range list {
						if m, ok := e.(map[string]any); ok && m["signature"] == sig {
							continue
						}
						kept = append(kept, e)
					}
					j["result"] = kept
					out, _ = json.Marshal(j)
				}
			}
		}
		_, _ = w.Write(out)
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// lastSig is the signature of the last transaction toward the token program (the burn just made).
func (n *solNet) lastSig() string {
	n.mu.Lock()
	defer n.mu.Unlock()
	all := n.sigs[s4Token]
	return all[len(all)-1].sig.String()
}

// E5-SF4: the monitors took a burn from either provider's listing and counted every undisputed record in flight,
// whatever provider showed it: provider 2 inventing a burn of 300 (the supplies, which both must agree on, never moved)
// read as BROKEN — credits and payouts flagged, the pausable ISM paused — on one provider's word. A burn one provider
// alone lists at or below its route's supply slot is no reading now: its token has no verdict (alarmed once it has
// stood two minutes), never a flag. Nor is it dropped: a real burn provider 1's listing omits leaves the token without a
// verdict (not a reading short by it) until its credit proves it.
func TestS6MonitorOneProviderBurnIsNoVerdict(t *testing.T) {
	r := newS4Monitor(t, false)
	r.n.slots(100, 100)
	r.poll()
	if v := r.verdict(); v != "holds" {
		t.Fatalf("poll 1: %s (%s)", v, r.cut())
	}
	r.n.burn(r.cfg, 0, 101, s4Alice, 300) // provider 2's invention: no supply change
	r.m.sol = [2]*rpc.Client{rpc.New(s6Unlisted(t, r.n.urls[0], r.n.lastSig())), rpc.New(r.n.urls[1])}
	r.m.both = twoProviders(r.m.sol)
	for i := 0; i < 14; i++ { // two minutes and more
		r.n.slots(102+uint64(i), 102+uint64(i))
		r.step(10 * time.Second)
		r.poll()
		if r.alarmed("identity:BUBBLE:flag") || r.verdict() == "BROKEN" {
			t.Fatalf("poll %d: one provider's invented burn read as a break: %s, flags %v", i+2, r.verdict(), r.m.Alarm.StandingKeys())
		}
	}
	t.Logf("provider 2's invented burn: %s; alarms %v", r.verdict(), r.m.Alarm.StandingKeys())
	if r.verdict() != "no verdict" || !r.alarmed("solana:unconfirmed:BUBBLE") {
		t.Fatalf("one provider's burn: %s, alarms %v", r.verdict(), r.m.Alarm.StandingKeys())
	}
}

// The other half: a real burn of bob's that provider 1's listing omits is not dropped (the reading short by 300 would
// hide a forged mint of that much): no verdict until the signers' credit proves it, then the identity holds.
func TestS6MonitorOneProviderRealBurnIsNotDropped(t *testing.T) {
	r := newS4Monitor(t, false)
	r.n.slots(100, 100)
	r.poll()
	bob := r.n.burn(r.cfg, 0, 101, s4Bob, 300)
	r.n.supply(s4Token, 101, 700)
	r.n.supply(s4Token, 102, 1000) // and a forged mint of 300, which a reading short by bob's burn would pass
	r.m.sol = [2]*rpc.Client{rpc.New(s6Unlisted(t, r.n.urls[0], r.n.lastSig())), rpc.New(r.n.urls[1])}
	r.m.both = twoProviders(r.m.sol)
	r.n.slots(103, 103)
	r.step(10 * time.Second)
	r.poll()
	if v := r.verdict(); v != "no verdict" || r.alarmed("identity:BUBBLE:flag") {
		t.Fatalf("a real burn one provider's listing omits: %s (%s)", v, r.cut())
	}
	// credited by the relayer (the signers' providers list it): owed 300, its mark set — the burn proven, the forged mint shows
	r.g.set(s4Route+":json", s4RouteJSON(1000, 300, 0, 0, ""))
	r.g.set(s4Mailbox+":delivered/"+s4IDHex(bob), "150")
	r.g.set(s4Route+":claim/"+s4IDHex(bob), s4Claim(bob, s4Bob, 300, 150))
	r.g.set(s4Mailbox+":json", s4MailboxJSON(0, 1))
	r.n.slots(130, 130)
	r.step(10 * time.Second)
	r.poll()
	if v := r.verdict(); v != "BROKEN" {
		t.Fatalf("once credited: %s (%s)", v, r.cut())
	}
}

// The signer the same: provider 2's invented burn halted it as a break (signer:halt, "the supply identity is broken")
// on one provider's word; now the round has no verdict, the halt never raised, and nothing it alone shows is signed.
func TestS6SignerOneProviderBurnIsNoVerdict(t *testing.T) {
	r := newS4Signer(t, false)
	r.n.slots(100, 100)
	r.round()
	r.n.burn(r.cfg, 0, 101, s4Alice, 300) // provider 2's invention
	r.s.sol = [2]*rpc.Client{rpc.New(s6Unlisted(t, r.n.urls[0], r.n.lastSig())), rpc.New(r.n.urls[1])}
	for i := 0; i < 3; i++ {
		r.n.slots(102+uint64(i)*10, 102+uint64(i)*10)
		r.round()
	}
	st := r.s.Status()
	t.Logf("provider 2's invented burn: halted %q, cut %q, signed %v", st["halted"], r.cut(), r.signed(0))
	if st["halted"] != "" || r.alarmed("signer:halt") || r.signed(0) {
		t.Fatalf("one provider's invented burn halted the signer: %q", st["halted"])
	}
	if !strings.Contains(r.cut(), "listed by provider 2 alone") {
		t.Fatalf("the round over one provider's burn: %q", r.cut())
	}
}

// hlwatch prints it UNCONFIRMED and gives the token no identity.
func TestS6HlwatchOneProviderBurnUnconfirmed(t *testing.T) {
	cfg, n, g := s4Config(false), newSolNet(t), newS4Gno()
	g.set(s4Mailbox+":json", s4MailboxJSON(0, 0))
	g.set(s4Route+":json", s4RouteJSON(1000, 0, 0, 0, ""))
	n.supply(s4Token, 1, 1000)
	n.burn(cfg, 0, 101, s4Alice, 300) // provider 2's invention
	providers := [2]*rpc.Client{rpc.New(s6Unlisted(t, n.urls[0], n.lastSig())), rpc.New(n.urls[1])}
	n.slots(102, 102)
	st := &WatchState{}
	st.init()
	ctx := context.Background()
	snap, err := TakeSnapshot(ctx, providers, cfg)
	if err != nil {
		t.Fatal(err)
	}
	v, err := ReadSolanaAt(ctx, providers, g, cfg, st, snap, 200, s4ClockZero)
	if err != nil {
		t.Fatal(err)
	}
	id, judged := v.Identities["BUBBLE"]
	verdict, _ := id.Verdict()
	t.Logf("judged %v %s; unconfirmed %q; errors %q", judged, verdict, v.Unconfirmed, v.Err)
	if judged || !strings.Contains(strings.Join(v.Unconfirmed, "; "), "listed by provider 2 alone") {
		t.Fatalf("hlwatch over one provider's invented burn: judged %v (%s), unconfirmed %q", judged, verdict, v.Unconfirmed)
	}
}

// ------------------------------------------------------------------------------------------- E5-SF2 --

// E5-SF2: the listing guard (minContextSlot at the supply's slot, E4-SF3) rests on the providers honouring it, and a
// listing answer carries no context slot to check: Alchemy ignores it. Two providers that ignore it, their listings four
// slots behind the supplies' (110 against 114), left the forger's own burn at 112 — out of the supply — out of the index:
// the forged mint passed as holding. Each provider is asked at the first poll and hourly for a listing far past its view:
// one that answers is alarmed (solana:min-context-ignored:<i>) and refused — no identity and no forged-credit judgement
// rest on the index while the config names it; the rest of the poll runs. A provider that honours it again clears it at
// the next hourly ask.
func TestS6MonitorRefusesAProviderThatIgnoresMinContext(t *testing.T) {
	r := newS4Monitor(t, false)
	r.n.ignoreMinList = [2]bool{true, true}
	r.n.supply(s4Token, 101, 1300)        // a forged mint of 300 …
	r.n.burn(r.cfg, 0, 102, s4Alice, 300) // … burned straight back
	r.n.supply(s4Token, 102, 1000)
	r.n.burn(r.cfg, 1, 112, s4Alice, 300) // and 300 of the forger's own
	r.n.supply(s4Token, 112, 700)
	r.n.slots(114, 114)
	r.n.lagListing(0, 4) // listed at 110: the burn at 102 seen, the one at 112 not
	r.n.lagListing(1, 4)
	r.poll()
	t.Logf("poll 1: %s (%s); alarms %v", r.verdict(), r.cut(), r.m.Alarm.StandingKeys())
	if v := r.verdict(); v == "holds" || r.alarmed("identity:BUBBLE:flag") {
		t.Fatalf("listings that ignore minContextSlot hid the forger's own burn: %s", v)
	}
	if !r.alarmed("solana:min-context-ignored:1") || !r.alarmed("solana:min-context-ignored:2") {
		t.Fatalf("the providers that ignore minContextSlot were not alarmed: %v", r.m.Alarm.StandingKeys())
	}
	// the providers honour it again (a config naming others): refused until the next hourly ask, then judged
	r.n.ignoreMinList = [2]bool{false, false}
	r.n.lagListing(0, 0)
	r.n.lagListing(1, 0)
	r.step(10 * time.Second)
	r.poll()
	if v := r.verdict(); v != "no verdict" {
		t.Fatalf("asked again before the hour: %s", v)
	}
	r.step(time.Hour)
	r.g.setHead(201, r.clock) // the node's head an hour on
	r.poll()
	if v := r.verdict(); v != "BROKEN" || r.alarmed("solana:min-context-ignored:1") || r.alarmed("solana:min-context-ignored:2") {
		t.Fatalf("an hour on, both honouring: %s, alarms %v", v, r.m.Alarm.StandingKeys())
	}
}

// One provider that ignores it is refused the same (the alarm names it); two that honour it are never refused.
func TestS6MonitorOneProviderIgnoresMinContext(t *testing.T) {
	r := newS4Monitor(t, false)
	r.n.ignoreMinList = [2]bool{false, true}
	r.n.slots(100, 100)
	r.poll()
	if v := r.verdict(); v != "no verdict" || !r.alarmed("solana:min-context-ignored:2") || r.alarmed("solana:min-context-ignored:1") {
		t.Fatalf("provider 2 ignoring minContextSlot: %s, alarms %v", v, r.m.Alarm.StandingKeys())
	}
	honest := newS4Monitor(t, false)
	honest.n.slots(100, 100)
	honest.poll()
	if v := honest.verdict(); v != "holds" || len(honest.m.Alarm.StandingKeys()) != 0 {
		t.Fatalf("two providers that honour it: %s, alarms %v", v, honest.m.Alarm.StandingKeys())
	}
}

// The signer the same: the forged burn hidden by the lagging listings was signed; now the round is no verdict.
func TestS6SignerRefusesAProviderThatIgnoresMinContext(t *testing.T) {
	r := newS4Signer(t, false)
	r.n.ignoreMinList = [2]bool{true, true}
	r.n.supply(s4Token, 101, 1300)        // a forged mint of 300 …
	r.n.burn(r.cfg, 0, 102, s4Alice, 300) // … burned straight back: the burn the forger wants signed
	r.n.supply(s4Token, 102, 1000)
	r.n.burn(r.cfg, 1, 112, s4Alice, 300) // and 300 of his own, which the lagging listings do not show yet
	r.n.supply(s4Token, 112, 700)
	r.n.slots(114, 114)
	r.n.lagListing(0, 4)
	r.n.lagListing(1, 4)
	r.round()
	r.round()
	t.Logf("signing %v, halted %q, cut %q, the forged burn signed %v", r.s.Status()["signing"], r.s.Status()["halted"], r.cut(), r.signed(0))
	if r.signed(0) {
		t.Fatalf("listings that ignore minContextSlot hid the forger's own burn: the forged burn signed")
	}
	if !r.alarmed("solana:min-context-ignored:1") || !strings.Contains(r.cut(), "ignores minContextSlot") {
		t.Fatalf("the signer over providers that ignore minContextSlot: %q, alarms %v", r.cut(), r.s.Alarm.StandingKeys())
	}
}

// hlwatch gives no identity over such a provider and says why.
func TestS6HlwatchRefusesAProviderThatIgnoresMinContext(t *testing.T) {
	cfg, n, g := s4Config(false), newSolNet(t), newS4Gno()
	g.set(s4Mailbox+":json", s4MailboxJSON(0, 0))
	g.set(s4Route+":json", s4RouteJSON(1000, 0, 0, 0, ""))
	n.supply(s4Token, 1, 1000)
	n.ignoreMinList = [2]bool{false, true}
	n.slots(102, 102)
	providers := n.clients()
	st := &WatchState{}
	st.init()
	ctx := context.Background()
	snap, err := TakeSnapshot(ctx, providers, cfg)
	if err != nil {
		t.Fatal(err)
	}
	v, err := ReadSolanaAt(ctx, providers, g, cfg, st, snap, 200, s4ClockZero)
	if err != nil {
		t.Fatal(err)
	}
	_, judged := v.Identities["BUBBLE"]
	t.Logf("judged %v; errors %q", judged, v.Err)
	if judged || !strings.Contains(strings.Join(v.Err, "; "), "provider 2 ignores minContextSlot") {
		t.Fatalf("hlwatch over a provider that ignores minContextSlot: judged %v, errors %q", judged, v.Err)
	}
}

// ------------------------------------------------------------------------------------- E5-GS1's sibling --

// s6Meter is p/hlroute's Meter: a level refilled at rate a day up to ceil, its remainder carried (base units × seconds).
type s6Meter struct{ level, at, frac int64 }

func (m *s6Meter) refill(now, rate, ceil int64) {
	const day = 86400
	if now <= m.at {
		return
	}
	el := now - m.at
	if el > day {
		el, m.frac = day, 0
	}
	num := (rate%day)*el + m.frac
	m.level += (rate/day)*el + num/day
	m.frac = num % day
	if m.level > ceil {
		m.level, m.frac = ceil, 0
	}
	m.at = now
}

// The Solana GNOT route's Claim(20) gas on a gno v1.5.1 node (lane s6's measure, /root/src/s6-measure on the devbox: the
// realm at e2e87c58, one recipient waiting): a Claim that pays nothing, the first installment, every other installment.
const (
	s6GasDry    = 27_042_011
	s6GasFirst  = 28_935_895
	s6GasSliver = 27_926_107
)

// s6ClaimChain is the live GNOT route with one claim credited to a fresh recipient, p/hlroute's Claim run on its meters
// at each Claim's block time (the relayer's clock plus the block it waits for), the gas each Claim uses and the fee the
// relayer's key pays for it at gnoland-1's floor price (GasFor × 0.001 ugnot).
type s6ClaimChain struct {
	clock          *time.Time
	block          time.Duration
	fee, cap, lock int64
	bridge, rcpt   s6Meter
	amount, paid   int64
	paidTotal      int64
	calls, dry     int
	fees           int64     // ugnot
	done           time.Time // when the claim was paid in full
	route, mailbox string
}

func (c *s6ClaimChain) now() int64 { return c.clock.Unix() }

func (c *s6ClaimChain) RenderAt(_ context.Context, pkg, path string, _ int64) (string, error) {
	switch {
	case pkg == c.mailbox && path == "json":
		return fmt.Sprintf(`{"domain":%d,"count":0,"delivered":1,"live":true,"routes":{}}`, hl.DomainGnoTest), nil
	case pkg == c.route && path == "json":
		b := c.bridge
		b.refill(c.now(), c.cap, c.cap)
		open := 0
		if c.paid < c.amount {
			open = 1
		}
		return fmt.Sprintf(`{"live":true,"symbol":"GNOT","lock":%d,"paidTotal":%d,"owed":%d,"cap":%d,"meter":%d,"fee":%d,"openClaims":%d,"claims":1,"migratedTo":"","paused":""}`,
			c.lock, c.paidTotal, c.amount-c.paid, c.cap, b.level, c.fee, open), nil
	}
	return "", fmt.Errorf("s6ClaimChain: no render %s:%s", pkg, path)
}

func (c *s6ClaimChain) Status(context.Context) (gno.Head, error) {
	return gno.Head{Height: 1000, Time: *c.clock}, nil
}

func (c *s6ClaimChain) Block(_ context.Context, h int64) (gno.Head, error) {
	return gno.Head{Height: h, Time: *c.clock}, nil
}

// Call runs p/hlroute's Claim for the one recipient in the block the call lands in.
func (c *s6ClaimChain) Call(_ context.Context, pkg, fn string, _ int64, _ ...string) (*gno.Result, error) {
	if pkg != c.route || fn != "Claim" {
		return nil, fmt.Errorf("s6ClaimChain: no call %s.%s", pkg, fn)
	}
	*c.clock = c.clock.Add(c.block) // the relayer waits for the block; the realm's now is its time
	now := c.now()
	c.bridge.refill(now, c.cap, c.cap)
	c.rcpt.refill(now, c.cap/4, c.cap/4)
	avail := min(c.bridge.level, c.rcpt.level, c.lock)
	used, count := int64(s6GasDry), 0
	if c.paid < c.amount {
		fee := int64(0)
		ok := avail > 0
		if c.paid == 0 {
			fee = min(c.fee, c.amount)
			ok = avail >= fee
		}
		if ok {
			pay := min(c.amount-c.paid-fee, avail-fee)
			total := fee + pay
			used = s6GasSliver
			if c.paid == 0 {
				used = s6GasFirst
			}
			c.paid += total
			if c.paid == c.amount {
				c.done = *c.clock
			}
			c.paidTotal += total
			c.lock -= total
			c.bridge.level -= total
			c.rcpt.level -= total
			count = 1
		}
	}
	c.calls++
	if count == 0 {
		c.dry++
	}
	c.fees += gno.GasFor(used) / 1000
	return &gno.Result{Hash: fmt.Sprintf("tx%d", c.calls), Height: int64(1000 + c.calls), GasUsed: used, Events: []gno.Event{{Type: "Claimed", Attrs: map[string]string{"count": fmt.Sprint(count)}}}}, nil
}

// s6PayClaim runs the relayer's Claim loop (3 s ticks; a Claim waits ≈3.43 s for its gnoland-1 block) over one claim of
// amount ugnot to a fresh recipient on the live GNOT route (fee 5 GNOT, cap = its 10,000 GNOT floor, the route's meter
// full), until it is paid in full: the Claims, the dry ones, the relayer's fees and how long it took.
func s6PayClaim(t *testing.T, amount int64) (*s6ClaimChain, time.Duration) {
	start := s4ClockZero
	clock := start
	c := &s6ClaimChain{clock: &clock, block: 3430 * time.Millisecond, fee: 5_000_000, cap: 10_000_000_000, lock: 130_000_000 + amount,
		amount: amount, route: testRoute, mailbox: testMailbox}
	c.bridge = s6Meter{level: c.cap, at: clock.Unix()}
	c.rcpt = s6Meter{at: clock.Unix()} // a fresh recipient's meter: empty at its credit
	f := newFake()
	r := testRelayer(f, "primary")
	r.Cfg.Routes[0].Symbol = "GNOT"
	r.gno = c
	r.now = func() time.Time { return clock }
	ctx := context.Background()
	for i := 0; c.paid < c.amount && i < 2_000_000; i++ {
		_ = r.claims(ctx)
		clock = clock.Add(3 * time.Second)
		if cs := r.state.Claims["GNOT"]; cs != nil && cs.Next.After(clock) {
			clock = clock.Add(cs.Next.Sub(clock).Truncate(3*time.Second) + 3*time.Second) // idle ticks until it is due
		}
	}
	return c, c.done.Sub(start)
}

// E5-GS1's Solana sibling, measured: a claim paid in installments kept the relayer calling Claim every tick — each Claim
// paid the recipient's meter's refill since the last (≈0.1 GNOT) and reset the backoff — ≈5,400 Claims and ≈190 GNOT of
// the relayer's gno key for one transfer of 1,000 GNOT to a fresh recipient on the live route (r/hlfees collects its
// 5 GNOT fee), ≈4,700 GNOT for one of 25,000 (the per-message maximum): the live relayer holds ≈100. A sliver counts as
// nothing paid now, and an installment that leaves the meter empty waits for it to refill — the backoff doubling to an
// hour — so each Claim pays what the wait refilled: under a fee per 1,000 GNOT, and the claim still paid in full as soon
// as its recipient's meter allows (≈0.4 day per 1,000 GNOT at the launch cap, less an hour at most).
func TestS6ClaimFeesPerTransfer(t *testing.T) {
	for _, amount := range []int64{1_000_000_000, 25_000_000_000} {
		c, took := s6PayClaim(t, amount)
		t.Logf("a claim of %d GNOT to a fresh recipient: %d Claims (%d paying nothing), %.3f GNOT of the relayer's fees, paid in full in %s",
			amount/1_000_000, c.calls, c.dry, float64(c.fees)/1e6, took.Round(time.Minute))
		if c.paid < c.amount {
			t.Fatalf("not paid in full: %d of %d", c.paid, c.amount)
		}
		if bound := c.fee * (amount / 1_000_000_000); c.fees > bound {
			t.Errorf("a claim of %d GNOT cost the relayer %.3f GNOT of Claims, over a fee per 1,000 GNOT (%.0f GNOT)", amount/1_000_000, float64(c.fees)/1e6, float64(bound)/1e6)
		}
		if meterBound := time.Duration(float64(amount) / float64(c.cap/4) * float64(24*time.Hour)); took > meterBound+time.Hour+10*time.Minute {
			t.Errorf("paid in %s, past the meter's own pace (%s) by more than an hour", took, meterBound)
		}
	}
}

// ------------------------------------------------------------------------------- I2's Claim batch, carried --

// s6GasChain is the relayer's gno node with the gno client's gas rule on Claim (I2's i2ClaimHost on bridge-eth): a
// Claim(n) pays min(n, open) claims, a fee's worth each (the route's paidTotal moves by a token a claim), using
// cost(paid) gas; the client simulates under the gas asked and once more under four times it when that is within the
// block's, and a Claim past both runs out of gas.
type s6GasChain struct {
	*fakeChain
	cost  func(paid int) int64
	open  int
	paid  int
	calls []string
	oog   int
}

func (h *s6GasChain) Call(ctx context.Context, pkg, fn string, gas int64, args ...string) (*gno.Result, error) {
	if fn != "Claim" {
		return h.fakeChain.Call(ctx, pkg, fn, gas, args...)
	}
	n, _ := strconv.Atoi(args[0])
	paid := min(n, h.open)
	used := h.cost(paid)
	h.calls = append(h.calls, fmt.Sprintf("Claim(%d)@%dM", n, gas/1_000_000))
	if used > gas && (4*gas > gno.MaxBlockGas || used > 4*gas) {
		h.oog++
		return nil, &gno.CallError{Pkg: pkg, Fn: fn, Err: errors.New("exit status 1"), Output: fmt.Sprintf("--= Error =--\nData: out of gas error\nMsg Traces:\n    0  gas used: %d (gas wanted %d)", min(used, 4*gas), gas)}
	}
	h.open -= paid
	h.paid += paid
	h.fakeChain.mu.Lock()
	h.fakeChain.renders[testRoute+":json"] = routeRender(int64(h.open), 50, int64(h.paid)*1_000_000, "")
	h.fakeChain.mu.Unlock()
	return &gno.Result{Hash: fmt.Sprintf("TX%d", len(h.calls)), GasUsed: used, Events: []gno.Event{{Type: "Claimed", Attrs: map[string]string{"count": strconv.Itoa(paid)}}}}, nil
}

// s6GasRig is a primary relayer over a route with 400 claims open, its meter holding the fee.
func s6GasRig(cost func(int) int64) (*Relayer, *s6GasChain) {
	f := newFake()
	f.renders[testRoute+":json"] = routeRender(400, 50, 0, "")
	h := &s6GasChain{fakeChain: f, cost: cost, open: 400}
	r := testRelayer(f, "primary")
	r.gno = h
	return r, h
}

// s6Ticks runs the Claim loop n times, a minute and a second apart (an out-of-gas is transient: retried in a minute).
func s6Ticks(r *Relayer, n int) {
	for i := 0; i < n; i++ {
		_ = r.claims(context.Background())
		advance(r, 61*time.Second)
	}
}

func s6Standing(r *Relayer, key string) bool {
	_, ok := r.Alarm.Standing()[key]
	return ok
}

// I2's measure on bridge-eth, the same code here: the relayer called Claim(20) under its 45M, which the gno client
// simulates once more under four times it (180M), and an out-of-gas is transient — a Claim(20) past 180M (60M and 7M a
// claim: 200M) ran out every minute for good, paying nothing, alarming nothing. The batch halves on an out-of-gas
// (Claim(10), 130M, fits), tries one more (Claim(11), 137M, past the 135M room) and stays at the most that fits.
func TestS6ClaimBatchFitsTheClientsGas(t *testing.T) {
	r, h := s6GasRig(func(p int) int64 { return 60_000_000 + 7_000_000*int64(p) })
	s6Ticks(r, 6)
	t.Logf("Claim(20) at 200M: calls %v; paid %d; out of gas %d", h.calls, h.paid, h.oog)
	if h.paid == 0 || h.oog != 1 || strings.Join(h.calls, " ") != "Claim(20)@45M Claim(10)@45M Claim(11)@45M Claim(10)@45M Claim(10)@45M Claim(10)@45M" {
		t.Fatalf("the Claim batch did not settle at the most the client's gas fits: calls %v, paid %d", h.calls, h.paid)
	}
}

// A Claim of one using more than half the room (100M and 7M a claim) grows by one at least while a full batch fits, to
// the most that fits (Claim(5), 135M), stopping below the size that went over (I2's review, finding 2).
func TestS6ClaimBatchGrowsPastAHighFixedCost(t *testing.T) {
	r, h := s6GasRig(func(p int) int64 { return 100_000_000 + 7_000_000*int64(p) })
	r.state.Claims["BUBBLE"] = &claimState{Batch: 1}
	s6Ticks(r, 8)
	t.Logf("a fixed cost of 100M: calls %v; out of gas %d", h.calls, h.oog)
	if strings.Join(h.calls, " ") != "Claim(1)@45M Claim(2)@45M Claim(3)@45M Claim(4)@45M Claim(5)@45M Claim(6)@45M Claim(5)@45M Claim(5)@45M" || h.oog != 0 {
		t.Fatalf("the batch did not grow past a Claim of one: %v", h.calls)
	}
}

// A Claim that fits the 180M but not three quarters of it (40M and 5.5M a claim: Claim(20) 150M) is scaled to the room
// before any call runs out (Claim(18), then 17), and grows back to twenty once the state is lighter.
func TestS6ClaimBatchShrinksBeforeItRunsOut(t *testing.T) {
	r, h := s6GasRig(func(p int) int64 { return 40_000_000 + 5_500_000*int64(p) })
	s6Ticks(r, 4)
	t.Logf("Claim(20) at 150M: calls %v; out of gas %d", h.calls, h.oog)
	if strings.Join(h.calls, " ") != "Claim(20)@45M Claim(18)@45M Claim(17)@45M Claim(17)@45M" || h.oog != 0 {
		t.Fatalf("the batch was not sized to three quarters of the client's ceiling: %v", h.calls)
	}
	h.cost = func(p int) int64 { return 20_000_000 + 4_000_000*int64(p) }
	s6Ticks(r, 2)
	if last := h.calls[len(h.calls)-1]; last != "Claim(20)@45M" {
		t.Fatalf("the batch did not grow back: %v", h.calls)
	}
}

// p/hlroute's first Claim after Launch walks every queue (ResetMeters: 429M at 400 queues) whatever the batch: halved to
// one, then the gas raised four times (simulated under 180M, then 720M), alarmed claim:gas:<symbol>; once a Claim goes the
// alarm clears, and once a Claim fits the relayer's own room the gas goes back to it and the batch grows.
func TestS6ClaimOfOneRaisesTheGas(t *testing.T) {
	walked := false
	r, h := s6GasRig(nil)
	h.cost = func(p int) int64 {
		if !walked {
			return 429_000_000
		}
		return 60_000_000 + 7_000_000*int64(p)
	}
	for i := 0; i < 6 && h.paid == 0; i++ {
		s6Ticks(r, 1)
		if i == 4 && !s6Standing(r, "claim:gas:BUBBLE") {
			t.Fatalf("a Claim of one out of gas was not alarmed: calls %v", h.calls)
		}
	}
	walked = h.paid > 0
	s6Ticks(r, 3)
	t.Logf("the walk at 429M: calls %v; paid %d; alarms %v", h.calls, h.paid, r.Alarm.StandingKeys())
	if want := "Claim(20)@45M Claim(10)@45M Claim(5)@45M Claim(2)@45M Claim(1)@45M Claim(1)@180M Claim(2)@180M Claim(3)@45M Claim(5)@45M"; strings.Join(h.calls, " ") != want {
		t.Fatalf("the walk's Claim: calls %v, want %s", h.calls, want)
	}
	if s6Standing(r, "claim:gas:BUBBLE") {
		t.Fatalf("the alarm stands after the Claim went: %v", r.Alarm.StandingKeys())
	}
}

// A Claim of one past the block's gas — raised to a quarter of it, the client's retry then the block's — is stuck: tried
// hourly, alarmed as such (I2's review, finding 6).
func TestS6ClaimPastTheBlockIsTriedHourly(t *testing.T) {
	r, h := s6GasRig(func(int) int64 { return 3_100_000_000 })
	s6Ticks(r, 10)
	text := r.Alarm.Standing()["claim:gas:BUBBLE"]
	t.Logf("past the block: calls %v; alarm %q", h.calls, text)
	if strings.Join(h.calls, " ") != "Claim(20)@45M Claim(10)@45M Claim(5)@45M Claim(2)@45M Claim(1)@45M Claim(1)@180M Claim(1)@720M Claim(1)@750M" || !strings.Contains(text, "payouts stand still") {
		t.Fatalf("a Claim past the block's gas, stuck: calls %v, alarm %q", h.calls, text)
	}
	advance(r, 61*time.Minute)
	s6Ticks(r, 1)
	if last := h.calls[len(h.calls)-1]; len(h.calls) != 9 || last != "Claim(1)@750M" {
		t.Fatalf("the stuck Claim was not tried again after an hour: %v", h.calls)
	}
}

// claim:gas:<symbol> ends with the payouts moving, or nothing left to pay (I2's review, finding 5).
func TestS6ClaimGasAlarmEndsWhenTheRouteIsIdle(t *testing.T) {
	r, h := s6GasRig(func(int) int64 { return 429_000_000 })
	s6Ticks(r, 5)
	if !s6Standing(r, "claim:gas:BUBBLE") {
		t.Fatalf("a Claim of one out of gas was not alarmed: calls %v", h.calls)
	}
	h.fakeChain.mu.Lock()
	h.fakeChain.renders[testRoute+":json"] = routeRender(0, 50, 0, "")
	h.fakeChain.mu.Unlock()
	s6Ticks(r, 1)
	if s6Standing(r, "claim:gas:BUBBLE") {
		t.Fatalf("claim:gas stands over a route with no open claim: %v", r.Alarm.StandingKeys())
	}
}

// ------------------------------------------------------------------------------------- E5-GS5's sibling --

// s6Hook is a webhook that keeps the alarms posted to it, by key.
type s6Hook struct {
	mu   sync.Mutex
	keys []string
}

func (h *s6Hook) serve(t *testing.T) string {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		var j struct {
			Level, Key string
		}
		_ = json.NewDecoder(req.Body).Decode(&j)
		h.mu.Lock()
		h.keys = append(h.keys, j.Level+" "+j.Key)
		h.mu.Unlock()
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// posted waits a moment for the webhook's posts (the Alarmer posts in a goroutine) and answers them.
func (h *s6Hook) posted(want int) []string {
	for i := 0; i < 100; i++ {
		h.mu.Lock()
		n := len(h.keys)
		h.mu.Unlock()
		if n >= want {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	time.Sleep(50 * time.Millisecond)
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string{}, h.keys...)
}

// E5-GS5's Solana sibling: every inbound credit locks a storage deposit from the relayer's gno key (≈0.76 GNOT for a
// sweep entry, measured), and a burn of a few base units was relayed and credited like any other — swept, no fee — so
// ≈130 of a stranger's dust burns (a Solana fee each) emptied the live relayer's ≈100 GNOT, every later Process failing.
// A burn below the route's fee is left now: never Processed, alarmed once, out of the pending list; the burn beside it is
// delivered as before.
func TestS6RelayerLeavesDust(t *testing.T) {
	sg := newS4Signer(t, false) // the signers sign every burn of something, dust included: anyone may bring it
	sg.n.slots(100, 100)
	sg.round()
	dust := sg.n.burn(sg.cfg, 0, 101, s4Alice, 3) // below the route's fee of 10
	honest := sg.n.burn(sg.cfg, 1, 102, s4Bob, 50)
	sg.n.supply(s4Token, 102, 1000-53)
	sg.n.slots(103, 103)
	sg.round()
	sg.round()
	if !sg.signed(0) || !sg.signed(1) {
		t.Fatalf("the signers did not sign both burns: %v", sg.s.Status())
	}
	signer := httptest.NewServer(sg.s.Handler())
	defer signer.Close()
	key := hex.EncodeToString(sg.s.Key.EdPublic())
	f := newFake()
	f.renders[testMailbox+":json"] = fmt.Sprintf(`{"domain":%d,"count":0,"delivered":0,"live":true,"signers":{"keys":[%q],"threshold":1,"groupA":[],"groupB":[]},"routes":{}}`, hl.DomainGnoTest, key)
	f.renders[testRoute+":json"] = routeRender(0, 50, 0, "") // fee 10
	for _, id := range [][32]byte{dust, honest} {
		f.renders[testMailbox+":delivered/"+s4IDHex(id)] = "0"
	}
	r := testRelayer(f, "primary")
	r.Cfg.Routes[0].Token = s4Token.String()
	r.Cfg.Solana.Mailbox = s4SolBox.String()
	r.Cfg.Signers = []SignerRef{{URL: signer.URL, Key: key}}
	r.Cfg.AllowHTTP = true
	r.sol = &sol.Client{RPC: rpc.New(sg.n.urls[0])}
	r.http = &http.Client{Timeout: 5 * time.Second}
	f.onCall = func(pkg, fn string, args []string) { // a Process delivers: its mark set
		if fn == "Process" && len(args) == 2 {
			raw, _ := hex.DecodeString(args[1])
			f.renders[testMailbox+":delivered/"+s4IDHex(hl.ID(raw))] = "150"
		}
	}
	var hook s6Hook
	r.Alarm = &Alarmer{Source: "relayer", URL: hook.serve(t)}
	ctx := context.Background()
	if err := r.solToGno(ctx); err != nil {
		t.Fatal(err)
	}
	if err := r.solToGno(ctx); err != nil { // a second tick
		t.Fatal(err)
	}
	calls := f.callsOf(testRoute + ".Process")
	posted := hook.posted(1)
	var pending []string
	for _, d := range r.state.Pending {
		pending = append(pending, fmt.Sprintf("#%d", d.Nonce))
	}
	t.Logf("Process calls %d; pending %v; left %v; alarms posted %v; standing %v", len(calls), pending, r.state.Left, posted, r.Alarm.StandingKeys())
	dustMsg, err := sg.s.readSigned("sol", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(calls) != 1 || strings.Contains(calls[0], dustMsg.Message) {
		t.Fatalf("the relayer Processed %d burns, the dust among them %v (the dust burn of 3, below the fee of 10, locks its key's deposit for a sweep)", len(calls), len(calls) > 0 && strings.Contains(strings.Join(calls, " "), dustMsg.Message))
	}
	dustKey := "alarm dust:" + s4IDHex(dust)
	if n := strings.Count(strings.Join(posted, "|"), dustKey); n != 1 {
		t.Fatalf("the dust burn was alarmed %d times, want once: %v", n, posted)
	}
	if len(r.Alarm.StandingKeys()) != 0 {
		t.Fatalf("a dust burn left a standing alarm: %v", r.Alarm.StandingKeys())
	}
	if fee, left := r.state.Left[s4IDHex(dust)]; !left || fee != 10 || len(r.state.Pending) != 1 || r.state.Pending[0].ID != dust {
		t.Fatalf("the dust burn is not kept pending as left under the fee of 10: pending %v, left %v", pending, r.state.Left)
	}
	// GovDAO lowers the fee to 2 (the review of lane s6, F5): the dust left under 10 is a delivery now, Processed at once
	f.mu.Lock()
	f.renders[testRoute+":json"] = strings.Replace(routeRender(0, 50, 0, ""), `"fee":10`, `"fee":2`, 1)
	f.mu.Unlock()
	if err := r.solToGno(ctx); err != nil {
		t.Fatal(err)
	}
	calls = f.callsOf(testRoute + ".Process")
	t.Logf("after the fee decrease: Process calls %d; left %v", len(calls), r.state.Left)
	if len(calls) != 2 || !strings.Contains(calls[1], dustMsg.Message) || len(r.state.Left) != 0 {
		t.Fatalf("the dust left under the old fee was not delivered once the fee fell below it: %d calls, left %v", len(calls), r.state.Left)
	}
	if n := strings.Count(strings.Join(hook.posted(1), "|"), dustKey); n != 1 {
		t.Fatalf("the dust burn was alarmed again: %d", n)
	}
}

// The relayer keeps at most maxLeft dust burns pending: past it the oldest are forgotten (a stranger's pile does not grow
// its state file without end).
func TestS6RelayerBoundsTheDustItKeeps(t *testing.T) {
	r := testRelayer(newFake(), "primary")
	var pending []solDispatch
	for i := 0; i < maxLeft+5; i++ {
		d := solDispatch{ID: [32]byte{byte(i), byte(i >> 8), 7}, Nonce: uint32(i)}
		pending = append(pending, d)
		r.state.Left[d.IDHex()] = 10
	}
	honest := solDispatch{ID: [32]byte{9, 9, 9}, Nonce: 99_999}
	pending = append(pending, honest)
	kept := r.boundLeft(pending)
	if len(kept) != maxLeft+1 || len(r.state.Left) != maxLeft || kept[0].Nonce != 5 || kept[len(kept)-1].ID != honest.ID {
		t.Fatalf("the dust kept: %d pending (first #%d), %d left", len(kept), kept[0].Nonce, len(r.state.Left))
	}
}

// The monitors' stall and age alarms: a burn below its route's fee waits for nobody (the relayers leave it), so a
// stranger's dust no longer raises the age alarm for good; a burn of a fee or more still does.
func TestS6MonitorDustWaitsForNobody(t *testing.T) {
	r := newS4Monitor(t, false)
	route := func(lock int64) string {
		return fmt.Sprintf(`{"address":"g1route","live":true,"symbol":"BUBBLE","lock":%d,"sentTotal":%d,"owed":0,"voided":0,"lost":0,"fee":10,"migratedTo":""}`, lock, lock)
	}
	r.g.set(s4Route+":json", route(1000))
	r.n.slots(100, 100)
	r.poll()
	r.n.burn(r.cfg, 0, 101, s4Alice, 3) // dust, left by the relayers
	r.n.supply(s4Token, 101, 997)
	r.m.state.LastDelivery = r.clock.Unix()
	for i := 0; i < 15; i++ { // two and a half hours
		r.n.slots(102+uint64(i), 102+uint64(i))
		r.step(10 * time.Minute)
		r.g.setHead(200+int64(i), r.clock)
		r.poll()
	}
	t.Logf("a dust burn uncredited for 2.5 h: %s; alarms %v", r.verdict(), r.m.Alarm.StandingKeys())
	if r.alarmed("age") || r.alarmed("stall") {
		t.Fatalf("a dust burn the relayers leave raised the stall and age alarms: %v", r.m.Alarm.StandingKeys())
	}
	r.n.burn(r.cfg, 1, 120, s4Bob, 50) // a burn of a fee and more, undelivered for 2.5 h
	r.n.supply(s4Token, 120, 947)
	for i := 0; i < 15; i++ {
		r.n.slots(121+uint64(i), 121+uint64(i))
		r.step(10 * time.Minute)
		r.g.setHead(300+int64(i), r.clock)
		r.poll()
	}
	if !r.alarmed("age") {
		t.Fatalf("an undelivered burn of a fee and more raised no age alarm: %v", r.m.Alarm.StandingKeys())
	}
}

// The relayers' gno float planned with what a transfer costs it (E5-GS5): the deposit its credit locks, not the Process
// alone — the live relayer's ≈100 GNOT is a day of the planned 50 transfers, not eighteen.
func TestS6RelayerGnoFloatPlanned(t *testing.T) {
	samples := []sample{{At: s4ClockZero, Balance: 100 * ugnotPerGNOT}}
	days, rate := runway("relayer-gno", samples, nil)
	t.Logf("relayer-gno at 100 GNOT: %.1f days at %.0f ugnot a day", days, rate)
	if days >= runwayDays || rate < 50*1.5*ugnotPerGNOT {
		t.Fatalf("the relayers' gno float is planned at %.2f GNOT a day: %.1f days for 100 GNOT (the deposit a credit locks, ≈1.49 GNOT, uncounted)", rate/ugnotPerGNOT, days)
	}
}

// ------------------------------------------------------------------------- E5-SF1: credits of nothing placed --

// s6Credit lands a credit no index holds: hlmailbox's delivered count and mark, and the route's claim/<id> as given.
func s6Credit(g *s4Gno, id [32]byte, claim string, height int64, delivered int) {
	g.set(s4Mailbox+":json", s4MailboxJSON(0, delivered))
	g.set(s4Mailbox+":delivered/"+s4IDHex(id), fmt.Sprint(height))
	g.set(s4Route+":claim/"+s4IDHex(id), claim)
}

// E5-SF1's other half, for any signature of a burn of nothing made before the signers stopped signing them: its credit
// no index can place held a window of no verdict (until the snapshot passed its block by the margin), one every ≈10 s
// holding the monitors for good. A credit whose claim is a sweep of nothing moves neither owed nor the supply wherever
// its burn lies, so it is placed in the poll it lands, its claim read once; a sweep of something, or an open claim, no
// index holds still waits for the index or the margin (its burn may lie above the snapshot).
func TestS6MonitorZeroCreditIsPlacedAtOnce(t *testing.T) {
	r := newS4Monitor(t, false)
	slot, h := uint64(100), int64(200)
	r.n.slots(slot, slot)
	r.g.setHead(h, s4ClockZero.Add(4*time.Second))
	r.poll()
	noVerdict := 0
	for i := 0; i < 6; i++ { // a credit of nothing every 10 s
		id := s6ID(fmt.Sprintf("zero %d", i))
		slot += 25
		h += 3
		r.step(10 * time.Second)
		s6Credit(r.g, id, s6Sweep(id, 0, h-1), h-1, i+1)
		r.g.setHead(h, s4ClockZero.Add(time.Duration(i+1)*10*time.Second+4*time.Second))
		r.n.slots(slot, slot)
		r.poll()
		if strings.Contains(r.cut(), "no verdict") {
			noVerdict++
		}
	}
	zeros := 0
	for _, u := range r.m.state.Unindexed {
		if u.Class == "sweep" {
			zeros++
		}
	}
	t.Logf("6 credits of nothing, one a poll: %d polls without a verdict, %d settled as sweeps; cut %q", noVerdict, zeros, r.cut())
	if noVerdict != 0 || zeros != 6 {
		t.Fatalf("credits of nothing held the monitor at no verdict on %d of 6 polls (%d marked)", noVerdict, zeros)
	}
	// a sweep of something no index holds: its burn may lie above the snapshot — no verdict until the margin
	dust := s6ID("dust")
	h += 3
	r.step(10 * time.Second)
	s6Credit(r.g, dust, s6Sweep(dust, 7, h-1), h-1, 7)
	r.g.setHead(h, s4ClockZero.Add(70*time.Second+4*time.Second))
	slot += 25
	r.n.slots(slot, slot)
	r.poll()
	if !strings.Contains(r.cut(), "in no dispatch index yet") {
		t.Fatalf("a sweep of something no index holds was placed at once: %s (%s)", r.verdict(), r.cut())
	}
}

// hlwatch the same: a reading over a credit of nothing has its identity.
func TestS6HlwatchZeroCreditIsPlaced(t *testing.T) {
	cfg, n, g := s4Config(false), newSolNet(t), newS4Gno()
	g.set(s4Route+":json", s4RouteJSON(1000, 0, 0, 0, ""))
	n.supply(s4Token, 1, 1000)
	zero := s6ID("zero")
	s6Credit(g, zero, s6Sweep(zero, 0, 199), 199, 1)
	g.setHead(200, s4ClockZero.Add(4*time.Second))
	n.slots(102, 102) // its slot ≈ 1 s after slot 100: inside the margin of the credit's block
	providers := n.clients()
	st := &WatchState{}
	st.init()
	ctx := context.Background()
	snap, err := TakeSnapshot(ctx, providers, cfg)
	if err != nil {
		t.Fatal(err)
	}
	v, err := ReadSolanaAt(ctx, providers, g, cfg, st, snap, 200, s4ClockZero)
	if err != nil {
		t.Fatal(err)
	}
	id, judged := v.Identities["BUBBLE"]
	verdict, _ := id.Verdict()
	t.Logf("judged %v %s; errors %q", judged, verdict, v.Err)
	if !judged || verdict != Holds {
		t.Fatalf("hlwatch over a credit of nothing: judged %v %s (%q)", judged, verdict, v.Err)
	}
}

// ------------------------------------------------------------------- the review of lane s6: its findings --

// The review's F1: hlmailbox's delivered count and a burn's mark are two reads, and a node that answers them from two
// backends a block apart (a load-balanced own node) gave bob's mark unset at a count that already held his credit; the
// mark was kept "read unset at that count" and never read again while the count stood, so once the credit's window
// passed its margin bob's 300 counted in flight and owed alike: BROKEN, flagged. Every uncredited mark is read again
// while the book holds a window the index could not place (and every ten minutes): the next poll reads it credited.
func TestS6MonitorStaleMarkHeals(t *testing.T) {
	r := newS4Monitor(t, false)
	r.n.slots(100, 100)
	r.poll()
	bob := r.n.burn(r.cfg, 0, 101, s4Bob, 300)
	r.n.supply(s4Token, 101, 700)
	r.n.slots(102, 102)
	r.step(10 * time.Second)
	r.poll()
	if v := r.verdict(); v != "holds" {
		t.Fatalf("before the credit: %s (%s)", v, r.cut())
	}
	r.g.set(s4Route+":json", s4RouteJSON(1000, 300, 0, 0, "")) // credited at 201: owed 300, the count 1
	r.g.set(s4Mailbox+":json", s4MailboxJSON(0, 1))
	r.g.setHead(201, s4ClockZero.Add(3*time.Second))
	var mu sync.Mutex
	stale := true
	r.g.hook(func(_, path string) {
		mu.Lock()
		defer mu.Unlock()
		if stale && path == "delivered/"+s4IDHex(bob) {
			stale = false // this one read came from a backend a block behind; every later read is current
			r.g.set(s4Mailbox+":delivered/"+s4IDHex(bob), "201")
		}
	})
	var out []string
	for _, slot := range []uint64{105, 120, 140, 160, 180, 200} {
		r.n.slots(slot, slot)
		r.step(10 * time.Second)
		r.poll()
		out = append(out, fmt.Sprintf("slot %d: %s", slot, r.verdict()))
	}
	t.Logf("polls: %s; bob credited %d; standing %v", strings.Join(out, ", "), r.m.state.Index[s4IDHex(bob)].Credited, r.m.Alarm.StandingKeys())
	if r.verdict() != "holds" || r.alarmed("identity:BUBBLE:flag") {
		t.Fatalf("one stale delivered-mark read left the credited burn in flight for good: %s (%s)", r.verdict(), r.cut())
	}
}

// The same on a signer: the stale read halted it as broken, and with no signer signing no credit moved the count.
func TestS6SignerStaleMarkHeals(t *testing.T) {
	r := newS4Signer(t, false)
	r.n.slots(100, 100)
	r.round()
	bob := r.n.burn(r.cfg, 0, 101, s4Bob, 300)
	r.n.supply(s4Token, 101, 700)
	r.n.slots(102, 102)
	r.round()
	r.round()
	if !r.signed(0) {
		t.Fatalf("bob's burn not signed: %v", r.s.Status())
	}
	r.g.set(s4Route+":json", s4RouteJSON(1000, 300, 0, 0, ""))
	r.g.set(s4Mailbox+":json", s4MailboxJSON(0, 1))
	r.g.setHead(201, s4ClockZero.Add(3*time.Second))
	var mu sync.Mutex
	stale := true
	r.g.hook(func(_, path string) {
		mu.Lock()
		defer mu.Unlock()
		if stale && path == "delivered/"+s4IDHex(bob) {
			stale = false
			r.g.set(s4Mailbox+":delivered/"+s4IDHex(bob), "201")
		}
	})
	var out []string
	for _, slot := range []uint64{105, 120, 140, 160, 300, 600} {
		r.n.slots(slot, slot)
		r.round()
		out = append(out, fmt.Sprintf("slot %d: halted %q", slot, r.s.Status()["halted"]))
	}
	t.Logf("%s", strings.Join(out, "; "))
	if h := fmt.Sprint(r.s.Status()["halted"]); h != "" {
		t.Fatalf("one stale delivered-mark read halted the signer for good: %s", h)
	}
}

// s6DispatchAcct is the dispatched-message account solNet.burn writes for a nonce.
func s6DispatchAcct(cfg *Config, nonce uint32) solana.PublicKey {
	return sol.Mailbox{Program: cfg.SolMailbox()}.DispatchedMessage(s4Key(fmt.Sprintf("unique %d", nonce)))
}

// The review's F2: a provider's dispatched-message account read (at finalized, no bound) answered by a backend a few
// slots behind the transaction took the burn for no dispatch and the cursor passed it for good: the burn on the other
// provider's word alone, its token with no verdict for good when nobody credits it (dust the relayers leave). The read
// is held to the transaction's slot now: a backend behind it fails the read, the transaction is read again next poll.
func TestS6MonitorLaggingAccountReadHeals(t *testing.T) {
	r := newS4Monitor(t, false)
	r.n.slots(100, 100)
	r.poll()
	r.n.burn(r.cfg, 0, 101, s4Alice, 1) // dust: nobody credits it
	acct := s6DispatchAcct(r.cfg, 0)
	r.n.supply(s4Token, 101, 999)
	r.n.pinAt(1, acct, 100) // provider 2's account read answered from slot 100 this once
	r.n.slots(102, 102)
	r.step(10 * time.Second)
	r.poll()
	r.n.pinAt(1, acct, 0) // its backend caught up
	var out []string
	for i := 0; i < 8; i++ {
		r.n.slots(110+uint64(i)*25, 110+uint64(i)*25)
		r.step(15 * time.Minute)
		r.g.setHead(int64(210+i), r.clock.Add(-2*time.Second))
		r.poll()
		out = append(out, r.verdict())
	}
	var providers []int
	for _, rec := range r.m.state.Index {
		providers = append(providers, rec.Providers)
	}
	t.Logf("verdicts over two hours %v; the record's providers %v; standing %v", out, providers, r.m.Alarm.StandingKeys())
	if r.verdict() != "holds" {
		t.Fatalf("an honest burn one provider's lagging account read missed held the token at no verdict: %s", r.verdict())
	}
}

// The same on a signer whose first provider's account read lagged: it had no verdict and halted after an hour, a later
// honest burn never signed.
func TestS6SignerLaggingAccountReadHeals(t *testing.T) {
	r := newS4Signer(t, false)
	clock := s4ClockZero
	r.s.now = func() time.Time { return clock }
	r.n.slots(100, 100)
	r.round()
	r.n.burn(r.cfg, 0, 101, s4Alice, 1) // dust
	acct := s6DispatchAcct(r.cfg, 0)
	r.n.supply(s4Token, 101, 999)
	r.n.pinAt(0, acct, 100) // provider 1's account read a few slots behind, once
	r.n.slots(102, 102)
	r.round()
	r.n.pinAt(0, acct, 0)
	r.n.burn(r.cfg, 1, 103, s4Bob, 300) // a later honest burn
	r.n.supply(s4Token, 103, 699)
	for i := 0; i < 80; i++ { // eighty rounds a minute apart
		clock = clock.Add(time.Minute)
		r.n.slots(110+uint64(i), 110+uint64(i))
		r.round()
	}
	st := r.s.Status()
	t.Logf("after 80 minutes: signing %v, halted %q, the later burn signed %v", st["signing"], st["halted"], r.signed(1))
	if !r.signed(1) || st["halted"] != "" {
		t.Fatalf("one lagging account read on provider 1 stopped this signer: halted %q, the later burn signed %v", st["halted"], r.signed(1))
	}
}

// The review's F3 (before lane s6, the E4-SF3 class through another door): both providers' account reads answered below
// bob's burn's transaction for one scan — taken for no dispatch on both, the cursors passed it, in no index: the reading
// short by 300, a forged mint of 300 read as holding on the monitor and its burn signed by the signer.
func TestS6BothAccountReadsLagNoLongerHideAForgedMint(t *testing.T) {
	m := newS4Monitor(t, false)
	m.n.slots(100, 100)
	m.poll()
	m.n.burn(m.cfg, 0, 101, s4Bob, 300)
	acct := s6DispatchAcct(m.cfg, 0)
	m.n.supply(s4Token, 101, 700)
	m.n.pinAt(0, acct, 100)
	m.n.pinAt(1, acct, 100)
	m.n.slots(102, 102)
	m.step(10 * time.Second)
	m.poll()
	m.n.pinAt(0, acct, 0)
	m.n.pinAt(1, acct, 0)
	m.n.supply(s4Token, 103, 1000) // a forged mint of 300, the lock unchanged
	for i := 0; i < 3; i++ {
		m.n.slots(110+uint64(i)*25, 110+uint64(i)*25)
		m.step(10 * time.Second)
		m.poll()
	}
	t.Logf("the monitor over the forged mint: %s; indexed %d", m.verdict(), len(m.m.state.Index))
	if m.verdict() != "BROKEN" {
		t.Fatalf("bob's burn dropped by two lagging account reads hid the forged mint: %s", m.verdict())
	}
	s := newS4Signer(t, false)
	s.n.slots(100, 100)
	s.round()
	s.n.burn(s.cfg, 0, 101, s4Bob, 300)
	acct = s6DispatchAcct(s.cfg, 0)
	s.n.supply(s4Token, 101, 700)
	s.n.pinAt(0, acct, 100)
	s.n.pinAt(1, acct, 100)
	s.n.slots(102, 102)
	s.round()
	s.n.pinAt(0, acct, 0)
	s.n.pinAt(1, acct, 0)
	s.n.supply(s4Token, 105, 1000)        // the forged mint …
	s.n.burn(s.cfg, 1, 106, s4Alice, 300) // … burned straight back
	s.n.supply(s4Token, 106, 700)
	for i := 0; i < 4; i++ {
		s.n.slots(110+uint64(i)*30, 110+uint64(i)*30)
		s.round()
	}
	t.Logf("the signer: bob's burn signed %v; the forged burn signed %v; halted %q", s.signed(0), s.signed(1), s.s.Status()["halted"])
	if s.signed(1) {
		t.Fatalf("the forged mint's burn was signed")
	}
}

// s6NullOnce is a provider in front of upstream whose getTransaction answers null once for one signature (a backend
// behind its listing).
func s6NullOnce(t *testing.T, upstream, sig string) string {
	var once sync.Once
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		body, _ := io.ReadAll(req.Body)
		var j struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		_ = json.Unmarshal(body, &j)
		nulled := false
		if j.Method == "getTransaction" && bytes.Contains(body, []byte(sig)) {
			once.Do(func() { nulled = true })
		}
		if nulled {
			_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": j.ID, "result": nil})
			return
		}
		resp, err := http.Post(upstream, "application/json", bytes.NewReader(body))
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		out, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		_, _ = w.Write(out)
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// The review's F2, its other door: a null transaction answer from a backend behind the provider's listing was filed as
// past the provider's retention and read again an hour later — the burn on the other provider's word for that hour, its
// token without a verdict. A null answer is read again at once (five times) before it is taken for the retention.
func TestS6NullTransactionReadAgainAtOnce(t *testing.T) {
	r := newS4Monitor(t, false)
	r.n.slots(100, 100)
	r.poll()
	r.n.burn(r.cfg, 0, 101, s4Alice, 1) // dust
	r.n.supply(s4Token, 101, 999)
	r.m.sol = [2]*rpc.Client{rpc.New(r.n.urls[0]), rpc.New(s6NullOnce(t, r.n.urls[1], r.n.lastSig()))}
	r.m.both = twoProviders(r.m.sol)
	var out []string
	var retry int64 = -1
	for i := 0; i < 3; i++ {
		r.n.slots(102+uint64(i), 102+uint64(i))
		r.step(10 * time.Second)
		r.poll()
		out = append(out, r.verdict())
		if sk := r.m.state.Skipped["BUBBLE#1"]; i == 0 && len(sk) == 1 {
			retry = sk[0].Retry // (solScan stamps a skip with the wall clock, not the rig's: the retry time itself is checked)
		}
	}
	t.Logf("verdicts %v; the null answer's skip retried at %d (0: at once)", out, retry)
	if retry != 0 || r.verdict() != "holds" {
		t.Fatalf("a null transaction answer was taken for the retention (retried at %d): the burn on one provider's word for an hour; verdicts %v", retry, out)
	}
}

// s6FarRefusing is a provider in front of upstream that refuses a minContextSlot far past its view with another error
// than −32016 (invalid params) — its listings may still ignore the bound (upstream's ignoreMinList).
func s6FarRefusing(t *testing.T, upstream string) string {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		body, _ := io.ReadAll(req.Body)
		var j struct {
			ID     json.RawMessage   `json:"id"`
			Method string            `json:"method"`
			Params []json.RawMessage `json:"params"`
		}
		_ = json.Unmarshal(body, &j)
		if j.Method == "getSignaturesForAddress" && len(j.Params) > 1 {
			var o struct {
				MinContextSlot uint64 `json:"minContextSlot"`
			}
			_ = json.Unmarshal(j.Params[1], &o)
			if o.MinContextSlot > 50_000 {
				_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": j.ID, "error": map[string]any{"code": -32602, "message": "Invalid params: minContextSlot is too far ahead"}})
				return
			}
		}
		resp, err := http.Post(upstream, "application/json", bytes.NewReader(body))
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		out, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		_, _ = w.Write(out)
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// The review's F4: the probe took any answer but a list or −32016 as "cannot tell" and kept the last answer —
// "honours" before the first — so two providers whose listings ignore the bound but refuse a far-off minContextSlot with
// another error passed it, and the forger's own burn hidden by their lagging listings let the forged mint read as
// holding. The check fails closed: a provider is refused until a −32016 shows it honours the bound.
func TestS6MinContextProbeFailsClosed(t *testing.T) {
	r := newS4Monitor(t, false)
	r.n.ignoreMinList = [2]bool{true, true}
	r.m.sol = [2]*rpc.Client{rpc.New(s6FarRefusing(t, r.n.urls[0])), rpc.New(s6FarRefusing(t, r.n.urls[1]))}
	r.m.both = twoProviders(r.m.sol)
	r.n.supply(s4Token, 101, 1300)        // a forged mint of 300 …
	r.n.burn(r.cfg, 0, 102, s4Alice, 300) // … burned straight back
	r.n.supply(s4Token, 102, 1000)
	r.n.burn(r.cfg, 1, 112, s4Alice, 300) // and 300 of the forger's own
	r.n.supply(s4Token, 112, 700)
	r.n.slots(114, 114)
	r.n.lagListing(0, 4)
	r.n.lagListing(1, 4)
	r.poll()
	t.Logf("verdict %s; cut %q; alarms %v", r.verdict(), r.cut(), r.m.Alarm.StandingKeys())
	if r.verdict() == "holds" {
		t.Fatalf("providers that refuse a far-off minContextSlot otherwise passed the probe: the forged mint holds")
	}
	// a provider honouring the bound but whose first probe failed (a timeout): refused that minute, judged after it
	h := newS4Monitor(t, false)
	h.n.slots(100, 100)
	var once sync.Once
	flaky := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		body, _ := io.ReadAll(req.Body)
		fail := false
		if bytes.Contains(body, []byte(`"getSignaturesForAddress"`)) && bytes.Contains(body, []byte(`minContextSlot`)) {
			once.Do(func() { fail = true })
		}
		if fail {
			http.Error(w, "upstream timeout", http.StatusGatewayTimeout)
			return
		}
		resp, err := http.Post(h.n.urls[1], "application/json", bytes.NewReader(body))
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		out, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		_, _ = w.Write(out)
	}))
	defer flaky.Close()
	h.m.sol = [2]*rpc.Client{rpc.New(h.n.urls[0]), rpc.New(flaky.URL)}
	h.m.both = twoProviders(h.m.sol)
	h.poll()
	first := h.verdict()
	h.step(61 * time.Second)
	h.poll()
	t.Logf("a provider whose first probe timed out: %s, then %s", first, h.verdict())
	if first != "no verdict" || h.verdict() != "holds" {
		t.Fatalf("a provider whose first probe timed out: %s, then %s", first, h.verdict())
	}
}

// The review's F6: "a fee's worth" was the route's fee, so a fee a decision lowered to 0 made a full batch of slivers
// worth calling again at once, every tick. One token at least is a fee's worth now.
func TestS6ClaimCadenceAtFeeZero(t *testing.T) {
	f := newFake()
	r := testRelayer(f, "primary")
	ctx := context.Background()
	open, paidTotal := int64(400), int64(0)
	render := func() string {
		return strings.Replace(routeRender(open, 50, paidTotal, ""), `"fee":10`, `"fee":0`, 1)
	}
	f.renders[testRoute+":json"] = render()
	f.events[testRoute+".Claim"] = []gno.Event{{Type: "Claimed", Attrs: map[string]string{"count": "20"}}}
	f.onCall = func(pkg, fn string, args []string) {
		if fn == "Claim" {
			paidTotal += 20 * 10 // twenty slivers of 10 base units: claims mid-installment, their meters empty
			f.renders[testRoute+":json"] = render()
		}
	}
	for i := 0; i < 5; i++ {
		_ = r.claims(ctx)
		advance(r, 3*time.Second)
	}
	calls := f.callsOf(testRoute + ".Claim")
	t.Logf("five ticks at fee 0: %d Claims, backoff %s", len(calls), r.state.Claims["BUBBLE"].Backoff)
	if len(calls) != 1 {
		t.Fatalf("full batches of slivers at fee 0 were called again every tick: %d Claims in five ticks", len(calls))
	}
}
