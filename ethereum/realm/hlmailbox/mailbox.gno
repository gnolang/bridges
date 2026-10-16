// Package hlmailbox is the gno.land end of the Ethereum bridge's message layer (eth/bridge/PLAN.md §5.1: the Solana
// bridge's mailbox, carried to Ethereum): realms send messages to other chains through it and take the messages other
// chains send them from it. Messages, ids and the merkle tree are Hyperlane's byte for byte (gno.land/p/…/hyperlane/v0),
// so the Ethereum side runs Hyperlane's own contracts. This is the bridge's second deployment, under the operator's
// namespace at eth/, sharing nothing with the Solana deployment's realms but the three pure packages (eth §4). A
// section number alone (§5.3) is the Solana plan's (bridge/PLAN.md), which the Ethereum plan carries over with its §0
// substitutions wherever it does not change a rule; "eth §x" is the Ethereum plan's own.
//
// Out. Dispatch takes a message from the realm that calls it, numbers it, puts its id in the merkle tree, keeps the
// message, and emits the Dispatch event the light client on Ethereum (GnoLightClient) proves from LastResultsHash
// (§13: id, nonce, origin, destination, sender, recipient, hash; eth §6.2). Reemit emits it again from a fresh block,
// by anyone.
//
// In. A recipient realm pulls its own messages: it calls Process with the message and the verifier's metadata;
// Process checks the caller is the recipient, that the message is for this chain and was never delivered, marks it
// delivered, and only then asks the active verifier — a realm that registered itself here, chosen by GovDAO by
// pkgpath — whether the message is genuine; a verifier's error is a panic, so the transaction and the mark revert
// together and a refused message stays deliverable.
//
// Who may change what. Nobody holds an admin key: every setting moves by a decision made with this realm's
// builders (gov.gno), at once or after 7 days (§5.3), and nothing but Launch and cancel before the one-shot
// Launch. Who decides is the governor's (M1k): the compiled launchGovernor alone, by its own call through Govern,
// until it yields (Yield, once, for every bridge realm at once), and GovDAO's proposals from then on — or from the
// first block, when no governor was compiled. The guardians and watch keys (flags.gno) can only pause: three flags
// per key per route — sends, credits, payouts — which the routes ask about with Paused. Every launch value is
// compiled in below and written by init.
package hlmailbox

import (
	"chain"
	"chain/runtime"
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"

	"gno.land/p/g1leu8d2vsplhehcfkjg50mwgdpxdkt8tztu95wr/decisions/v0"
	hl "gno.land/p/g1leu8d2vsplhehcfkjg50mwgdpxdkt8tztu95wr/hyperlane/v0"
	"gno.land/p/nt/avl/v0"
)

