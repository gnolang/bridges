// Package hlgov carries GovDAO's decisions to Ethereum (eth/bridge/PLAN.md §5.3, §6.5, §13; the Solana plan's §5.3,
// §6.5 and §13 carried to Ethereum): a proposal made with one of the builders below, when it executes, dispatches a
// message through r/hlmailbox to HlAdmin, the immutable contract that holds every Ethereum authority — the
// decision's kind, its parameters and its sequence number (this realm's own counter, taken at execution) — and
// HlAdmin records it by that number and executes it after its clock. Anyone may re-dispatch an executed decision
// (the same number and parameters) until HlAdmin has recorded it. The builders check the parameters with HlAdmin's
// own rules where a realm can read them (an address's form, a fee against the compiled maximum, a proxy against the
// compiled five, a body under 512 bytes, eth §5.3), so a proposal that would fail on Ethereum is refused when it is
// made; what a realm cannot read — a contract's code hash, a successor's state — the proposer and hlwatch read
// before the vote. Every address a body carries is an Ethereum address, 32 bytes left-padded, read by
// hlmailbox.ParseEthAddress (a non-zero top twelve bytes refused here, as HlAdmin spends such a decision) and
// written in EIP-55 case on the pages. Kinds 4, 10 and 11 are reserved and have no builder (§13); a cancel's body
// starts with an 8-byte salt drawn in the executing transaction, so two cancels of one list differ; kind 12 names a
// token's mint limits on the gate (the per-message maximum and the day cap, in base units), one of the 7-day group.
// A section number alone (§5.3) is the Solana plan's (bridge/PLAN.md); "eth §x" is the Ethereum plan's.
//
// Two decisions change this realm alone: `set decision recipient` (the contract decisions go to, a 7-day change
// naming the recipient it replaces and applied only while that stands, how a successor of HlAdmin ever receives
// one) and `sync sequence` (at once: the counter is raised — never lowered — to the number HlAdmin expects next,
// when a foreign decision has taken one, §6.5). The counter starts at a compiled number (launchSequence: 0 for a
// chain's first r/hlgov, the predecessor's last number for a successor), so no number HlAdmin recorded is ever
// reused. Before the Launch decision every builder but the cancel of a queued change here is refused. Who decides is
// the governor's (M1k): the compiled launchGovernor through Govern until it yields on r/hlmailbox, GovDAO's
// proposals after. Every launch value is compiled in below.
package hlgov

import (
	"chain"
	"chain/runtime"
	"crypto/keccak256"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"strconv"
	"strings"
	"time"

	"gno.land/p/g1leu8d2vsplhehcfkjg50mwgdpxdkt8tztu95wr/decisions/v0"
	hl "gno.land/p/g1leu8d2vsplhehcfkjg50mwgdpxdkt8tztu95wr/hyperlane/v0"
	"gno.land/p/nt/avl/v0"
	"gno.land/r/g1leu8d2vsplhehcfkjg50mwgdpxdkt8tztu95wr/eth/hlbubble"
	"gno.land/r/g1leu8d2vsplhehcfkjg50mwgdpxdkt8tztu95wr/eth/hlgnot"
	"gno.land/r/g1leu8d2vsplhehcfkjg50mwgdpxdkt8tztu95wr/eth/hlmailbox"
	"gno.land/r/gov/dao"
)

const (
	namespace = "gno.land/r/g1leu8d2vsplhehcfkjg50mwgdpxdkt8tztu95wr/eth/"
	pkgPath   = namespace + "hlgov" // must match gnomod.toml

	// The kinds (eth §13).
	KindCancel      = uint8(1)
	KindUnpause     = uint8(2)
	KindTokenISM    = uint8(3)
	KindReserved    = uint8(4) // reserved (eth §13): recorded by HlAdmin and spent at execution; no builder here, nor for 10 and 11
	KindGuardians   = uint8(5)
	KindRouter      = uint8(6)
	KindProtocolFee = uint8(7)
	KindUpgrade     = uint8(8)
	KindSuccessor   = uint8(9)
	KindMintLimits  = uint8(12) // set mint limits (eth §13): a token's per-message maximum and day cap on the gate; the 7-day group

	maxCancel = 16 // numbers one cancel names
	// maxProtocolFee is the most kind 7 may set, in wei: the immutable maximum of Hyperlane's ProtocolFee hook, 0.001
	// ETH, fixed at its deploy (eth §5.5, §5.8 step 3; the red team's ER11 lowered it from 0.01).
	maxProtocolFee = uint64(1_000_000_000_000_000)
	// MaxDecisionBody bounds a decision's body (kind ‖ number ‖ params) at 512 bytes, the bound HlAdmin's rules
	// carry over from the Solana deployment (eth §5.3: "a body under 512 bytes"; there it was what a delivery could
	// carry, the assumptions audit's F18): a body HlAdmin would not record would stick this realm's sequence
	// with its number untaken. Every builder's body is far below (a cancel at most 163 bytes with its salt, kind 5 at
	// most 142 with four entries, kind 8 105 and its call); the bound stands where this realm dispatches, checked when a proposal
	// is made and in its closure.
	MaxDecisionBody = 512
	// maxUpgradeCall is the most calldata kind 8 carries (ER7): what MaxDecisionBody leaves after the kind, the number
	// and the proxy, implementation and code hash, 32 bytes each.
	maxUpgradeCall = MaxDecisionBody - 9 - 3*32
	syncReach      = uint64(1) << 32 // a sync sequence raises the counter by at most this (the realm's own bound, §13; kind 10 is reserved since v35)
	seatCount      = 6
	guardianSeats  = 4
	maxSeatChanges = 4 // kind 5: two guardian seats plus either watch slot

	// The launch values (eth §5.3, §13): the four guardians' and two watch keys' Ethereum addresses as HlAdmin's
	// constructor carries them, each on its own line for the stage step to swap. PLACEHOLDERS until the people have
	// keys, which init refuses on gnoland-1 (mainnet.gno). HlAdmin's address (the first decision recipient) and the
	// five proxies kind 8 alone may upgrade — the mailbox, token 0 (GNOT's HypERC20), token 1 (BUBBLE's), the light
	// client and the gate, the gate also the only non-zero ISM kind 3 names — are the deployment's contracts hlmailbox
	// compiles (its ethdeploy.gno, one list, which the routes refuse as recipients too), read from it at deploy.
	launchEthSeat1 = "0x1111111111111111111111111111111111111111"
	launchEthSeat2 = "0x2222222222222222222222222222222222222222"
	launchEthSeat3 = "0x3333333333333333333333333333333333333333"
	launchEthSeat4 = "0x4444444444444444444444444444444444444444"
	launchEthSeat5 = "0x5555555555555555555555555555555555555555"
	launchEthSeat6 = "0x6666666666666666666666666666666666666666"

	// launchSequence is the number of the last decision this realm's predecessor dispatched — 0 for a chain's
	// first r/hlgov. A successor r/hlgov for a new chain is deployed with its predecessor's last number here
	// (§5.3), so its counter starts where HlAdmin's stands and never restarts below a recorded number.
	// Written by init; the page shows it beside the counter.
	launchSequence = uint64(0)
)

// launchGovernor is the governor (M1k): the mailbox's, compiled here too so a member reads it in this realm's
// served source; init refuses a deploy with another. The e2e swaps it for gnodev's test1.
const launchGovernor = address("g1dpkz6em0wejhymn0wf047h6lta047h6lvxn4gx")

var (
	destination = hlmailbox.LaunchOrigin()
	recipient   = ethContract(hlmailbox.EthHlAdmin) // HlAdmin at launch: the first decision recipient
	former      [][32]byte                          // every decision recipient a swap replaced and none since restored (R29, E2-RL2): kind 9 refuses each for good, a swap may name one again
	gate        = ethContract(hlmailbox.EthGate)    // the gate (PausableGate's proxy): the only non-zero ISM kind 3 names
	proxies     [5][32]byte                         // the five proxies kind 8 may upgrade (hlmailbox's EthMailbox … EthGate): the deployment's for life
	proxyNames  [5]string                           // … and their names
	ethSeats    [seatCount + 1][32]byte             // Ethereum's seats as this realm last set them: a mirror kind 5 warns against, shown on the page

	last uint64   // the number of the last decision dispatched; the next is last + 1
	sent avl.Tree // seqKey(number) → *decision

	ledger decisions.Ledger

	// guardedOn is the gno.land domain init's guard checked the launch values at (guardLaunch): the suite reads it
	// (TestInitGuarded), so a tree whose init lost the call fails the suite (E3-RL7, as hlmailbox's guardedOn).
	guardedOn uint32
)

// decision is one dispatched decision: its body (kind ‖ number ‖ params) and its dispatches; a kind 6 for a route
// of this chain also keeps the route it enrolled and the height it noted with the mailbox (R37: a cancel of it
// clears that note while it is the latest).
type decision struct {
	kind   uint8
	body   []byte
	at     int64  // first dispatched
	id     string // the latest message id
	count  int    // dispatches so far
	text   string
	route  string // a kind 6 for this chain's domain: the route realm it enrolled; "" otherwise
	height int64  // … and the block height NoteEnrolled recorded for it
}

type (
	recipientParams struct{ from, to [32]byte } // a swap: the recipient it replaces (Q-F6b) and the one it names
	sequenceParams  struct{ n uint64 }
	cancelParams    struct{ id int64 }
	kindParams      struct {
		kind   uint8
		params []byte
		seats  []int
		keys   [][32]byte
	}
)

