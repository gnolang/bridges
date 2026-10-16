// Package hlbubble is the BUBBLE lock of the Ethereum bridge: its BUBBLE route on gno.land (eth/bridge/PLAN.md
// §5.2, the Solana plan's §5.2–§5.5 carried to Ethereum), the same shape as r/hlgnot over the BUBBLE token
// (gno.land/r/…/bubble, at its live path) instead of coins. It is the second BUBBLE lock, beside the Solana route's
// and sharing nothing with it (eth §4). A section number alone (§5.3) is the Solana plan's (bridge/PLAN.md); "eth §x"
// is the Ethereum plan's.
//
// Out. TransferRemote takes amount + fee of the caller's BUBBLE through the token's allowance (the caller approves
// this realm first), keeps the amount in the lock, pays the fee to r/hlfees, and sends a token message through
// r/hlmailbox to the synthetic BUBBLE on Ethereum (Hyperlane's HypERC20, the router), which mints as much there to
// an Ethereum address.
//
// In. A burn on Ethereum comes back as a token message: Process takes it from the mailbox, checks it came from the
// enrolled router, and writes a claim in the recipient's queue; Claim pays claims in turns under the meters — a
// tenth of the lock a day (never below the floor), a quarter of that for any one recipient, the fee first. The old
// 1,000,000 BUBBLE day cap and the admin's pause are gone: the meter and the guardians' flags replace them.
//
// Who may change what. Nobody but the governor through the builders until it yields on r/hlmailbox, and GovDAO
// after (M1k); the guardians and watch keys can only pause, through r/hlmailbox. Every launch value is compiled in
// below; nothing moves until the Launch decision executes. The state machine is gno.land/p/…/hlroute/v0; this
// realm holds the BUBBLE and makes the calls.
package hlbubble

import (
	"chain"
	"chain/runtime"
	"encoding/hex"
	"strconv"
	"strings"
	"time"

	"gno.land/p/g1leu8d2vsplhehcfkjg50mwgdpxdkt8tztu95wr/decisions/v0"
	"gno.land/p/g1leu8d2vsplhehcfkjg50mwgdpxdkt8tztu95wr/hlroute/v0"
	hl "gno.land/p/g1leu8d2vsplhehcfkjg50mwgdpxdkt8tztu95wr/hyperlane/v0"
	"gno.land/p/nt/avl/v0"
	"gno.land/r/g1leu8d2vsplhehcfkjg50mwgdpxdkt8tztu95wr/bubble"
	"gno.land/r/g1leu8d2vsplhehcfkjg50mwgdpxdkt8tztu95wr/eth/hlmailbox"
	"gno.land/r/gov/dao"
)

const (
	namespace = "gno.land/r/g1leu8d2vsplhehcfkjg50mwgdpxdkt8tztu95wr/eth/"
	pkgPath   = namespace + "hlbubble" // must match gnomod.toml

	mailboxPath = namespace + "hlmailbox"
	feesPath    = namespace + "hlfees"
	bubblePath  = "gno.land/r/g1leu8d2vsplhehcfkjg50mwgdpxdkt8tztu95wr/bubble"
	hlgnotPath  = namespace + "hlgnot"
	hlgovPath   = namespace + "hlgov"

	one = int64(1_000_000) // one BUBBLE in base units

	// The launch values (§5.3, §5.5): the fee, the minimum, the fee floor, the cap floor, the per-message maximum;
	// the router is launchRouter, below. The fee and the minimum are the round-2 plan red team's (E2-RT5, as
	// hlgnot's: GNOT's at the Solana routes' ratio, 100 BUBBLE to 5 GNOT; §11.5 is Jae's), not Solana's 100 and 200.
	launchFee      = 400 * one
	launchMinimum  = 800 * one
	launchFeeFloor = 40 * one
	launchCapFloor = 10_000 * one
	launchMaximum  = 25_000 * one

	maxI64 = uint64(1<<63 - 1)
)

// launchGovernor is the governor (M1k): the mailbox's, compiled here too so a member reads it in this realm's
// served source; init refuses a deploy with another. The e2e swaps it for gnodev's test1.
const launchGovernor = address("g1dpkz6em0wejhymn0wf047h6lta047h6lvxn4gx")

// launchRouter is the router at launch (eth §5.2): the BUBBLE HypERC20 proxy's Ethereum address, which the setup
// step deploys before the realms are uploaded (eth §5.8). A PLACEHOLDER — the test route of the vectors, the low
// twenty bytes of sha256("hlbubble test route") in EIP-55 case — until the stage step swaps in the deployed address;
// the e2e swaps it for its run's; on gnoland-1 init refuses it (mainnet.gno). Read by hlmailbox.ParseEthAddress at
// deploy, like every Ethereum address here. Its own line, so one sed on `^const launchRouter = "…"` finds it.
const launchRouter = "0x051D52Fd082d902f8E0CF300a60304D484cf2d07"

var (
	st = hlroute.New(hlroute.Launch{Symbol: "BUBBLE", Fee: launchFee, Minimum: launchMinimum, FeeFloor: launchFeeFloor,
		CapFloor: launchCapFloor, Maximum: launchMaximum, Domain: hlmailbox.LaunchOrigin(), Router: mustAddr(launchRouter),
		Now: time.Now().Unix()})

	// realms are the addresses a transfer may not name as its recipient on either side (§5.2), this deployment's
	// hlverifier among them (E2-RL3, M1-NOTES' F-3).
	realms = []address{chain.PackageAddress(pkgPath), chain.PackageAddress(mailboxPath), chain.PackageAddress(feesPath),
		chain.PackageAddress(bubblePath), chain.PackageAddress(hlgnotPath), chain.PackageAddress(hlgovPath),
		chain.PackageAddress(namespace + "hlverifier")}

	// payees are the credit and sweep votes' payees by decision number (E3-CF7(b), F9-5); see hlgnot's.
	payees avl.Tree // strconv of the decision's number → address

	// guardedOn is the gno.land domain init's guard checked the launch values at (guardLaunch): the suite reads it
	// (TestInitGuarded), so a tree whose init lost the call fails the suite (E3-RL7, as hlmailbox's guardedOn).
	guardedOn uint32
)