const (
	namespace = "gno.land/r/g1leu8d2vsplhehcfkjg50mwgdpxdkt8tztu95wr/eth/"
	pkgPath   = namespace + "hlmailbox" // must match gnomod.toml

	// DomainMainnet and DomainTest are gno.land's Hyperlane domain ids, "GnoL" and "GnoT" in ASCII; nobody in the
	// Hyperlane registry holds either (2026-10-10). gnoland-1 is mainnet; every other chain is a test chain.
	DomainMainnet  = uint32(1198419788)
	DomainTest     = uint32(1198419796)
	mainnetChainID = "gnoland-1"

	// EthereumMainnet is the destination's domain on gnoland-1: Ethereum's own Hyperlane domain, its chain id 1 (eth
	// §0, §2: hyperlane-registry). On any other gno chain it is testEthereumDomain, below.
	EthereumMainnet = uint32(1)

	// The launch values (§5.3): the active verifier, the signers and their rule, the routes. The signer keys below
	// are PLACEHOLDERS (the three test signers of the vectors) until the operator's three machines exist; the
	// launch vote reads the real ones in the deployed source. The e2e swaps them for its run's keys; on gnoland-1 init
	// refuses them, as every dev tree launch value (mainnet.gno).
	launchVerifier    = namespace + "hlverifier"
	launchThreshold   = 2
	launchSigners     = "ec4ff84bc5faf45e487f9026f51b72df9f75de612614948c9bb8ed21f7d5d593,44204958a13ca0984fb9bbfb8beecdba35f2d1930a86f551e76669c1c394f65d,9082be72255bbb5af3906569c52055c5dedfce85623f10d12226b4ffff6f1839"
	launchRouteGNOT   = namespace + "hlgnot"
	launchRouteBUBBLE = namespace + "hlbubble"
	hlgovPath         = namespace + "hlgov" // r/hlgov: the one realm that notes a route's enrolment on Ethereum (NoteEnrolled, AR1)

	maxSigners = 255 // a signature names its signer in one byte

	// MaxBody bounds the body Dispatch carries (the round-3 realms audit's E3-RL1): a longer one is refused before
	// anything is stored or emitted. Dispatch is open to anyone, and the body a stranger sizes is what msg/<nonce>
	// renders, the page every machine of the bridge reads nonce by nonce: a 50 KB body put it past vm/qrender's
	// 3,000,000,000 gas, stopping each walk at that nonce for good. The bridge's own bodies are far below — a route's
	// token message is 64 bytes (p/hyperlane's TokenMessage: the recipient and the amount, no metadata), r/hlgov's
	// decision at most 512 (its MaxDecisionBody, the 9-byte kind and number included) — and at the bound the page
	// and a msgs/ page of twenty such stay far inside a query's gas (render.gno; eth/scripts/gnotest.sh's render-gas
	// gate holds them).
	MaxBody = 2048
)

// testEthereumDomain is the destination's domain on every gno chain but gnoland-1 (eth §5.1): 31337 here, anvil's
// default chain id, which the local run's mailbox takes as its domain; the stage step of a Sepolia deployment
// rewrites it to 11155111, Sepolia's. Its own line, so one sed on `^const testEthereumDomain = uint32(…)` finds it.
const testEthereumDomain = uint32(31337)

// launchGovernor is the governor (M1k, §5.3): the one address whose own direct call decides — through Govern, the
// same closures, checks, queues and delays a GovDAO proposal's execution runs — until it yields (Yield), from
// which block GovDAO's proposals decide and nothing else; "" means GovDAO from the first block, which init refuses
// on gnoland-1, where the launch is the governor's (launchFault, E3-RL2). Every bridge realm compiles the same
// address and refuses to deploy with another (its init reads Governor here), and reads Yielded here, so no realm is
// ever governed apart from the rest. A PLACEHOLDER (testutils.TestAddress("hl-governor")) until the launch notice
// names the real key; the e2e swaps it for gnodev's test1 in all six realms.
// Its own line, so one sed on `^const launchGovernor = address(…)` finds it in every realm. On gnoland-1 init refuses
// the placeholder (mainnet.gno), and every other realm refuses a governor that is not this one.
const launchGovernor = address("g1dpkz6em0wejhymn0wf047h6lta047h6lvxn4gx")

var (
	localDomain  = domainOf(runtime.ChainID())
	launchOrigin = ethereumOf(runtime.ChainID())
	// hook is this realm's address as Hyperlane carries it: the merkle tree hook id inside every checkpoint signed
	// for a message from here.
	hook = mustBytes32(chain.PackageAddress(pkgPath))

	tree hl.Tree
	sent avl.Tree // nonceKey(nonce) → *sentMsg
	done avl.Tree // message id in hex → the block height it was delivered at

	verifiers avl.Tree // pkgpath → Verifier, every realm that registered
	active    avl.Tree // domainKey(origin) → the pkgpath of the verifier GovDAO chose for that origin
	// verifierAt is every realm ever made an origin's active verifier — the launch verifier, and each one a set
	// verifier decision activated since — by address (string) → pkgpath: realms that move no coins, which the routes
	// refuse as a send's recipient, sweep at a credit and refuse as a vote's payee (IsVerifier, E4-RL5)
	verifierAt avl.Tree

	signerKeys []Key // the one signer set, in the order signatures name them
	signerRule Rule

	live       bool  // after the Launch decision executed
	launchedAt int64 // unix time it did

	yielded int64 // the block height the governor yielded at (Yield); 0 until then — every bridge realm reads it

	ledger decisions.Ledger

	// guardedOn is the chain id init's guard checked the launch values at (mustNotBeDevTree): the suite reads it
	// (TestInitGuarded), so a tree whose init lost the call — the one line between the dev tree's public test keys and
	// a deploy counted as gnoland-1 — fails the suite, where before only a hand-run stage control saw it (E3-RL7).
	guardedOn string
)

