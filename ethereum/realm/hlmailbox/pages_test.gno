package hlmailbox

import (
	"strconv"
	"strings"
	"testing"

	"gno.land/p/g1leu8d2vsplhehcfkjg50mwgdpxdkt8tztu95wr/decisions/v0"
	hl "gno.land/p/g1leu8d2vsplhehcfkjg50mwgdpxdkt8tztu95wr/hyperlane/v0"
	"gno.land/p/nt/testutils/v0"
)

// The pages the bridge's machines read, at their bounds (the round-3 realms audit's E3-RL1 and E3-RL4). The tests
// named TestRenderGas… render one such page each and do nothing else, so the "--- GAS:" line gno test prints for each
// is that page's cost in the harness; eth/scripts/gnotest.sh's render-gas gate holds every one under RENDER_GAS_MAX, a
// third of gnoland-1's vm/qrender budget of 3,000,000,000. The setups before them are not probes.

const capSender = namespace + "hlcapsender"

// TestMaxBody (E3-RL1): Dispatch carries a body of MaxBody bytes and refuses one byte more — a realm's or a
// stranger's MsgCall alike — before anything is stored or emitted: the refused call leaves the count and the root as
// they were (revive keeps what a refused call wrote in this harness, so a check placed after the writes would show
// here). The bridge's own bodies fit with room: a route's token message is 64 bytes (p/hyperlane's TokenMessage, no
// metadata, whatever the recipient and the amount), r/hlgov's decision at most 512 (held in its own suite).
func TestMaxBody(cur realm, t *testing.T) {
	if n := len(hl.TokenMessage([32]byte{0xff}, 1<<63-1, nil)); n != 64 || MaxBody != 2048 || n*32 != MaxBody {
		t.Fatalf("a token message is %d bytes, the bound %d", n, MaxBody)
	}
	n0, root0 := Count(), Root()
	testing.SetRealm(testing.NewCodeRealm(capSender))
	aborts(t, "hlmailbox: the body is 2049 bytes, above the 2048 a message carries (MaxBody)", func() { Dispatch(cross(cur), ethTest, [32]byte{1}, make([]byte, MaxBody+1)) })
	testing.SetRealm(testing.NewUserRealm(stranger))
	aborts(t, "hlmailbox: the body is 50000 bytes, above the 2048 a message carries (MaxBody)", func() { Dispatch(cross(cur), ethTest, [32]byte{1}, make([]byte, 50_000)) })
	if Count() != n0 || Root() != root0 {
		t.Fatalf("a refused body was kept: count %d (was %d)", Count(), n0)
	}
	testing.SetRealm(testing.NewCodeRealm(capSender))
	Dispatch(cross(cur), ethTest, [32]byte{1}, make([]byte, MaxBody))
	raw, _ := Message(n0)
	m, err := hl.ParseMessage(unhex(raw))
	if Count() != n0+1 || err != nil || len(m.Body) != MaxBody {
		t.Fatalf("a body of MaxBody bytes: count %d, %v, %d", Count(), err, len(m.Body))
	}
}

// TestHexQuote (E3-RL1): msg/<nonce> and msgs/<from> write their hex between plain quotes (hq), byte for byte what
// strconv.Quote wrote before (q(hx(…))): every byte value, every length to 64, and whole pages against the old form
// built here — the latest message's (TestMaxBody's, a MaxBody body) and a msgs/ page of the last four.
func TestHexQuote(cur realm, t *testing.T) {
	var all []byte
	for i := 0; i < 256; i++ {
		all = append(all, byte(i))
	}
	for n := 0; n <= 64; n++ {
		for _, b := range [][]byte{all[:n], all[256-n:], all[100 : 100+n]} {
			if hq(b) != q(hx(b)) {
				t.Fatalf("%x: %s, not %s", b, hq(b), q(hx(b)))
			}
		}
	}
	if hq(all) != q(hx(all)) || hq(nil) != "\"\"" {
		t.Fatalf("all bytes: %s", hq(all))
	}
	old := func(n uint32) string {
		s := sent.Get(nonceKey(n)).(*sentMsg)
		id := hl.ID([]byte(s.raw))
		return "{\"nonce\":" + u32(n) + ",\"id\":" + q(hx(id[:])) + ",\"root\":" + q(hx(s.root[:])) +
			",\"height\":" + strconv.FormatInt(s.height, 10) + ",\"message\":" + q(hx([]byte(s.raw))) + "}"
	}
	last := Count() - 1
	if Render("msg/"+u32(last)) != old(last) {
		t.Fatalf("msg/%d differs from the quoted form:\n%s\n%s", last, Render("msg/"+u32(last)), old(last))
	}
	var parts []string
	for n := last - 3; n <= last; n++ {
		parts = append(parts, old(n))
	}
	if Render("msgs/"+u32(last-3)) != "["+strings.Join(parts, ",")+"]" {
		t.Fatalf("msgs/%d differs from the quoted form", last-3)
	}
}

