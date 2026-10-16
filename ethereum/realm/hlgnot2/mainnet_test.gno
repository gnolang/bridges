package hlgnot2

import (
	"strconv"
	"strings"
	"testing"

	"gno.land/r/g1leu8d2vsplhehcfkjg50mwgdpxdkt8tztu95wr/eth/hlmailbox"
)

// TestMainnetPlaceholder (the round-2 realms audit's E2-RL1): on gnoland-1 init refuses the deploy while the launch
// router is one of the dev tree's two (this route's own placeholder, token 0 (GNOT)'s, or the other token's), in any
// spelling, naming it; a staged router passes, and every other chain is taken as it is. The rule is devPlaceholder,
// called with a gno.land domain: init passes hlmailbox's LocalDomain (the harness runs init on a test chain).
func TestMainnetPlaceholder(cur realm, t *testing.T) {
	const tail = ": refused on gnoland-1, where the stage step writes the deployed value"
	own, other := "0x0BA554Aa0D0566e582dE0238a089Ec102c2e05a4", "0x051D52Fd082d902f8E0CF300a60304D484cf2d07" // the dev tree's two routers, spelled out
	compiled := mustAddr(launchRouter)
	if hlmailbox.EthAddress(compiled) != own {
		t.Fatalf("the compiled router %s", hlmailbox.EthAddress(compiled))
	}
	if why := devPlaceholder(hlmailbox.DomainMainnet, compiled); why != "hlgnot2: launchRouter is a dev tree placeholder, "+own+tail {
		t.Fatalf("the dev router on gnoland-1: %q", why)
	}
	for _, d := range []uint32{hlmailbox.DomainTest, hlmailbox.LocalDomain(), 0, 1} {
		if why := devPlaceholder(d, compiled); why != "" {
			t.Fatalf("the dev router on domain %d: %q", d, why)
		}
	}
	for _, s := range []string{other, strings.ToLower(own), "0x" + strings.ToUpper(other[2:]), strings.Repeat("0", 24) + other[2:]} {
		k := mustAddr(s)
		if why := devPlaceholder(hlmailbox.DomainMainnet, k); why != "hlgnot2: launchRouter is a dev tree placeholder, "+hlmailbox.EthAddress(k)+tail || !strings.EqualFold(hlmailbox.EthAddress(k)[2:], s[len(s)-40:]) {
			t.Fatalf("router %s: %q", s, why)
		}
	}
	staged := compiled
	staged[31] ^= 1 // one bit off: a real router as far as the guard knows
	if why := devPlaceholder(hlmailbox.DomainMainnet, staged); why != "" {
		t.Fatalf("a staged router refused: %q", why)
	}
}

// TestInitGuarded (the round-3 realms audit's E3-RL7): init called its guard. guardedOn is what the call returned, the
// gno.land domain it checked the launch values at: a tree whose init lost the call fails here.
func TestInitGuarded(cur realm, t *testing.T) {
	if guardedOn == 0 || guardedOn != hlmailbox.LocalDomain() {
		t.Fatalf("init never ran its guard (guardLaunch): guardedOn is %d", guardedOn)
	}
}

// TestDecisionsJSONPages (the round-3 realms audit's E3-RL4): "decisions.json/<from>" is this route's ledger in pages
// (hlmailbox.DecisionsJSONPage, whose own suite holds the paging): the page from 1 holds the first objects
// "decisions.json" holds, byte for byte; past the last, and for no number, a page is "[]".
func TestDecisionsJSONPages(cur realm, t *testing.T) {
	whole, page := Render("decisions.json"), Render("decisions.json/1")
	if st.Ledger.Last() == 0 || page == "[]" || !strings.HasPrefix(whole, strings.TrimSuffix(page, "]")) || Render("decisions.json/0") != page {
		t.Fatalf("the first page is not the ledger's first objects:\n%s\n%s", page, whole)
	}
	if Render("decisions.json/"+strconv.FormatInt(st.Ledger.Last()+1, 10)) != "[]" || Render("decisions.json/x") != "[]" {
		t.Fatalf("a page past the last")
	}
}
