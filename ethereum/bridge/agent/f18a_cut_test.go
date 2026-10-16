package agent

// Fold F18a (round 6): the Go trust path's E6-GT1 — the cut's window: only hlmailbox, the routes, their lines and the
// head inside it, taken again while it moves, a BROKEN reading across a moved cut acted on, the Ethereum side's rules
// run whatever the cut did, `cut` at flag level after an hour without a verdict — and E6-GT4's first item (the marks
// above the index read largest first). Each test fails on the parent (e3d0053f); the audit's PoCs are
// audit-r6/go-trust-pocs/zz_audit6_cut_test.go (their landings here are in a new block, as on one node).

import (
	"context"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"

	"github.com/gnolang/bridges/solana/bridge/gno"
	"github.com/gnolang/bridges/solana/bridge/hl"
	"github.com/gnolang/bridges/ethereum/bridge/ethx"
	"github.com/gnolang/bridges/ethereum/bridge/gnoside"
)

// f18aNode is the own node with the cut's window marked on it: the window opens at an hlmailbox json read and closes at
// the hlmailbox json read that follows a head read (cutMoved's, after headAfterCut's — on this tree and the parent
// alike), and what a test lands at one of its points lands in a new block (the fake's head moves on and the mark is set
// at it), as one node applies a block — never in a block the window already read the head of.
type f18aNode struct {
	*m3bGnoNode
	lastRoute string // the pkgpath of the last route a window reads (its routes' read ends there)

	mu         sync.Mutex
	inWindow   bool
	lastStatus bool // the last read was a head read
	takings    int
	// afterFirst, afterRoutes and afterHead land something right after hlmailbox's first read, after the last route's
	// read and after the head read inside the window, afterWindow right after the window's last read (nil: nothing)
	afterFirst, afterRoutes, afterHead, afterWindow func()
}

func (n *f18aNode) RenderAt(ctx context.Context, pkg, path string, h int64) (string, error) {
	v, err := n.m3bGnoNode.RenderAt(ctx, pkg, path, h)
	var land func()
	n.mu.Lock()
	switch {
	case pkg == m3bMailPath && path == "json" && !n.inWindow:
		n.inWindow = true
		n.takings++
		land = n.afterFirst
	case pkg == m3bMailPath && path == "json" && n.lastStatus:
		n.inWindow = false
		land = n.afterWindow
	case n.inWindow && pkg == n.lastRoute && path == "json":
		land = n.afterRoutes
	}
	n.lastStatus = false
	n.mu.Unlock()
	if land != nil {
		land()
	}
	return v, err
}

func (n *f18aNode) Status(ctx context.Context) (gno.Head, error) {
	h, err := n.m3bGnoNode.Status(ctx)
	n.mu.Lock()
	var land func()
	if n.inWindow {
		land = n.afterHead
	}
	n.lastStatus = true
	n.mu.Unlock()
	if land != nil {
		land()
	}
	return h, err
}

// newBlock commits a block on the fake node: its head is the next height, which it answers.
func (n *f18aNode) newBlock() int64 {
	n.m3bGnoNode.mu.Lock()
	defer n.m3bGnoNode.mu.Unlock()
	n.m3bGnoNode.height++
	return n.m3bGnoNode.height
}

// deliver marks a message id delivered in a new block.
func (n *f18aNode) deliver(id string) int64 {
	h := n.newBlock()
	n.set(m3bMailPath, "delivered/"+id, strconv.FormatInt(h, 10))
	return h
}

func (n *f18aNode) takingsSoFar() int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.takings
}

// f18aTries is how many times a poll takes the cut's window while it moves (the code's cutTries; a literal here, so
// this file compiles on the parent).
const f18aTries = 3

// f18aMonRig is the monitor over the fakes with its own node marked (f18aNode) and both providers current to their
// heads (gt1Chain: the head 6 s old, the finalized block 100 under it): what they show above the index is known.
func f18aMonRig(t *testing.T) (*monRig, *f18aNode) {
	r := newMonRig(t)
	gt1Chain(r.chain, r.now)
	n := &f18aNode{m3bGnoNode: r.gno, lastRoute: m3bNS + "hlbubble"}
	r.m.own = n
	return r, n
}

// f18aKeepPace keeps the fake Ethereum with the rig's clock: the head current, the finalized block 64 blocks behind it.
func f18aKeepPace(c *m3bChain, now time.Time) {
	c.mu.Lock()
	t0 := c.t0
	c.mu.Unlock()
	h := (uint64(now.Unix()) - t0) / 12
	c.mine(h, h-64)
}

