package hlgov

import (
	"strconv"
	"strings"
	"testing"

	"gno.land/r/g1leu8d2vsplhehcfkjg50mwgdpxdkt8tztu95wr/eth/hlmailbox"
)

// TestMainnetPlaceholders (the round-2 realms audit's E2-RL1): on gnoland-1 init refuses the deploy while an Ethereum
// seat is still one of the dev tree's six — on its own line or another's, in any spelling — or the decision
// recipient is the dev tree's HlAdmin, naming which; a staged tree passes, and every other chain is taken as it is.
// The rule is devPlaceholder, called with a gno.land domain: init passes hlmailbox's LocalDomain (the harness runs
// init on a test chain). It reads the compiled values, never the mirror the earlier tests moved.
func TestMainnetPlaceholders(cur realm, t *testing.T) {
	const tail = ": refused on gnoland-1, where the stage step writes the deployed value"
	devSeats := []string{"0x1111111111111111111111111111111111111111", "0x2222222222222222222222222222222222222222",
		"0x3333333333333333333333333333333333333333", "0x4444444444444444444444444444444444444444",
		"0x5555555555555555555555555555555555555555", "0x6666666666666666666666666666666666666666"}
	var compiled [seatCount + 1][32]byte
	for n := 1; n <= seatCount; n++ {
		compiled[n] = mustAddr([]string{"", launchEthSeat1, launchEthSeat2, launchEthSeat3, launchEthSeat4, launchEthSeat5, launchEthSeat6}[n])
		if hlmailbox.EthAddress(compiled[n]) != devSeats[n-1] {
			t.Fatalf("the compiled seat %d is %s, not the dev tree's %s", n, hlmailbox.EthAddress(compiled[n]), devSeats[n-1])
		}
	}
	hlAdmin := ethContract(hlmailbox.EthHlAdmin)
	if hlmailbox.EthAddress(hlAdmin) != launchRecipient {
		t.Fatalf("the compiled HlAdmin %s", hlmailbox.EthAddress(hlAdmin))
	}
	// the dev tree: refused on gnoland-1 (the first seat named), taken on every other chain
	if why := devPlaceholder(hlmailbox.DomainMainnet, compiled, hlAdmin); why != "hlgov: launchEthSeat1 is a dev tree placeholder, "+devSeats[0]+tail {
		t.Fatalf("the dev tree on gnoland-1: %q", why)
	}
	for _, d := range []uint32{hlmailbox.DomainTest, hlmailbox.LocalDomain(), 0, 1, 1198419787} {
		if why := devPlaceholder(d, compiled, hlAdmin); why != "" {
			t.Fatalf("the dev tree on domain %d: %q", d, why)
		}
	}
	// a staged tree: six fresh seats and a fresh HlAdmin
	var seats [seatCount + 1][32]byte
	for n := 1; n <= seatCount; n++ {
		seats[n][12], seats[n][31] = 0xf6, byte(n)
	}
	staged := seats
	var admin [32]byte
	admin[12], admin[31] = 0xf6, 0xad
	if why := devPlaceholder(hlmailbox.DomainMainnet, seats, admin); why != "" {
		t.Fatalf("a staged tree refused: %q", why)
	}
	// each dev seat on each line, in its own spelling and as 64 hex digits
	for j, s := range devSeats {
		for n := 1; n <= seatCount; n++ {
			seats = staged
			spelled := s
			if (j+n)%2 == 1 {
				spelled = strings.Repeat("0", 24) + s[2:]
			}
			seats[n] = mustAddr(spelled)
			if why := devPlaceholder(hlmailbox.DomainMainnet, seats, admin); why != "hlgov: launchEthSeat"+strconv.Itoa(n)+" is a dev tree placeholder, "+s+tail {
				t.Fatalf("dev seat %s on line %d: %q", s, n, why)
			}
		}
	}
	// the dev tree's HlAdmin as the recipient, in lower case; a near miss passes
	if why := devPlaceholder(hlmailbox.DomainMainnet, staged, mustAddr(strings.ToLower(launchRecipient))); why != "hlgov: the decision recipient is the dev tree's HlAdmin, "+launchRecipient+tail {
		t.Fatalf("the dev HlAdmin: %q", why)
	}
	near := hlAdmin
	near[31] ^= 1
	if why := devPlaceholder(hlmailbox.DomainMainnet, staged, near); why != "" {
		t.Fatalf("a near miss refused: %q", why)
	}
}