// sentMsg is one dispatched message and the checkpoint it made.
type sentMsg struct {
	raw    string   // the message as it went out
	root   [32]byte // the tree's root right after its id went in, at index = its nonce
	height int64
}

// Key is a signer's ed25519 public key.
type Key [32]byte

// Rule is how many signers must sign: Threshold of the set, or — when both groups are named (§11.13) — one key of
// GroupA and one of GroupB. The verifier applies it; Satisfiable says whether a set can still meet it.
type Rule struct {
	Threshold int
	GroupA    []Key
	GroupB    []Key
}

// TwoGroups is whether the rule is "one of A and one of B" rather than a threshold.
func (r Rule) TwoGroups() bool { return len(r.GroupA) > 0 && len(r.GroupB) > 0 }

// String says the rule in words.
func (r Rule) String() string {
	if r.TwoGroups() {
		return "one of group A (" + keysHex(r.GroupA) + ") and one of group B (" + keysHex(r.GroupB) + ")"
	}
	return strconv.Itoa(r.Threshold) + " of the set"
}

func (r Rule) equal(o Rule) bool {
	return r.Threshold == o.Threshold && sameKeys(r.GroupA, o.GroupA) && sameKeys(r.GroupB, o.GroupB)
}

// sameAs is whether two rules are the same rule, the groups compared as sets.
func (r Rule) sameAs(o Rule) bool {
	return r.Threshold == o.Threshold && sameSet(r.GroupA, o.GroupA) && sameSet(r.GroupB, o.GroupB)
}

// Verifier decides whether an incoming message is genuine (§5.1). A verifier is a realm; it registers once with
// RegisterVerifier and GovDAO activates it by pkgpath. Both methods are non-crossing. An implementation is a type
// declared in the registering realm: its methods then run under that realm's authority (borrow rule #1) and can
// write nothing of this realm's. A type declared in /p/ with no object to anchor on (`type V int`) would run under
// this realm's own authority instead — the no-anchor case — which RegisterVerifier cannot tell apart, so GovDAO
// activates a verifier whose source it has read; what the mailbox hands a verifier is the recipient's bytes
// (Verify) or a copy of its own keys (Satisfiable), never a value it keeps.
//
// A verifier refuses by a returned error, never by a panic (the round-5 realms audit's E5-RL4): Process quotes a
// returned refusal (echo: ASCII, at most 64 bytes of it), but a panic in Verify is the verifier realm's own — its
// method runs under that realm, so the panic crosses a realm boundary on its way out and aborts the transaction as
// it was written (interrealm v2 §9: only revive, a test builtin, catches it), and this realm can neither catch nor
// quote it. A verifier that panicked with the metadata it could not parse would put the caller's bytes into the
// block's results (E4-GS1: one such failed Process a block, from anyone, makes the block unprovable to Ethereum).
// GovDAO reads this in the verifier's source before it votes to activate it; NewSetVerifierRequest's description
// says it.
type Verifier interface {
	Verify(origin uint32, metadata, message []byte) error
	Satisfiable(signers []Key) bool
}

func domainOf(chainID string) uint32 {
	if chainID == mainnetChainID {
		return DomainMainnet
	}
	return DomainTest
}

// ethereumOf is the destination's Hyperlane domain for a gno chain: Ethereum's on gnoland-1, the compiled test
// domain on every other chain (eth §5.1).
func ethereumOf(chainID string) uint32 {
	if chainID == mainnetChainID {
		return EthereumMainnet
	}
	return testEthereumDomain
}