// init refuses a deploy whose launch values are not this deployment's (guardLaunch).
func init() {
	guardedOn = guardLaunch()
}

// guardLaunch is init's guard. It refuses a deploy whose compiled governor is not the mailbox's (M1k): the bridge's
// realms are governed by one key until it yields, or by GovDAO from the first block, never a mix; one whose launch
// router is one of the dev tree's on gnoland-1 (devPlaceholder, E2-RL1); and one whose launch router is not the
// mailbox's token 1 (wrongRouter). It returns the domain it checked at.
func guardLaunch() uint32 {
	if launchGovernor != hlmailbox.Governor() {
		// echo-ok(hlmailbox.Governor): the mailbox's compiled governor, a bech32 address
		panic("hlbubble: the compiled governor " + string(launchGovernor) + " is not the mailbox's " + string(hlmailbox.Governor()))
	}
	d := hlmailbox.LocalDomain()
	if why := devPlaceholder(d, st.Router); why != "" {
		// echo-ok(why): the guard's own refusal: ASCII words, contract names, EIP-55 and bech32 addresses and pkgpaths, a few hundred bytes at most: far under the cut
		panic(why)
	}
	if why := wrongRouter(st.Router); why != "" {
		// echo-ok(why): the guard's own refusal: ASCII words, contract names, EIP-55 and bech32 addresses and pkgpaths, a few hundred bytes at most: far under the cut
		panic(why)
	}
	return d
}

// wrongRouter is why init refuses k, the launch router as compiled (st.Router), or "" (E-M1's Q3): it must be
// hlmailbox's compiled token 1, the BUBBLE HypERC20 proxy; see hlgnot's.
func wrongRouter(k [32]byte) string {
	_, token := hlmailbox.EthContractAt(hlmailbox.EthToken1)
	if k == token {
		return ""
	}
	return "hlbubble: the compiled launch router " + hlmailbox.EthAddress(k) + " is not the mailbox's token 1 (BUBBLE), " + hlmailbox.EthAddress(token) + ": both must be the BUBBLE HypERC20 proxy"
}

// mustAddr reads an Ethereum address with the deployment's one parser (hlmailbox.ParseEthAddress); see hlgnot's.
func mustAddr(s string) [32]byte {
	k, err := hlmailbox.ParseEthAddress(s)
	if err != nil {
		// echo-ok(err): ParseEthAddress's refusal: its ASCII words and its echo of what it read (echo, E4-GS1)
		panic("hlbubble: " + err.Error())
	}
	return k
}

// ethWords names this deployment's remote chain in p/hlroute's text (its descriptions and the page's identity line
// say "Solana"); see hlgnot's.
// echo-ok(strings.ReplaceAll): its callers hand it p/hlroute's own texts (a page, a description), in which it swaps a word
func ethWords(s string) string { return strings.ReplaceAll(s, "Solana", "Ethereum") }

func now() int64 { return time.Now().Unix() }

func u32(n uint32) string { return strconv.FormatUint(uint64(n), 10) }
func itoa(n int64) string { return strconv.FormatInt(n, 10) }

func caller(cur realm) address {
	if !cur.IsCurrent() {
		panic("hlbubble: that realm value is not live; pass your own cur")
	}
	return cur.Previous().Address()
}

func mustHex(s, what string) []byte {
	b, err := hex.DecodeString(strings.TrimPrefix(strings.TrimSpace(s), "0x"))
	if err != nil {
		panic("hlbubble: the " + what + " is not hex") // echo-ok(what): every caller names its argument with a constant: ASCII words
	}
	return b
}

// isRealm is whether an address is a realm of the bridge: the seven compiled here, every route realm the mailbox
// registers (R39: a successor's address is refused as a recipient at send and swept at a credit, to be credited to
// its owner by id, never paid into a realm's account outside every counter), every one it dropped (AT6: a
// re-pointed-away successor, a route no more that nothing of the line can move BUBBLE out of) and every realm that
// announced itself to it as a route's successor, registered yet or not (E3-RL3), and every realm that is or was an
// origin's active verifier (E4-RL5, hlmailbox.IsVerifier). A handful of reads. A credit sweeps, and a vote refuses as
// its recipient, the Solana deployment's six realms and the zero address too (unpayable); a send asks isRealm alone,
// its recipient being an Ethereum address.
func isRealm(a address) bool {
	for _, r := range realms {
		if r == a {
			return true
		}
	}
	if hlmailbox.IsAnnouncedSuccessor(a) || hlmailbox.IsVerifier(a) {
		return true
	}
	for _, p := range hlmailbox.Routes() {
		if chain.PackageAddress(p) == a {
			return true
		}
	}
	for _, p := range hlmailbox.DroppedRoutes() {
		if chain.PackageAddress(p) == a {
			return true
		}
	}
	return false
}

// zeroAddress is the gno address of thirty-two zero bytes: what a burn whose recipient field was left empty names
// (Hyperlane's transferRemote takes any bytes32), an account no key holds.
var zeroAddress = zeroAddr()

func zeroAddr() address {
	a, err := hl.Bytes32ToAddress([32]byte{})
	if err != nil {
		// echo-ok(err): p/hyperlane's fixed text, ASCII, never reached: 32 zero bytes always decode
		panic(err.Error())
	}
	return a
}