// TestSeatsFault (the round-3 realms audit's E3-RL2): init refuses, on every chain, Ethereum seats no launch may
// compile — the zero address on any line, and one key on two lines, every pair — as HlAdmin's constructor does
// (Guardians.init); six distinct keys pass, the compiled dev ones among them. guardLaunch, init's guard, asks it.
func TestSeatsFault(cur realm, t *testing.T) {
	var seats [seatCount + 1][32]byte
	for n := 1; n <= seatCount; n++ {
		seats[n][12], seats[n][31] = 0xe7, byte(n)
	}
	if why := seatsFault(seats); why != "" {
		t.Fatalf("six distinct seats: %q", why)
	}
	var compiled [seatCount + 1][32]byte
	for n := 1; n <= seatCount; n++ {
		compiled[n] = mustAddr([]string{"", launchEthSeat1, launchEthSeat2, launchEthSeat3, launchEthSeat4, launchEthSeat5, launchEthSeat6}[n])
	}
	if why := seatsFault(compiled); why != "" {
		t.Fatalf("the compiled seats: %q", why)
	}
	for n := 1; n <= seatCount; n++ {
		s := seats
		s[n] = [32]byte{}
		if why := seatsFault(s); why != "hlgov: launchEthSeat"+strconv.Itoa(n)+" is the zero address, a seat no key holds (HlAdmin refuses it at construction)" {
			t.Fatalf("seat %d zero: %q", n, why)
		}
		for m := 1; m < n; m++ {
			s := seats
			s[n] = s[m]
			if why := seatsFault(s); why != "hlgov: launchEthSeat"+strconv.Itoa(m)+" and launchEthSeat"+strconv.Itoa(n)+" are both "+hlmailbox.EthAddress(s[m])+": a key holds one seat (HlAdmin refuses it at construction)" {
				t.Fatalf("seats %d and %d one key: %q", m, n, why)
			}
		}
	}
}

// TestMaxDecisionBodyFits (E3-RL1): every decision this realm dispatches — at most MaxDecisionBody bytes, the 9-byte
// kind and number included — fits the body the mailbox carries (hlmailbox.MaxBody) with room, so the bound on a
// stranger's Dispatch never refuses one of GovDAO's decisions; the longest a builder writes, a kind 8 with the most
// calldata, is exactly MaxDecisionBody.
func TestMaxDecisionBodyFits(cur realm, t *testing.T) {
	if MaxDecisionBody != 512 || hlmailbox.MaxBody < 4*MaxDecisionBody || 9+3*32+maxUpgradeCall != MaxDecisionBody {
		t.Fatalf("the decision bound %d against the mailbox's %d", MaxDecisionBody, hlmailbox.MaxBody)
	}
}

// TestTitles (E3-RL6): every proposal this realm's builders make is titled "eth/hlgov: " and its kind — the Solana
// deployment's r/hlgov titles its "hlgov: …", and GovDAO's list shows titles alone.
func TestTitles(cur realm, t *testing.T) {
	p := NewUnpauseRequest(cross(cur), 1).Proposal()
	f := NewSetProtocolFeeRequest(cross(cur), 1, "0x5aAeb6053F3E94C9b9A09f33669435E7Ef1BeAed").Proposal()
	if p.Title() != "eth/hlgov: unpause" || f.Title() != "eth/hlgov: set protocol fee" {
		t.Fatalf("titles %q %q", p.Title(), f.Title())
	}
}

// TestInitGuarded (E3-RL7): init called its guard; guardedOn is what the call returned, the gno.land domain it checked
// the launch values at.
func TestInitGuarded(cur realm, t *testing.T) {
	if guardedOn == 0 || guardedOn != hlmailbox.LocalDomain() {
		t.Fatalf("init never ran its guard (guardLaunch): guardedOn is %d", guardedOn)
	}
}

// TestDecisionsJSONPages (E3-RL4): "decisions.json/<from>" is this realm's ledger in pages (hlmailbox.DecisionsJSONPage,
// whose own suite holds the paging): the page from 1 holds the first objects "decisions.json" holds; past the last,
// and for no number, a page is "[]".
func TestDecisionsJSONPages(cur realm, t *testing.T) {
	whole, page := Render("decisions.json"), Render("decisions.json/1")
	if ledger.Last() == 0 || page == "[]" || !strings.HasPrefix(whole, strings.TrimSuffix(page, "]")) || Render("decisions.json/0") != page {
		t.Fatalf("the first page is not the ledger's first objects:\n%s\n%s", page, whole)
	}
	if Render("decisions.json/"+strconv.FormatInt(ledger.Last()+1, 10)) != "[]" || Render("decisions.json/x") != "[]" {
		t.Fatalf("a page past the last")
	}
}