func mustBytes32(a address) [32]byte {
	b, err := hl.AddressToBytes32(a)
	if err != nil {
		panic(echo(err.Error())) // p/hyperlane's refusal names the address as it came
	}
	return b
}

func caller(cur realm) address {
	if !cur.IsCurrent() {
		panic("hlmailbox: that realm value is not live; pass your own cur")
	}
	return cur.Previous().Address()
}

func u32(n uint32) string { return strconv.FormatUint(uint64(n), 10) }

func hx(b []byte) string { return hex.EncodeToString(b) }

func keysHex(ks []Key) string {
	s := make([]string, len(ks))
	for i, k := range ks {
		s[i] = hx(k[:])
	}
	return strings.Join(s, ",")
}

// nonceKey sorts nonces by number.
func nonceKey(n uint32) string {
	s := u32(n)
	return strings.Repeat("0", 10-len(s)) + s
}

func domainKey(d uint32) string { return nonceKey(d) }

func init() {
	guardedOn = mustNotBeDevTree() // on gnoland-1 no launch value may still be the dev tree's, and on every chain the set must be one a launch can run (mainnet.gno)
	signerKeys = parseKeys(launchSigners)
	signerRule = Rule{Threshold: launchThreshold}
	active.Set(domainKey(launchOrigin), launchVerifier)
	verifierAt.Set(string(chain.PackageAddress(launchVerifier)), launchVerifier)
	initSeats()
	routes.Set(launchRouteGNOT, &routeRec{})
	routes.Set(launchRouteBUBBLE, &routeRec{})
}

// ------------------------------------------------------------- reading --

// LocalDomain is this chain's Hyperlane domain id.
func LocalDomain() uint32 { return localDomain }

// LaunchOrigin is the Ethereum domain the launch values name: 1 on gnoland-1, the compiled test domain elsewhere.
func LaunchOrigin() uint32 { return launchOrigin }

// Address is this realm's address.
func Address() address { return chain.PackageAddress(pkgPath) }

// Hook is this mailbox's merkle tree hook id in hex: the 32 bytes every checkpoint for a message from here names.
func Hook() string { return hx(hook[:]) }

// Count is how many messages went out: the next message's nonce.
func Count() uint32 { return tree.Count }

// Root is the merkle tree's root now, in hex.
func Root() string {
	r := tree.Root()
	return hx(r[:])
}

// Live is whether the Launch decision has executed: before it the routes refuse sends and credits.
func Live() bool { return live }

// LaunchedAt is the unix time the Launch decision executed, 0 before: the routes' meters start empty there.
func LaunchedAt() int64 { return launchedAt }

// Governor is the compiled governor (M1k): the address whose own call decides until it yields; "" when GovDAO
// governs from the first block. Every other bridge realm compiles the same and checks it here at deploy.
func Governor() address { return launchGovernor }

// Yielded is the block height the governor yielded at (Yield), 0 until then: what every bridge realm reads before
// it accepts a decision — the governor's own call before, GovDAO's proxy after (decisions.DAOGoverns).
func Yielded() int64 { return yielded }

// Message is the message dispatched with this nonce, in hex, and the tree's root right after it ("", "" if none).
func Message(nonce uint32) (message, root string) {
	v := sent.Get(nonceKey(nonce))
	if v == nil {
		return "", ""
	}
	s := v.(*sentMsg)
	return hx([]byte(s.raw)), hx(s.root[:])
}

// Delivered is the block height a message (by id, in hex) was delivered at here; 0 if it never was.
func Delivered(id string) int64 {
	v := done.Get(cleanID(id))
	if v == nil {
		return 0
	}
	return v.(int64)
}

func cleanID(id string) string {
	return strings.TrimPrefix(strings.ToLower(strings.TrimSpace(id)), "0x")
}

// Signers is the signer set and its rule: copies, so a verifier can read them and nobody can change them.
func Signers() ([]Key, Rule) {
	ks := append([]Key(nil), signerKeys...)
	r := Rule{Threshold: signerRule.Threshold, GroupA: append([]Key(nil), signerRule.GroupA...), GroupB: append([]Key(nil), signerRule.GroupB...)}
	return ks, r
}