// unpayable is whether a credit to `to` is swept, and a vote refuses `to` as its recipient: a realm of the bridge
// (isRealm), one of the Solana deployment's six (hlmailbox.IsSolanaRealm, E-M1's Q6), or the zero address — a burn
// with an empty recipient field (the round-2 realms audit's E2-RL3) — each an account nothing moves coins out of. Such
// a credit waits in the sweep queue until GovDAO credits it by id to its owner.
func unpayable(to address) bool {
	return to == zeroAddress || isRealm(to) || hlmailbox.IsSolanaRealm(to)
}

func mustBeLive() {
	if !hlmailbox.Live() {
		panic("hlbubble: not launched: nothing is sent, credited or paid before the Launch decision")
	}
}

func mustNotBePaused(fn string) {
	if hlmailbox.Paused(pkgPath, fn) {
		panic("hlbubble: " + fn + " are paused by a guardian or a watch key") // echo-ok(fn): one of hlmailbox's three function names, ASCII constants
	}
}

// mustNotBeDropped refuses a realm the mailbox dropped from its line (R35): its predecessor re-pointed the migration
// to another successor while this realm had pulled nothing, so it is a route no more — Ethereum's token may still
// enroll it as the router until the next kind 6, and a send or a credit here would be against a lock no realm of the
// line holds. The mailbox refuses its Dispatch and its Process too; the line's last realm takes every message
// addressed to it once its import has ended.
func mustNotBeDropped() {
	if by := hlmailbox.Dropped(pkgPath); by != "" {
		// echo-ok(by): a dropped route's predecessor as the mailbox recorded it: pkgpath, ASCII by tm2's path rules
		panic("hlbubble: a route no more: " + by + " re-pointed its migration to another successor; the line's last realm takes every message and send")
	}
}

func syncLaunch() {
	if la := hlmailbox.LaunchedAt(); la != st.LaunchSeen {
		st.ResetMeters(la)
		st.LaunchSeen = la
	}
}

// ------------------------------------------------------------- reading --

// Address is this realm's address: the spender to approve on the bubble realm before TransferRemote, the holder
// of the lock, and (as 32 bytes) the router the HypERC20 on Ethereum enrolls for gno.land's domain.
func Address() address { return chain.PackageAddress(pkgPath) }

// Locked is the lock: what backs synthetic BUBBLE on Ethereum, by the counters (sent − paid).
func Locked() int64 { return st.Lock }

// Balance is the BUBBLE this realm holds; it is the lock unless someone sent BUBBLE here outside a transfer.
func Balance() int64 { return bubble.BalanceOf(Address()) }

// Router is the router on Ethereum as Hyperlane carries it, 32 bytes in hex (the twelve zero bytes and the address).
func Router() string { return hex.EncodeToString(st.Router[:]) }

// RouterAddress is the router on Ethereum as an address, in EIP-55 case: the BUBBLE HypERC20 proxy.
func RouterAddress() string { return hlmailbox.EthAddress(st.Router) }

// Domain is the Ethereum domain this route sends to and takes from.
func Domain() uint32 { return st.Domain }

func Fee() int64      { return st.Fee }
func Minimum() int64  { return st.Minimum }
func FeeFloor() int64 { return st.FeeFloor }
func CapFloor() int64 { return st.CapFloor }
func Maximum() int64  { return st.Maximum }
func Cap() int64      { return st.Cap() }

// Counters is the identity's five counters (§5.9): sentTotal, paidTotal, owed, voided, lost.
func Counters() (sentTotal, paidTotal, owed, voided, lost int64) {
	return st.SentTotal, st.PaidTotal, st.Owed, st.Voided, st.Lost
}

// Migrated is the successor's pkgpath once a migration applied, "" before.
func Migrated() string { return st.MigratedTo }

// ---------------------------------------------------------------- out --

// TransferRemote sends amount of the caller's BUBBLE to recipient on destination and returns the message id in hex.
// The recipient is an Ethereum address, read and refused as hlgnot's TransferRemote says (eth §5.2: 0x and 40 hex
// digits, EIP-55 when mixed case, or 64 hex digits whose first 24 are zero; never the zero address, this route's
// own router, an address at or below 0xffff, a contract of the Ethereum deployment (ER10) or a realm of the bridge). First, on the bubble realm, the caller approves THIS realm (bubble.Approve,
// spender Address()) for at least amount + fee: the amount is locked here, the fee goes to r/hlfees. The amount is
// between the minimum and the per-message maximum.
func TransferRemote(cur realm, destination uint32, recipient string, amount int64) string {
	who := caller(cur)
	mustBeLive()
	mustNotBePaused(hlmailbox.FnSends)
	mustNotBeDropped()
	st.MustSend(hlmailbox.Predecessor(pkgPath), env(cur)) // a successor takes no send before its last page is in (R35, F28) and before kind 6 names it (AT7)
	if destination != st.Domain {
		panic("hlbubble: no route to domain " + u32(destination))
	}
	st.CheckSend(amount)
	to, err := hlmailbox.ParseEthAddress(recipient)
	if err != nil {
		// echo-ok(err): ParseEthAddress's refusal: its ASCII words and its echo of what it read (echo, E4-GS1)
		panic("hlbubble: the recipient is " + err.Error())
	}
	// the zero address, this route's router, an address at or below 0xffff and every contract of the deployment (ER10)
	if why := hlmailbox.RefusedEthRecipient(to, st.Router); why != "" {
		// echo-ok(why): hlmailbox.RefusedEthRecipient's ASCII words and an EIP-55 address, a few hundred bytes at most: far under the cut
		panic("hlbubble: the recipient is " + why)
	}
	if a, err := hl.Bytes32ToAddress(to); err == nil && isRealm(a) {
		panic("hlbubble: the recipient is a realm of the bridge")
	}
	self := Address()
	before := bubble.BalanceOf(self)
	bubble.TransferFrom(cross(cur), who, self, amount+st.Fee)
	if bubble.BalanceOf(self) != before+amount+st.Fee {
		panic("hlbubble: the lock moved by the wrong amount")
	}
	bubble.Transfer(cross(cur), chain.PackageAddress(feesPath), st.Fee)
	id := hlmailbox.Dispatch(cross(cur), destination, st.Router, hl.TokenMessage(to, uint64(amount), nil))
	idHex := hex.EncodeToString(id[:])
	st.Send(now(), idHex, who, amount)
	// echo-ok(who, idHex): the sender's bech32 address; the message id in hex
	chain.Emit("SentTransferRemote",
		"destination", u32(destination),
		"recipient", hex.EncodeToString(to[:]),
		"amount", itoa(amount),
		"fee", itoa(st.Fee),
		"from", string(who),
		"id", idHex,
	)
	return idHex
}