// E6-GT1, the attack the monitors exist for: a forger with two signer keys lands one forged credit (a delivery no burn
// carries, owed by its amount) in every block — here inside every taking of every window, after the routes' read. Each
// moves hlmailbox's delivered count, so no cut holds; but a delivery landing inside the window only takes from the
// right side, so the reading BROKEN across the moved cut — past E5-GT1's bound, nothing above the index — is flagged at
// the first poll (the parent: no verdict, ever, nothing flagged). hlwatch over the same chains names the break. The
// control: the same forgery, nothing landing — flagged at the first poll.
func TestF18aForgerInEveryWindowIsFlaggedAtOnce(t *testing.T) {
	for _, moving := range []bool{false, true} {
		r, n := f18aMonRig(t)
		forged := 0
		land := func() {
			forged++
			n.deliver(fmt.Sprintf("%064x", 0xf0f0f0+forged))
			r.gno.set(m3bNS+"hlgnot", "json", m3bRouteJSON(1000, int64(300*forged), 0, 0))
		}
		land() // the first forged credit lands before the first poll
		if moving {
			n.afterRoutes = land
		}
		r.step(10 * time.Second)
		if err := r.poll(); err != nil {
			t.Fatal(err)
		}
		cut := cutText(r.m.Status())
		t.Logf("moving=%v: %d takings, %d forged credits; gno flags %v, eth flag txs %v; cut %q", moving, n.takingsSoFar(), forged, r.gnoFlags(), r.snd.calls, cut)
		if len(r.gnoFlags()) != 2 || r.snd.count() != 1 || !r.alarmed("identity:GNOT:flag") {
			t.Fatalf("moving=%v: the forged credits were not flagged at the first poll: %v %v %v", moving, r.gnoFlags(), r.snd.calls, r.m.Alarm.StandingKeys())
		}
		if moving {
			if n.takingsSoFar() != f18aTries || !strings.HasPrefix(cut, "no verdict") || !strings.Contains(r.m.Alarm.Standing()["identity:GNOT:flag"], "read across a cut that moved") {
				t.Fatalf("the cut was not taken %d times, each moving, and the break judged across it: takings %d, cut %q, %v", f18aTries, n.takingsSoFar(), cut, r.m.Alarm.Standing()["identity:GNOT:flag"])
			}
			// a guardian's hlwatch over the same node, the forger landing on: no verdict, the identity BROKEN, never green
			r.m.ReadOnly = true
			r.step(10 * time.Second)
			v, green, why := r.view(t)
			if green || len(v.Broken) != 1 || v.Broken[0] != "GNOT" {
				t.Fatalf("hlwatch over the moving cuts: broken %v, green %v: %s", v.Broken, green, why)
			}
		}
	}
}

// The signer's half: a BROKEN reading across a cut that moved in every taking — a forged mint burned back, and a forged
// credit landing inside every window — halts it at once on the break (the parent: no verdict each round, the halt only
// "no verdict for an hour"; nothing named the break).
func TestF18aSignerHaltsOnABreakAcrossAMovingCut(t *testing.T) {
	r := newSignerRig(t)
	gt1Chain(r.chain, r.now)
	n := &f18aNode{m3bGnoNode: r.gno, lastRoute: m3bNS + "hlbubble"}
	r.s.gno = n
	r.round()
	if r.status()["signing"] != true {
		t.Fatalf("setup: %v", r.status()["cut"])
	}
	forged := 0
	n.afterRoutes = func() {
		forged++
		n.deliver(fmt.Sprintf("%064x", 0xf0f0f0+forged))
		r.gno.set(m3bNS+"hlgnot", "json", m3bRouteJSON(1000, int64(300*forged), 0, 0))
	}
	r.step(time.Second)
	r.round()
	st := r.status()
	t.Logf("signer: %d takings; signing=%v halted=%q", n.takingsSoFar(), st["signing"], st["halted"])
	if st["signing"] == true || !strings.Contains(fmt.Sprint(st["halted"]), "read across a cut that moved") {
		t.Fatalf("the signer did not halt on the break across the moving cut: %v", st)
	}
}