// lastID is the number of the last decision a "decisions.json/<from>" page holds, as a reader finds it: the "id" of
// its last object (the test texts never hold `{"id":`); 0 for "[]".
func lastID(page string) int64 {
	i := strings.LastIndex(page, "{\"id\":")
	if i < 0 {
		return 0
	}
	s := page[i+len("{\"id\":"):]
	n, _ := strconv.ParseInt(s[:strings.Index(s, ",")], 10, 64)
	return n
}

// readPages reads a ledger's pages as a reader does — from 1, then from the last number a page held plus one, until a
// page is "[]" — and returns their objects joined as one array, and how many pages and objects there were.
func readPages(render func(from string) string) (joined string, pages, objects int) {
	var inner []string
	from := int64(1)
	for pages < 1000 {
		p := render(strconv.FormatInt(from, 10))
		if p == "[]" {
			break
		}
		pages++
		objects += strings.Count(p, "{\"id\":")
		inner = append(inner, p[1:len(p)-1])
		from = lastID(p) + 1
	}
	return "[" + strings.Join(inner, ",") + "]", pages, objects
}

// asciiJSON is p/decisions' JSON with every rune outside ASCII written as its \u escape, as the pages write it: their
// strings go through strconv.QuoteToASCII (q, E5-RL3), the same JSON values in ASCII alone.
func asciiJSON(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r < 0x80 {
			b.WriteByte(byte(r))
			continue
		}
		h := strconv.FormatInt(int64(r), 16)
		b.WriteString("\\u" + strings.Repeat("0", 4-len(h)) + h)
	}
	return b.String()
}

// TestDecisionsJSONPage (E3-RL4): a "decisions.json/<from>" page holds the decisions numbered from <from> on, at most
// DecisionsPageMax and no further one once its text would pass DecisionsPageText bytes (but always the first), each
// object as "decisions.json" writes it, byte for byte but for its runes outside ASCII, which the page escapes
// (asciiJSON, E5-RL3) — so the pages read on until "[]" are the whole ledger — over
// texts that need every escape strconv.Quote makes, and every state; "[]" for an empty ledger, past the last, and for
// a <from> that is no number, negative or past int64; 0 reads as 1. The ledger here: 25 short decisions, four of 6,000
// bytes, 16 short ones — read as pages 1–20 (the count), 21–27 (the text: the 28th would pass it, though more follow)
// and 28–45. The realm's own Render routes the path to its own ledger, whose pages are its "decisions.json" too.
func TestDecisionsJSONPage(cur realm, t *testing.T) {
	var l decisions.Ledger
	for _, from := range []string{"0", "1", "2"} {
		if DecisionsJSONPage(&l, from) != "[]" {
			t.Fatalf("an empty ledger from %s: %s", from, DecisionsJSONPage(&l, from))
		}
	}
	texts := []string{"plain", "a quote \" and a backslash \\", "new\nline\ttab\r", "unicode — em dash, 日本, \u00a0", "\x00\x01\x1f\x7f control", ""}
	record := func(text string, i int) {
		d := l.Record("kind "+strconv.Itoa(i%3), text, nil, 100, 200+int64(i), 300+int64(i))
		switch i % 4 {
		case 1:
			l.Apply(d, 400, "note \"quoted\"\n")
		case 2:
			l.Cancel(d.ID, 500)
		case 3:
			l.Spend(d, "spent")
		}
	}
	for i := 0; i < 25; i++ {
		record(texts[i%len(texts)], i)
	}
	for i := 25; i < 29; i++ {
		record(strings.Repeat("x", 6000), i)
	}
	for i := 29; i < 45; i++ {
		record(texts[i%len(texts)], i)
	}
	size := func(id int64) int {
		d := l.Get(id)
		return len(d.Kind) + len(d.Text) + len(d.State()) + len(d.Note)
	}
	whole := asciiJSON(l.JSON())
	joined, pages, objects := readPages(func(from string) string { return DecisionsJSONPage(&l, from) })
	if joined != whole || pages != 3 || objects != 45 {
		t.Fatalf("the pages joined (%d pages, %d objects) are not decisions.json:\n%s\n%s", pages, objects, joined, whole)
	}
	for _, c := range []struct{ from, last int64 }{{1, 20}, {21, 27}, {28, 45}, {26, 27}, {27, 28}, {45, 45}} {
		p := DecisionsJSONPage(&l, strconv.FormatInt(c.from, 10))
		text := 0
		for id := c.from; id <= c.last; id++ {
			text += size(id)
		}
		if !strings.HasPrefix(p, "[{\"id\":"+strconv.FormatInt(c.from, 10)+",") || lastID(p) != c.last || strings.Count(p, "{\"id\":") != int(c.last-c.from+1) || text > DecisionsPageText {
			t.Fatalf("the page from %d: to %d (want %d), %d bytes of text", c.from, lastID(p), c.last, text)
		}
	}
	if size(21)+size(22)+size(23)+size(24)+size(25)+size(26)+size(27)+size(28) <= DecisionsPageText {
		t.Fatalf("the 28th would not pass the text bound")
	}
	if DecisionsJSONPage(&l, "0") != DecisionsJSONPage(&l, "1") {
		t.Fatalf("0 is not read as 1")
	}
	for _, from := range []string{"46", "1000", "9223372036854775807", "9223372036854775808", "-1", "x", "", " 1", "1 ", "0x1", "+1"} {
		if p := DecisionsJSONPage(&l, from); p != "[]" {
			t.Fatalf("from %q: %s", from, p)
		}
	}
	// the realm's own path: its ledger's pages, whose objects are its decisions.json
	if Render("decisions.json/x") != "[]" || Render("decisions.json/"+strconv.FormatInt(ledger.Last()+1, 10)) != "[]" {
		t.Fatalf("the realm's page past the last: %s", Render("decisions.json/x"))
	}
	joined, _, objects = readPages(func(from string) string { return Render("decisions.json/" + from) })
	if ledger.Last() == 0 || joined != asciiJSON(Render("decisions.json")) || int64(objects) != ledger.Last() {
		t.Fatalf("the realm's pages (%d objects) are not its decisions.json (%d)", objects, ledger.Last())
	}
}

