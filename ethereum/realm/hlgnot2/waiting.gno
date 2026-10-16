package hlgnot2

import (
	"strconv"
	"strings"

	"gno.land/p/g1leu8d2vsplhehcfkjg50mwgdpxdkt8tztu95wr/hlroute/v0"
	"gno.land/p/nt/avl/v0"
)

// The waiting index (the round-4 realms audit's E4-RL1). p/hlroute's claims/<from> page skips the claims before <from>
// by walking them, every waiting recipient's queue one claim at a time — ≈1.6M gas a claim on a gno v1.5.1 node — so
// past ≈1,600 waiting claims on one route that page is out of vm/qrender's 3,000,000,000, and a monitor's poll stops
// there before the Ethereum checks. p/hlroute is shared with the live Solana deployment and immutable, so this realm
// keeps its own index beside it: every claim that became open here, by the place it took when it did; waiting/<from>
// reads a page of them from any place at the cost of a seek and a page (waitingPage).
//
// Its contract, which the machines read by: every claim open on this route is in the index, once. An entry whose claim
// has left the queue — paid in full, voided, gone with a migration's page — stays until this realm takes it out, and
// the page shows it in its state, for the reader to skip. A migration's page takes its claims out (Pull); every other
// such entry goes with the tidy each credit makes (Process: the next tidyStep entries from where the last tidy stopped,
// wrapping at the end), so the index stays near the open claims without a cost on Claim, the payouts' path. A claim
// that becomes open again — a restore, a credit by vote — takes a new place, above every place given before, so a
// reader that keeps the last place it read reads only what entered since (json's "waitingLast").
var (
	waitingByPos avl.Tree // posKey(place) → claim id
	waitingPos   avl.Tree // claim id → place (int64)
	waitingLast  int64    // the last place given; 0 before any
	waitingTidy  int64    // the place the last tidy looked at last

	// reopens are the claims a pending decision opens only when Execute applies it, by the decision's number: a credit
	// by vote's id, and a restore's ids that were not open once the decision was recorded (its forged claims; a
	// withheld one, restored at once, entered in the closure) — the builders' own reading of their parameters, kept by
	// the closure (p/hlroute's params are unexported) for Execute to enter what the decision re-opened after its 7
	// days, and dropped when Execute applies it or a cancel cancels it (the round-5 realms audit's E5-RL2).
	reopens avl.Tree // strconv of the decision's number → []string ids
)

// tidyStep is how many entries each credit's tidy looks at: two, so the tidy outruns the claims payouts finish — at
// most one an entry — and the index stays within about twice the open claims while credits come.
const tidyStep = 2

// pullPage is how many records a migration page hands to the successor (Pull: st.Export(page, pullPage)): 50, where
// p/hlroute's PullPage, shared with the live Solana routes and immutable, is 500 (the round-5 realms audit's E5-RL1).
// A page of 500 open claims, each leaving this index as the page takes it and entering the successor's, was 5.28B gas
// on a gno v1.5.1 node — past gnoland-1's block of 3,000,000,000 — and page 0 moves the lock, so a migration stalled
// there or later freezes the route for good. A page's cost grows with the route (the successor's trees deepen as it
// imports): adopted whole on the node, a page of 100 open claims was 1.09B at 200 claims and 1.85B at 3,000; of 50,
// 0.55B and 1.04B (fold-r5/f17c.md). Pull and Adopt of a page are held under a third of the block at their node figures
// (eth/scripts/gnotest.sh's migration gate, TestTxGasPull and TestTxGasAdopt). It lives here, in the file the three
// routes keep the same (gnotest.sh compares them), so every route pages alike.
const pullPage = 50

// maxRestoreIDs is the most ids a restore names here (NewRestoreRequest), where p/hlroute's lists take 256
// (hlroute.MaxIDs): before the yield the governor's Govern runs the builder and the closure in one transaction, and a
// restore of 256 withheld claims at once — p/hlroute's own parse, checks and restores (≈2.7B on a gno v1.5.1 node) and
// this index's 256 new places — was 3.69B there with the list read once, past the block of 3,000,000,000; 128 is
// 1.93B. A 256-id void is restored by two decisions (E5-RL2; fold-r5/f17c.md).
const maxRestoreIDs = 128

func posKey(n int64) string {
	s := strconv.FormatInt(n, 10)
	return strings.Repeat("0", 19-len(s)) + s
}

// enter puts the claim id, if it is open now, at the next place of the index, out of any place it held: a claim
// credited (Process), credited by a vote, restored, or brought by a predecessor's page.
func enter(id string) {
	// echo-ok(id): a claim id this realm holds (Process's hex, a page's, a builder's cleaned one): 64 hex digits mustID passes
	if c := st.ClaimOf(id); c != nil && c.State == hlroute.Open {
		enterClaim(c)
	}
}

// enterClaim puts an open claim at the next place of the index, out of any place it held: its old entry by place
// goes, and its place by id is replaced in the same Set (three tree writes, not leave's two and two Sets).
func enterClaim(c *hlroute.Claim) {
	if v := waitingPos.Get(c.ID); v != nil {
		waitingByPos.Remove(posKey(v.(int64)))
	}
	waitingLast++
	waitingByPos.Set(posKey(waitingLast), c.ID)
	waitingPos.Set(c.ID, waitingLast)
}

