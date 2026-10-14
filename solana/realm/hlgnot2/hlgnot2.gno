// Package hlgnot2 is the reference successor realm of the GNOT route (bridge/PLAN.md §5.3; the round-6 items AS5,
// AS6 and C384: "No successor realm exists yet"): r/hlgnot's code over the same state machine, with the route it
// succeeds compiled in, so a migration can run end to end — on the local chain the e2e stages, on devnet, and one
// day on mainnet, where a successor is deployed under a path of GovDAO's choosing and read in full before the vote
// (§8.3). It is a route realm like any other once its import has ended: the same sends, credits, claims, meters,
// builders and pages as r/hlgnot (hlgnot.gno says what each does; only what differs is said here).
//
// What differs. (1) It announces itself to the mailbox from its own init as a GNOT successor (R25, AQ5), so a
// migrate can name it. (2) Adopt, callable by anyone page by page as Claim is (AS6): it calls the predecessor's
// Pull and imports the page in the same call — page 0 taking the lock and the counters, the mailbox's launch time
// passed so the meters start empty (R34), importing cleared at the last page (AP4) — so the pair's sum never
// breaks for a block and no page is ever stranded on a driver's key. (3) Before its last page is in it takes no
// send (R35, F28: hlroute.MustSend), credits nothing (R32: hlroute.MustTake) and is no router Solana enrols (R35:
// kind 6 waits for page 0); a message addressed to the predecessor, or to a successor a re-point dropped, is its
// to take from then on (AQ1, R35). The predecessor's path is a compiled constant a member reads in the served
// source; the realm refuses to adopt from any other.
package hlgnot2

import (
	"chain"
	"chain/banker"
	"chain/runtime"
	"chain/runtime/unsafe"
	"encoding/hex"
	"strconv"
	"strings"
	"time"

	"gno.land/p/g1leu8d2vsplhehcfkjg50mwgdpxdkt8tztu95wr/decisions/v0"
	"gno.land/p/g1leu8d2vsplhehcfkjg50mwgdpxdkt8tztu95wr/hlroute/v0"
	hl "gno.land/p/g1leu8d2vsplhehcfkjg50mwgdpxdkt8tztu95wr/hyperlane/v0"
	"gno.land/r/g1leu8d2vsplhehcfkjg50mwgdpxdkt8tztu95wr/hlgnot"
	"gno.land/r/g1leu8d2vsplhehcfkjg50mwgdpxdkt8tztu95wr/hlmailbox"
	"gno.land/r/gov/dao"
)

const (
	namespace = "gno.land/r/g1leu8d2vsplhehcfkjg50mwgdpxdkt8tztu95wr/"
	pkgPath   = namespace + "hlgnot2" // must match gnomod.toml

	// predecessorPath is the route this realm succeeds: the one its Adopt pulls from, compiled in so a member reads
	// it in the served source (§8.3) and a migrate from any other route is a dead end this realm refuses.
	predecessorPath = namespace + "hlgnot"

	mailboxPath  = namespace + "hlmailbox"
	feesPath     = namespace + "hlfees"
	bubblePath   = namespace + "bubble"
	hlbubblePath = namespace + "hlbubble"
	hlgovPath    = namespace + "hlgov"

	gnot = int64(1_000_000) // one GNOT in ugnot

	// The launch values (§5.3, §5.5): the same as r/hlgnot's at this realm's deploy — the settings travel in no page
	// (a successor compiles the values current when it is deployed; GovDAO re-sets any that moved since by the
	// builders below). The router is the synthetic GNOT program on Solana, a PLACEHOLDER (the test route of the
	// vectors) until the setup step gives the program id; bridge/local/e2e.sh swaps it for its run's.
	launchFee      = 5 * gnot
	launchMinimum  = 10 * gnot
	launchFeeFloor = 2 * gnot
	launchCapFloor = 10_000 * gnot
	launchMaximum  = 25_000 * gnot
	launchRouter   = "F9xTYLGwB6RZ3uKmXhabKhwvzVUeGCfkd6WBth6C52CK" // sha256("hlgnot test route"), base58

	maxI64 = uint64(1<<63 - 1)
)