func init() {
	ethSeats[1], ethSeats[2], ethSeats[3] = mustAddr(launchEthSeat1), mustAddr(launchEthSeat2), mustAddr(launchEthSeat3)
	ethSeats[4], ethSeats[5], ethSeats[6] = mustAddr(launchEthSeat4), mustAddr(launchEthSeat5), mustAddr(launchEthSeat6)
	guardedOn = guardLaunch()
	for i := range proxies {
		proxyNames[i], proxies[i] = hlmailbox.EthContractAt(hlmailbox.EthMailbox + i)
	}
	last = launchSequence
}

// guardLaunch is init's guard, over the launch values as init has read them. It refuses a deploy whose compiled
// governor is not the mailbox's (M1k): the bridge's realms are governed by one key until it yields, or by GovDAO
// from the first block, never a mix; on gnoland-1 one whose Ethereum seat or decision recipient is still the dev
// tree's (devPlaceholder, E2-RL1); and on every chain one whose Ethereum seats are no launch's (seatsFault, E3-RL2).
// It returns the domain it checked at.
func guardLaunch() uint32 {
	if launchGovernor != hlmailbox.Governor() {
		// echo-ok(hlmailbox.Governor): the mailbox's compiled governor, a bech32 address
		panic("hlgov: the compiled governor " + string(launchGovernor) + " is not the mailbox's " + string(hlmailbox.Governor()))
	}
	d := hlmailbox.LocalDomain()
	if why := devPlaceholder(d, ethSeats, recipient); why != "" {
		// echo-ok(why): the guard's own refusal: ASCII words, contract names, EIP-55 and bech32 addresses and pkgpaths, a few hundred bytes at most: far under the cut
		panic(why)
	}
	if why := seatsFault(ethSeats); why != "" {
		// echo-ok(why): the guard's own refusal: ASCII words, contract names, EIP-55 and bech32 addresses and pkgpaths, a few hundred bytes at most: far under the cut
		panic(why)
	}
	return d
}

// seatsFault names the first of the Ethereum seats, as init reads them (each the 32 bytes an address is carried
// as), that no launch may compile, or "" (the round-3 realms audit's E3-RL2: a stage step whose shell variables came
// out empty, or that copied one key into two seat lines): the zero address, a seat no key holds, and a key in two
// seats. HlAdmin refuses both at construction on every chain (Guardians.init: every key non-zero, no key twice),
// and this realm's mirror of the seats — what its kind 5 and its swap warn and refuse against — refuses them too.
func seatsFault(seats [seatCount + 1][32]byte) string {
	for n := 1; n <= seatCount; n++ {
		if seats[n] == ([32]byte{}) {
			return "hlgov: launchEthSeat" + strconv.Itoa(n) + " is the zero address, a seat no key holds (HlAdmin refuses it at construction)"
		}
		for m := 1; m < n; m++ {
			if seats[n] == seats[m] {
				return "hlgov: launchEthSeat" + strconv.Itoa(m) + " and launchEthSeat" + strconv.Itoa(n) + " are both " + ea(seats[n]) + ": a key holds one seat (HlAdmin refuses it at construction)"
			}
		}
	}
	return ""
}

// ethContract is the address hlmailbox compiles for one of the deployment's contracts (ethdeploy.gno).
func ethContract(i int) [32]byte {
	_, k := hlmailbox.EthContractAt(i)
	return k
}

// mustAddr reads an Ethereum address with the deployment's one parser (hlmailbox.ParseEthAddress: 0x and 40 hex
// digits, EIP-55 when mixed case, or 64 hex digits whose first 24 are zero), left-padded to 32 bytes: every
// address a decision carries, so a non-zero top twelve bytes — which makes HlAdmin spend the decision (eth §13) —
// is refused when the proposal is made.
func mustAddr(s string) [32]byte {
	k, err := hlmailbox.ParseEthAddress(s)
	if err != nil {
		// echo-ok(err): ParseEthAddress's refusal: its ASCII words and its echo of what it read (echo, E4-GS1)
		panic("hlgov: " + err.Error())
	}
	return k
}

// mustHash reads a 32-byte code hash (kinds 8 and 9): 64 hex digits, with or without 0x; never zero nor the empty
// code's hash, keccak256 of nothing (eth §13 as plan e4 has it). HlAdmin executes a kind 8 or 9 only on code whose
// EXTCODEHASH is the decision's: while the target holds no code the decision pends (NoCodeYet), and once it holds
// some — whose hash is neither of these — the decision is spent, so a decision pinning either could never execute.
func mustHash(s, what string) [32]byte {
	var h [32]byte
	b, err := hex.DecodeString(strings.TrimPrefix(strings.TrimSpace(s), "0x"))
	if err != nil || len(b) != 32 {
		panic("hlgov: the " + what + " is 32 bytes in hex (64 digits, with or without 0x), not " + echo(s)) // echo-ok(what): every caller names the field with a constant: ASCII words
	}
	copy(h[:], b)
	if h == ([32]byte{}) {
		panic("hlgov: the " + what + " is all zeros, " + neverExecutes) // echo-ok(what): every caller names the field with a constant: ASCII words
	}
	if h == keccak256.Sum256(nil) {
		panic("hlgov: the " + what + " is the empty code's hash (keccak256 of nothing), " + neverExecutes) // echo-ok(what): every caller names the field with a constant: ASCII words
	}
	return h
}

// neverExecutes ends mustHash's refusals, in ASCII: they take a field's name too, so no cut may split a rune (E5-RL3).
const neverExecutes = "which no contract's code hashes to: HlAdmin could never execute the decision; it pends while the target holds no code (NoCodeYet) and is spent once it holds some (eth section 13)"

// ea writes 32 bytes as an Ethereum address in EIP-55 case (hlmailbox.EthAddress).
func ea(k [32]byte) string { return hlmailbox.EthAddress(k) }

// proxyName is the name of the proxy at k, one of the five kind 8 may upgrade; "" for any other address.
func proxyName(k [32]byte) string {
	for i, p := range proxies {
		if p == k {
			return proxyNames[i]
		}
	}
	return ""
}

func now() int64 { return time.Now().Unix() }

func itoa(n int64) string    { return strconv.FormatInt(n, 10) }
func u64s(n uint64) string   { return strconv.FormatUint(n, 10) }
func hx(b []byte) string     { return hex.EncodeToString(b) }
func seqKey(n uint64) string { return strings.Repeat("0", 20-len(u64s(n))) + u64s(n) }

func caller(cur realm) address {
	if !cur.IsCurrent() {
		panic("hlgov: that realm value is not live; pass your own cur")
	}
	return cur.Previous().Address()
}

func mustBeLive() {
	if !hlmailbox.Live() {
		panic("hlgov: not launched: before the Launch decision only the cancel of a queued change is proposed")
	}
}

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
			panic("hlgov: only " + decisions.DAOPath + " executes a decision, not " + who(prev.PkgPath(), prev.Address()))
		}
		return
	}
	if !prev.IsUser() || prev.Address() != launchGovernor {
		// echo-ok(who): the previous realm's pkgpath, ASCII by tm2's path rules, or its bech32 address
		panic("hlgov: until the governor yields, " + string(launchGovernor) + " alone decides, by Govern from its own key: not " + who(prev.PkgPath(), prev.Address()) + " (a GovDAO proposal is refused until the yield)")
	}
}

// Govern is the governor's way in before the yield (M1k): the same closure GovDAO's proxy runs after it, called
// non-crossing in this frame so it reads the governor from cur.Previous() — and dispatches through the mailbox
// under this realm's own authority, as the proxy's execution does; refused for anyone else and for everyone once
// the governor yielded on r/hlmailbox (or when none was compiled); see hlgnot's.
func Govern(cur realm, r Request) {
	if !cur.IsCurrent() {
		panic("hlgov: that realm value is not live; pass your own cur")
	}
	if r.decide == nil {
		panic("hlgov: not a builder's request")
	}
	if yielded := hlmailbox.Yielded(); decisions.DAOGoverns(launchGovernor, yielded) {
		if launchGovernor == "" {
			panic("hlgov: no governor was compiled, " + decisions.DAOPath + " governs from the first block: a decision is a GovDAO proposal, never Govern")
		}
		panic("hlgov: the governor yielded at height " + itoa(yielded) + ", " + decisions.DAOPath + " governs since: a decision is a GovDAO proposal, never Govern")
	}
	mustBeGovernor(cur)
	if err := r.decide(cur); err != nil {
		// echo-ok(err): p/decisions' refusal: ASCII words and numbers
		panic("hlgov: " + err.Error())
	}
}

// ------------------------------------------------------------- reading --

// Address is this realm's address: the sender HlAdmin accepts (its compiled hlgov, as 32 bytes).
func Address() address { return chain.PackageAddress(pkgPath) }

// Recipient is the contract decisions are addressed to: HlAdmin's Ethereum address, in EIP-55 case.
func Recipient() string { return ea(recipient) }

// Former lists every decision recipient a swap replaced and no later swap restored, in EIP-55 case, in the order they
// were replaced (R29, E2-RL2): retired HlAdmins, or a contract a swap named too early or by mistake.
func Former() []string {
	out := make([]string, len(former))
	for i, k := range former {
		out[i] = ea(k)
	}
	return out
}

// Proxy is the address of one of the five contracts kind 8 may upgrade, in EIP-55 case: 0 the mailbox, 1 token 0
// (GNOT), 2 token 1 (BUBBLE), 3 the light client, 4 the gate; "" for any other index.
func Proxy(i int) string {
	if i < 0 || i >= len(proxies) {
		return ""
	}
	return ea(proxies[i])
}