// E6-GT1's liveness half: a stranger delivers one of its own dust a block (the relayers leave dust; anyone may pay its
// Process with the signers' public signatures) — each landing inside a round's window. The window is taken again at
// once and holds (the next block is a block away): every round has its verdict, and the signer signs on (the parent:
// one verdict in 400 rounds, halted after an hour). The pile is read after the window, each mark placed by its height.
func TestF18aStrangerDeliveryEachBlockLeavesTheSignerSigning(t *testing.T) {
	r := newSignerRig(t)
	n := &f18aNode{m3bGnoNode: r.gno, lastRoute: m3bNS + "hlbubble"}
	r.s.gno = n
	r.round()
	gnot := gnoside.RealmKey(m3bNS + "hlgnot")
	r.chain.mine(130, 130)
	var pile []string
	for i := 0; i < 100; i++ {
		id := r.chain.burn(m3bGNOT, gnot, r.alice, 1, 120)
		pile = append(pile, hex.EncodeToString(id[:]))
	}
	r.step(time.Second)
	r.round()
	if st := r.status(); st["signing"] != true || st["signed"].(int) < 100 {
		t.Fatalf("setup: signing %v, signed %v (%v)", st["signing"], st["signed"], st["cut"])
	}
	next, landed := 0, false
	n.afterRoutes = func() {
		if !landed && next < len(pile) { // one delivery a block: the round's first taking
			landed = true
			n.deliver(pile[next])
			next++
		}
	}
	block := uint64(131)
	verdicts := 0
	for i := 0; i < 400; i++ { // a round every 10 s: 66 minutes
		r.chain.mine(block, block-1)
		pile = append(pile, func() string { id := r.chain.burn(m3bGNOT, gnot, r.alice, 1, block); return hex.EncodeToString(id[:]) }())
		block++
		r.step(10 * time.Second)
		landed = false
		last := r.s.lastVerdict
		r.round()
		if r.s.lastVerdict != last {
			verdicts++
		}
	}
	st := r.status()
	t.Logf("400 rounds, %d delivered inside a window, %d with a verdict; signing %v, halted %q", next, verdicts, st["signing"], st["halted"])
	if verdicts != 400 || st["signing"] != true || next != 400 {
		t.Fatalf("a stranger's delivery a block starved the signer: %d verdicts of 400, signing %v, halted %q", verdicts, st["signing"], st["halted"])
	}
}

// The monitor's liveness the same way: a delivery landing in a poll's window, a poll after poll — every poll its verdict.
func TestF18aStrangerDeliveryEachBlockLeavesTheMonitorJudging(t *testing.T) {
	r, n := f18aMonRig(t)
	landed, k := false, 0
	n.afterRoutes = func() {
		if !landed {
			landed = true
			k++
			n.deliver(fmt.Sprintf("%064x", 0xd0d0+k)) // a delivery of an id no burn carries (a vote's, a stranger's)
		}
	}
	verdicts := 0
	for i := 0; i < 60; i++ {
		r.step(10 * time.Second)
		f18aKeepPace(r.chain, r.now)
		landed = false
		if err := r.poll(); err != nil {
			t.Fatal(err)
		}
		if r.m.last.Verdict {
			verdicts++
		}
	}
	t.Logf("60 polls, %d deliveries inside a window, %d with a verdict; cut %q", k, verdicts, cutText(r.m.Status()))
	if verdicts != 60 || k != 60 {
		t.Fatalf("a delivery a poll inside the window starved the monitor: %d verdicts of 60", verdicts)
	}
}

