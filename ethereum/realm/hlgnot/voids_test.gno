package hlgnot

import (
	"chain"
	"chain/runtime"
	"strconv"
	"strings"
	"testing"

	"gno.land/p/g1leu8d2vsplhehcfkjg50mwgdpxdkt8tztu95wr/hlroute/v0"
	"gno.land/r/g1leu8d2vsplhehcfkjg50mwgdpxdkt8tztu95wr/eth/hlmailbox"
)

// instCredit books a claim as Process does (st.Credit, the tally, the waiting index's entry): the alone tests' credit.
func instCredit(t *testing.T, id string, to address, amount int64) {
	t.Helper()
	epoch, _ := hlmailbox.PayoutsEpoch(pkgPath)
	h := runtime.ChainHeight()
	if st.Credit(now(), h, epoch, id, to, amount, hlroute.BySigners, false) {
		t.Fatalf("the claim of %d was swept", amount)
	}
	countCredit(h)
	enter(id)
}

// TestAloneVoidBounds (the round-6 realms audit's E6-RL1; voids.gno): the credit tally counts Process's credit and a
// credit by vote's at the heights they come in at; a void names maxVoidIDs ids at most and a range the tally counts
// maxVoidCredits credits at most in, each refused before p/hlroute walks more; a range closes at the block before its
// proposal's, so a credit of the block a void is made in is no range's. Run alone (gnotest.sh): its records would
// overflow the page the whole suite's migration takes in one.
func TestAloneVoidBounds(cur realm, t *testing.T) {
	if Migrated() != "" {
		t.Skip("the route has migrated (the whole suite): the void bounds run alone, on a fresh route")
	}
	st.Send(now(), strings.Repeat("c0", 32), alice, 10_000*gnot) // a lock the claims stand under (nothing is paid here)
	to := func(i int) address { return chain.PackageAddress("probe/void/" + strconv.Itoa(i)) }
	// the tally: Process's credit, at its height
	h := runtime.ChainHeight()
	asRelayer(cur)
	Process(cross(cur), inAlice100Meta, inAlice100Msg)
	if n := creditsIn(hlroute.Range{From: h, To: h}); n != 1 {
		t.Fatalf("the tally at Process's height: %d", n)
	}
	// a credit by vote's, when its Execute applies it
	vid := strings.Repeat("c1", 32)
	asMember(cur)
	pass(cur, NewCreditRequest(cross(cur), vid, carol, 100*gnot))
	skipDays(7)
	asRelayer(cur)
	Execute(cross(cur), st.Ledger.Last())
	if hv := st.ClaimOf(vid).Height; creditsIn(hlroute.Range{From: hv, To: hv}) != 1 {
		t.Fatalf("the tally at the credit by vote's height %d: %d", hv, creditsIn(hlroute.Range{From: hv, To: hv}))
	}
	// a list: maxVoidIDs ids at most
	ids := make([]string, maxVoidIDs+1)
	for i := range ids {
		ids[i] = txgasID(0xc2, i)
		instCredit(t, ids[i], to(i), 25*gnot)
	}
	asMember(cur)
	aborts(t, "hlgnot: a void names "+itoa(maxVoidIDs+1)+" ids, "+itoa(maxVoidIDs)+" at most: void them by more than one decision", func() {
		NewVoidRequest(cross(cur), strings.Join(ids, ","), false, "", "")
	})
	asMember(cur)
	if req := NewVoidRequest(cross(cur), strings.Join(ids[:maxVoidIDs], ","), false, "", ""); !strings.HasPrefix(req.Description(), "void as withheld: "+ids[0]+",") {
		t.Fatalf("the void of %d ids: %s", maxVoidIDs, head(req.Description()))
	}
	// a range: maxVoidCredits+1 credits at one height refused, maxVoidCredits at another taken
	testing.SkipHeights(1)
	hr := runtime.ChainHeight()
	for i := 0; i <= maxVoidCredits; i++ {
		instCredit(t, txgasID(0xc3, i), to(1000+i), 25*gnot)
	}
	testing.SkipHeights(1)
	hs := runtime.ChainHeight()
	for i := 0; i < maxVoidCredits; i++ {
		instCredit(t, txgasID(0xc4, i), to(2000+i), 25*gnot)
	}
	testing.SkipHeights(1)
	asMember(cur)
	aborts(t, "hlgnot: a void of claims credited at heights "+itoa(hr)+"-"+itoa(hr)+" takes more than "+itoa(maxVoidCredits)+" credits", func() {
		NewVoidRequest(cross(cur), "heights:"+itoa(hr)+"-"+itoa(hr), false, "", "")
	})
	asMember(cur)
	aborts(t, "takes more than "+itoa(maxVoidCredits)+" credits", func() { NewVoidRequest(cross(cur), "since:"+itoa(hr), false, "", "") })
	asMember(cur)
	if req := NewVoidRequest(cross(cur), "heights:"+itoa(hs)+"-"+itoa(hs), false, "", ""); req.Description() != "void as withheld: claims credited at heights "+itoa(hs)+"-"+itoa(hs) {
		t.Fatalf("the range of %d credits: %s", maxVoidCredits, req.Description())
	}
	// a range closes at the block before its proposal's: this block's credit is no range's until the next block
	hn := runtime.ChainHeight()
	instCredit(t, txgasID(0xc5, 0), to(3000), 25*gnot)
	asMember(cur)
	aborts(t, "no claim to void in claims credited at heights "+itoa(hn)+"-"+itoa(hn-1), func() {
		NewVoidRequest(cross(cur), "heights:"+itoa(hn)+"-"+itoa(hn), false, "", "")
	})
	testing.SkipHeights(1)
	asMember(cur)
	if req := NewVoidRequest(cross(cur), "since:"+itoa(hn), false, "", ""); req.Description() != "void as withheld: the verifier's credits since height "+itoa(hn)+" up to height "+itoa(hn) {
		t.Fatalf("the since range a block on: %s", req.Description())
	}
	checkWaiting(t, "TestAloneVoidBounds")
}