// reopenNow enters every claim of ids that is open now and returns the others (E5-RL2): a decision's closure calls it
// once the decision is recorded — a withheld claim a restore restored at once enters here, and the rest, a restore's
// forged claims and a credit's id, are what its Execute may open after the 7 days (reopens) — and Execute calls it
// once the decision applied. One ClaimOf an id; a claim open already (restored by another decision since the proposal
// was made) takes a new place, as any claim that opens again does.
func reopenNow(ids []string) (later []string) {
	for _, id := range ids {
		// echo-ok(id): a restore's ids out of its draft (CleanID's, restoreList) or a credit's CleanID'd id: 64 hex digits
		if c := st.ClaimOf(id); c != nil && c.State == hlroute.Open {
			enterClaim(c)
		} else {
			later = append(later, id)
		}
	}
	return later
}

// restoreList is the ids a restore's draft names, as p/hlroute's DraftRestore parsed them (E5-RL2): its text is
// "restore: " and the ids — each hlroute.CleanID's 64 lower-case hex digits — joined by commas (p/hlroute v0,
// immutable), so they are cut out by place rather than parsed a second time: hlroute.ParseIDs's repeat check is
// quadratic, ≈0.42B of a 256-id restore on a gno v1.5.1 node. ok is false for a draft of any other form (it cannot
// happen).
func restoreList(d hlroute.Draft) (ids []string, ok bool) {
	const head = "restore: "
	s := d.Text
	if d.Kind != hlroute.KindRestore || !strings.HasPrefix(s, head) || len(s) == len(head) {
		return nil, false
	}
	s = s[len(head):]
	ids = make([]string, 0, (len(s)+1)/65)
	for i := 0; i < len(s); i += 65 {
		if i+64 > len(s) || (i+64 < len(s) && s[i+64] != ',') {
			return nil, false
		}
		ids = append(ids, s[i:i+64])
	}
	return ids, true
}

// leave takes the claim id's entry out of the index, if it has one.
func leave(id string) {
	if v := waitingPos.Get(id); v != nil {
		waitingByPos.Remove(posKey(v.(int64)))
		waitingPos.Remove(id)
	}
}

// tidy looks at the next n entries from where the last tidy stopped, wrapping once to the first, and takes out those
// whose claims are no longer open: paid in full, voided or gone. Each credit makes one (Process), at a fixed cost.
func tidy(n int) {
	var gone []string
	looked := 0
	look := func(from int64) {
		waitingByPos.Iterate(posKey(from), "", func(k string, v any) bool {
			looked++
			waitingTidy, _ = strconv.ParseInt(strings.TrimLeft(k, "0"), 10, 64)
			if c := st.ClaimOf(v.(string)); c == nil || c.State != hlroute.Open {
				gone = append(gone, v.(string))
			}
			return looked >= n
		})
	}
	look(waitingTidy + 1)
	if looked < n {
		waitingTidy = 0
		look(1)
	}
	for _, id := range gone {
		leave(id)
	}
}

// reopened are the claims decision id may make open again (reopens), or nil.
func reopened(id int64) []string {
	if v := reopens.Get(strconv.FormatInt(id, 10)); v != nil {
		return v.([]string)
	}
	return nil
}

// waitingPage is waiting/<from> (E4-RL1): up to hlroute.PageLines entries of the waiting index from place <from> on
// (0 reads as 1), in place order, as a JSON array — each claim/<id>'s object with its place first,
// {"pos":P,"id":…,"to":…,"amount":N,"paid":N,"state":…,"by":…,"at":N,"height":N,"seq":N}, or {"pos":P,"id":…,
// "state":"gone"} for a claim no longer here — and "[]" past the last place or for a <from> that is no number (decimal
// digits alone). A page shorter than hlroute.PageLines is the end. Its cost is a seek and a page wherever <from> is
// (TestRenderGasWaitingPage, at 2,000 waiting claims).
func waitingPage(from string) string {
	n, err := strconv.ParseUint(from, 10, 63)
	if err != nil {
		return "[]"
	}
	var b strings.Builder
	b.WriteString("[")
	k := 0
	waitingByPos.Iterate(posKey(int64(n)), "", func(key string, v any) bool {
		if k > 0 {
			b.WriteString(",")
		}
		b.WriteString(waitingJSON(strings.TrimLeft(key, "0"), v.(string)))
		k++
		return k >= hlroute.PageLines
	})
	b.WriteString("]")
	return b.String()
}

// waitingJSON is one entry: claim/<id>'s object (p/hlroute's ClaimJSON) with its place first, byte for byte. Its
// strings go between plain quotes — a claim's id is 64 lower-case hex digits, its recipient a canonical bech32 address
// (p/hyperlane's Bytes32ToAddress, or a vote's hlmailbox.CanonicalAddress), its state and credit p/hlroute's words,
// none of which strconv.Quote escapes — at a fraction of strconv.Quote's gas (TestWaitingPage holds the forms equal).
func waitingJSON(pos, id string) string {
	c := st.ClaimOf(id) // echo-ok(id): an entry's claim id, 64 hex digits as enter put it
	if c == nil {
		// echo-ok(pos, id, c): the index's place (decimal digits), a claim's id (64 lower-case hex digits), its recipient (canonical bech32), its state and credit (p/hlroute's words)
		return "{\"pos\":" + pos + ",\"id\":\"" + id + "\",\"state\":\"gone\"}"
	}
	// echo-ok(pos, id, c): the index's place (decimal digits), a claim's id (64 lower-case hex digits), its recipient (canonical bech32), its state and credit (p/hlroute's words)
	return "{\"pos\":" + pos + ",\"id\":\"" + c.ID + "\",\"to\":\"" + string(c.To) + "\",\"amount\":" + itoa(c.Amount) +
		",\"paid\":" + itoa(c.Paid) + ",\"state\":\"" + c.State + "\",\"by\":\"" + c.By + "\",\"at\":" + itoa(c.At) +
		",\"height\":" + itoa(c.Height) + ",\"seq\":" + itoa(c.Seq) + "}"
}