// SignerKeys is the signer set in hex, comma separated, in the order signatures name them.
func SignerKeys() string { return keysHex(signerKeys) }

// ActiveVerifier is the pkgpath of the verifier GovDAO chose for an origin ("" if none).
func ActiveVerifier(origin uint32) string {
	v := active.Get(domainKey(origin))
	if v == nil {
		return ""
	}
	return v.(string)
}

// VerifierRegistered is whether a realm at this pkgpath registered as a verifier.
func VerifierRegistered(path string) bool { return verifiers.Has(path) }

// IsVerifier is whether a is the address of a realm that is, or was, an origin's active verifier here: the launch
// verifier, and every one a set verifier decision activated since (the round-4 realms audit's E4-RL5: Proposal 2's
// verifier, activated after launch, was no realm of the routes' lists, so a burn naming it was credited as a claim and
// paid into a realm that moves no coins). The routes refuse such an address as a send's recipient, sweep a burn naming
// it and refuse it as a vote's payee, as they do the launch verifier; one replaced since stays one — it moves no coins
// either. Only a decision activates, so no stranger makes another's address one. One avl read; a is compared as
// written (the routes pass a canonical address).
func IsVerifier(a address) bool { return verifierAt.Has(string(a)) }

// --------------------------------------------------------------- keys --

func parseKey(s string) Key {
	p := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(s)), "0x")
	b, err := hex.DecodeString(p)
	if err != nil || len(b) != 32 {
		panic("hlmailbox: not a 32-byte ed25519 key in hex: " + echo(p))
	}
	var k Key
	copy(k[:], b)
	return k
}

// parseKeys reads comma-separated keys in hex, refusing a key named twice.
func parseKeys(s string) []Key {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	if len(parts) > maxSigners {
		panic("hlmailbox: more than 255 keys")
	}
	ks := make([]Key, 0, len(parts))
	for _, p := range parts {
		k := parseKey(p)
		if indexOf(ks, k) >= 0 {
			panic("hlmailbox: a key named twice: " + hx(k[:]))
		}
		ks = append(ks, k)
	}
	return ks
}

func indexOf(ks []Key, k Key) int {
	for i, x := range ks {
		if x == k {
			return i
		}
	}
	return -1
}

// sameKeys is whether two lists hold the same keys in the same order.
func sameKeys(a, b []Key) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// subset is whether every key of a is in b.
func subset(a, b []Key) bool {
	for _, k := range a {
		if indexOf(b, k) < 0 {
			return false
		}
	}
	return true
}

// sameSet is whether two lists hold the same keys, in any order (a list never names a key twice).
func sameSet(a, b []Key) bool { return len(a) == len(b) && subset(a, b) && subset(b, a) }

// ----------------------------------------------------------- verifiers --

// RegisterVerifier is how a verifier realm offers itself: called once from that realm (its init, usually), it is
// kept under the caller's pkgpath, which GovDAO names to activate it. Only an /r/ realm may register, and once;
// a sub-realm identity (`cur.Sub(…)`, a token a realm mints for free, whose pkgpath `host#subpath` passes the
// prefix test) is refused, so every entry costs a package deployment and the listing (bounded; render.gno)
// cannot be flooded. Nothing is ever unregistered.
func RegisterVerifier(cur realm, v Verifier) {
	if !cur.IsCurrent() {
		panic("hlmailbox: that realm value is not live; pass your own cur")
	}
	path := cur.Previous().PkgPath()
	if !strings.HasPrefix(path, "gno.land/r/") {
		// echo-ok(path): the caller's pkgpath, ASCII by tm2's path rules
		panic("hlmailbox: only a realm under gno.land/r/ registers a verifier, not " + path)
	}
	if cur.Previous().Subpath() != "" {
		// echo-ok(path): the caller's pkgpath, ASCII by tm2's path rules
		panic("hlmailbox: a realm registers with its own cur, not a sub-identity (" + path + ")")
	}
	if v == nil {
		panic("hlmailbox: a nil verifier")
	}
	if verifiers.Has(path) {
		// echo-ok(path): the caller's pkgpath, ASCII by tm2's path rules
		panic("hlmailbox: " + path + " registered already")
	}
	verifiers.Set(path, v)
	// echo-ok(path): the caller's pkgpath, ASCII by tm2's path rules
	chain.Emit("VerifierRegistered", "pkgpath", path)
}