// ----------------------------------------------------------------- in --

// Process takes a token message from Ethereum and credits a claim to its recipient. Anyone may call it with the
// verifier's metadata and the message, both in hex. A refused message stays undelivered. A realm registered as a
// successor credits nothing — its own messages included (R32) — before the last page of its predecessor's
// migration is in (AQ1); a migrated realm refuses before the mailbox is asked (see hlgnot's).
func Process(cur realm, metadata, message string) {
	caller(cur)
	mustBeLive()
	mustNotBePaused(hlmailbox.FnCredits)
	mustNotBeDropped()
	msg := mustHex(message, "message")
	m, err := hl.ParseMessage(msg)
	if err != nil {
		panic(err.Error()) // echo-ok(err): p/hyperlane's ParseMessage refuses with one fixed ASCII text (hyperlane.gno:73)
	}
	if st.Migrated() {
		// echo-ok(st.MigratedTo): the successor a migrate decision named: an announced realm's pkgpath, ASCII by tm2's path rules
		panic("hlbubble: migrated to " + st.MigratedTo + ": the successor takes this message")
	}
	if self, err := hl.AddressToBytes32(Address()); err != nil {
		panic(err.Error()) // echo-ok(err): p/hyperlane's ASCII text and this realm's own bech32 address, which always decodes
	} else {
		st.MustTake(m.Recipient == self, hlmailbox.Predecessor(pkgPath))
	}
	t := now()
	// echo-ok(m): m.Sender is 32 bytes, compared with the routers, never echoed
	if m.Origin != st.Domain || !st.RouterAccepts(t, m.Sender) {
		panic("hlbubble: the message is not from the router enrolled for domain " + u32(st.Domain))
	}
	rcpt, amount, _, err := hl.ParseTokenMessage(m.Body)
	if err != nil {
		panic(err.Error()) // echo-ok(err): p/hyperlane's ParseTokenMessage refuses with fixed ASCII texts (hyperlane.gno:224, :228)
	}
	if amount > maxI64 {
		panic("hlbubble: the amount is past what BUBBLE can hold")
	}
	origin, sender, body := hlmailbox.Process(cross(cur), mustHex(metadata, "metadata"), msg)
	if origin != m.Origin || sender != m.Sender || string(body) != string(m.Body) {
		panic("hlbubble: the mailbox read the message differently") // cannot happen: the same bytes, the same parser
	}
	// a recipient that is no gno address (its top twelve bytes not zero, §13), a realm of the bridge, one of the
	// Solana deployment's six or the zero address (unpayable) is swept: owed, paying nobody until GovDAO credits it
	// by id to its owner
	to, err := hl.Bytes32ToAddress(rcpt)
	recipient := string(to)
	if err != nil {
		recipient = hex.EncodeToString(rcpt[:])
	}
	idb := hl.ID(msg)
	id := hex.EncodeToString(idb[:])
	epoch, _ := hlmailbox.PayoutsEpoch(pkgPath)
	h := runtime.ChainHeight()
	// echo-ok(to): p/hyperlane's bech32 of the recipient's 32 bytes (or none when they are no gno address: swept)
	swept := st.Credit(t, h, epoch, id, to, int64(amount), hlroute.BySigners, err != nil || unpayable(to))
	countCredit(h) // the credit tally a range void is bounded by (voids.gno, E6-RL1)
	if !swept {
		enter(id) // a claim: into the waiting index (waiting.gno, E4-RL1)
	}
	tidy(tidyStep) // and the index's next entries looked at, those no longer open taken out
	// echo-ok(id): the message id in hex
	chain.Emit("Credited",
		"origin", u32(origin),
		"recipient", recipient, // echo-ok(recipient): p/hyperlane's bech32 of the 32 bytes (Bytes32ToAddress), or their hex
		"amount", itoa(int64(amount)),
		"id", id,
		"swept", strconv.FormatBool(swept),
	)
}

// Claim pays up to n claims (n ≤ 20) taking recipients in turns under the meters, the fee as each claim's first
// installment and a later installment only once both meters hold the fee or what the claim still owes, if less
// (installments.gno: the round-5 go-spend audit's E5-GS1); see hlgnot's. Anyone may call it; the relayers do. It
// returns how many claims it paid something on.
func Claim(cur realm, n int) int {
	caller(cur)
	mustBeLive()
	mustNotBePaused(hlmailbox.FnPayouts)
	mustNotBeDropped()
	syncLaunch()
	epoch, at := hlmailbox.PayoutsEpoch(pkgPath)
	paid := claimTurns(cur, n, now(), epoch, at)
	chain.Emit("Claimed", "count", strconv.Itoa(paid))
	return paid
}

func pay(cur realm, to address, amount int64) { bubble.Transfer(cross(cur), to, amount) }