// launchGovernor is the governor (M1k): the mailbox's, compiled here too so a member reads it in this realm's
// served source; init refuses a deploy with another (a successor deployed after the yield compiles it all the
// same: the mailbox's Yielded is what closes Govern). bridge/local/e2e.sh swaps it for gnodev's test1.
const launchGovernor = address("g1dpkz6em0wejhymn0wf047h6lta047h6lvxn4gx")

var (
	st = hlroute.New(hlroute.Launch{Symbol: "GNOT", Fee: launchFee, Minimum: launchMinimum, FeeFloor: launchFeeFloor,
		CapFloor: launchCapFloor, Maximum: launchMaximum, Domain: hlmailbox.LaunchOrigin(), Router: mustKey(launchRouter),
		Now: time.Now().Unix()})

	// realms are the addresses a transfer may not name as its recipient on either side (§5.2): the predecessor
	// among them; every route the mailbox registers is one too (isRealm, R39).
	realms = []address{chain.PackageAddress(pkgPath), chain.PackageAddress(predecessorPath), chain.PackageAddress(mailboxPath),
		chain.PackageAddress(feesPath), chain.PackageAddress(bubblePath), chain.PackageAddress(hlbubblePath), chain.PackageAddress(hlgovPath)}
)

// init announces this realm to the mailbox as a possible successor of the GNOT route (R25, AQ5), from its own
// frame at deploy: what a migrate naming it requires, proving a realm at the path and its intent and nothing more.
// It refuses a deploy whose compiled governor is not the mailbox's (M1k) first.
func init(cur realm) {
	if launchGovernor != hlmailbox.Governor() {
		panic("hlgnot2: the compiled governor " + string(launchGovernor) + " is not the mailbox's " + string(hlmailbox.Governor()))
	}
	hlmailbox.AnnounceSuccessor(cross(cur), hlmailbox.SymbolGNOT)
}

func mustKey(s string) [32]byte {
	k, err := hl.ParseKey32(s)
	if err != nil {
		panic(err.Error())
	}
	return k
}

func now() int64 { return time.Now().Unix() }

func u32(n uint32) string { return strconv.FormatUint(uint64(n), 10) }
func itoa(n int64) string { return strconv.FormatInt(n, 10) }

func caller(cur realm) address {
	if !cur.IsCurrent() {
		panic("hlgnot2: that realm value is not live; pass your own cur")
	}
	return cur.Previous().Address()
}

func mustHex(s, what string) []byte {
	b, err := hex.DecodeString(strings.TrimPrefix(strings.TrimSpace(s), "0x"))
	if err != nil {
		panic("hlgnot2: the " + what + " is not hex")
	}
	return b
}