// satisfiable asks every active verifier whether the signer set ks can still meet the rule; an active verifier
// that is not registered answers no. Each verifier is handed its own copy of ks: the builders store ks after
// asking, and a verifier that runs under this realm's authority (the no-anchor case, see Verifier) must find
// nothing of the mailbox's to write into.
func satisfiable(ks []Key) bool {
	ok := true
	active.Iterate("", "", func(_ string, v any) bool {
		path := v.(string)
		r := verifiers.Get(path)
		if r == nil || !r.(Verifier).Satisfiable(append([]Key(nil), ks...)) {
			ok = false
			return true
		}
		return false
	})
	return ok
}

// ---------------------------------------------------------------- out --

// Dispatch sends body to recipient on destination, from whoever calls it — a realm, usually — and returns the
// message's id. The sender is the caller's address as Hyperlane carries one; the nonce is the message's index in
// the merkle tree. The event is §13's: id, nonce, origin, destination, sender, recipient, hash. A body above MaxBody
// bytes is refused first (E3-RL1).
func Dispatch(cur realm, destination uint32, recipient [32]byte, body []byte) [32]byte {
	if len(body) > MaxBody {
		panic("hlmailbox: the body is " + strconv.Itoa(len(body)) + " bytes, above the " + strconv.Itoa(MaxBody) + " a message carries (MaxBody): its msg/<nonce> page would cost the walks that read every nonce more than a query may spend")
	}
	sender := mustBytes32(caller(cur))
	// a successor a re-point dropped is a route no more (R35): Ethereum's token (its HypERC20) may still enroll it
	// as the router until the next kind 6, so a message from it would mint against a lock no realm of the line holds
	if path := cur.Previous().PkgPath(); path != "" {
		if by := Dropped(path); by != "" {
			// echo-ok(path, by): the caller's pkgpath, ASCII by tm2's path rules; a dropped route's predecessor as the mailbox recorded it: pkgpath, ASCII by tm2's path rules
			panic("hlmailbox: " + path + " is a route no more (its predecessor " + by + " re-pointed the migration to another successor): it sends nothing")
		}
	}
	m := hl.Message{
		Version:     hl.Version,
		Nonce:       tree.Count,
		Origin:      localDomain,
		Sender:      sender,
		Destination: destination,
		Recipient:   recipient,
		Body:        body,
	}
	raw := m.Bytes()
	id := hl.ID(raw)
	tree.Insert(id)
	root := tree.Root()
	sent.Set(nonceKey(m.Nonce), &sentMsg{raw: string(raw), root: root, height: runtime.ChainHeight()})
	emitDispatch(m, raw, id)
	return id
}

func emitDispatch(m hl.Message, raw []byte, id [32]byte) {
	h := sha256.Sum256(raw)
	chain.Emit("Dispatch",
		"id", hx(id[:]),
		"nonce", u32(m.Nonce),
		"origin", u32(m.Origin),
		"destination", u32(m.Destination),
		"sender", hx(m.Sender[:]),
		"recipient", hx(m.Recipient[:]),
		"hash", hx(h[:]),
	)
}

// Reemit emits the Dispatch event of a message dispatched earlier again, in this block, by anyone: a message
// dispatched before a validator-set change, or whose results leaf is bigger than the ISM accepts, is then delivered
// from a fresh header. The id is unchanged, so the other side's processed-message record still refuses a second
// delivery.
func Reemit(cur realm, nonce uint32) {
	caller(cur)
	v := sent.Get(nonceKey(nonce))
	if v == nil {
		panic("hlmailbox: no message with nonce " + u32(nonce))
	}
	raw := []byte(v.(*sentMsg).raw)
	m, err := hl.ParseMessage(raw)
	if err != nil {
		// echo-ok(err): p/hyperlane's fixed ASCII text for a message this mailbox wrote itself
		panic(err.Error())
	}
	emitDispatch(m, raw, hl.ID(raw))
}