func isFormer(k [32]byte) bool {
	for _, f := range former {
		if f == k {
			return true
		}
	}
	return false
}

// mustNotBeFormer refuses a former decision recipient as a kind 9's successor (R29): a retired HlAdmin, whose
// authorities went to a successor, records nothing more (Retired) and has inherited already, so HlAdmin would spend a
// kind 9 naming it (eth §13) and the vote is wasted. Checked when the proposal is made and in its closure. The
// recipient swap no longer asks (E2-RL2): it may name a former recipient again, so a swap that landed before the kind
// 9 it follows executed on Ethereum — or one to a wrong contract — is undone by a second swap, never frozen.
func mustNotBeFormer(k [32]byte) {
	if isFormer(k) {
		panic("hlgov: never a former HlAdmin (" + ea(k) + "): a decision recipient a swap replaced is never a successor — a retired HlAdmin has inherited already, so HlAdmin would spend the decision (R29)")
	}
}

// retire records a swap: the recipient it replaces joins the former ones, and the one it names, if it was a former
// recipient (E2-RL2: a swap undone by a second one), leaves them — Former is every recipient replaced and not since
// restored, each once.
func retire(old, next [32]byte) {
	var out [][32]byte
	for _, f := range former {
		if f != next && f != old {
			out = append(out, f)
		}
	}
	former = append(out, old)
}

// Destination is the Ethereum domain decisions go to: hlmailbox's launch origin (1 on gnoland-1).
func Destination() uint32 { return destination }

// Last is the number of the last decision dispatched; Next the number the next one takes; Start the compiled
// number the counter began at (the predecessor's last number; 0 for a chain's first r/hlgov).
func Last() uint64  { return last }
func Next() uint64  { return last + 1 }
func Start() uint64 { return launchSequence }

// EthSeat is the Ethereum address this realm last set for a seat (1–4 guardians, 5–6 watch keys), in EIP-55 case.
func EthSeat(n int) string {
	if n < 1 || n > seatCount {
		return ""
	}
	return ea(ethSeats[n])
}

// Body is a dispatched decision's body in hex, by number ("" if none).
func Body(n uint64) string {
	v := sent.Get(seqKey(n))
	if v == nil {
		return ""
	}
	return hx(v.(*decision).body)
}

// -------------------------------------------------------------- encoding --

func be32(n uint32) []byte { return binary.BigEndian.AppendUint32(nil, n) }
func be64(n uint64) []byte { return binary.BigEndian.AppendUint64(nil, n) }

// body is kind u8 ‖ seq u64 BE ‖ params (§13).
func body(kind uint8, n uint64, params []byte) []byte {
	b := make([]byte, 0, 9+len(params))
	b = append(b, kind)
	b = append(b, be64(n)...)
	return append(b, params...)
}

// mustFit refuses a body above MaxDecisionBody: the one check every dispatch makes, so no builder can author a
// decision HlAdmin would not record.
func mustFit(raw []byte) {
	if len(raw) > MaxDecisionBody {
		panic("hlgov: the decision body is " + strconv.Itoa(len(raw)) + " bytes, above the " + strconv.Itoa(MaxDecisionBody) + " HlAdmin's rules allow (MaxDecisionBody): it would stick the sequence with its number untaken")
	}
}

// dispatch sends a decision with the next number and records it; a body above MaxDecisionBody is refused first.
func dispatch(cur realm, kind uint8, params []byte, text string, t int64) uint64 {
	n := last + 1
	raw := body(kind, n, params)
	mustFit(raw)
	id := hlmailbox.Dispatch(cross(cur), destination, recipient, raw)
	last = n
	sent.Set(seqKey(n), &decision{kind: kind, body: raw, at: t, id: hx(id[:]), count: 1, text: text})
	chain.Emit("DecisionSent", "number", u64s(n), "kind", strconv.Itoa(int(kind)), "id", hx(id[:]))
	return n
}

// Redispatch sends an executed decision again — the same number and parameters, a new message — by anyone, until
// HlAdmin has recorded it (a copy it has recorded is accepted and ignored there).
func Redispatch(cur realm, n uint64) {
	caller(cur)
	v := sent.Get(seqKey(n))
	if v == nil {
		panic("hlgov: no decision " + u64s(n))
	}
	d := v.(*decision)
	id := hlmailbox.Dispatch(cross(cur), destination, recipient, d.body)
	d.id = hx(id[:])
	d.count++
	chain.Emit("DecisionSent", "number", u64s(n), "kind", strconv.Itoa(int(d.kind)), "id", hx(id[:]))
}

// ------------------------------------------------------------- builders --

func propose(cur realm, kind, text string, apply func(cur realm, made, t int64)) Request {
	if !cur.IsCurrent() {
		panic("hlgov: that realm value is not live; pass your own cur")
	}
	made := now()
	cb := func(cur realm) error {
		mustBeGovernor(cur)
		t := now()
		if err := decisions.Expired(made, t); err != nil {
			return err
		}
		apply(cur, made, t)
		return nil
	}
	return Request{proposal: dao.NewProposalRequest(titlePrefix+kind, text, dao.NewSimpleExecutor(0, cur, cb, text)), text: text, decide: cb}
}

// titlePrefix begins every proposal title this realm's builders make (E3-RL6): the Solana deployment's r/hlgov titles
// its "hlgov: <kind>", and GovDAO's list shows titles alone, so a member tells the two bridges' decisions apart by
// "eth/" before the realm's name — the deployment's own segment of the namespace.
const titlePrefix = "eth/hlgov: "

// proposeKind is an Ethereum decision's builder: at execution it dispatches the message and records the decision as
// applied; the parameters were checked at creation and are checked again then, the body's size with them.
func proposeKind(cur realm, kind uint8, name, text string, params []byte, check func()) Request {
	mustBeLive()
	mustFit(body(kind, 0, params))
	check()
	return propose(cur, name, text, func(cur realm, made, t int64) {
		mustBeLive()
		check()
		n := dispatch(cur, kind, params, text, t)
		d := ledger.Record(name, text, kindParams{kind: kind, params: params}, made, t, t)
		ledger.Apply(d, t, "dispatched as decision "+u64s(n))
	})
}

func parseNumber(p string) uint64 {
	n, err := strconv.ParseUint(strings.TrimSpace(p), 10, 64)
	if err != nil || n == 0 {
		panic("hlgov: not a decision number: " + echo(p))
	}
	return n
}

// salt is the 8 bytes a cancel body starts with (§13: random, ignored by HlAdmin,
// so no body is predictable and two cancels of one list differ). gno.land gives a realm no block
// hash, so it is sha256 over what the block and the decision give — the chain id, the height, the block time, the
// mailbox's nonce, the decision number and the ledger's next id — drawn in the executing transaction; the decision
// number alone makes two cancels of one list differ, the rest make it as unpredictable as the block it lands in.
func salt(t int64) []byte {
	b := []byte(runtime.ChainID())
	b = append(b, be64(uint64(runtime.ChainHeight()))...)
	b = append(b, be64(uint64(t))...)
	b = append(b, be32(hlmailbox.Count())...)
	b = append(b, be64(last+1)...)
	b = append(b, be64(uint64(ledger.Last()+1))...)
	h := sha256.Sum256(b)
	return h[:8]
}

// cancelSet is what a cancel names, as parsed: a range from-to, inclusive (form 1), or a list of numbers in the order
// written (form 0).
type cancelSet struct {
	rng      bool
	from, to uint64   // the range
	ns       []uint64 // the list
}

// parseCancel reads a cancel's numbers: "from-to", or numbers comma separated (1 to 16, none twice). Each number is
// parsed alone (parseNumber), whatever space surrounds it.
func parseCancel(numbers string) cancelSet {
	if i := strings.Index(numbers, "-"); i >= 0 {
		from, to := parseNumber(numbers[:i]), parseNumber(numbers[i+1:])
		if to < from {
			panic("hlgov: the range runs from the lower number to the higher")
		}
		return cancelSet{rng: true, from: from, to: to}
	}
	var ns []uint64
	for _, p := range strings.Split(numbers, ",") {
		if strings.TrimSpace(p) == "" {
			continue
		}
		n := parseNumber(p)
		for _, x := range ns {
			if x == n {
				panic("hlgov: number " + u64s(n) + " named twice")
			}
		}
		ns = append(ns, n)
	}
	if len(ns) == 0 || len(ns) > maxCancel {
		panic("hlgov: a cancel names 1 to " + strconv.Itoa(maxCancel) + " numbers, or a range")
	}
	return cancelSet{ns: ns}
}

// String is the cancel's numbers as the description writes them: "from-to", or the list joined by ", " — the parsed
// numbers, never what the proposer typed (E2-RL4: a range hidden behind "2" and eighty newlines, say).
func (c cancelSet) String() string {
	if c.rng {
		return u64s(c.from) + "-" + u64s(c.to)
	}
	parts := make([]string, len(c.ns))
	for i, n := range c.ns {
		parts[i] = u64s(n)
	}
	return strings.Join(parts, ", ")
}

// params are the body's bytes after the salt (§13): form 1, channel 0, from and to; or form 0, the count, and each
// number with its channel 0.
func (c cancelSet) params() []byte {
	if c.rng {
		return append(append([]byte{1, 0}, be64(c.from)...), be64(c.to)...)
	}
	params := []byte{0, byte(len(c.ns))}
	for _, n := range c.ns {
		params = append(params, 0)
		params = append(params, be64(n)...)
	}
	return params
}