// capFirst is the first of the twenty messages with a MaxBody body that TestCapMessagesSetup dispatches.
var capFirst uint32

// TestCapMessagesSetup dispatches twenty messages with a body of MaxBody bytes, every byte 0xff (hex is one cost
// whatever the byte), for the probes below: not a probe.
func TestCapMessagesSetup(cur realm, t *testing.T) {
	capFirst = Count()
	body := []byte(strings.Repeat("\xff", MaxBody))
	for i := 0; i < pageMax; i++ {
		testing.SetRealm(testing.NewCodeRealm(capSender))
		Dispatch(cross(cur), ethTest, [32]byte{2}, body)
	}
	if Count() != capFirst+pageMax {
		t.Fatalf("count %d", Count())
	}
}

// TestRenderGasMsgAtCap: msg/<nonce> of a MaxBody body, the page every walk reads a nonce at a time.
func TestRenderGasMsgAtCap(cur realm, t *testing.T) {
	if s := Render("msg/" + u32(capFirst)); len(s) < 2*(77+MaxBody) {
		t.Fatalf("msg/%d: %d bytes", capFirst, len(s))
	}
}

// TestRenderGasMsgsAtCap: a msgs/<from> page of twenty MaxBody bodies, the most a page holds.
func TestRenderGasMsgsAtCap(cur realm, t *testing.T) {
	if s := Render("msgs/" + u32(capFirst)); len(s) < pageMax*2*(77+MaxBody) {
		t.Fatalf("msgs/%d: %d bytes", capFirst, len(s))
	}
}

// gasLedger holds the decisions two of the decision page probes render: twenty as r/hlgov writes a kind 6 (the round-3
// audit's ≈475 bytes of JSON each), then twenty whose text together fills a page's DecisionsPageText. gasLongest is
// the longest decision a builder writes, in this realm's own ledger: the cancel of a rotation naming 255 signer keys
// in two groups, whose text nests the rotation's (≈33 KB).
var (
	gasLedger           decisions.Ledger
	gasFull, gasLongest int64
)