func env(cur realm) hlroute.Env {
	return hlroute.Env{
		Flagged: func(id string, height int64, verifier bool) bool {
			return hlmailbox.Flagged(pkgPath, id, height, verifier)
		},
		ClearFlags:   func(fn string) bool { return hlmailbox.ClearFlags(cross(cur), fn) },
		RemoveSeat:   func(seat int, holder address) bool { return hlmailbox.VacateSeat(cross(cur), seat, holder) },
		Delivered:    func(id string) bool { return hlmailbox.Delivered(id) != 0 },
		MarkDone:     func(id string) { hlmailbox.MarkDone(cross(cur), id) },
		Epoch:        func() int64 { e, _ := hlmailbox.PayoutsEpoch(pkgPath); return e },
		Height:       func() int64 { return runtime.ChainHeight() },
		Pay:          func(to address, amount int64) { pay(cur, to, amount) },
		PayFee:       func(amount int64) { pay(cur, chain.PackageAddress(feesPath), amount) },
		AddRoute:     func(path string) { hlmailbox.AddRoute(cross(cur), path) },
		AnnouncedFor: func(path string) string { return hlmailbox.SuccessorFor(path) },
		Predecessor:  func() string { return hlmailbox.Predecessor(pkgPath) },
		IsRoute:      func(path string) bool { return hlmailbox.IsRoute(path) },
		Enrolled:     func() (int64, int64) { return hlmailbox.Enrolled(pkgPath) },
	}
}

// ------------------------------------------------------------ builders --

// The builders (§5.3): the same as r/hlgnot's, amounts in BUBBLE base units. A decision made with one of them —
// the governor's through Govern until it yields on r/hlmailbox, a GovDAO proposal's after (M1k) — is the only way
// a setting changes; see hlgnot's for the clocks and the Request.

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
			panic("hlbubble: only " + decisions.DAOPath + " executes a decision, not " + who(prev.PkgPath(), prev.Address()))
		}
		return
	}
	if !prev.IsUser() || prev.Address() != launchGovernor {
		// echo-ok(who): the previous realm's pkgpath, ASCII by tm2's path rules, or its bech32 address
		panic("hlbubble: until the governor yields, " + string(launchGovernor) + " alone decides, by Govern from its own key: not " + who(prev.PkgPath(), prev.Address()) + " (a GovDAO proposal is refused until the yield)")
	}
}

// titlePrefix begins every proposal title this realm's builders make (E3-RL6): "eth/" tells this deployment's
// proposals from the Solana deployment's "hlbubble: <kind>" on GovDAO's list; see hlgnot's.
const titlePrefix = "eth/hlbubble: "

func propose(cur realm, d hlroute.Draft) Request { return proposePaying(cur, d, "", nil, 0) }

// proposePaying is propose for a vote that pays payee — a credit or a sweep payment; "" for every other builder:
// its closure asks unpayable of the payee again before it records the decision and keeps the payee for Execute to
// ask once more (E3-CF7(b), F9-5); reopen are the claims the decision may make open again (the waiting index's) and
// cancels the decision a cancel cancels (0 for any other); see hlgnot's.
func proposePaying(cur realm, d hlroute.Draft, payee address, reopen []string, cancels int64) Request {
	if !cur.IsCurrent() {
		panic("hlbubble: that realm value is not live; pass your own cur")
	}
	d.Text = ethWords(d.Text) // the description names Ethereum as the remote chain, where p/hlroute's says Solana
	made := now()
	cb := func(cur realm) error {
		mustBeGovernor(cur)
		t := now()
		if err := decisions.Expired(made, t); err != nil {
			return err
		}
		if d.Kind != hlroute.KindCancel {
			mustBeLive()
		}
		if d.Kind == hlroute.KindApplyRefund {
			mustNotBePaused(hlmailbox.FnPayouts) // a refund moves BUBBLE out of the lock: under a standing payouts flag it waits like a claim (§5.9; the proposal survives for a retry)
		}
		if payee != "" && unpayable(payee) {
			panic("hlbubble: the vote pays " + string(payee) + ", which is a realm of the bridge now (a route registered, dropped or announced as a successor since the vote was made): never paid; credit the id to its owner afresh") // echo-ok(payee): canonical bech32, ASCII (the credit and sweep builders' CanonicalAddress)
		}
		dec := st.Decide(made, t, d, env(cur))
		if payee != "" {
			payees.Set(itoa(dec.ID), payee)
		}
		// the claims it may make open again (a restore's, a credit's): those open now — a withheld claim restored at
		// once — enter the waiting index here, the rest are kept, while the decision pends, for Execute to enter when it
		// applies (waiting.gno, E4-RL1, E5-RL2); a cancel drops what the decision it cancels kept
		if later := reopenNow(reopen); len(later) > 0 && dec.Pending() {
			reopens.Set(itoa(dec.ID), later)
		}
		if cancels != 0 {
			reopens.Remove(itoa(cancels))
		}
		// echo-ok(dec): a decision's kind, a builder's constant, and its state, p/decisions' word
		chain.Emit("Decision", "id", itoa(dec.ID), "kind", dec.Kind, "state", dec.State())
		return nil
	}
	return Request{proposal: dao.NewProposalRequest(titlePrefix+d.Kind, d.Text, dao.NewSimpleExecutor(0, cur, cb, d.Text)), text: d.Text, decide: cb}
}

// mustStillBePayable refuses to apply the pending credit or sweep vote numbered id while its payee is unpayable
// (E3-CF7(b), F9-5): it stays pending for GovDAO's cancel; see hlgnot's.
func mustStillBePayable(id int64) {
	if d := st.Ledger.Get(id); d == nil || !d.Pending() {
		return
	}
	v := payees.Get(itoa(id))
	if v == nil {
		return
	}
	if to := v.(address); unpayable(to) {
		// echo-ok(to): a vote's payee, kept in its canonical bech32 spelling, ASCII (CanonicalAddress at the builder)
		panic("hlbubble: decision " + itoa(id) + " pays " + string(to) + ", which is a realm of the bridge now (a route registered, dropped or announced as a successor since the vote): it stays pending; cancel it, and credit the id to its owner afresh")
	}
}

