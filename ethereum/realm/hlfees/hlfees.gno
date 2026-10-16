// Package hlfees is the Ethereum bridge's fee realm (eth/bridge/PLAN.md §5.5, the Solana plan's §5.5 on gno.land):
// this deployment's routes pay every fee here — GNOT as coins, BUBBLE through the token realm — and only the
// treasurer a decision names may withdraw, to itself. The treasurer at launch is compiled in below; a decision made
// with the builder changes it, 7 days after it is made — the governor's own through Govern until it yields on
// r/hlmailbox, a GovDAO proposal's after (M1k). A section number alone (§5.3) is the Solana plan's (bridge/PLAN.md),
// which the Ethereum plan carries over; "eth §x" is the Ethereum plan's own.
package hlfees

import (
	"chain"
	"chain/banker"
	"strconv"
	"strings"
	"time"

	"gno.land/p/g1leu8d2vsplhehcfkjg50mwgdpxdkt8tztu95wr/decisions/v0"
	"gno.land/r/g1leu8d2vsplhehcfkjg50mwgdpxdkt8tztu95wr/bubble"
	"gno.land/r/g1leu8d2vsplhehcfkjg50mwgdpxdkt8tztu95wr/eth/hlmailbox"
	"gno.land/r/gov/dao"
)

const (
	namespace = "gno.land/r/g1leu8d2vsplhehcfkjg50mwgdpxdkt8tztu95wr/eth/"
	pkgPath   = namespace + "hlfees" // must match gnomod.toml

	// launchTreasurer is the operator's named key (§5.5, §11.12): a PLACEHOLDER — the deployer's hotkey — until
	// the launch notice names it; GovDAO can name the chain's own address instead at any vote.
	launchTreasurer = address("g1leu8d2vsplhehcfkjg50mwgdpxdkt8tztu95wr")
)

// launchGovernor is the governor (M1k): the mailbox's, compiled here too so a member reads it in this realm's
// served source; init refuses a deploy with another. The e2e swaps it for gnodev's test1.
const launchGovernor = address("g1dpkz6em0wejhymn0wf047h6lta047h6lvxn4gx")

var (
	treasurer = launchTreasurer
	ledger    decisions.Ledger

	// guardedOn is the gno.land domain init's guard checked the launch values at (guardLaunch): the suite reads it
	// (TestInitGuarded), so a tree whose init lost the call fails the suite (E3-RL7, as hlmailbox's guardedOn).
	guardedOn uint32
)

// init refuses a deploy whose launch values are not this deployment's (guardLaunch).
func init() {
	guardedOn = guardLaunch()
}

// guardLaunch is init's guard. It refuses a deploy whose compiled governor is not the mailbox's (M1k): the bridge's
// realms are governed by one key until it yields, or by GovDAO from the first block, never a mix; and, on every
// chain, one whose treasurer is no gno address in its canonical spelling (treasurerFault). It returns the domain it
// checked at.
func guardLaunch() uint32 {
	if launchGovernor != hlmailbox.Governor() {
		// echo-ok(hlmailbox.Governor): the mailbox's compiled governor, a bech32 address
		panic("hlfees: the compiled governor " + string(launchGovernor) + " is not the mailbox's " + string(hlmailbox.Governor()))
	}
	if why := treasurerFault(launchTreasurer); why != "" {
		// echo-ok(why): the guard's own refusal: ASCII words, contract names, EIP-55 and bech32 addresses and pkgpaths, a few hundred bytes at most: far under the cut
		panic(why)
	}
	return hlmailbox.LocalDomain()
}

// treasurerFault is why init refuses a as the launch treasurer, or "" (the round-2 fold's F9-6, E3-RL2): Withdraw
// compares the caller's address — gno's one canonical spelling, bech32 in lower case — with the treasurer as written,
// so a treasurer staged empty, in upper case or with bech32m's checksum (all of which IsValid takes) could never
// withdraw, and the fees would wait for a set-treasurer decision 7 days long. Held on every chain: the treasurer is
// out of the dev-value guard on purpose (any real key may be it), its spelling is not.
func treasurerFault(a address) string {
	if a == "" || hlmailbox.CanonicalAddress(a) != a {
		return "hlfees: launchTreasurer is " + string(a) + ", no gno address in its canonical form (bech32, lower case): no call would ever be the treasurer's, and the fees would wait for a decision naming another"
	}
	return ""
}

type (
	treasurerParams struct{ to address }
	cancelParams    struct{ id int64 }
)

func now() int64 { return time.Now().Unix() }

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

// Address is this realm's address: where the routes pay the fees.
func Address() address { return chain.PackageAddress(pkgPath) }

// Treasurer is the one address that may withdraw.
func Treasurer() address { return treasurer }

// Balances are the fees held: ugnot and BUBBLE base units.
func Balances() (ugnot, bubbles int64) {
	return banker.NewReadonlyBanker().GetCoin(Address(), "ugnot"), bubble.BalanceOf(Address())
}