// isRealm is whether an address is a realm of the bridge: the seven compiled here, every route realm the mailbox
// registers (R39) and every one it dropped (AT6: a re-pointed-away successor, hlgnotx in this realm's tests, a
// route no more that nothing of the line can move coins out of).
func isRealm(a address) bool {
	for _, r := range realms {
		if r == a {
			return true
		}
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

func mustBeLive() {
	if !hlmailbox.Live() {
		panic("hlgnot2: not launched: nothing is sent, credited or paid before the Launch decision")
	}
}

func mustNotBePaused(fn string) {
	if hlmailbox.Paused(pkgPath, fn) {
		panic("hlgnot2: " + fn + " are paused by a guardian or a watch key")
	}
}

// mustNotBeDropped refuses this realm once the mailbox dropped it from the line (R35): the predecessor re-pointed
// its migration to another successor while this realm had pulled nothing, so it is a route no more; the line's
// last realm takes every message addressed to it once its import has ended.
func mustNotBeDropped() {
	if by := hlmailbox.Dropped(pkgPath); by != "" {
		panic("hlgnot2: a route no more: " + by + " re-pointed its migration to another successor; the line's last realm takes every message and send")
	}
}

// predecessor is the route the mailbox registers this realm as the successor of: "" until a migrate names it.
func predecessor() string { return hlmailbox.Predecessor(pkgPath) }

// syncLaunch empties the meters at the launch, once (§5.4); after page 0 the import has set LaunchSeen already
// (R34), so nothing is reset here.
func syncLaunch() {
	if la := hlmailbox.LaunchedAt(); la != st.LaunchSeen {
		st.ResetMeters(la)
		st.LaunchSeen = la
	}
}

// ------------------------------------------------------------- reading --

// Address is this realm's address: the holder of the lock once page 0 is in, and (as 32 bytes) the router the
// Solana program enrols once kind 6 names it.
func Address() address { return chain.PackageAddress(pkgPath) }

// Predecessor is the route this realm is built to succeed (compiled), whatever the mailbox registers.
func Predecessor() string { return predecessorPath }

// Locked is the lock: what backs synthetic GNOT on Solana, by the counters (sent − paid).
func Locked() int64 { return st.Lock }

// Balance is the ugnot this realm's account holds; it is the lock unless someone sent coins here outside a transfer.
func Balance() int64 { return banker.NewReadonlyBanker().GetCoin(Address(), "ugnot") }

// Router is the enrolled router on Solana, in hex.
func Router() string { return hex.EncodeToString(st.Router[:]) }

// Domain is the Solana domain this route sends to and takes from.
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

// Imported is how many of the predecessor's pages are in; Importing whether more are to come (from page 0 until
// the page that says no more follows).
func Imported() int   { return st.Imported() }
func Importing() bool { return st.Importing() }

// Migrated is this realm's own successor's pkgpath once a migration applied, "" before.
func Migrated() string { return st.MigratedTo }

// ---------------------------------------------------------------- adopt --

// Adopt pulls page `page` of the predecessor's migration and imports it, in one call (AS6, §5.3: "its Pull imports
// a page and takes its lock in the same call, so the pair's sum never breaks for a block"). Anyone may call it, as
// anyone may call Claim (the caller pays gas and collects the records' storage deposits as the pages free them,
// §5.5) — so a successor never waits on a driver's key, and the pages after page 0 are never stranded on a
// predecessor that refuses everything. The pages go in order from 0: page 0 moves the lock and the counters
// (under a standing payouts flag on the predecessor it waits for the unpause, like a sweep payment, R27) and
// empties the meters as of now with the mailbox's launch time taken as the meters' last emptying (R34); the last
// page clears importing (AP4), from when this realm sends and credits as a launch route does. Only the compiled
// predecessor, and only once a migrate decision there named this realm: its Pull refuses any other caller.
func Adopt(cur realm, page int) {
	caller(cur)
	mustBeLive()
	if pred := predecessor(); pred != predecessorPath {
		if pred == "" {
			panic("hlgnot2: no migrate decision of " + predecessorPath + " names this realm yet: nothing to adopt")
		}
		panic("hlgnot2: this realm succeeds " + predecessorPath + " by its source, and the mailbox registers it as " + pred + "'s successor: nothing to adopt")
	}
	if page != st.Imported() {
		panic("hlgnot2: page " + strconv.Itoa(st.Imported()) + " is the next to adopt, not " + strconv.Itoa(page))
	}
	if st.Imported() > 0 && !st.Importing() {
		panic("hlgnot2: the migration's last page is in already: nothing more to adopt")
	}
	snap := hlgnot.Pull(cross(cur), page)
	epoch, _ := hlmailbox.PayoutsEpoch(pkgPath)
	st.Import(now(), epoch, hlmailbox.LaunchedAt(), snap)
	chain.Emit("Adopted", "from", predecessorPath, "page", strconv.Itoa(page), "lock", itoa(snap.Lock), "claims", strconv.Itoa(len(snap.Claims)), "sweeps", strconv.Itoa(len(snap.Sweeps)), "sends", strconv.Itoa(len(snap.Sends)), "more", strconv.FormatBool(snap.More))
}

// ---------------------------------------------------------------- out --

// TransferRemote sends amount of GNOT to recipient on destination (a Solana address in base58, or 32 bytes in
// hex) and returns the message id in hex: hlgnot's rules, and none before the last page of the migration is in
// (R35, F28), since before page 0 nothing has moved and the migration may still be re-pointed, dropping this realm
// with the sender's coins in it.
func TransferRemote(cur realm, destination uint32, recipient string, amount int64) string {
	mustBeLive()
	mustNotBePaused(hlmailbox.FnSends)
	mustNotBeDropped()
	st.MustSend(predecessor(), env(cur)) // no send before the last page (R35, F28), none before kind 6 names this realm (AT7)
	if destination != st.Domain {
		panic("hlgnot2: no route to domain " + u32(destination))
	}
	st.CheckSend(amount)
	to, err := hl.ParseKey32(recipient)
	if err != nil {
		panic(err.Error())
	}
	if to == ([32]byte{}) {
		panic("hlgnot2: the recipient is all zeros")
	}
	if a, err := hl.Bytes32ToAddress(to); err == nil && isRealm(a) {
		panic("hlgnot2: the recipient is a realm of the bridge")
	}
	prev := cur.Previous()
	if !prev.IsUserCall() {
		panic("hlgnot2: a direct user call only")
	}
	want := amount + st.Fee
	sent := unsafe.OriginSend()
	if len(sent) != 1 || sent[0].Denom != "ugnot" || sent[0].Amount != want {
		panic("hlgnot2: send exactly " + itoa(want) + "ugnot (the amount plus the fee of " + itoa(st.Fee) + "), not " + sent.String())
	}
	runtime.AssertOriginCall() // a direct MsgCall, so the envelope landed here and nothing ran in between
	who := prev.Address()
	banker.NewBanker(banker.BankerTypeOriginSend, cur).SendCoins(Address(), chain.PackageAddress(feesPath), chain.Coins{{"ugnot", st.Fee}})
	id := hlmailbox.Dispatch(cross(cur), destination, st.Router, hl.TokenMessage(to, uint64(amount), nil))
	idHex := hex.EncodeToString(id[:])
	st.Send(now(), idHex, who, amount)
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

// Process takes a token message from Solana and credits a claim to its recipient (hlgnot's rules): nothing before
// the last page of the migration is in, a message addressed to this realm included (R32); from then on one
// addressed to the predecessor, or to a successor a re-point dropped, is this realm's to take (AQ1, R35) — the
// mailbox hands it over, this realm credits it.
func Process(cur realm, metadata, message string) {
	caller(cur)
	mustBeLive()
	mustNotBePaused(hlmailbox.FnCredits)
	mustNotBeDropped()
	msg := mustHex(message, "message")
	m, err := hl.ParseMessage(msg)
	if err != nil {
		panic(err.Error())
	}
	if st.Migrated() {
		panic("hlgnot2: migrated to " + st.MigratedTo + ": the successor takes this message")
	}
	if self, err := hl.AddressToBytes32(Address()); err != nil {
		panic(err.Error())
	} else {
		st.MustTake(m.Recipient == self, predecessor())
	}
	t := now()
	if m.Origin != st.Domain || !st.RouterAccepts(t, m.Sender) {
		panic("hlgnot2: the message is not from the router enrolled for domain " + u32(st.Domain))
	}
	rcpt, amount, _, err := hl.ParseTokenMessage(m.Body)
	if err != nil {
		panic(err.Error())
	}
	if amount > maxI64 {
		panic("hlgnot2: the amount is past what GNOT can hold")
	}
	origin, sender, body := hlmailbox.Process(cross(cur), mustHex(metadata, "metadata"), msg)
	if origin != m.Origin || sender != m.Sender || string(body) != string(m.Body) {
		panic("hlgnot2: the mailbox read the message differently") // cannot happen: the same bytes, the same parser
	}
	to, err := hl.Bytes32ToAddress(rcpt)
	recipient := string(to)
	if err != nil {
		recipient = hex.EncodeToString(rcpt[:])
	}
	idb := hl.ID(msg)
	id := hex.EncodeToString(idb[:])
	epoch, _ := hlmailbox.PayoutsEpoch(pkgPath)
	swept := st.Credit(t, runtime.ChainHeight(), epoch, id, to, int64(amount), hlroute.BySigners, err != nil || isRealm(to))
	chain.Emit("Credited",
		"origin", u32(origin),
		"recipient", recipient,
		"amount", itoa(int64(amount)),
		"id", id,
		"swept", strconv.FormatBool(swept),
	)
}

// Claim pays up to n claims (n ≤ 20) taking recipients in turns under the meters, the fee as each claim's first
// installment. Anyone may call it; the relayers do. It returns how many claims it paid something on.
func Claim(cur realm, n int) int {
	caller(cur)
	mustBeLive()
	mustNotBePaused(hlmailbox.FnPayouts)
	mustNotBeDropped()
	syncLaunch()
	epoch, at := hlmailbox.PayoutsEpoch(pkgPath)
	paid := st.Claim(now(), n, epoch, at, env(cur))
	chain.Emit("Claimed", "count", strconv.Itoa(paid))
	return paid
}

func pay(cur realm, to address, amount int64) {
	banker.NewBanker(banker.BankerTypeRealmSend, cur).SendCoins(Address(), to, chain.Coins{{"ugnot", amount}})
}

// env is what the state machine asks of this realm.
func env(cur realm) hlroute.Env {
	return hlroute.Env{
		Flagged:      func(id string, height int64, verifier bool) bool { return hlmailbox.Flagged(pkgPath, id, height, verifier) },
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

// The builders (§5.3): hlgnot's, over this realm's state — the governor's decisions through Govern until it
// yields on r/hlmailbox, GovDAO proposals' after (M1k; hlgnot's Request and Govern say how). A refund names the
// sends the pages brought (AQ2), 14 days after kind 6 enrolled this realm (AR1, R37); a migrate on waits for this
// realm's own import to end (R31).

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
			panic("hlgnot2: only " + decisions.DAOPath + " executes a decision, not " + who(prev.PkgPath(), prev.Address()))
		}
		return
	}
	if !prev.IsUser() || prev.Address() != launchGovernor {
		panic("hlgnot2: until the governor yields, " + string(launchGovernor) + " alone decides, by Govern from its own key: not " + who(prev.PkgPath(), prev.Address()) + " (a GovDAO proposal is refused until the yield)")
	}
}

func propose(cur realm, d hlroute.Draft) Request {
	if !cur.IsCurrent() {
		panic("hlgnot2: that realm value is not live; pass your own cur")
	}
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
			mustNotBePaused(hlmailbox.FnPayouts) // a refund moves coins out of the lock: under a standing payouts flag it waits like a claim (§5.9; the proposal survives for a retry)
		}
		dec := st.Decide(made, t, d, env(cur))
		chain.Emit("Decision", "id", itoa(dec.ID), "kind", dec.Kind, "state", dec.State())
		return nil
	}
	return Request{proposal: dao.NewProposalRequest("hlgnot2: "+d.Kind, d.Text, dao.NewSimpleExecutor(0, cur, cb, d.Text)), text: d.Text, decide: cb}
}