// Govern is the governor's way in before the yield (M1k): the same closure GovDAO's proxy runs after it, called
// non-crossing in this frame so it reads the governor from cur.Previous(); refused for anyone else and for
// everyone once the governor yielded on r/hlmailbox (or when none was compiled); see hlgnot's.
func Govern(cur realm, r Request) {
	if !cur.IsCurrent() {
		panic("hlbubble: that realm value is not live; pass your own cur")
	}
	if r.decide == nil {
		panic("hlbubble: not a builder's request")
	}
	if yielded := hlmailbox.Yielded(); decisions.DAOGoverns(launchGovernor, yielded) {
		if launchGovernor == "" {
			panic("hlbubble: no governor was compiled, " + decisions.DAOPath + " governs from the first block: a decision is a GovDAO proposal, never Govern")
		}
		panic("hlbubble: the governor yielded at height " + itoa(yielded) + ", " + decisions.DAOPath + " governs since: a decision is a GovDAO proposal, never Govern")
	}
	mustBeGovernor(cur)
	if err := r.decide(cur); err != nil {
		// echo-ok(err): p/decisions' refusal: ASCII words and numbers
		panic("hlbubble: " + err.Error())
	}
}

func NewSetCapFloorRequest(cur realm, n int64) Request {
	mustBeLive()
	return propose(cur, st.DraftCapFloor(n))
}

func NewSetMaximumRequest(cur realm, n int64) Request {
	mustBeLive()
	return propose(cur, st.DraftMaximum(n))
}

func NewSetFeeRequest(cur realm, fee, minimum int64) Request {
	mustBeLive()
	return propose(cur, st.DraftFee(fee, minimum))
}

func NewSetFeeFloorRequest(cur realm, n int64) Request {
	mustBeLive()
	return propose(cur, st.DraftFeeFloor(n))
}

// NewSetRouterRequest swaps the router, an Ethereum address, after 7 days; the description names it in EIP-55 case;
// never a contract of the deployment but this route's own token (refusedRouter, E2-RL7); see hlgnot's.
func NewSetRouterRequest(cur realm, router string) Request {
	mustBeLive()
	k := mustAddr(router)
	if why := refusedRouter(k); why != "" {
		// echo-ok(why): refusedRouter's ASCII words, a contract's name and EIP-55 addresses, a few hundred bytes at most: far under the cut
		panic(why)
	}
	return propose(cur, st.DraftRouter(k, hlmailbox.EthAddress(k)))
}

// refusedRouter is why a router swap refuses k, or "" (E2-RL7): a contract of the deployment other than the BUBBLE
// HypERC20 proxy, this route's own token; see hlgnot's.
func refusedRouter(k [32]byte) string {
	_, token := hlmailbox.EthContractAt(hlmailbox.EthToken1)
	if name := hlmailbox.EthContractName(k); name != "" && k != token {
		return "hlbubble: the router " + hlmailbox.EthAddress(k) + " is " + name + " of this deployment, not the BUBBLE token: every send after the swap would strand undelivered, and the token's burns would be refused once the old router's 7 days end"
	}
	return ""
}

// NewVoidRequest voids credits by message id (a claim, or a sweep entry as forged) or by a range of credits
// ("heights:<from>-<to>" or "since:<from>", closed at the block before the proposal's), maxVoidIDs ids or
// maxVoidCredits credits at most (voids.gno, E6-RL1), as withheld or forged, with the unpause option (the seats'
// holders recorded; a seat whose occupant changed by execution is skipped): see hlgnot's.
func NewVoidRequest(cur realm, ids string, forged bool, unpauseFns, removeSeats string) Request {
	mustBeLive()
	mustBeASCII("ids", ids)
	mustBeASCII("unpause functions", unpauseFns)
	mustBeASCII("seats", removeSeats)
	before := runtime.ChainHeight() - 1 // a range closes at the block before this one (voids.gno)
	if before < 1 {
		before = 1 // never 0: a since range closed at 0 would be p/hlroute's open one
	}
	if r, ranged := hlroute.ParseRange(ids); ranged {
		if why := rangeRefusal(closedAt(r, before)); why != "" {
			panic("hlbubble: " + why) // echo-ok(why): rangeRefusal's ASCII words and numbers: a count, p/hlroute's Range in words (its heights)
		}
	}
	d := st.DraftVoid(ids, forged, unpauseFns, removeSeats, before, hlmailbox.Seat)
	if p := d.Params.(hlroute.VoidParams); len(p.IDs) > maxVoidIDs {
		panic("hlbubble: a void names " + strconv.Itoa(len(p.IDs)) + " ids, " + strconv.Itoa(maxVoidIDs) + " at most: void them by more than one decision")
	}
	return propose(cur, d)
}

// NewRestoreRequest restores voided claims by message id, 40 at most (maxRestoreIDs, E5-RL2, the void gate); see
// hlgnot's.
func NewRestoreRequest(cur realm, ids string) Request {
	mustBeLive()
	mustBeASCII("ids", ids)
	d := st.DraftRestore(ids)
	list, ok := restoreList(d) // the draft's own parse of the ids, read once (E5-RL2)
	if !ok {
		panic("hlbubble: the restore's draft does not list its ids as p/hlroute writes them") // cannot happen
	}
	if len(list) > maxRestoreIDs {
		panic("hlbubble: a restore names " + strconv.Itoa(len(list)) + " ids, " + strconv.Itoa(maxRestoreIDs) + " at most: restore them by more than one decision")
	}
	return proposePaying(cur, d, "", list, 0)
}