// E6-GT1 (2): the Ethereum side's rules run whatever the gno cut gave. A header the client recorded that is not
// gno.land's (a validator majority's forgery, the fork rule's case), while a stranger's delivery lands inside every
// taking of every window — no poll has a verdict — is flagged once it has stood two minutes (the parent: thirty minutes,
// no flag); the stalls and ages run too (a burn waiting past two hours raises `age`).
func TestF18aEthSideRulesRunOverMovingCuts(t *testing.T) {
	for _, moving := range []bool{false, true} {
		r, n := f18aMonRig(t)
		k := 0
		if moving {
			n.afterRoutes = func() { k++; n.deliver(fmt.Sprintf("%064x", 0xd0d0+k)) }
		}
		gnot := gnoside.RealmKey(m3bNS + "hlgnot")
		r.chain.setSupply(m3bGNOT, 2, 10_000_000)
		r.gno.set(m3bNS+"hlgnot", "json", m3bRouteJSON(10_000_000, 0, 0, 0))
		r.chain.burn(m3bGNOT, gnot, r.alice, 7_000_000, 90)           // a burn of the fee and more, never credited: it waits
		r.recordHeader(58, r.chain.finalized-2, [32]byte{0xf0, 0x12}) // a forged header at height 58, recorded at a final block
		verdicts := 0
		for i := 0; i < 18; i++ { // three minutes
			r.step(10 * time.Second)
			f18aKeepPace(r.chain, r.now)
			if err := r.poll(); err != nil {
				t.Fatal(err)
			}
			if r.m.last.Verdict {
				verdicts++
			}
		}
		t.Logf("moving=%v: %d deliveries, %d polls with a verdict; mismatch %v; eth flag txs %v", moving, k, verdicts, r.m.state.Record.Mismatch != nil, r.snd.calls)
		if r.m.state.Record.Mismatch == nil || r.snd.count() == 0 {
			t.Fatalf("moving=%v: the forged header was not flagged: mismatch %v, eth flag txs %v", moving, r.m.state.Record.Mismatch, r.snd.calls)
		}
		if moving && verdicts != 0 {
			t.Fatalf("a poll had a verdict over a window that moved in every taking: %d", verdicts)
		}
		// two hours on, the burn waiting since the first poll raises `age`, verdict or none
		r.step(2 * time.Hour)
		f18aKeepPace(r.chain, r.now)
		if err := r.poll(); err != nil {
			t.Fatal(err)
		}
		if !r.alarmed("age") {
			t.Fatalf("moving=%v: the stalls and ages did not run: %v", moving, r.m.Alarm.StandingKeys())
		}
	}
}