// top is the highest number the cancel names.
func (c cancelSet) top() uint64 {
	if c.rng {
		return c.to
	}
	var top uint64
	for _, n := range c.ns {
		if n > top {
			top = n
		}
	}
	return top
}

// NewCancelRequest is kind 1: cancel pending decisions of the mailbox channel on Ethereum (channel 0, the only one
// this realm writes), by a list of numbers (comma separated, at most 16: form 0) or by a range "from-to",
// inclusive (form 1); each number one this realm dispatched. It executes at once there. The body is
// `salt u64 ‖ form u8 ‖ …` (§13), the salt drawn when the proposal executes, so the description names the numbers
// — as parsed, "from-to" or the list joined by ", ", never the text as typed (E2-RL4) — and the ledger's note and the
// decision's page carry the salt.
func NewCancelRequest(cur realm, numbers string) Request {
	mustBeLive()
	c := parseCancel(numbers)
	params := c.params()
	text := "cancel decisions " + c.String()
	mustFit(body(KindCancel, 0, append(make([]byte, 8), params...))) // with the salt's 8 bytes; dispatch checks it again
	check := func() {
		if top := c.top(); top > last {
			panic("hlgov: decision " + u64s(top) + " was never dispatched")
		}
	}
	check()
	return propose(cur, "cancel", text, func(cur realm, made, t int64) {
		mustBeLive()
		check()
		p := append(salt(t), params...)
		n := dispatch(cur, KindCancel, p, text, t)
		note := "dispatched as decision " + u64s(n) + ", salt " + hx(p[:8])
		// a cancelled kind 6 for a route of this chain clears the enrolment note it left, while that note is the
		// latest (R37, AS2): the route's refund clock restarts with a re-proposal, instead of opening on a kind 6
		// Ethereum never executed
		eachCancelled(c, func(num uint64, d *decision) {
			if d.kind != KindRouter || d.route == "" {
				return
			}
			if h, _ := hlmailbox.Enrolled(d.route); h != d.height {
				return
			}
			hlmailbox.ClearEnrolled(cross(cur), d.route)
			note += "; the enrolment note of " + d.route + " cleared (kind 6 #" + u64s(num) + " cancelled): its refund clock restarts with a re-proposal"
		})
		d := ledger.Record("cancel", text, kindParams{kind: KindCancel, params: p}, made, t, t)
		ledger.Apply(d, t, note)
	})
}

// eachCancelled walks the decisions a cancel names — the list, or every dispatched number of the range — in number
// order for a range and in the list's order for a list.
func eachCancelled(c cancelSet, fn func(n uint64, d *decision)) {
	if c.rng {
		sent.Iterate(seqKey(c.from), seqKey(c.to+1), func(k string, v any) bool {
			n, _ := strconv.ParseUint(strings.TrimLeft(k, "0"), 10, 64)
			fn(n, v.(*decision))
			return false
		})
		return
	}
	for _, n := range c.ns {
		if v := sent.Get(seqKey(n)); v != nil {
			fn(n, v.(*decision))
		}
	}
}

// NewUnpauseRequest is kind 2: clear the flags set before its delivery on the gate (object 0, PausableGate) or on
// HlAdmin (object 1), 24 hours after it arrives, unless two guardians' flags hold.
func NewUnpauseRequest(cur realm, object uint8) Request {
	if object > 1 {
		panic("hlgov: the object is 0 (the gate) or 1 (HlAdmin)")
	}
	name := "the gate"
	if object == 1 {
		name = "HlAdmin"
	}
	return proposeKind(cur, KindUnpause, "unpause", "unpause "+name, []byte{object}, func() {})
}

func parseToken(token uint8) string {
	switch token {
	case 0:
		return "GNOT"
	case 1:
		return "BUBBLE"
	}
	panic("hlgov: the token is 0 (GNOT) or 1 (BUBBLE)")
}

// NewSetTokenISMRequest is kind 3: point a token (0 GNOT, 1 BUBBLE) at the gate, or at none (the zero address,
// which falls back to the mailbox's default ISM — the gate, for the deployment's life): `token u8 ‖ ism 32` (eth
// §13), the ISM an Ethereum address, the compiled gate's or zero; a new gate is a kind-8 upgrade in place.
//
// Kinds 4, 10 and 11 are reserved (eth §13: HlAdmin records them and spends them) and have no builder here.
func NewSetTokenISMRequest(cur realm, token uint8, ism string) Request {
	name := parseToken(token)
	k := mustAddr(ism)
	if k != gate && k != ([32]byte{}) {
		panic("hlgov: the ISM must be the gate " + ea(gate) + " or the zero address, not " + ea(k))
	}
	params := append([]byte{token}, k[:]...)
	return proposeKind(cur, KindTokenISM, "set token ISM", "set "+name+"'s ISM to "+ea(k), params, func() {})
}

// parseSeatChanges reads "seat:address" pairs (comma separated; the address an Ethereum address, read by
// hlmailbox.ParseEthAddress): seats 1–6, two guardian seats at most, four entries at most, no seat or address twice,
// never the zero address nor a contract of the deployment (E2-RL7's class, the F6 review's follow-up: a seat's key
// is a guardian's or a watch key's; hlmailbox's sixteen flag and hold nothing, so the seat would be held by nobody).
func parseSeatChanges(changes string) ([]int, [][32]byte) {
	var ns []int
	var ks [][32]byte
	g := 0
	for _, p := range strings.Split(changes, ",") {
		i := strings.Index(p, ":")
		if i < 0 {
			panic("hlgov: a change is seat:address, not " + echo(p))
		}
		n, err := strconv.Atoi(strings.TrimSpace(p[:i]))
		if err != nil || n < 1 || n > seatCount {
			panic("hlgov: the seat must be 1 to 6, not " + echo(p[:i]))
		}
		for _, x := range ns {
			if x == n {
				panic("hlgov: seat " + strconv.Itoa(n) + " named twice")
			}
		}
		k := mustAddr(p[i+1:])
		if k == ([32]byte{}) {
			panic("hlgov: the zero address for seat " + strconv.Itoa(n))
		}
		if name := hlmailbox.EthContractName(k); name != "" {
			// echo-ok(name): hlmailbox.EthContractName's: a contract's name, an ASCII constant
			panic("hlgov: seat " + strconv.Itoa(n) + "'s key " + ea(k) + " is " + name + " of this deployment, a contract of the bridge that flags and holds nothing: the seat would be held by nobody")
		}
		for _, x := range ks {
			if x == k {
				panic("hlgov: an address named twice: " + ea(k))
			}
		}
		if n <= guardianSeats {
			g++
		}
		ns = append(ns, n)
		ks = append(ks, k)
	}
	if len(ns) == 0 || len(ns) > maxSeatChanges {
		panic("hlgov: 1 to " + strconv.Itoa(maxSeatChanges) + " seats per decision")
	}
	if g > 2 {
		panic("hlgov: two guardian seats at most per decision")
	}
	return ns, ks
}