// ----------------------------------------------------------------- in --

// Process takes a message from another chain for the caller, who must be its recipient — or a route that
// succeeds the recipient along the chain of migrations (AQ1: a burn addressed to a migrated route is the
// successor's to credit, once its last page is in; the route decides when), a successor a re-point dropped from
// that chain counted as a hop of its predecessor's (R35: the line's last realm takes what is addressed to it; the
// dropped realm itself, a route no more, is refused). metadata is whatever the origin's active verifier needs
// (the signers' checkpoint and signatures, for the ed25519 one). The message must be for this chain and never
// delivered; it is marked delivered, then the verifier is asked, and a verifier's error is a panic, so the whole
// transaction reverts with the mark and the message stays deliverable (§5.1). It is then handed back for the
// recipient to act on.
func Process(cur realm, metadata, message []byte) (origin uint32, sender [32]byte, body []byte) {
	who := caller(cur)
	callerPath := cur.Previous().PkgPath()
	if by := Dropped(callerPath); by != "" {
		// echo-ok(callerPath, by): the caller's pkgpath, ASCII by tm2's path rules; a dropped route's predecessor as the mailbox recorded it: pkgpath, ASCII by tm2's path rules
		panic("hlmailbox: " + callerPath + " is a route no more (its predecessor " + by + " re-pointed the migration to another successor): the line's last realm takes this message")
	}
	m, err := hl.ParseMessage(message)
	if err != nil {
		panic(err.Error()) // echo-ok(err): p/hyperlane's ParseMessage refuses with one fixed ASCII text (hyperlane.gno:73)
	}
	if m.Version != hl.Version {
		panic("hlmailbox: message version " + strconv.Itoa(int(m.Version)) + ", not " + strconv.Itoa(hl.Version))
	}
	if m.Destination != localDomain {
		panic("hlmailbox: the message is for domain " + u32(m.Destination) + ", not this one (" + u32(localDomain) + ")")
	}
	if mustBytes32(who) != m.Recipient && !successorOf(callerPath, m.Recipient) {
		panic("hlmailbox: only the message's recipient, or a successor of it, can take it")
	}
	idb := hl.ID(message)
	id := hx(idb[:])
	if done.Has(id) {
		panic("hlmailbox: already delivered")
	}
	path := active.Get(domainKey(m.Origin))
	if path == nil {
		panic("hlmailbox: no verifier for domain " + u32(m.Origin))
	}
	v := verifiers.Get(path.(string))
	if v == nil {
		// echo-ok(path): the active verifier's pkgpath, ASCII by tm2's path rules (activated by a decision)
		panic("hlmailbox: the active verifier " + path.(string) + " is not registered")
	}
	done.Set(id, runtime.ChainHeight())
	if err := v.(Verifier).Verify(m.Origin, metadata, message); err != nil {
		panic(echo(err.Error())) // a verifier's refusal may name the caller's bytes (hlverifier's never does; a later one GovDAO activates might)
	}
	// echo-ok(id): the message id in hex
	chain.Emit("Process",
		"origin", u32(m.Origin),
		"nonce", u32(m.Nonce),
		"id", id,
		"sender", hx(m.Sender[:]),
		"recipient", hx(m.Recipient[:]),
	)
	return m.Origin, m.Sender, m.Body
}

// MarkDone marks a message id delivered without a message: a route's credit-by-vote (§5.3) does it, so the id
// can never be credited twice. Only a registered route may call it, and only for an id not done.
func MarkDone(cur realm, id string) {
	mustBeRoute(cur)
	id = cleanID(id)
	if b, err := hex.DecodeString(id); err != nil || len(b) != 32 {
		panic("hlmailbox: not a 32-byte message id in hex: " + echo(id))
	}
	if done.Has(id) {
		panic("hlmailbox: already delivered")
	}
	done.Set(id, runtime.ChainHeight())
}