// E6-GT1 (3): `cut` is raised at flag level once the monitor has had no verdict for an hour — the clock kept in the
// state, so a restart (or the next `hlwatch status -state`) keeps it (the parent: an alarm, repeated hourly, for good).
func TestF18aCutAtFlagLevelAfterAnHourWithoutAVerdict(t *testing.T) {
	r, n := f18aMonRig(t)
	r.m.State = filepath.Join(t.TempDir(), "monitor.json")
	k := 0
	n.afterRoutes = func() { k++; n.deliver(fmt.Sprintf("%064x", 0xd0d0+k)) }
	poll := func() {
		r.step(10 * time.Second)
		f18aKeepPace(r.chain, r.now)
		if err := r.poll(); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 6*59; i++ { // 59 minutes
		poll()
	}
	if !r.alarmed("cut") || flagLevel(r.m.Alarm, "cut") {
		t.Fatalf("59 minutes of moving cuts: cut alarmed %v, at flag level %v", r.alarmed("cut"), flagLevel(r.m.Alarm, "cut"))
	}
	for i := 0; i < 12; i++ {
		poll()
	}
	if !flagLevel(r.m.Alarm, "cut") || !strings.Contains(r.m.Alarm.Standing()["cut"], "NO VERDICT FOR AN HOUR") {
		t.Fatalf("an hour without a verdict: cut %q, at flag level %v", r.m.Alarm.Standing()["cut"], flagLevel(r.m.Alarm, "cut"))
	}
	// a restart: a fresh monitor over the same state file raises it at flag level at its first moving cut
	m2 := &Monitor{Cfg: r.m.Cfg, State: r.m.State, Alarm: &Alarmer{Source: "test2"}, Every: 10 * time.Second, pair: r.m.pair, own: n, watch: r.gno, sources: r.m.sources, ethWatch: r.snd, now: func() time.Time { return r.now }}
	if err := m2.init(); err != nil {
		t.Fatal(err)
	}
	r.m = m2
	poll()
	if !flagLevel(r.m.Alarm, "cut") {
		t.Fatalf("after a restart, the hour without a verdict was forgotten: %v", r.m.Alarm.Standing()["cut"])
	}
	// a cut that holds clears it
	n.afterRoutes = nil
	poll()
	if r.alarmed("cut") {
		t.Fatalf("a held cut left cut standing: %v", r.m.Alarm.Standing()["cut"])
	}
}

// f18aRec records the reads a monitor makes of its own node, in order, with the providers' calls made so far (the
// audit's a6Rec): what lies inside a cut's window.
type f18aRec struct {
	*m3bGnoNode
	r   *monRig
	mu  sync.Mutex
	log []f18aRead
}

type f18aRead struct {
	what string
	eth  int
}

func (x *f18aRec) note(what string) {
	n := 0
	for _, p := range []*m3bProvider{x.r.pa, x.r.pb} {
		p.mu.Lock()
		for _, c := range p.calls {
			n += c
		}
		p.mu.Unlock()
	}
	x.mu.Lock()
	x.log = append(x.log, f18aRead{what, n})
	x.mu.Unlock()
}

func (x *f18aRec) RenderAt(ctx context.Context, pkg, path string, h int64) (string, error) {
	x.note("render " + strings.TrimPrefix(pkg, m3bNS) + ":" + path)
	return x.m3bGnoNode.RenderAt(ctx, pkg, path, h)
}

func (x *f18aRec) Status(ctx context.Context) (gno.Head, error) {
	x.note("status")
	return x.m3bGnoNode.Status(ctx)
}

// window is the last cut window of a poll: from the first hlmailbox read after the first head read to the last
// hlmailbox read that follows a head read (cutMoved's), with the delivered/<id> renders and the provider calls inside.
func (x *f18aRec) window() (reads, marks, eth int) {
	x.mu.Lock()
	defer x.mu.Unlock()
	first, last, sawStatus := -1, -1, false
	for i, r := range x.log {
		switch {
		case r.what == "status":
			sawStatus = true
		case r.what == "render hlmailbox:json" && sawStatus && first < 0:
			first = i
		case r.what == "render hlmailbox:json" && first >= 0 && i > 0 && x.log[i-1].what == "status":
			last = i
		}
	}
	if first < 0 || last < 0 {
		return 0, 0, 0
	}
	for _, r := range x.log[first : last+1] {
		if strings.Contains(r.what, "delivered/") {
			marks++
		}
	}
	return last - first + 1, marks, x.log[last].eth - x.log[first].eth
}

// E6-GT1 (4): the long reads are outside the window. A pile of 100 dust (the count moved since their marks were read:
// every one read again), a reading BROKEN with a delivery unplaced (every uncredited mark read again in doubt) and 200
// burns above the index (their marks read): the window between hlmailbox's two reads holds no delivered mark and no
// provider call — hlmailbox twice, the routes, the head (the parent: 404 reads, 398 of them marks, 6 provider calls).
func TestF18aWindowHoldsNoMarkAndNoProvider(t *testing.T) {
	r := newMonRig(t)
	gt1Chain(r.chain, r.now)
	rec := &f18aRec{m3bGnoNode: r.gno, r: r}
	r.m.own = rec
	if err := r.poll(); err != nil {
		t.Fatal(err)
	}
	gnot := gnoside.RealmKey(m3bNS + "hlgnot")
	var pile []string
	for i := 0; i < 100; i++ {
		id := r.chain.burn(m3bGNOT, gnot, r.alice, 1, 95)
		pile = append(pile, hex.EncodeToString(id[:]))
	}
	r.step(10 * time.Second)
	f18aKeepPace(r.chain, r.now)
	if err := r.poll(); err != nil {
		t.Fatal(err)
	}
	// one of the pile delivered (the count moves), a forged credit of 300, 200 of the forger's burns above the index
	r.gno.set(m3bMailPath, "delivered/"+pile[0], strconv.FormatInt(r.gno.height, 10))
	r.gno.set(m3bMailPath, "delivered/"+strings.Repeat("f0", 32), strconv.FormatInt(r.gno.height, 10))
	r.gno.set(m3bNS+"hlgnot", "json", m3bRouteJSON(1000, 300, 0, 0))
	for i := 0; i < 200; i++ {
		r.chain.burn(m3bGNOT, gnot, r.alice, 1, r.chain.finalized+10)
	}
	r.step(10 * time.Second)
	rec.mu.Lock()
	rec.log = nil
	rec.mu.Unlock()
	if err := r.poll(); err != nil {
		t.Fatal(err)
	}
	reads, marks, eth := rec.window()
	t.Logf("the window: %d node reads, %d of them delivered marks, %d provider calls; flags %v", reads, marks, eth, r.gnoFlags())
	if reads == 0 || marks != 0 || eth != 0 || len(r.gnoFlags()) == 0 {
		t.Fatalf("the window holds %d reads, %d marks, %d provider calls (flags %v)", reads, marks, eth, r.gnoFlags())
	}
}

// The window's proof (cutorder.go): a delivery or a dispatch landing inside the window takes from the right side only,
// or adds a lag E5-GT1's bound counts. Over an honest bridge — a burn of 200 in flight, a send of 100 waiting on
// Ethereum, a burn of 50 above the index — each landing, at each point of the window and in every taking, leaves the
// moved cut's reading unflagged (holds, short, or BROKEN within the lag's bound); with a forged mint of 1,000 on
// Ethereum the same landings keep it flagged at the first poll (the parent: no verdict, nothing flagged).
func TestF18aLandingInsideTheWindowOnlyTakesFromTheRightSide(t *testing.T) {
	type landing struct {
		name  string
		point string // afterFirst, afterRoutes, afterHead
		land  func(s *f18aScene, r *monRig, n *f18aNode)
	}
	landings := []landing{
		{"the in-flight burn credited", "afterFirst", (*f18aScene).creditInFlight},
		{"the in-flight burn credited", "afterRoutes", (*f18aScene).creditInFlight},
		{"the in-flight burn credited", "afterHead", (*f18aScene).creditInFlight},
		{"the burn above the index credited (a lag)", "afterFirst", (*f18aScene).creditAbove},
		{"the burn above the index credited (a lag)", "afterRoutes", (*f18aScene).creditAbove},
		{"a send dispatched", "afterFirst", (*f18aScene).dispatch},
		{"a send dispatched", "afterRoutes", (*f18aScene).dispatch},
		{"a stranger's delivery", "afterRoutes", (*f18aScene).strangerDelivery},
	}
	for _, forged := range []bool{true, false} {
		for _, l := range landings {
			r, n := f18aMonRig(t)
			s := newF18aScene(t, r, n, forged)
			land := func() { l.land(s, r, n) }
			switch l.point {
			case "afterFirst":
				n.afterFirst = land
			case "afterRoutes":
				n.afterRoutes = land
			case "afterHead":
				n.afterHead = land
			}
			r.step(10 * time.Second)
			if err := r.poll(); err != nil {
				t.Fatal(err)
			}
			id, _ := r.m.Status()["identity"].(map[string]any)["GNOT"].(map[string]any)
			t.Logf("forged=%v, %s %s: %d takings, %d landed; identity %v (%v); flags %v", forged, l.name, l.point, n.takingsSoFar(), s.landed, id["verdict"], id["text"], r.gnoFlags())
			if !forged && (len(r.gnoFlags()) > 0 || r.snd.count() > 0) {
				t.Fatalf("an honest landing, %s %s, was flagged across the moving cut: %v (%v)", l.name, l.point, r.gnoFlags(), id)
			}
			if forged && (len(r.gnoFlags()) == 0 || r.snd.count() == 0) {
				t.Fatalf("a forged mint under %s %s was not flagged across the moving cut: %v (%v)", l.name, l.point, r.m.Alarm.StandingKeys(), id)
			}
			if n.takingsSoFar() != f18aTries || s.landed < f18aTries {
				t.Fatalf("%s %s: the window did not move in every taking: %d takings, %d landed", l.name, l.point, n.takingsSoFar(), s.landed)
			}
		}
	}
}

// f18aScene is the honest bridge the window's proof lands on: GNOT locked 1,300 on gno.land; on Ethereum 1,200 minted,
// less a burn of 200 in flight (block 90, below the finalized block 100: the supply at F 1,000) and a burn of 50 above
// the index (block 150: both providers, current, show it); a send of 100 locked on gno.land and not yet delivered on
// Ethereum. The identity holds: 1,000 + 0 owed + 200 in + 100 out = 1,300. Forged, a mint of 1,000 more on Ethereum.
type f18aScene struct {
	r               *monRig
	n               *f18aNode
	inflight, above string // the burns' ids
	owed, lock      int64
	sends           uint32
	landed          int
	credited        map[string]bool
}

func newF18aScene(t *testing.T, r *monRig, n *f18aNode, forged bool) *f18aScene {
	s := &f18aScene{r: r, n: n, lock: 1200, credited: map[string]bool{}} // the send below locks 100 more
	gnot := gnoside.RealmKey(m3bNS + "hlgnot")
	r.chain.view(m3bMailbox, "delivered", func(uint64, []any) []any { return []any{false} })
	minted := int64(1200)
	if forged {
		minted += 1000
	}
	r.chain.setSupply(m3bGNOT, 2, minted)
	in := r.chain.burn(m3bGNOT, gnot, r.alice, 200, 90)
	ab := r.chain.burn(m3bGNOT, gnot, r.alice, 50, 150)
	s.inflight, s.above = hex.EncodeToString(in[:]), hex.EncodeToString(ab[:])
	s.addSend(r.gno.height)
	s.setRoute()
	return s
}

func (s *f18aScene) setRoute() {
	s.r.gno.set(m3bNS+"hlgnot", "json", m3bRouteJSON(s.lock, s.owed, 0, 0))
}

// addSend dispatches a send of 100 from hlgnot to GNOT on Ethereum at gno height h: hlmailbox's message and count, and
// the route's lock (the route json is set by the caller).
func (s *f18aScene) addSend(h int64) {
	n := s.sends
	s.sends++
	s.r.gno.set(m3bMailPath, "msg/"+strconv.FormatUint(uint64(n), 10), f18aSendMsg(n, 100, h))
	b := fmt.Sprintf(`{"domain":%d,"live":true,"count":%d,"signers":{"keys":[],"threshold":2},"seats":["","","","","%s",""],"routes":{}}`, m3bGnoDom, s.sends, monWatchGno)
	s.r.gno.set(m3bMailPath, "json", b)
	s.lock += 100
}

// credit credits one of the scene's burns once (owed by its amount, its mark at a new block), and lands a stranger's
// delivery at every later taking (a delivery to no route: hlmailbox's count alone moves), so every taking moves.
func (s *f18aScene) credit(id string, amount int64) {
	s.landed++
	if s.credited[id] {
		s.n.deliver(fmt.Sprintf("%064x", 0xd0d0+s.landed))
		return
	}
	s.credited[id] = true
	s.owed += amount
	s.n.deliver(id)
	s.setRoute()
}

func (s *f18aScene) creditInFlight(_ *monRig, _ *f18aNode) { s.credit(s.inflight, 200) }
func (s *f18aScene) creditAbove(_ *monRig, _ *f18aNode)    { s.credit(s.above, 50) }

// dispatch lands a send of 100 in a new block (hlmailbox's count and message, the lock).
func (s *f18aScene) dispatch(_ *monRig, n *f18aNode) {
	s.landed++
	s.addSend(n.newBlock())
	s.setRoute()
}

// strangerDelivery lands a delivery to no route (hlmailbox's count alone moves).
func (s *f18aScene) strangerDelivery(_ *monRig, n *f18aNode) {
	s.landed++
	n.deliver(fmt.Sprintf("%064x", 0xd0d0+s.landed))
}

// f18aSendMsg is hlmailbox's msg/<nonce> render of a send from hlgnot to GNOT on Ethereum.
func f18aSendMsg(nonce uint32, amount uint64, h int64) string {
	m := hl.Message{Version: 3, Nonce: nonce, Origin: m3bGnoDom, Sender: gnoside.RealmKey(m3bNS + "hlgnot"), Destination: m3bEthDom, Recipient: ethx.AddressTo32(m3bGNOT), Body: hl.TokenMessage(ethx.AddressTo32(common.HexToAddress("0x00000000000000000000000000000000000a11ce")), amount, nil)}
	raw := m.Bytes()
	id := hl.ID(raw)
	return fmt.Sprintf(`{"nonce":%d,"id":%q,"root":"%s","height":%d,"message":%q}`, nonce, hex.EncodeToString(id[:]), strings.Repeat("00", 32), h, hex.EncodeToString(raw))
}

// The scene holds as read with nothing landing, and its forged twin is flagged (the window's proof's controls).
func TestF18aWindowSceneControls(t *testing.T) {
	for _, forged := range []bool{false, true} {
		r, n := f18aMonRig(t)
		newF18aScene(t, r, n, forged)
		r.step(10 * time.Second)
		if err := r.poll(); err != nil {
			t.Fatal(err)
		}
		id, _ := r.m.Status()["identity"].(map[string]any)["GNOT"].(map[string]any)
		t.Logf("forged=%v: identity %v (%v); flags %v", forged, id["verdict"], id["text"], r.gnoFlags())
		if forged != (len(r.gnoFlags()) > 0) || (!forged && id["verdict"] != "holds") {
			t.Fatalf("forged=%v: identity %v, flags %v", forged, id, r.gnoFlags())
		}
	}
}

// E6-GT4, item 1: E5-GT1's bound reads the marks above the index largest first, so what stays unread past its 200 —
// counted as credited — is the smallest. A forged credit of 50,000 under 1,000 of the forger's dust and five of its burns
// of 25,000 above the index (all uncredited) is flagged at the first poll: the five are read, the unread 800 are dust,
// a bound of 800 (the parent: by id, 805 unread worth 100,801 — the forgery waited E4-GT1's 13–19 minutes again).
func TestF18aAboveMarksReadLargestFirst(t *testing.T) {
	r, _ := f18aMonRig(t)
	r.chain.setSupply(m3bGNOT, 1, 1_000_000)
	r.gno.set(m3bNS+"hlgnot", "json", m3bRouteJSON(1_000_000, 0, 0, 0))
	r.step(10 * time.Second)
	if err := r.poll(); err != nil {
		t.Fatal(err)
	}
	gnot := gnoside.RealmKey(m3bNS + "hlgnot")
	for i := 0; i < 1000; i++ {
		r.chain.burn(m3bGNOT, gnot, r.alice, 1, r.chain.finalized+5)
	}
	for i := 0; i < 5; i++ {
		r.chain.burn(m3bGNOT, gnot, r.alice, 25_000, r.chain.finalized+6)
	}
	r.gno.set(m3bMailPath, "delivered/"+strings.Repeat("f1", 32), strconv.FormatInt(r.gno.height, 10))
	r.gno.set(m3bNS+"hlgnot", "json", m3bRouteJSON(1_000_000, 50_000, 0, 0))
	r.step(10 * time.Second)
	if err := r.poll(); err != nil {
		t.Fatal(err)
	}
	unread, unreadAmt := 0, int64(0)
	if a := r.m.above; a != nil {
		for _, b := range a.burns {
			if b.Credited == -1 {
				unread++
				unreadAmt += b.Amount
			}
		}
	}
	t.Logf("flags %v, eth flag txs %d, pending %d; above the index %d unread (amount %d)", r.gnoFlags(), r.snd.count(), len(r.m.state.Pending), unread, unreadAmt)
	if len(r.gnoFlags()) == 0 || r.snd.count() == 0 || len(r.m.state.Pending) != 0 || unreadAmt >= 25_000 {
		t.Fatalf("the forged credit under the forger's burns above the index was not flagged at once: flags %v, pending %v, unread %d (%d)", r.gnoFlags(), r.m.state.Pending, unread, unreadAmt)
	}
}

// The projection's guard (cutorder.go): a mark read after the window and set above its head is a credit after the
// window, its burn still in flight in the window's state. E3-GT2's attack: a forged mint of 300 burned back (in flight)
// halts the signer; the honest credit of 300 lands just after the window — owed, read in it, lacks it — and its mark is
// read set (the BROKEN reading's re-read of every mark). Taken as credited, the burn would leave flight and the reading
// hold (short by exactly the forgery): the signer would resume and sign the forged burn. Placed by its height, it stays
// in flight: the round reads BROKEN, and the next, with owed holding the credit, too.
func TestF18aCreditAfterTheWindowStaysInFlight(t *testing.T) {
	r := newSignerRig(t)
	n := &f18aNode{m3bGnoNode: r.gno, lastRoute: m3bNS + "hlbubble"}
	r.s.gno = n
	gnot := gnoside.RealmKey(m3bNS + "hlgnot")
	honest := r.chain.burn(m3bGNOT, gnot, r.alice, 300, 80) // nonce 0: alice's 300, in flight
	r.round()
	r.chain.mine(110, 110)
	r.chain.setSupply(m3bGNOT, 105, 1000)          // a forged mint of 300 …
	r.chain.burn(m3bGNOT, gnot, r.alice, 300, 106) // … burned straight back (nonce 1)
	r.step(time.Second)
	r.round()
	if st := r.status(); st["signing"] == true {
		t.Fatalf("the forged mint did not halt the signer: %v", st)
	}
	landed := false
	n.afterWindow = func() {
		if !landed {
			landed = true // right after the window, before the marks are read: a new block
			n.deliver(hex.EncodeToString(honest[:]))
			r.gno.set(m3bNS+"hlgnot", "json", m3bRouteJSON(1000, 300, 0, 0))
		}
	}
	for round := 1; round <= 2; round++ {
		r.step(time.Second)
		r.round()
		st := r.status()
		t.Logf("round %d: landed %v; signing %v; halted %q", round, landed, st["signing"], st["halted"])
		if st["signing"] == true || !strings.Contains(fmt.Sprint(st["halted"]), "broken") {
			t.Fatalf("round %d: the credit landing after the window lifted the halt over the forged burn: %v", round, st)
		}
		if _, err := r.s.readSigned(1); err == nil {
			t.Fatal("the forged burn (nonce 1) was signed")
		}
		// one node's reads, judged: a verdict over the break, never a refusal over a second backend (a credit after the
		// window counted in the window's state is more deliveries placed than hlmailbox counted there)
		if r.alarmed(f18aBackends) || strings.HasPrefix(fmt.Sprint(st["cut"]), "no verdict") {
			t.Fatalf("round %d: one node's reads were not judged: %v", round, st["cut"])
		}
	}
}