// seatWarnings names the keys that hold a seat as this realm last set it. The real seats are Ethereum's (HlAdmin's
// and the gate's), and HlAdmin refuses a seat's current key itself (§6.5), spending the decision: a true no-op. The
// mirror here can sit ahead of the truth (a kind 5 HlAdmin spent) or behind it (a kind 5 of another decision moved a
// seat), so it is no ground to refuse a proposal — the builder warns in the description and the member reads
// `hlwatch`'s list of the seats as they stand.
func seatWarnings(ns []int, ks [][32]byte) string {
	var parts []string
	for i := range ns {
		for s := 1; s <= seatCount; s++ {
			if ethSeats[s] == ks[i] {
				parts = append(parts, ea(ks[i])+" holds seat "+strconv.Itoa(s)+" as last set here")
			}
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return " (warning: " + strings.Join(parts, "; ") + " — HlAdmin spends a decision naming a seat's current key, so check the seats as they stand on Ethereum before voting)"
}

// ethSeatOf is the Ethereum seat k holds as this realm last set it (1–4 guardians, 5–6 watch keys), or 0: the mirror,
// which can drift from Ethereum's seats (seatWarnings), enough to refuse a swap naming a key a kind 5 named.
func ethSeatOf(k [32]byte) int {
	for n := 1; n <= seatCount; n++ {
		if ethSeats[n] == k {
			return n
		}
	}
	return 0
}

// NewSetGuardiansRequest is kind 5: replace guardian seats (1–4, two at most) and watch slots (5–6) on Ethereum,
// four entries at most: `count u8 ‖ (seat u8 ‖ key 32)…` (eth §13), each key an Ethereum address left-padded, never
// zero nor a contract of the deployment (parseSeatChanges), nor one of HlAdmin's line (hlAdminLine: the recipient or
// a former one, checked when made and in the closure). A key that holds a seat as this realm last set it is
// warned about in the description, not refused: HlAdmin's own check spends a decision naming a seat's current key,
// and this realm's mirror of the seats can drift from the truth. It lands 7 days after delivery through any flag that
// is no hold, and under a hold on the later of its own 7th day and the 21st day of the earliest hold overlapping its
// pending life (eth §6.5; HlAdmin's _replacementFloor).
func NewSetGuardiansRequest(cur realm, changes string) Request {
	mustBeLive()
	ns, ks := parseSeatChanges(changes)
	params := []byte{byte(len(ns))}
	parts := make([]string, len(ns))
	for i, n := range ns {
		params = append(params, byte(n))
		params = append(params, ks[i][:]...)
		parts[i] = "seat " + strconv.Itoa(n) + " to " + ea(ks[i])
	}
	text := "set guardians and watch keys: " + strings.Join(parts, ", ") + seatWarnings(ns, ks)
	// nor one of HlAdmin's line (hlAdminLine), which moves with the swaps: checked when made and in the closure
	check := func() {
		for i, k := range ks {
			if why := hlAdminLine(k); why != "" {
				// echo-ok(why): pastRecipient's words and EIP-55 addresses, a few hundred bytes at most: far under the cut
				panic("hlgov: seat " + strconv.Itoa(ns[i]) + "'s key " + ea(k) + " is " + why + ": an address decisions go to, or went to, never holds a seat")
			}
		}
	}
	check()
	return propose(cur, "set guardians", text, func(cur realm, made, t int64) {
		mustBeLive()
		check()
		n := dispatch(cur, KindGuardians, params, text, t)
		for i, s := range ns {
			ethSeats[s] = ks[i]
		}
		d := ledger.Record("set guardians", text, kindParams{kind: KindGuardians, params: params, seats: ns, keys: ks}, made, t, t)
		ledger.Apply(d, t, "dispatched as decision "+u64s(n))
	})
}

// NewEnrollRouterRequest is kind 6: enroll a token's remote router for a domain, `token u8 ‖ domain u32 ‖ router
// 32` (eth §13), the router 32 bytes whose top twelve are zero, written as an address (0x and 40 hex digits, EIP-55
// when mixed case, or 64 hex digits whose first 24 are zero; a router with non-zero top bytes, which no route of
// this deployment has, is refused as every address parameter is — E2-RL6 kept that reading, plan e5 §13 to say so),
// never the zero address for any domain (HlAdmin executes it as unenrolling, and unenrolling is no decision of the
// table, eth §13), never for Ethereum's own domain nor gno.land's other one (refusedDomain, E2-RL7: a burn there is
// final and credited nowhere). For gno.land's own domain the router must be a route realm registered with hlmailbox
// — the launch routes, and the successors their migrations named — as 32 bytes (a gno address left-padded): the
// token contract (HypERC20) takes every Dispatch from the router it enrolls as a mint order, and hlmailbox's
// Dispatch is open to any realm, so any other address would mint synthetic that no lock backs; the token's own route
// (R36), unmigrated, and — a successor — one that pulled page 0 (R35). The rules are checked when the proposal is
// made and again when it executes. The closure then notes with the mailbox the route it enrolled and the height
// (hlmailbox.NoteEnrolled, AR1): that route's refund builder waits 14 days after the note (R37: a week for the
// delivery, a week for Ethereum's clock — HlAdmin executes a kind 6 7 days after its delivery), since a refunded send
// stays deliverable on Ethereum until the predecessor's router is replaced; a cancel of the kind 6 clears the note
// (NewCancelRequest).
func NewEnrollRouterRequest(cur realm, token uint8, domain uint32, router string) Request {
	mustBeLive()
	name := parseToken(token)
	k := mustAddr(router)
	if k == ([32]byte{}) {
		// HlAdmin executes a kind 6 naming the zero router as unenrolling the domain (unenrollRemoteRouter; the
		// token's own enrollRemoteRouter would leave the domain enrolled at zero, a send there a message to address
		// 0), and unenrolling is no decision of the table (eth §13)
		panic("hlgov: the router is the zero address, which HlAdmin executes as unenrolling the domain: unenrolling is no decision of this table (eth §13)")
	}
	if domain == 0 {
		panic("hlgov: the domain must be set")
	}
	if why := refusedDomain(domain, hlmailbox.LocalDomain(), hlmailbox.LaunchOrigin()); why != "" {
		// echo-ok(why): refusedDomain's words and domain numbers, a few hundred bytes at most: far under the cut
		panic(why)
	}
	local := domain == hlmailbox.LocalDomain()
	check := func() string {
		if !local {
			return ""
		}
		path := routePathOf(k)
		if path == "" {
			panic("hlgov: for this chain's domain the router must be a route realm registered with hlmailbox, not " + ea(k))
		}
		// the token's own route, and a live one (R36): the other token's route — or its successor — as this
		// token's router strands this token's burns behind per-id votes until a corrective kind 6 lands, refuses
		// its sends on Ethereum meanwhile, and writes the enrolment note on the other route, opening its refunds
		// while the other token's router still delivers (AR1's double mint again); a migrated route's successor
		// is the token's router, never the route itself
		if sym := hlmailbox.RouteSymbol(path); sym == "" {
			// echo-ok(path, sym, name, s, pred): a registered route's pkgpath, ASCII by tm2's path rules, a route's symbol and a token's name (constants)
			panic("hlgov: " + path + " is registered for no token (it never announced the route it succeeds): a kind 6 names the token's own route")
		} else if sym != name {
			// echo-ok(path, sym, name, s, pred): a registered route's pkgpath, ASCII by tm2's path rules, a route's symbol and a token's name (constants)
			panic("hlgov: " + path + " is the " + sym + " route's, not " + name + "'s: a kind 6 names the token's own route")
		}
		if s := hlmailbox.Successor(path); s != "" {
			// echo-ok(path, sym, name, s, pred): a registered route's pkgpath, ASCII by tm2's path rules, a route's symbol and a token's name (constants)
			panic("hlgov: " + path + " migrated to " + s + ": never a migrated route, since its successor is the token's router")
		}
		// a successor that pulled nothing is never enrolled (R35): before page 0 its migration may still be
		// re-pointed (AR2) and the route dropped — a realm in nobody's chain that Ethereum would then enroll as the
		// router for the deployment's life, every burn's recipient, flaggable by nobody
		if pred := hlmailbox.Predecessor(path); pred != "" && !hlmailbox.Pulled(path) {
			// echo-ok(path, sym, name, s, pred): a registered route's pkgpath, ASCII by tm2's path rules, a route's symbol and a token's name (constants)
			panic("hlgov: " + path + " succeeds " + pred + " and has pulled nothing yet (page 0 is not in): a kind 6 enrolling it waits for its first page, since a migrate that pulled nothing may be re-pointed and Ethereum must never enroll a dropped route")
		}
		return path
	}
	params := append(append([]byte{token}, be32(domain)...), k[:]...)
	text := "enroll " + name + "'s router for domain " + strconv.FormatUint(uint64(domain), 10) + ": " + ea(k)
	mustFit(body(KindRouter, 0, params))
	if path := check(); path != "" {
		text += " (" + path + ")"
	}
	return propose(cur, "enroll router", text, func(cur realm, made, t int64) {
		mustBeLive()
		path := check()
		n := dispatch(cur, KindRouter, params, text, t)
		note := "dispatched as decision " + u64s(n)
		if path != "" {
			h := runtime.ChainHeight()
			hlmailbox.NoteEnrolled(cross(cur), path, h)
			sd := sent.Get(seqKey(n)).(*decision)
			sd.route, sd.height = path, h
			note += "; " + path + " noted enrolled on Ethereum at height " + itoa(h)
		}
		d := ledger.Record("enroll router", text, kindParams{kind: KindRouter, params: params}, made, t, t)
		ledger.Apply(d, t, note)
	})
}

// refusedDomain is why kind 6 refuses domain on the chain whose gno.land domain is local and whose launch origin
// (Ethereum's domain) is origin, or "" (the round-2 realms audit's E2-RL7): Ethereum's own domain — a token enrolled
// for its own chain would dispatch every burn there, which no relayer delivers and no signer counts — and gno.land's
// other domain (1198419796 on gnoland-1, 1198419788 on a test chain), which no client on Ethereum proves: either way a
// burn to it is final and credited nowhere. A domain of another chain takes any router; this chain's takes its route.
func refusedDomain(domain, local, origin uint32) string {
	d := strconv.FormatUint(uint64(domain), 10)
	if domain == origin {
		return "hlgov: domain " + d + " is Ethereum's own: a token enrolled for it would dispatch every burn there, which no relayer delivers and no signer counts — final and credited nowhere"
	}
	if domain != local && (domain == hlmailbox.DomainMainnet || domain == hlmailbox.DomainTest) {
		return "hlgov: domain " + d + " is gno.land's other domain (this chain's is " + strconv.FormatUint(uint64(local), 10) + "): no client on Ethereum proves it, so a burn to it would be final and credited nowhere"
	}
	return ""
}

// routePathOf is the pkgpath of the route realm hlmailbox registers whose address, as 32 bytes, is k; "" if none.
func routePathOf(k [32]byte) string {
	for _, path := range hlmailbox.Routes() {
		if b, err := hl.AddressToBytes32(chain.PackageAddress(path)); err == nil && b == k {
			return path
		}
	}
	return ""
}

// NewSetProtocolFeeRequest is kind 7: the mailbox's protocol fee and its beneficiary, `fee u64 ‖ beneficiary 32`
// (eth §13): the fee in wei, at most ProtocolFee's immutable maximum of 0.001 ETH (10^15 wei, maxProtocolFee; ER11),
// the beneficiary a non-zero Ethereum address (the treasurer's, eth §5.5), never a contract of the deployment
// (E2-RL7's class, the F6 review's follow-up: none of the sixteen takes ETH, so the hook's collectProtocolFees would
// revert, the fees held there until another kind 7) nor one of HlAdmin's line (hlAdminLine, checked when made and in
// the closure).
func NewSetProtocolFeeRequest(cur realm, wei uint64, beneficiary string) Request {
	if wei > maxProtocolFee {
		panic("hlgov: the fee is at most " + u64s(maxProtocolFee) + " wei (0.001 ETH), ProtocolFee's immutable maximum")
	}
	k := mustAddr(beneficiary)
	if k == ([32]byte{}) {
		panic("hlgov: the beneficiary is the zero address")
	}
	if name := hlmailbox.EthContractName(k); name != "" {
		// echo-ok(name): hlmailbox.EthContractName's: a contract's name, an ASCII constant
		panic("hlgov: the beneficiary " + ea(k) + " is " + name + " of this deployment, which takes no ETH: collectProtocolFees would revert, the fees held in the hook until another kind 7")
	}
	params := append(be64(wei), k[:]...)
	// nor one of HlAdmin's line (hlAdminLine), which moves with the swaps: checked when made and in the closure
	check := func() {
		if why := hlAdminLine(k); why != "" {
			// echo-ok(why): pastRecipient's words and EIP-55 addresses, a few hundred bytes at most: far under the cut
			panic("hlgov: the beneficiary " + ea(k) + " is " + why + ": the fees never go to an address decisions go to, or went to")
		}
	}
	return proposeKind(cur, KindProtocolFee, "set protocol fee", "set the protocol fee to "+u64s(wei)+" wei for "+ea(k), params, check)
}

// hlAdminLine is why k, a seat's key, kind 7's beneficiary or kind 8's implementation, is one of HlAdmin's line —
// every address decisions go to or went to: the decision recipient (HlAdmin, or a successor once a swap names it) and
// every former recipient, which stays former until a swap names it again, so for good in practice (a key a swap named
// by mistake too) — or "" (F9's reviews: the first's finding 5, the second's 2 and 4). The launch HlAdmin is also among
// hlmailbox's sixteen. The converse, a swap naming a seat's key, is the swap's (ethSeatOf).
func hlAdminLine(k [32]byte) string {
	if k == recipient {
		return "the decision recipient (HlAdmin's line)"
	}
	if isFormer(k) {
		return "a former decision recipient (HlAdmin's line)"
	}
	return ""
}

// NewUpgradeRequest is kind 8: upgrade one of the five contracts behind a proxy to a new implementation, `proxy 32 ‖
// implementation 32 ‖ codehash 32 ‖ call` (eth §13, with the red team's ER7), executed by HlAdmin as
// ProxyAdmin.upgrade(proxy, implementation) when call is empty and as upgradeAndCall(proxy, implementation, call)
// otherwise. The proxy is one of the five compiled proxies — the mailbox, token 0, token 1, the light client, the gate
// (hlmailbox's EthMailbox … EthGate) — and never anything else (HlAdmin spends any other; it is no proxy itself, so it
// can never be upgraded: a new HlAdmin is a successor by kind 9); the implementation a non-zero Ethereum address other
// than the proxy and every other contract of the deployment (E2-RL7: hlmailbox's sixteen) but the proxy's own launch
// implementation (Q-F6c: a rollback to the audited launch code, which stays deployed; the description says so — any
// other proxy's launch implementation is refused like the rest), nor one of HlAdmin's line (hlAdminLine, checked when
// made and in the closure); codehash the implementation's EXTCODEHASH, 64 hex digits, never zero nor the empty code's
// hash (mustHash) — at execution HlAdmin leaves the decision pending while the implementation holds no code (NoCodeYet)
// and spends it on another hash or an EIP-7702 designator, none of which a realm can read, so the proposer and hlwatch
// read the deployed code before the vote (eth §6.5, §13, plan e4); call the calldata in hex ("" or "0x" for none), at
// most 407 bytes (maxUpgradeCall: what the 512-byte body leaves), the rest of the body. Checked when the proposal is
// made and when it executes.
func NewUpgradeRequest(cur realm, proxy, implementation, codehash, call string) Request {
	p, impl := mustAddr(proxy), mustAddr(implementation)
	h := mustHash(codehash, "code hash")
	if impl == ([32]byte{}) || impl == p {
		panic("hlgov: the implementation must be set and differ from the proxy")
	}
	// an implementation is code deployed for the upgrade, never another contract of the deployment (E2-RL7): an
	// upgrade to one would run that contract's code behind the proxy. The proxy's own launch implementation is the
	// one exception (Q-F6c): a rollback to the audited launch code, which stays deployed
	rollback := impl == launchImplementation(p)
	if name := hlmailbox.EthContractName(impl); name != "" && !rollback {
		// echo-ok(name): hlmailbox.EthContractName's: a contract's name, an ASCII constant
		panic("hlgov: the implementation " + ea(impl) + " is " + name + " of this deployment: an upgrade names code deployed for it, or the proxy's own launch implementation (a rollback), never another of the deployment's sixteen contracts")
	}
	data := mustCall(call)
	check := func() {
		if proxyName(p) == "" {
			panic("hlgov: " + ea(p) + " is none of the five proxies kind 8 upgrades (the mailbox " + ea(proxies[0]) + ", token 0 " + ea(proxies[1]) + ", token 1 " + ea(proxies[2]) + ", the light client " + ea(proxies[3]) + ", the gate " + ea(proxies[4]) + "): HlAdmin would only spend the decision")
		}
		// nor one of HlAdmin's line, which moves with the swaps: HlAdmin's code behind a proxy runs it on that
		// contract's storage
		if why := hlAdminLine(impl); why != "" {
			// echo-ok(why): pastRecipient's words and EIP-55 addresses, a few hundred bytes at most: far under the cut
			panic("hlgov: the implementation " + ea(impl) + " is " + why + ": an address decisions go to, or went to, is never code behind a proxy")
		}
	}
	check()
	params := append(append(append(append([]byte{}, p[:]...), impl[:]...), h[:]...), data...)
	text := "upgrade " + proxyName(p) + " " + ea(p) + " to implementation " + ea(impl)
	if rollback {
		text += " (its launch implementation: a rollback)"
	}
	text += " with code hash 0x" + hx(h[:])
	if len(data) == 0 {
		text += ", no call (upgrade)"
	} else {
		text += ", then call 0x" + hx(data) + " (" + strconv.Itoa(len(data)) + " bytes, upgradeAndCall)"
	}
	return proposeKind(cur, KindUpgrade, "upgrade", text, params, check)
}

// launchImplementation is the launch implementation of the proxy at k, one of the five kind 8 may upgrade —
// hlmailbox's EthMailboxImpl … EthGateImpl, in the proxies' order (ethdeploy.gno) — or zeros for any other address.
func launchImplementation(k [32]byte) [32]byte {
	for i, p := range proxies {
		if p == k {
			return ethContract(hlmailbox.EthMailboxImpl + i)
		}
	}
	return [32]byte{}
}

// mustCall reads kind 8's call (ER7): hex, with or without 0x, "" or "0x" for none; at most maxUpgradeCall bytes,
// so the body stays within MaxDecisionBody.
func mustCall(s string) []byte {
	h := strings.TrimPrefix(strings.TrimSpace(s), "0x")
	if h == "" {
		return nil
	}
	b, err := hex.DecodeString(h)
	if err != nil {
		panic("hlgov: the call is calldata in hex (an even number of hex digits, with or without 0x), not " + echo(s))
	}
	if len(b) > maxUpgradeCall {
		panic("hlgov: the call is " + strconv.Itoa(len(b)) + " bytes; at most " + strconv.Itoa(maxUpgradeCall) + " fit in the " + strconv.Itoa(MaxDecisionBody) + "-byte decision body after the proxy, the implementation and the code hash")
	}
	return b
}

// NewTransferToSuccessorRequest is kind 9: hand every authority to a successor of HlAdmin, `successor 32 ‖ codehash 32`
// (eth §13) — never HlAdmin itself, the decision recipient this realm knows, checked when the proposal is made and
// again when it executes (a recipient swap landing in between would otherwise turn a pre-armed transfer into one
// HlAdmin only spends: it would hand every authority to itself, the Solana programs lane's S-15); never a former
// recipient (R29), a retired contract that has inherited already; and never another contract of the deployment (E2-RL7:
// hlmailbox's sixteen, none of which answers predecessor() with HlAdmin). At execution HlAdmin leaves the decision
// pending while the successor holds no code (NoCodeYet), and spends it unless the successor's code is no EIP-7702
// designator, its EXTCODEHASH equals codehash, its predecessor() is HlAdmin, it has not inherited, it names no
// successor of its own and its clock factor is HlAdmin's (eth §13, plan e4) — none of which a realm can read, so the
// proposer and hlwatch read them before the vote (eth §5.3: "immutable" or "MUTABLE: would be spent"). The code hash is
// 64 hex digits, never zero nor the empty code's hash (mustHash).
func NewTransferToSuccessorRequest(cur realm, successor, codehash string) Request {
	p := mustAddr(successor)
	h := mustHash(codehash, "code hash")
	if p == ([32]byte{}) {
		panic("hlgov: the successor must be set")
	}
	check := func() {
		if p == recipient {
			panic("hlgov: never HlAdmin itself (" + ea(p) + "), not the current HlAdmin as its own successor: HlAdmin would only spend the decision")
		}
		mustNotBeFormer(p)
		// no other contract of the deployment answers predecessor() with HlAdmin (M1-NOTES' F-7, E2-RL7): HlAdmin
		// would never execute the decision, pending for good or spent
		if name := hlmailbox.EthContractName(p); name != "" {
			// echo-ok(name): hlmailbox.EthContractName's: a contract's name, an ASCII constant
			panic("hlgov: the successor " + ea(p) + " is " + name + " of this deployment, which can never succeed HlAdmin (its predecessor() is not HlAdmin): HlAdmin would never execute the decision")
		}
	}
	params := append(append([]byte{}, p[:]...), h[:]...)
	return proposeKind(cur, KindSuccessor, "transfer to successor", "transfer every authority to "+ea(p)+" (code hash 0x"+hx(h[:])+")", params, check)
}

// routeMaximum is a token's per-message maximum on its gno.land route now (base units): what a send may carry out.
func routeMaximum(token uint8) uint64 {
	if token == 0 {
		return uint64(hlgnot.Maximum())
	}
	return uint64(hlbubble.Maximum())
}

// NewSetMintLimitsRequest is kind 12: a token's (0 GNOT, 1 BUBBLE) per-message maximum and rolling day cap on the
// gate, in base units (eth §13: token u8 ‖ maximum u64 ‖ day cap u64; set there at launch, eth §5.4, to 25,000 a
// message and a day cap of the smaller of 100,000 and a tenth of the expected lock, for each token). A maximum of 0
// (every mint refused) and a day cap below the maximum (a message at the maximum could never pass) are refused when
// the proposal is made: HlAdmin and the gate take any values, so the nonsense is caught here, as every builder
// catches it early. The maximum is never below the token's route's per-message maximum on gno.land (R15), checked
// when the proposal is made and when it executes: a send between the two would leave gno.land and be refused at
// delivery for good, stranded until a refund by id — so a raise is Ethereum's limit first (this), the route's maximum
// after, and a lowering the other way round. It lands 7 days after delivery.
func NewSetMintLimitsRequest(cur realm, token uint8, maximum, dayCap uint64) Request {
	name := parseToken(token)
	if maximum == 0 {
		panic("hlgov: the per-message maximum is at least 1")
	}
	if dayCap < maximum {
		panic("hlgov: the day cap is at least the per-message maximum")
	}
	check := func() {
		if rm := routeMaximum(token); maximum < rm {
			// echo-ok(name): GNOT or BUBBLE, a constant
			panic("hlgov: a per-message maximum of " + u64s(maximum) + " is below the " + name + " route's maximum of " + u64s(rm) + " (base units): a send between the two would strand on Ethereum; lower the route's maximum first, then this")
		}
	}
	params := append([]byte{token}, be64(maximum)...)
	params = append(params, be64(dayCap)...)
	text := "set " + name + "'s mint limits: " + u64s(maximum) + " a message, " + u64s(dayCap) + " a day (base units)"
	return proposeKind(cur, KindMintLimits, "set mint limits", text, params, check)
}

// mustRaise refuses a sync sequence that would not raise the counter (§5.3: it never lowers it): n is the number
// the next decision takes, so the counter becomes n-1, which must be above the counter now — and by at most 2^32,
// the realm's own bound (§13, R18; kind 10 is reserved and moves nothing on Ethereum), so a mistyped number can
// neither wrap the counter onto numbers HlAdmin recorded nor put every decision beyond the next number it expects.
func mustRaise(n uint64) {
	if n == last+1 {
		panic("hlgov: the next number is " + u64s(n) + " already")
	}
	if n <= last {
		panic("hlgov: sync sequence only ever raises the counter: it stands at " + u64s(last) + " (the next number is " + u64s(last+1) + ") and " + u64s(n) + " is not above it")
	}
	if n > last+1+syncReach {
		panic("hlgov: sync sequence reaches at most 2^32 above the next number (the realm's own bound): the next number is " + u64s(last+1) + " and " + u64s(n) + " is beyond " + u64s(last+1+syncReach))
	}
}

// NewSyncSequenceRequest raises this realm's counter so that the next decision takes number n — the number HlAdmin
// expects next after a foreign decision took one — at once; nothing moves on Ethereum. It only ever raises: a number
// at or below the next one is refused when the proposal is made and again when it executes (the counter may have
// moved in between), so a number HlAdmin recorded is never reused.
func NewSyncSequenceRequest(cur realm, n uint64) Request {
	mustBeLive()
	if n == 0 {
		panic("hlgov: the next number is at least 1")
	}
	mustRaise(n)
	text := "sync sequence: the next decision takes number " + u64s(n) + " (it was " + u64s(last+1) + ")"
	return propose(cur, "sync sequence", text, func(cur realm, made, t int64) {
		mustBeLive()
		mustRaise(n)
		last = n - 1
		d := ledger.Record("sync sequence", text, sequenceParams{n}, made, t, t)
		ledger.Apply(d, t, "at once")
		chain.Emit("SequenceSynced", "next", u64s(n))
	})
}

// NewSetDecisionRecipientRequest changes the contract decisions are addressed to, 7 days after the proposal executes:
// how a successor of HlAdmin receives them (eth §6.5: it follows a kind 9). It names the recipient it replaces (from)
// and the contract it sets, Ethereum addresses both, and holds only while from is the recipient (Q-F6b; as hlmailbox's
// queued rotations hold only while the set they were decided against stands): refused when the proposal is made and in
// its closure while another contract is the recipient, and at Execute, where it stays pending for GovDAO's cancel. So a
// swap pre-armed or left queued never applies while another swap's target stands in place of the recipient it was
// decided against — say after its own target served and handed over, when it would point decisions back at a retired
// HlAdmin. The rule reads the recipient as it is, not the swaps since: a stale swap whose recipient is later restored
// applies again (F9's review's finding 3), so GovDAO cancels a stale swap before a swap back restores the recipient it
// names. Never the current recipient, nor a contract of the deployment but HlAdmin (E2-RL7: hlmailbox's sixteen record
// no decision), nor a key that holds an Ethereum seat as last set here (ethSeatOf; refused when made, in the closure
// and at Execute, F9's second review). A former recipient may be named again (E2-RL2): a swap that lands while the kind
// 9 it follows still waits on Ethereum (one flag on HlAdmin defers it) left the old HlAdmin unreachable, frozen under
// that flag; a second swap back to it, made against the recipient that replaced it, lets GovDAO send it the unpause,
// and the kind 9, done, is followed again. A decision sent to a retired HlAdmin only reverts there. A swap back made
// and applied while the recipient it replaces stands is taken even when the kind 9 executed on Ethereum in between,
// which no realm sees: hlwatch's to alarm (Q-F6b's stale undo).
func NewSetDecisionRecipientRequest(cur realm, from, contract string) Request {
	mustBeLive()
	f, p := mustAddr(from), mustAddr(contract)
	if p == ([32]byte{}) {
		panic("hlgov: the recipient must be set")
	}
	check := func() {
		// decided against the recipient as it stands (Q-F6b): the swap names it, and holds only while it stands
		if f != recipient {
			panic("hlgov: the swap replaces " + ea(f) + ", but the decision recipient is " + ea(recipient) + ": a swap names the recipient it replaces, as it stands")
		}
		if p == recipient {
			panic("hlgov: the recipient must differ from the current one (" + ea(p) + ")")
		}
		// HlAdmin's line only (E2-RL7): the launch HlAdmin, which a swap back restores (E2-RL2), or a successor of
		// it; every other contract of the deployment records no decision, and a swap to one would cut HlAdmin off
		if name := hlmailbox.EthContractName(p); name != "" && p != ethContract(hlmailbox.EthHlAdmin) {
			// echo-ok(name): hlmailbox.EthContractName's: a contract's name, an ASCII constant
			panic("hlgov: " + ea(p) + " is " + name + " of this deployment, which records no decision: the recipient is HlAdmin or a successor of it")
		}
		// nor a guardian's or watch key's address (F9's second review, finding 3: the converse of HlAdmin's line)
		if n := ethSeatOf(p); n != 0 {
			panic("hlgov: " + ea(p) + " holds seat " + strconv.Itoa(n) + " as last set here, a guardian's or watch key's: the recipient is HlAdmin or a successor of it")
		}
	}
	check()
	text := "set decision recipient to " + ea(p) + ", replacing " + ea(f) + " (applies only while " + ea(f) + " is the recipient)"
	return propose(cur, "set decision recipient", text, func(cur realm, made, t int64) {
		mustBeLive()
		check()
		d := ledger.Record("set decision recipient", text, recipientParams{from: f, to: p}, made, t, t+decisions.Delay)
		d.Note = "queued 7 days; applies only while " + ea(f) + " is the recipient"
	})
}

// NewCancelQueuedRequest cancels a queued change of this realm (a recipient swap), at once; it is proposed before
// Launch too.
func NewCancelQueuedRequest(cur realm, id int64) Request {
	d := ledger.Get(id)
	if d == nil || !d.Pending() {
		panic("hlgov: decision " + itoa(id) + " is not pending")
	}
	text := "cancel queued change " + itoa(id) + " (" + d.Text + ")"
	return propose(cur, "cancel queued", text, func(cur realm, made, t int64) {
		if _, err := ledger.Cancel(id, t); err != nil {
			// echo-ok(err): p/decisions' refusal: ASCII words and numbers
			panic("hlgov: " + err.Error())
		}
		c := ledger.Record("cancel queued", text, cancelParams{id}, made, t, t)
		ledger.Apply(c, t, "at once: cancelled")
	})
}

// Execute applies a queued change (a recipient swap) whose 7 days have passed; anyone may call it. It applies only
// while the recipient the swap replaces is the recipient (Q-F6b): one that finds another there — a swap landed in
// between — is refused and stays pending for GovDAO's cancel (taken again only if that recipient is restored), and
// so never finds its target the recipient already, the target differing from the recipient it replaces. A target
// that was a recipient before is taken (E2-RL2). The recipient it replaces joins the former ones, and the target
// leaves them (retire).
func Execute(cur realm, id int64) {
	caller(cur)
	mustBeLive()
	t := now()
	d, err := ledger.Due(id, t)
	if err != nil {
		// echo-ok(err): p/decisions' refusal: ASCII words and numbers
		panic("hlgov: " + err.Error())
	}
	p, ok := d.Params.(recipientParams)
	if !ok {
		panic("hlgov: decision " + itoa(id) + " has nothing to apply")
	}
	if p.from != recipient {
		panic("hlgov: decision " + itoa(id) + " replaces " + ea(p.from) + ", and the decision recipient is " + ea(recipient) + " now: a swap applies only against the recipient it was decided against — cancel decision " + itoa(id) + " and propose the swap against the recipient as it stands")
	}
	// a kind 5 that seated the target after the swap was decided leaves it stale too: pending for GovDAO's cancel
	if n := ethSeatOf(p.to); n != 0 {
		panic("hlgov: decision " + itoa(id) + " names " + ea(p.to) + ", which holds seat " + strconv.Itoa(n) + " as last set here, a guardian's or watch key's: cancel decision " + itoa(id))
	}
	retire(recipient, p.to)
	recipient = p.to
	ledger.Apply(d, t, "applied after the delay")
	chain.Emit("RecipientSet", "contract", ea(recipient))
}

// --------------------------------------------------------------- pages --

// pageDecisions is how many of the newest decisions this realm's page lists (lastDecisions).
const pageDecisions = 20

// lastDecisions is the page's decisions: the newest pageDecisions, newest first, each read by its number (a ledger
// numbers 1, 2, … and removes none) and written as p/decisions' page writes a line, its kind first (the round-4
// realms audit's E4-RL2). p/decisions' Page walks the whole ledger to pick each kind's last hundred — a cost that
// grows with every decision this realm ever made — and hleth's audit reads this page (the Ethereum seats above), so
// the page is held to a bound (TestRenderGasGovPage); every decision is at "decisions", "decisions.json" and
// "decisions.json/<from>".
func lastDecisions() string {
	n := ledger.Last()
	if n == 0 {
		return "No decision yet: one exists only when a proposal made with a builder executes.\n"
	}
	var b strings.Builder
	for id := n; id > 0 && id > n-pageDecisions; id-- {
		d := ledger.Get(id)
		if d == nil {
			continue
		}
		// echo-ok(d, strings.TrimLeft): a decision's number, kind, state, text and note: this realm's builders write them from validated parameters and their own words
		b.WriteString("- #" + itoa(d.ID) + " " + d.Kind + ", " + d.State() + ": " + d.Text + " (recorded " + itoa(d.Recorded))
		if d.Queued() {
			b.WriteString(", due " + itoa(d.Due))
		}
		switch {
		case d.Applied != 0:
			b.WriteString(", applied " + itoa(d.Applied))
		case d.Cancelled != 0:
			b.WriteString(", cancelled " + itoa(d.Cancelled))
		}
		b.WriteString(")")
		if d.Note != "" {
			// echo-ok(d, strings.TrimLeft): a decision's number, kind, state, text and note: this realm's builders write them from validated parameters and their own words
			b.WriteString(" — " + d.Note)
		}
		b.WriteString("\n")
	}
	if n > pageDecisions {
		b.WriteString("- … and " + itoa(n-pageDecisions) + " before them: every decision at \"decisions\", \"decisions.json\" and \"decisions.json/<from>\"\n")
	}
	return b.String()
}

// Render: "" the page (its decisions the newest twenty, lastDecisions); "json"; "decisions" and "decisions.json" the
// ledger, and "decisions.json/<from>" a page of it (at most 20 decisions, hlmailbox.DecisionsJSONPage);
// "decision/<n>" one dispatched decision.
func Render(path string) string {
	switch {
	case path == "":
		var b strings.Builder
		b.WriteString("# GovDAO's decisions for Ethereum\n\n")
		b.WriteString("A decision made with one of this realm's builders — the governor's own until it yields, a GovDAO proposal's execution after — sends it to HlAdmin on Ethereum with the next sequence number; HlAdmin records it by number and executes it after its clock. Anyone may re-dispatch an executed decision until it is recorded.\n\n")
		if hlmailbox.Live() {
			b.WriteString("- **live** since " + itoa(hlmailbox.LaunchedAt()) + "\n")
		} else {
			b.WriteString("- **not launched**: no decision is sent before the Launch decision\n")
		}
		b.WriteString("- " + decisions.GovernorLine(launchGovernor, hlmailbox.Yielded()) + "\n")
		b.WriteString("- destination: domain " + strconv.FormatUint(uint64(destination), 10) + " (Ethereum), recipient `" + ea(recipient) + "` (HlAdmin)\n")
		if len(former) > 0 {
			parts := make([]string, len(former))
			for i, k := range former {
				parts[i] = "`" + ea(k) + "`"
			}
			// echo-ok(strings.Join, strings.TrimLeft): EIP-55 addresses (ea), quoted, or a decision's number
			b.WriteString("- former recipients (never a successor by kind 9; a swap may name one again): " + strings.Join(parts, ", ") + "\n")
		}
		b.WriteString("- the five proxies kind 8 alone upgrades (compiled, the deployment's for life):")
		for i, name := range proxyNames {
			if i > 0 {
				b.WriteString(";")
			}
			// echo-ok(name): a proxy's name, a constant
			b.WriteString(" " + name + " `" + ea(proxies[i]) + "`")
		}
		b.WriteString(" — the gate is also the only non-zero ISM kind 3 names; kinds 4, 10 and 11 are reserved and have no builder here\n")
		b.WriteString("- sequence: starts at " + u64s(launchSequence) + " (compiled: the predecessor's last number, 0 for a chain's first r/hlgov); last number " + u64s(last) + "; the next takes number " + u64s(last+1) + "; " + strconv.Itoa(sent.Size()) + " sent from here\n")
		b.WriteString("- " + decisions.Clocks() + " (the recipient swap's delay and every builder's deadline; Ethereum's clocks are HlAdmin's)\n")
		b.WriteString("- Ethereum seats as last set here:\n")
		for i := 1; i <= seatCount; i++ {
			kind := "guardian"
			if i > guardianSeats {
				kind = "watch key"
			}
			// echo-ok(kind): guardian or watch key, a constant
			b.WriteString("  - seat " + strconv.Itoa(i) + " (" + kind + "): `" + ea(ethSeats[i]) + "`\n")
		}
		if sent.Size() > 0 {
			b.WriteString("\n## Sent\n\n")
			sent.ReverseIterateByOffset(0, 20, func(k string, v any) bool {
				d := v.(*decision)
				// echo-ok(strings.Join, strings.TrimLeft, d): EIP-55 addresses (ea), quoted, or a decision's number; a decision's number, kind, state, text and note: this realm's builders write them from validated parameters and their own words
				b.WriteString("- #" + strings.TrimLeft(k, "0") + " kind " + strconv.Itoa(int(d.kind)) + ": " + d.text + " (sent " + strconv.Itoa(d.count) + "×, last id `" + d.id + "`)\n")
				return false
			})
		}
		b.WriteString("\n## Decisions\n\n" + lastDecisions())
		return b.String()
	case path == "json":
		fs := Former()
		for i := range fs {
			fs[i] = strconv.QuoteToASCII(fs[i])
		}
		// echo-ok(strings.Join, strings.TrimLeft): EIP-55 addresses (ea), quoted, or a decision's number
		return "{\"address\":" + strconv.QuoteToASCII(string(Address())) + ",\"live\":" + strconv.FormatBool(hlmailbox.Live()) +
			"," + decisions.GovernorJSON(launchGovernor, hlmailbox.Yielded()) +
			",\"destination\":" + strconv.FormatUint(uint64(destination), 10) + ",\"recipient\":" + strconv.QuoteToASCII(ea(recipient)) + ",\"former\":[" + strings.Join(fs, ",") + "]" +
			",\"proxies\":{\"mailbox\":" + strconv.QuoteToASCII(ea(proxies[0])) + ",\"token0\":" + strconv.QuoteToASCII(ea(proxies[1])) + ",\"token1\":" + strconv.QuoteToASCII(ea(proxies[2])) +
			",\"client\":" + strconv.QuoteToASCII(ea(proxies[3])) + ",\"gate\":" + strconv.QuoteToASCII(ea(proxies[4])) + "}" +
			",\"start\":" + u64s(launchSequence) + ",\"last\":" + u64s(last) + ",\"next\":" + u64s(last+1) + ",\"clocks\":" + decisions.ClocksJSON() + ",\"decisions\":" + itoa(ledger.Last()) + "}"
	case path == "decisions":
		return "# Decisions\n\n" + ledger.Page()
	case path == "decisions.json":
		return ledger.JSON()
	case strings.HasPrefix(path, "decisions.json/"):
		return hlmailbox.DecisionsJSONPage(&ledger, path[len("decisions.json/"):])
	case strings.HasPrefix(path, "decision/"):
		n, err := strconv.ParseUint(path[len("decision/"):], 10, 64)
		if err != nil {
			return "null"
		}
		v := sent.Get(seqKey(n))
		if v == nil {
			return "null"
		}
		d := v.(*decision)
		return "{\"number\":" + u64s(n) + ",\"kind\":" + strconv.Itoa(int(d.kind)) + ",\"body\":" + strconv.QuoteToASCII(hx(d.body)) +
			",\"id\":" + strconv.QuoteToASCII(d.id) + ",\"count\":" + strconv.Itoa(d.count) + ",\"at\":" + itoa(d.at) + ",\"text\":" + strconv.QuoteToASCII(d.text) + "}"
	}
	return "unknown path; see the page"
}