// Withdraw pays everything held to the treasurer, who alone may call it.
func Withdraw(cur realm) {
	if !cur.IsCurrent() {
		panic("hlfees: that realm value is not live; pass your own cur")
	}
	if cur.Previous().Address() != treasurer {
		panic("hlfees: only the treasurer withdraws")
	}
	ugnot, bubbles := Balances()
	if ugnot > 0 {
		banker.NewBanker(banker.BankerTypeRealmSend, cur).SendCoins(Address(), treasurer, chain.Coins{{"ugnot", ugnot}})
	}
	if bubbles > 0 {
		bubble.Transfer(cross(cur), treasurer, bubbles)
	}
	// echo-ok(treasurer): the treasurer, a canonical bech32 address (treasurerFault at init and at the decision)
	chain.Emit("Withdrawn", "to", string(treasurer), "ugnot", itoa(ugnot), "bubble", itoa(bubbles))
}

// ------------------------------------------------------------ builders --

// Request is what a builder returns (M1k): the proposal GovDAO's proxy executes after the yield (Proposal), and
// the same decision the governor runs directly before it (Govern); see hlgnot's.
type Request struct {
	proposal dao.ProposalRequest
	text     string
	decide   func(cur realm) error
}

// Proposal is the request as a GovDAO proposal: `dao.MustCreateProposal(cross(cur), req.Proposal())`.
func (r Request) Proposal() dao.ProposalRequest { return r.proposal }

// Description is the proposal's: the parameters as the builder wrote them.
func (r Request) Description() string { return r.text }

// who names a previous realm in a refusal: its pkgpath, or a user's address.
func who(path string, a address) string {
	if path != "" {
		return path
	}
	return string(a)
}

// mustBeGovernor is a decision's identity check (M1k), read from the closure's own live cur: the governor's own
// call until it yields on r/hlmailbox, gno.land/r/gov/dao's proxy exactly after (or from the first block, when no
// governor was compiled); see hlgnot's.
func mustBeGovernor(cur realm) {
	prev := cur.Previous()
	if decisions.DAOGoverns(launchGovernor, hlmailbox.Yielded()) {
		if p := prev.PkgPath(); p != decisions.DAOPath {
			// echo-ok(who): the previous realm's pkgpath, ASCII by tm2's path rules, or its bech32 address
			panic("hlfees: only " + decisions.DAOPath + " executes a decision, not " + who(prev.PkgPath(), prev.Address()))
		}
		return
	}
	if !prev.IsUser() || prev.Address() != launchGovernor {
		// echo-ok(who): the previous realm's pkgpath, ASCII by tm2's path rules, or its bech32 address
		panic("hlfees: until the governor yields, " + string(launchGovernor) + " alone decides, by Govern from its own key: not " + who(prev.PkgPath(), prev.Address()) + " (a GovDAO proposal is refused until the yield)")
	}
}

func mustBeLive() {
	if !hlmailbox.Live() {
		panic("hlfees: not launched: before the Launch decision only cancel is proposed")
	}
}

func propose(cur realm, kind, text string, apply func(made, t int64)) Request {
	if !cur.IsCurrent() {
		panic("hlfees: that realm value is not live; pass your own cur")
	}
	made := now()
	cb := func(cur realm) error {
		mustBeGovernor(cur)
		t := now()
		if err := decisions.Expired(made, t); err != nil {
			return err
		}
		apply(made, t)
		return nil
	}
	return Request{proposal: dao.NewProposalRequest(titlePrefix+kind, text, dao.NewSimpleExecutor(0, cur, cb, text)), text: text, decide: cb}
}

// titlePrefix begins every proposal title this realm's builders make (E3-RL6): "eth/" tells this deployment's
// proposals from the Solana deployment's "hlfees: <kind>" on GovDAO's list, which shows titles alone.
const titlePrefix = "eth/hlfees: "

// Govern is the governor's way in before the yield (M1k): the same closure GovDAO's proxy runs after it, called
// non-crossing in this frame so it reads the governor from cur.Previous(); refused for anyone else and for
// everyone once the governor yielded on r/hlmailbox (or when none was compiled); see hlgnot's.
func Govern(cur realm, r Request) {
	if !cur.IsCurrent() {
		panic("hlfees: that realm value is not live; pass your own cur")
	}
	if r.decide == nil {
		panic("hlfees: not a builder's request")
	}
	if yielded := hlmailbox.Yielded(); decisions.DAOGoverns(launchGovernor, yielded) {
		if launchGovernor == "" {
			panic("hlfees: no governor was compiled, " + decisions.DAOPath + " governs from the first block: a decision is a GovDAO proposal, never Govern")
		}
		panic("hlfees: the governor yielded at height " + itoa(yielded) + ", " + decisions.DAOPath + " governs since: a decision is a GovDAO proposal, never Govern")
	}
	mustBeGovernor(cur)
	if err := r.decide(cur); err != nil {
		// echo-ok(err): p/decisions' refusal: ASCII words and numbers
		panic("hlfees: " + err.Error())
	}
}