func NewCreditRequest(cur realm, id string, to address, amount int64) Request {
	mustBeLive()
	mustBeASCII("id", id)
	mustBeASCII("recipient", string(to))
	if to = hlmailbox.CanonicalAddress(to); to == "" || unpayable(to) {
		panic("hlbubble: the recipient must be an address outside the bridge's realms")
	}
	cid, _ := hlroute.CleanID(id) // DraftCredit refuses an id that is none
	return proposePaying(cur, st.DraftCredit(id, to, amount, hlmailbox.Delivered(id) != 0), to, []string{cid}, 0)
}

// NewSweepRequest pays sweep entries, named by message id, to an address after 7 days — their sum, at most the
// lock — and drops them (R19): see hlgnot's. Both votes read the address in its canonical spelling
// (hlmailbox.CanonicalAddress) and credit or pay that one: BUBBLE's ledger keys an account by its string, so a
// payment under another spelling would sit under a key nobody spends. unpayable is asked when the proposal is made,
// when it executes and when the vote applies (proposePaying, Execute: E3-CF7(b)).
func NewSweepRequest(cur realm, ids string, to address) Request {
	mustBeLive()
	mustBeASCII("ids", ids)
	mustBeASCII("recipient", string(to))
	if to = hlmailbox.CanonicalAddress(to); to == "" || unpayable(to) {
		panic("hlbubble: the recipient must be an address outside the bridge's realms")
	}
	return proposePaying(cur, st.DraftSweep(ids, to), to, nil, 0)
}

// NewRefundRequest queues a refund of imported sends by id, 7 days, once kind 6 has enrolled this realm on Ethereum
// for 14 days (AR1, R37); applied then by NewApplyRefundRequest, never by Execute (AT2); see hlgnot's.
func NewRefundRequest(cur realm, ids string) Request {
	mustBeLive()
	mustBeASCII("ids", ids)
	return propose(cur, st.DraftRefund(ids, now(), env(cur)))
}

// NewApplyRefundRequest applies a queued refund decision whose 7 days have passed, at once (AT2): GovDAO's own act
// after hlwatch's check that the token's router on Ethereum is this realm; under a standing payouts flag it waits
// like a claim; see hlgnot's.
func NewApplyRefundRequest(cur realm, id int64) Request {
	mustBeLive()
	return propose(cur, st.DraftApplyRefund(id, now(), env(cur)))
}

// NewWriteOffRequest books refunded sends delivered on Ethereum after their refund as lost, after 7 days (AR1); see
// hlgnot's.
func NewWriteOffRequest(cur realm, ids string) Request {
	mustBeLive()
	mustBeASCII("ids", ids)
	return propose(cur, st.DraftWriteOff(ids))
}

// NewMigrateRequest: a successor that announced itself to the mailbox for the BUBBLE route (R25, AQ5) and is no
// route already (R31), after 7 days; a realm that succeeds another migrates on only once its own import has ended
// (R31); see hlgnot's.
func NewMigrateRequest(cur realm, to string) Request {
	mustBeLive()
	mustBeASCII("successor", to)
	return propose(cur, st.DraftMigrate(to, pkgPath, env(cur)))
}

func NewCancelRequest(cur realm, id int64) Request {
	return proposePaying(cur, st.DraftCancel(id), "", nil, id)
}

// Execute applies a queued decision whose 7 days have passed. Anyone may call it. A sweep payment moves BUBBLE
// out of the lock, so under a standing payouts flag it waits for the unpause like a claim (§5.9). A credit or a sweep
// payment whose payee became a realm of the bridge in its 7 days stays pending (mustStillBePayable, E3-CF7(b)). A
// refund is never Execute's (AT2): the state refuses the kind, and GovDAO applies it by NewApplyRefundRequest.
func Execute(cur realm, id int64) {
	caller(cur)
	mustBeLive()
	if d := st.Ledger.Get(id); d != nil && d.Kind == hlroute.KindSweep {
		mustNotBePaused(hlmailbox.FnPayouts)
	}
	mustStillBePayable(id)
	dec := st.Execute(now(), id, env(cur))
	if dec.Kind == hlroute.KindCredit {
		countCredit(runtime.ChainHeight()) // the credit by vote p/hlroute queued at this height (voids.gno, E6-RL1)
	}
	// a decision that re-opens claims (a forged claim's restore, a credit by vote) moves them in the waiting index to a
	// new place, those open now (waiting.gno, E4-RL1): the ids its closure kept, not the ones it entered (E5-RL2)
	reopenNow(reopened(id))
	reopens.Remove(itoa(id))
	// echo-ok(dec): a decision's kind, a builder's constant, and its state, p/decisions' word
	chain.Emit("Decision", "id", itoa(dec.ID), "kind", dec.Kind, "state", dec.State())
}