// TestDecisionPagesSetup fills gasLedger, and makes the rotation and its cancel with this realm's builders through
// GovDAO (the governor yielded in TestYield): not a probe.
func TestDecisionPagesSetup(cur realm, t *testing.T) {
	const at = int64(1791955682)
	for i := 0; i < DecisionsPageMax; i++ {
		d := gasLedger.Record("enroll router", "enroll GNOT's router for domain 1198419788: 0x000000000000000000000000AbCdEf0123456789aBcDeF0123456789AbCdEf01 (gno.land/r/g1zdaz6sypk5tdsjc2fzsqrrn4ttn8qzmnl4waqp/eth/hlgnot2)", nil, at, at+int64(i), at+int64(i))
		gasLedger.Apply(d, at+int64(i), "dispatched as decision "+strconv.Itoa(i)+"; gno.land/r/g1zdaz6sypk5tdsjc2fzsqrrn4ttn8qzmnl4waqp/eth/hlgnot2 noted enrolled on Ethereum at height 1234567")
	}
	gasFull = gasLedger.Last() + 1
	each := DecisionsPageText/DecisionsPageMax - len("void") - len("applied") - len("applied after the delay; 1 voided")
	for i := 0; i < DecisionsPageMax; i++ {
		d := gasLedger.Record("void", strings.Repeat("v", each), nil, at, at, at+decisions.Delay)
		gasLedger.Apply(d, at+decisions.Delay, "applied after the delay; 1 voided")
	}
	keys := make([]Key, maxSigners)
	for i := range keys {
		keys[i][0], keys[i][1] = byte(i), 0xab
	}
	pass(cur, NewRotateSignersRequest(cross(cur), keysHex(keys), 0, keysHex(keys[:127]), keysHex(keys[127:])))
	rotation := ledger.Last()
	pass(cur, NewCancelRequest(cross(cur), rotation))
	gasLongest = ledger.Last()
	if d := ledger.Get(gasLongest); d.Kind != "cancel" || len(d.Text) < len(ledger.Get(rotation).Text) || len(d.Text) < 33_000 || ledger.Get(rotation).State() != "cancelled" {
		t.Fatalf("the longest decision: %s, %d bytes", d.Kind, len(d.Text))
	}
}

// TestRenderGasDecisionsPage: a page of twenty decisions as r/hlgov writes them.
func TestRenderGasDecisionsPage(cur realm, t *testing.T) {
	if p := DecisionsJSONPage(&gasLedger, "1"); lastID(p) != DecisionsPageMax {
		t.Fatalf("the page holds %d", lastID(p))
	}
}

// TestRenderGasDecisionsFull: the fullest page of many decisions, twenty whose text fills DecisionsPageText.
func TestRenderGasDecisionsFull(cur realm, t *testing.T) {
	if p := DecisionsJSONPage(&gasLedger, strconv.FormatInt(gasFull, 10)); lastID(p) != gasFull+DecisionsPageMax-1 {
		t.Fatalf("the full page ends at %d", lastID(p))
	}
}

// TestRenderGasDecisionsLongest: this realm's page of the longest decision a builder writes, as the machines read it.
func TestRenderGasDecisionsLongest(cur realm, t *testing.T) {
	if p := Render("decisions.json/" + strconv.FormatInt(gasLongest, 10)); lastID(p) != gasLongest || len(p) < 33_000 {
		t.Fatalf("the longest page: %d bytes", len(p))
	}
}

// routeFlagsJSON is the "flags" array the json writes for route, cut out of the json j.
func routeFlagsJSON(j, route string) string {
	i := strings.Index(j, q(route)+":{\"epoch\":")
	if i < 0 {
		return ""
	}
	a := i + strings.Index(j[i:], ",\"flags\":[") + len(",\"flags\":")
	return j[a : a+strings.Index(j[a:], ",\"predecessor\":")]
}