// NewSetTreasurerRequest names the address that may withdraw, 7 days after the decision is made: in its canonical
// spelling (hlmailbox.CanonicalAddress; eth F9, as the routes' credit and sweep votes read theirs) — IsValid takes
// other spellings of the same twenty bytes too, upper case and bech32m's checksum, which no caller's address ever
// equals, so a treasurer named so could never withdraw.
func NewSetTreasurerRequest(cur realm, to address) Request {
	mustBeLive()
	// one decoder answers both questions, an address or not and in which spelling (F9's second review, finding 1):
	// were IsValid (native code) ever to take what the round trip refuses, the treasurer would become ""
	typed := to
	if to = hlmailbox.CanonicalAddress(to); to == "" {
		panic("hlfees: not an address: " + echo(string(typed)))
	}
	if to == treasurer {
		panic("hlfees: " + string(to) + " is the treasurer already") // echo-ok(to): canonical bech32 (CanonicalAddress above), ASCII
	}
	text := "set treasurer to " + string(to) + " (now " + string(treasurer) + ")"
	return propose(cur, "set treasurer", text, func(made, t int64) {
		mustBeLive()
		d := ledger.Record("set treasurer", text, treasurerParams{to}, made, t, t+decisions.Delay)
		d.Note = "queued 7 days"
		chain.Emit("Decision", "id", itoa(d.ID), "kind", "set treasurer", "state", "queued")
	})
}

// NewCancelRequest cancels a pending decision of this realm, at once.
func NewCancelRequest(cur realm, id int64) Request {
	d := ledger.Get(id)
	if d == nil || !d.Pending() {
		panic("hlfees: decision " + itoa(id) + " is not pending")
	}
	text := "cancel decision " + itoa(id) + " (" + d.Text + ")"
	return propose(cur, "cancel", text, func(made, t int64) {
		if _, err := ledger.Cancel(id, t); err != nil {
			// echo-ok(err): p/decisions' refusal: ASCII words and numbers
			panic("hlfees: " + err.Error())
		}
		c := ledger.Record("cancel", text, cancelParams{id}, made, t, t)
		ledger.Apply(c, t, "at once: cancelled")
	})
}

// Execute applies a queued decision whose 7 days have passed; anyone may call it.
func Execute(cur realm, id int64) {
	if !cur.IsCurrent() {
		panic("hlfees: that realm value is not live; pass your own cur")
	}
	t := now()
	d, err := ledger.Due(id, t)
	if err != nil {
		// echo-ok(err): p/decisions' refusal: ASCII words and numbers
		panic("hlfees: " + err.Error())
	}
	p, ok := d.Params.(treasurerParams)
	if !ok {
		panic("hlfees: decision " + itoa(id) + " has nothing to apply")
	}
	treasurer = p.to
	ledger.Apply(d, t, "applied after the delay")
	// echo-ok(treasurer): the treasurer, a canonical bech32 address (treasurerFault at init and at the decision)
	chain.Emit("TreasurerSet", "address", string(treasurer))
}

// --------------------------------------------------------------- pages --

// Render: "" the page; "json" the figures; "decisions" and "decisions.json" the decisions, and
// "decisions.json/<from>" a page of them (at most 20, hlmailbox.DecisionsJSONPage).
func Render(path string) string {
	if strings.HasPrefix(path, "decisions.json/") {
		return hlmailbox.DecisionsJSONPage(&ledger, path[len("decisions.json/"):])
	}
	ugnot, bubbles := Balances()
	switch path {
	case "":
		var b strings.Builder
		b.WriteString("# The bridge's fees\n\n")
		b.WriteString("Every fee the bridge takes lands here: GNOT as coins, BUBBLE as the token. Only the treasurer a decision names withdraws, to itself.\n\n")
		b.WriteString("- held: " + itoa(ugnot) + " ugnot, " + itoa(bubbles) + " BUBBLE base units\n")
		// echo-ok(treasurer): the treasurer, a canonical bech32 address (treasurerFault at init and at the decision)
		b.WriteString("- treasurer: " + string(treasurer) + "\n")
		b.WriteString("- " + decisions.GovernorLine(launchGovernor, hlmailbox.Yielded()) + "\n")
		b.WriteString("- " + decisions.Clocks() + "\n")
		b.WriteString("\n## Decisions\n\n" + ledger.Page())
		return b.String()
	case "json":
		return "{\"address\":" + strconv.QuoteToASCII(string(Address())) + ",\"treasurer\":" + strconv.QuoteToASCII(string(treasurer)) +
			"," + decisions.GovernorJSON(launchGovernor, hlmailbox.Yielded()) +
			",\"ugnot\":" + itoa(ugnot) + ",\"bubble\":" + itoa(bubbles) + ",\"clocks\":" + decisions.ClocksJSON() + ",\"decisions\":" + itoa(ledger.Last()) + "}"
	case "decisions":
		return "# Decisions\n\n" + ledger.Page()
	case "decisions.json":
		return ledger.JSON()
	}
	return "unknown path; see the page"
}