// Pull is the successor's: once a migrate decision names it, the realm at that pkgpath calls Pull from its own
// code, page by page from 0, and receives the lock (transferred to its address, with page 0) and the records to
// import, up to 25 a page (pullPage, waiting.gno: a page of 500 outgrew gnoland-1's block, E5-RL1, and one of 50 a
// third of it at 10,000 claims, E6-RL3; the snapshot's More says whether a page follows); a page pulled already hands
// over nothing. Page 0 moves the lock, so under a standing payouts flag it waits for the unpause like a sweep payment
// (R27, R16); the flag follows the claims to the successor (hlmailbox.AddRoute).
func Pull(cur realm, page int) *hlroute.Snapshot {
	if !cur.IsCurrent() {
		panic("hlbubble: that realm value is not live; pass your own cur")
	}
	path := cur.Previous().PkgPath()
	if st.MigratedTo == "" || path != st.MigratedTo {
		panic("hlbubble: only the successor named by a migrate decision pulls")
	}
	if page == 0 {
		mustNotBePaused(hlmailbox.FnPayouts)
	}
	snap := st.Export(page, pullPage)
	for _, c := range snap.Claims {
		leave(c.ID) // the page takes its claims: out of this route's waiting index (waiting.gno, E4-RL1)
	}
	if page == 0 {
		hlmailbox.NotePulled(cross(cur), path) // the lock moved: the mailbox answers Pulled (R35) — kind 6 may enrol the successor, and the migration is never re-pointed
	}
	if snap.Lock > 0 {
		pay(cur, chain.PackageAddress(path), snap.Lock)
	}
	// echo-ok(path): the successor's pkgpath, ASCII by tm2's path rules: the one the migrate decision named (checked above)
	chain.Emit("Migrated", "to", path, "page", strconv.Itoa(page), "lock", itoa(snap.Lock), "claims", strconv.Itoa(len(snap.Claims)), "sweeps", strconv.Itoa(len(snap.Sweeps)), "sends", strconv.Itoa(len(snap.Sends)), "more", strconv.FormatBool(snap.More))
	return snap
}

// --------------------------------------------------------------- pages --

func pausedText() string {
	var fns []string
	for _, fn := range []string{hlmailbox.FnSends, hlmailbox.FnCredits, hlmailbox.FnPayouts} {
		if hlmailbox.Paused(pkgPath, fn) {
			fns = append(fns, fn)
		}
	}
	return strings.Join(fns, ", ")
}

// Render: "" the route's page; "decisions"; "json"; "decisions.json"; "decisions.json/<from>" (at most 20 decisions a
// page, hlmailbox.DecisionsJSONPage); "claim/<id>"; "send/<id>"; "claims/<from>", "sweeps/<from>" and
// "refunds/<from>", up to 100 entries a page; "waiting/<from>", the waiting index (waitingPage, E4-RL1: the machines'
// read of the open claims, where claims/<from> walks every claim before <from>) — hlgnot's Render says more.
func Render(path string) string {
	epoch, at := hlmailbox.PayoutsEpoch(pkgPath)
	switch {
	case strings.HasPrefix(path, "claims/"):
		return st.ClaimsPage(parseFrom(path[len("claims/"):]))
	case strings.HasPrefix(path, "sweeps/"):
		return st.SweepsPage(parseFrom(path[len("sweeps/"):]))
	case strings.HasPrefix(path, "send/"):
		// echo-ok(path, st.SendJSON): p/hlroute answers "null" for anything but a 32-byte hex id (CleanID refuses it), echoing nothing, and writes hex, a canonical address and numbers
		return st.SendJSON(path[len("send/"):])
	case strings.HasPrefix(path, "refunds/"):
		return st.RefundsPage(parseFrom(path[len("refunds/"):]))
	case path == "":
		var b strings.Builder
		b.WriteString("# BUBBLE across chains\n\n")
		b.WriteString("The Ethereum bridge's BUBBLE route on gno.land. BUBBLE sent to Ethereum is locked here, and the synthetic BUBBLE there (an ERC-20, Hyperlane's HypERC20) is backed by it one to one; a burn there is credited here as a claim, paid under the meters. Every setting moves only by a decision made with this realm's builders — the governor's own until it yields, a GovDAO proposal after.\n\n")
		if hlmailbox.Live() {
			b.WriteString("- **live** since " + itoa(hlmailbox.LaunchedAt()) + "\n")
		} else {
			b.WriteString("- **not launched**: the values below are the compiled launch values, waiting for the Launch decision\n")
		}
		b.WriteString("- " + decisions.GovernorLine(launchGovernor, hlmailbox.Yielded()) + "\n")
		b.WriteString("- balance: " + st.Amount(Balance()) + "\n")
		b.WriteString("- router on Ethereum: `" + RouterAddress() + "` (the BUBBLE token contract, domain " + u32(st.Domain) + ")\n")
		b.WriteString(ethWords(st.Figures(now(), epoch, at, hlmailbox.LaunchedAt(), pausedText())))
		b.WriteString("\n## Decisions\n\n" + st.Ledger.Page())
		return b.String()
	case path == "decisions":
		return "# Decisions\n\n" + st.Ledger.Page()
	case path == "json":
		return "{\"address\":" + strconv.QuoteToASCII(string(Address())) + ",\"live\":" + strconv.FormatBool(hlmailbox.Live()) +
			"," + decisions.GovernorJSON(launchGovernor, hlmailbox.Yielded()) +
			",\"balance\":" + itoa(Balance()) + ",\"paused\":" + strconv.QuoteToASCII(pausedText()) + ",\"routerAddress\":" + strconv.QuoteToASCII(RouterAddress()) +
			"," + st.JSON(now(), epoch, at, hlmailbox.LaunchedAt()) + ",\"waitingLast\":" + itoa(waitingLast) + "}"
	case path == "decisions.json":
		return st.Ledger.JSON()
	case strings.HasPrefix(path, "decisions.json/"):
		return hlmailbox.DecisionsJSONPage(&st.Ledger, path[len("decisions.json/"):])
	case strings.HasPrefix(path, "waiting/"):
		return waitingPage(path[len("waiting/"):])
	case strings.HasPrefix(path, "claim/"):
		// echo-ok(path, st.ClaimJSON): p/hlroute answers "null" for anything but a 32-byte hex id (CleanID refuses it), echoing nothing, and writes hex, a canonical address, numbers and its words
		return st.ClaimJSON(path[len("claim/"):])
	}
	return "unknown path; see the route's page"
}

// parseFrom reads a page's offset; -1 (an empty page) for anything but a number.
func parseFrom(s string) int {
	n, err := strconv.Atoi(s)
	if err != nil || n < 0 {
		return -1
	}
	return n
}