// TestMailboxJSONFlags (E3-RL1's class on the json the machines read): the json writes each route's standing flags as
// strconv.Quote wrote Flags' lines — a guardian's payouts flag naming ids, one naming a range, a watch key's flag
// naming nothing — and the signer keys as q(hx(k)) wrote them, byte for byte, the flags' ids and the keys now written
// without strconv.Quote's cost.
func TestMailboxJSONFlags(cur realm, t *testing.T) {
	for n := 1; n <= seatCount; n++ {
		seats[n] = testutils.TestAddress("f10-seat-" + strconv.Itoa(n))
	}
	testing.SetRealm(testing.NewUserRealm(Seat(1)))
	SetFlag(cross(cur), launchRouteGNOT, FnPayouts, strings.Repeat("ab", 32)+",0x"+strings.Repeat("CD", 32))
	testing.SetRealm(testing.NewUserRealm(Seat(2)))
	SetFlag(cross(cur), launchRouteGNOT, FnPayouts, "heights:1-2")
	testing.SetRealm(testing.NewUserRealm(Seat(3)))
	SetFlag(cross(cur), launchRouteBUBBLE, FnPayouts, "since:7")
	testing.SetRealm(testing.NewUserRealm(Seat(5)))
	SetFlag(cross(cur), launchRouteGNOT, FnSends, "")
	j := Render("json")
	if len(Flags(launchRouteGNOT)) < 3 || !strings.Contains(strings.Join(Flags(launchRouteGNOT), "|"), " naming "+strings.Repeat("ab", 32)+","+strings.Repeat("cd", 32)) {
		t.Fatalf("the flags: %v", Flags(launchRouteGNOT))
	}
	for _, route := range Routes() {
		var parts []string
		for _, f := range Flags(route) {
			parts = append(parts, q(f))
		}
		if got, want := routeFlagsJSON(j, route), "["+strings.Join(parts, ",")+"]"; got != want {
			t.Fatalf("%s's flags in the json:\n%s\nnot the quoted lines:\n%s", route, got, want)
		}
	}
	old := func(ks []Key) string {
		parts := make([]string, len(ks))
		for i, k := range ks {
			parts[i] = q(hx(k[:]))
		}
		return "[" + strings.Join(parts, ",") + "]"
	}
	if keysJSON(signerKeys) != old(signerKeys) || !strings.Contains(j, "\"signers\":{\"keys\":"+old(signerKeys)+",") {
		t.Fatalf("the keys: %s", keysJSON(signerKeys))
	}
}

// TestMailboxJSONSetup puts the json at its bound for the probe below: 255 signer keys in two groups (what a rotation
// may name), and every guardian's payouts flag naming 256 ids (maxFlagIDs) on both launch routes. Not a probe.
func TestMailboxJSONSetup(cur realm, t *testing.T) {
	keys := make([]Key, maxSigners)
	for i := range keys {
		keys[i][0], keys[i][1] = byte(i), 0xcd
	}
	signerKeys, signerRule = keys, Rule{GroupA: keys[:127], GroupB: keys[127:]}
	var ids []string
	for i := 0; i < maxFlagIDs; i++ {
		h := strconv.FormatInt(int64(i), 16)
		ids = append(ids, strings.Repeat("e", 64-len(h))+h)
	}
	list := strings.Join(ids, ",")
	for _, route := range []string{launchRouteGNOT, launchRouteBUBBLE} {
		for n := 1; n <= guardians; n++ {
			testing.SetRealm(testing.NewUserRealm(Seat(n)))
			SetFlag(cross(cur), route, FnPayouts, list)
		}
	}
	if len(Flags(launchRouteBUBBLE)) < guardians {
		t.Fatalf("the flags: %d", len(Flags(launchRouteBUBBLE)))
	}
}

// TestRenderGasMailboxJSON: the json the machines read for the signers, the seats and the flags, at its bound.
func TestRenderGasMailboxJSON(cur realm, t *testing.T) {
	if j := Render("json"); len(j) < 2*guardians*maxFlagIDs*65 {
		t.Fatalf("the json: %d bytes", len(j))
	}
}

// deliveredProbe is the last of the ids TestDeliveredSetup marks delivered.
var deliveredProbe string

// TestDeliveredSetup marks 2,000 message ids delivered, as a route's credit by vote does (done: id → height), so
// delivered/<id> reads a tree of that size (E4-RL2: every page the machines read has a probe).
func TestDeliveredSetup(cur realm, t *testing.T) {
	for i := 0; i < 2000; i++ {
		h := strconv.FormatInt(int64(i), 16)
		id := strings.Repeat("d", 56) + strings.Repeat("0", 8-len(h)) + h
		testing.SetRealm(testing.NewCodeRealm(launchRouteGNOT))
		MarkDone(cross(cur), id)
		deliveredProbe = id
	}
}

// TestRenderGasDelivered: delivered/<id>, the page the relayers and the monitors read a burn's delivery by.
func TestRenderGasDelivered(cur realm, t *testing.T) {
	if Render("delivered/"+deliveredProbe) == "0" {
		t.Fatalf("%s not delivered", deliveredProbe)
	}
}