// Govern is the governor's way in before the yield (M1k): the same closure GovDAO's proxy runs after it, called
// non-crossing in this frame so it reads the governor from cur.Previous(); refused for anyone else and for
// everyone once the governor yielded on r/hlmailbox (or when none was compiled); see hlgnot's.
func Govern(cur realm, r Request) {
	if !cur.IsCurrent() {
		panic("hlgnot2: that realm value is not live; pass your own cur")
	}
	if r.decide == nil {
		panic("hlgnot2: not a builder's request")
	}
	if yielded := hlmailbox.Yielded(); decisions.DAOGoverns(launchGovernor, yielded) {
		if launchGovernor == "" {
			panic("hlgnot2: no governor was compiled, " + decisions.DAOPath + " governs from the first block: a decision is a GovDAO proposal, never Govern")
		}
		panic("hlgnot2: the governor yielded at height " + itoa(yielded) + ", " + decisions.DAOPath + " governs since: a decision is a GovDAO proposal, never Govern")
	}
	mustBeGovernor(cur)
	if err := r.decide(cur); err != nil {
		panic("hlgnot2: " + err.Error())
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

func NewSetRouterRequest(cur realm, router string) Request {
	mustBeLive()
	return propose(cur, st.DraftRouter(mustKey(router), router))
}

func NewVoidRequest(cur realm, ids string, forged bool, unpauseFns, removeSeats string) Request {
	mustBeLive()
	return propose(cur, st.DraftVoid(ids, forged, unpauseFns, removeSeats, runtime.ChainHeight(), hlmailbox.Seat))
}

// NewRestoreRequest restores voided claims, voided sweep entries and written-off sends (R38) by message id.
func NewRestoreRequest(cur realm, ids string) Request {
	mustBeLive()
	return propose(cur, st.DraftRestore(ids))
}

func NewCreditRequest(cur realm, id string, to address, amount int64) Request {
	mustBeLive()
	if !to.IsValid() || isRealm(to) {
		panic("hlgnot2: the recipient must be an address outside the bridge's realms")
	}
	return propose(cur, st.DraftCredit(id, to, amount, hlmailbox.Delivered(id) != 0))
}

func NewSweepRequest(cur realm, ids string, to address) Request {
	mustBeLive()
	if !to.IsValid() || isRealm(to) {
		panic("hlgnot2: the recipient must be an address outside the bridge's realms")
	}
	return propose(cur, st.DraftSweep(ids, to))
}

// NewRefundRequest queues a refund of the sends the pages brought, by message id, to their senders, 7 days: the
// cure for sends the migration stranded undelivered — once kind 6 has enrolled this realm on Solana for 14 days by
// r/hlgov's note with the mailbox (AR1, R37), checked when the proposal is made and when it applies. After the 7
// days GovDAO applies it by NewApplyRefundRequest (AT2), never anyone by Execute.
func NewRefundRequest(cur realm, ids string) Request {
	mustBeLive()
	return propose(cur, st.DraftRefund(ids, now(), env(cur)))
}

// NewApplyRefundRequest applies a queued refund decision whose 7 days have passed, at once (AT2): GovDAO's own act,
// the member having read hlwatch's gate first — the token's router on Solana is this realm and the kind 6 is
// executed there — since this realm cannot see Solana, and a refund applied while the predecessor is still the
// router lets the refunded sender deliver his send there after it (minted twice, AR1); Execute refuses a refund.
// Under a standing payouts flag it waits for the unpause like a claim; see hlgnot's.
func NewApplyRefundRequest(cur realm, id int64) Request {
	mustBeLive()
	return propose(cur, st.DraftApplyRefund(id, now(), env(cur)))
}

func NewWriteOffRequest(cur realm, ids string) Request {
	mustBeLive()
	return propose(cur, st.DraftWriteOff(ids))
}

// NewMigrateRequest hands this realm's lock, claims and send records on to a successor of its own after 7 days —
// one announced for the GNOT route (R25, AQ5), no route already (R31), and only once this realm's own import has
// ended (R31): a successor's successor is the line's next hop, which the tools follow sixteen deep.
func NewMigrateRequest(cur realm, to string) Request {
	mustBeLive()
	return propose(cur, st.DraftMigrate(to, pkgPath, env(cur)))
}

func NewCancelRequest(cur realm, id int64) Request {
	return propose(cur, st.DraftCancel(id))
}

// Execute applies a queued decision whose 7 days have passed; a sweep payment waits under a standing payouts flag
// like a claim. A refund is never Execute's (AT2): the state refuses the kind, and GovDAO applies it by
// NewApplyRefundRequest after hlwatch's Solana check.
func Execute(cur realm, id int64) {
	caller(cur)
	mustBeLive()
	if d := st.Ledger.Get(id); d != nil && d.Kind == hlroute.KindSweep {
		mustNotBePaused(hlmailbox.FnPayouts)
	}
	dec := st.Execute(now(), id, env(cur))
	chain.Emit("Decision", "id", itoa(dec.ID), "kind", dec.Kind, "state", dec.State())
}

// Pull is this realm's own successor's, once a migrate decision names it: hlgnot's rules — page by page from 0,
// the lock paid with page 0 (under a standing payouts flag it waits), page 0 noted pulled with the mailbox (R35).
func Pull(cur realm, page int) *hlroute.Snapshot {
	if !cur.IsCurrent() {
		panic("hlgnot2: that realm value is not live; pass your own cur")
	}
	path := cur.Previous().PkgPath()
	if st.MigratedTo == "" || path != st.MigratedTo {
		panic("hlgnot2: only the successor named by a migrate decision pulls")
	}
	if page == 0 {
		mustNotBePaused(hlmailbox.FnPayouts)
	}
	snap := st.Export(page, hlroute.PullPage)
	if page == 0 {
		hlmailbox.NotePulled(cross(cur), path)
	}
	if snap.Lock > 0 {
		pay(cur, chain.PackageAddress(path), snap.Lock)
	}
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

// successionLine says where the migration stands, for the page.
func successionLine() string {
	switch pred := predecessor(); {
	case pred == "":
		return "- succeeds `" + predecessorPath + "` (compiled; announced to the mailbox): no migrate decision names this realm yet\n"
	case st.Imported() == 0:
		return "- succeeds `" + pred + "`: migrate applied, page 0 not adopted yet (anyone may call Adopt(0)); nothing is sent or credited before the last page\n"
	case st.Importing():
		return "- succeeds `" + pred + "`: " + strconv.Itoa(st.Imported()) + " pages adopted, more to come (anyone may call Adopt(" + strconv.Itoa(st.Imported()) + ")); nothing is sent or credited before the last page\n"
	}
	return "- succeeds `" + predecessor() + "`: the migration is complete (" + strconv.Itoa(st.Imported()) + " pages adopted); a route like any other\n"
}

// Render: hlgnot's paths — "" the route's page, "decisions", "json", "decisions.json", "claim/<id>", "send/<id>",
// "claims/<from>", "sweeps/<from>", "refunds/<from>" — the page adding where the migration stands, the json its
// predecessor (the compiled one; "importing" and "imported" say where the pages stand).
func Render(path string) string {
	epoch, at := hlmailbox.PayoutsEpoch(pkgPath)
	switch {
	case strings.HasPrefix(path, "claims/"):
		return st.ClaimsPage(parseFrom(path[len("claims/"):]))
	case strings.HasPrefix(path, "sweeps/"):
		return st.SweepsPage(parseFrom(path[len("sweeps/"):]))
	case strings.HasPrefix(path, "send/"):
		return st.SendJSON(path[len("send/"):])
	case strings.HasPrefix(path, "refunds/"):
		return st.RefundsPage(parseFrom(path[len("refunds/"):]))
	case path == "":
		var b strings.Builder
		b.WriteString("# GNOT across chains (successor)\n\n")
		b.WriteString("The bridge's GNOT route on gno.land, succeeding `" + predecessorPath + "`: GNOT sent to Solana is locked here, and the synthetic GNOT there is backed by it one to one; a burn there is credited here as a claim, paid under the meters. Every setting moves only by a decision made with this realm's builders — the governor's own until it yields, a GovDAO proposal after.\n\n")
		if hlmailbox.Live() {
			b.WriteString("- **live** since " + itoa(hlmailbox.LaunchedAt()) + "\n")
		} else {
			b.WriteString("- **not launched**: the values below are the compiled launch values, waiting for the Launch decision\n")
		}
		b.WriteString("- " + decisions.GovernorLine(launchGovernor, hlmailbox.Yielded()) + "\n")
		b.WriteString(successionLine())
		b.WriteString("- balance: " + st.Amount(Balance()) + "\n")
		b.WriteString(st.Figures(now(), epoch, at, hlmailbox.LaunchedAt(), pausedText()))
		b.WriteString("\n## Decisions\n\n" + st.Ledger.Page())
		return b.String()
	case path == "decisions":
		return "# Decisions\n\n" + st.Ledger.Page()
	case path == "json":
		return "{\"address\":" + strconv.Quote(string(Address())) + ",\"live\":" + strconv.FormatBool(hlmailbox.Live()) +
			"," + decisions.GovernorJSON(launchGovernor, hlmailbox.Yielded()) +
			",\"predecessor\":" + strconv.Quote(predecessorPath) +
			",\"balance\":" + itoa(Balance()) + ",\"paused\":" + strconv.Quote(pausedText()) + "," + st.JSON(now(), epoch, at, hlmailbox.LaunchedAt()) + "}"
	case path == "decisions.json":
		return st.Ledger.JSON()
	case strings.HasPrefix(path, "claim/"):
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
