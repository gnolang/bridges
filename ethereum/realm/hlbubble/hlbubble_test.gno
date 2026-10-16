package hlbubble

import (
	"chain"
	"chain/runtime"
	"crypto/bech32"
	"encoding/hex"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"gno.land/p/g1leu8d2vsplhehcfkjg50mwgdpxdkt8tztu95wr/decisions/v0"
	"gno.land/p/g1leu8d2vsplhehcfkjg50mwgdpxdkt8tztu95wr/hlroute/v0"
	hl "gno.land/p/g1leu8d2vsplhehcfkjg50mwgdpxdkt8tztu95wr/hyperlane/v0"
	"gno.land/p/nt/testutils/v0"
	"gno.land/r/g1leu8d2vsplhehcfkjg50mwgdpxdkt8tztu95wr/bubble"
	"gno.land/r/g1leu8d2vsplhehcfkjg50mwgdpxdkt8tztu95wr/eth/hlmailbox"
	"gno.land/r/gov/dao"
	govinit "gno.land/r/gov/dao/init/v0"
)

// The shared state machine is tested in p/hlroute and the GovDAO path in r/hlgnot; these tests cover what is
// BUBBLE's own: the allowance, the token moving on sends, claims and fees, and the swept burn below the fee. Who
// decides follows the chain's own order (M1k): the compiled governor makes every decision directly through Govern
// from TestSetup to TestBuilders, then TestYield hands over to GovDAO and the tests after run as proposals — pass
// picks the path by the yield.

const (
	ethTest      = uint32(31337) // the mailbox's testEthereumDomain: this route's domain here
	testVerifier = namespace + "hltestverifier"
	dayHeights   = 86400 / 5
	// ethWallet is a recipient on Ethereum: EIP-55's second example, mixed case as its checksum writes it
	ethWallet = "0xfB6916095ca1df60bB79Ce92cE3Ea74c37c5d359"
)

var (
	// premineTo holds bubble's premine from that realm's init: the tests' faucet.
	premineTo = address("g1ecsuj0q572jr0dhu29q9njtnmw03hyu7tyyvv6")
	member    = testutils.TestAddress("hl-member")
	alice     = testutils.TestAddress("hlb-alice")
	bob       = testutils.TestAddress("hlb-bob")
	carol     = testutils.TestAddress("hlb-carol")
	relayer   = testutils.TestAddress("hlb-relayer")
	fees      = address("")
)

type verifier2of3 struct{}

func (verifier2of3) Verify(origin uint32, metadata, message []byte) error {
	ks, rule := hlmailbox.Signers()
	keys := make([][]byte, len(ks))
	for i := range ks {
		k := ks[i]
		keys[i] = k[:]
	}
	return hl.VerifyMultisig(keys, rule.Threshold, metadata, message)
}

func (verifier2of3) Satisfiable(signers []hlmailbox.Key) bool {
	_, rule := hlmailbox.Signers()
	return len(signers) >= rule.Threshold
}

// anyVerifier takes every message: the last test crafts messages the vectors do not carry (a burn whose recipient
// is a successor route's address), so the signatures are not its subject.
type anyVerifier struct{}

func (anyVerifier) Verify(origin uint32, metadata, message []byte) error { return nil }
func (anyVerifier) Satisfiable(signers []hlmailbox.Key) bool             { return true }

func unhex(s string) []byte {
	b, err := hex.DecodeString(s)
	if err != nil {
		panic(err)
	}
	return b
}

func idOf(msgHex string) string {
	id := hl.ID(unhex(msgHex))
	return hex.EncodeToString(id[:])
}

func aborts(t *testing.T, want string, f func()) {
	t.Helper()
	r := revive(f)
	if r == nil {
		t.Fatalf("expected an abort containing %q, got none", want)
	}
	msg := ""
	switch e := r.(type) {
	case error:
		msg = e.Error()
	case string:
		msg = e
	}
	if !strings.Contains(msg, want) {
		t.Fatalf("abort %q does not contain %q", msg, want)
	}
}

// pass makes a builder's decision by whoever governs now (M1k): the governor's own Govern before the yield, and
// GovDAO's proposal — created by the member, voted yes, executed — after it.
func pass(cur realm, req Request) {
	if hlmailbox.Yielded() == 0 {
		testing.SetRealm(testing.NewUserRealm(launchGovernor))
		Govern(cross(cur), req)
		return
	}
	testing.SetRealm(testing.NewUserRealm(member))
	pid := dao.MustCreateProposal(cross(cur), req.Proposal())
	testing.SetRealm(testing.NewUserRealm(member))
	dao.MustVoteOnProposalSimple(cross(cur), int64(pid), "YES")
	testing.SetRealm(testing.NewUserRealm(member))
	if !dao.ExecuteProposal(cross(cur), pid) {
		panic("the proposal did not execute")
	}
}

// passMailbox is pass for the mailbox's own requests (the launch, the verifier).
func passMailbox(cur realm, req hlmailbox.Request) {
	if hlmailbox.Yielded() == 0 {
		testing.SetRealm(testing.NewUserRealm(launchGovernor))
		hlmailbox.Govern(cross(cur), req)
		return
	}
	testing.SetRealm(testing.NewUserRealm(member))
	pid := dao.MustCreateProposal(cross(cur), req.Proposal())
	testing.SetRealm(testing.NewUserRealm(member))
	dao.MustVoteOnProposalSimple(cross(cur), int64(pid), "YES")
	testing.SetRealm(testing.NewUserRealm(member))
	if !dao.ExecuteProposal(cross(cur), pid) {
		panic("the proposal did not execute")
	}
}

// refusedByDAO proves GovDAO's proxy cannot make a builder's decision before the yield (M1k): the proposal is
// created and voted, its execution aborts on the governor's rule, and nothing is recorded.
func refusedByDAO(cur realm, t *testing.T, what string, req Request) {
	t.Helper()
	n := st.Ledger.Last()
	testing.SetRealm(testing.NewUserRealm(member))
	pid := dao.MustCreateProposal(cross(cur), req.Proposal())
	testing.SetRealm(testing.NewUserRealm(member))
	dao.MustVoteOnProposalSimple(cross(cur), int64(pid), "YES")
	testing.SetRealm(testing.NewUserRealm(member))
	aborts(t, "hlbubble: until the governor yields, "+string(launchGovernor)+" alone decides, by Govern from its own key: not gno.land/r/gov/dao (a GovDAO proposal is refused until the yield)", func() { dao.ExecuteProposal(cross(cur), pid) })
	if st.Ledger.Last() != n {
		t.Fatalf("%s: a refused proposal recorded a decision", what)
	}
}

func asMember(cur realm)  { testing.SetRealm(testing.NewUserRealm(member)) }
func asRelayer(cur realm) { testing.SetRealm(testing.NewUserRealm(relayer)) }
func skipDays(n int64)    { testing.SkipHeights(n*dayHeights + 1) }

func fund(cur realm, who address, n int64) {
	testing.SetRealm(testing.NewUserRealm(premineTo))
	bubble.Transfer(cross(cur), who, n)
}

// refusal is the text f panics with, "" if it does not.
func refusal(f func()) string {
	switch e := revive(f).(type) {
	case error:
		return e.Error()
	case string:
		return e
	}
	return ""
}

// chainSafe is whether a refusal's text stays valid UTF-8 however gno cuts it (E4-GS1): a failed transaction's panic
// text, and its Log line, are cut at 1,024 bytes by byte offset, so the text is all ASCII, or valid UTF-8 and short
// enough that no cut reaches it.
func chainSafe(s string) bool {
	if !utf8.ValidString(s) {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			return len(s) <= 900
		}
	}
	return true
}

// badStrings are what a stranger hands a builder to poison a block (E4-GS1): two bytes no rune begins with (the
// audit's PoC, gno v1.5.1), and 1,200 bytes of em dashes — valid UTF-8 that gno's 1,024-byte cut splits mid-rune
// once a refusal echoes them.
var badStrings = []string{"\xff\xfe", strings.Repeat("\xe2\x80\x94", 400)}

// head is the start of a long text, for a failure's message.
func head(s string) string {
	if len(s) > 160 {
		return s[:160]
	}
	return s
}

// checkWaiting holds the waiting index to its contract (waiting.gno, E4-RL1): every claim open on the route — as
// p/hlroute's own walk lists them (claims/<from>, at the tests' scale) — is in the index once, every entry's place
// recorded by its id, and the two trees hold the same entries. It returns how many entries name a claim no longer open
// (paid, voided or gone, which the page shows in their state until a credit's tidy takes them out).
func checkWaiting(t *testing.T, step string) int {
	t.Helper()
	open := map[string]bool{}
	for from := 0; ; from += hlroute.PageLines {
		ids := idsIn(st.ClaimsPage(from))
		for _, id := range ids {
			open[id] = true
		}
		if len(ids) < hlroute.PageLines {
			break
		}
	}
	seen := map[string]bool{}
	stale := 0
	waitingByPos.Iterate("", "", func(k string, v any) bool {
		id := v.(string)
		if seen[id] {
			t.Fatalf("%s: %s is twice in the waiting index", step, id)
		}
		seen[id] = true
		if p := waitingPos.Get(id); p == nil || posKey(p.(int64)) != k {
			t.Fatalf("%s: %s's recorded place does not point at its entry %s", step, id, k)
		}
		if c := st.ClaimOf(id); c == nil || c.State != hlroute.Open {
			stale++
		}
		return false
	})
	for id := range open {
		if !seen[id] {
			t.Fatalf("%s: the open claim %s is not in the waiting index", step, id)
		}
	}
	if waitingByPos.Size() != waitingPos.Size() || waitingByPos.Size() != len(open)+stale {
		t.Fatalf("%s: %d entries by place, %d by id, %d open claims and %d stale entries", step, waitingByPos.Size(), waitingPos.Size(), len(open), stale)
	}
	return stale
}

// idsIn are the claim ids a page of claims names, in order.
func idsIn(page string) []string {
	var ids []string
	for {
		i := strings.Index(page, "\"id\":\"")
		if i < 0 {
			return ids
		}
		page = page[i+6:]
		ids = append(ids, page[:64])
		page = page[64:]
	}
}

func TestLaunchValues(cur realm, t *testing.T) {
	route, _ := hlmailbox.ParseEthAddress(testRoute)
	if hlmailbox.Live() || Locked() != 0 || Fee() != 400*one || Minimum() != 800*one || FeeFloor() != 40*one || CapFloor() != 10_000*one ||
		Maximum() != 25_000*one || Domain() != ethTest || Router() != hex.EncodeToString(route[:]) {
		t.Fatalf("launch values: fee %d min %d floor %d cap %d max %d router %s", Fee(), Minimum(), FeeFloor(), CapFloor(), Maximum(), Router())
	}
	// the figures stand inside every rule p/hlroute holds a decision to — the fee floor at most the fee
	// (DraftFeeFloor), the fee at most a quarter of the cap floor (DraftFee, DraftCapFloor), the minimum positive and
	// at most the maximum (DraftFee, DraftMaximum) — and the minimum above the fee (eth §5.5: twice it)
	if FeeFloor() > Fee() || Fee() > CapFloor()/4 || Minimum() <= Fee() || Minimum() > Maximum() {
		t.Fatalf("the launch figures break a rule: fee %d floor %d minimum %d cap floor %d maximum %d", Fee(), FeeFloor(), Minimum(), CapFloor(), Maximum())
	}
	// the router is an Ethereum address (eth §5.2): the twelve zero bytes and the address, in EIP-55 case on the
	// page and in the json
	if RouterAddress() != testRoute || launchRouter != testRoute || Router() != "000000000000000000000000"+strings.ToLower(testRoute[2:]) {
		t.Fatalf("the router %s (%s)", RouterAddress(), Router())
	}
	// init refuses a launch router other than the mailbox's token 1 (E-M1's Q3): its rule takes the compiled router
	// and refuses the others — the other token (a stage step that swapped the two), the router one bit off, the zero
	// address
	if why := wrongRouter(route); why != "" {
		t.Fatalf("the compiled router refused: %s", why)
	}
	off := route
	off[31] ^= 1
	for _, k := range [][32]byte{mustAddr("0x0BA554Aa0D0566e582dE0238a089Ec102c2e05a4"), off, {}} {
		if why := wrongRouter(k); why != "hlbubble: the compiled launch router "+hlmailbox.EthAddress(k)+" is not the mailbox's token 1 (BUBBLE), "+testRoute+": both must be the BUBBLE HypERC20 proxy" {
			t.Fatalf("router %s: %q", hlmailbox.EthAddress(k), why)
		}
	}
	p := Render("")
	if !strings.Contains(p, "fee 400 BUBBLE (floor 40 BUBBLE); minimum 800 BUBBLE; per-message maximum 25000 BUBBLE") || !strings.Contains(p, "- router on Ethereum: `"+testRoute+"` (the BUBBLE token contract, domain 31337)\n") ||
		!strings.Contains(p, "so the supply on Ethereum should be 0 BUBBLE up to messages in flight") || strings.Contains(p, "Solana") || !strings.Contains(Render("json"), "\"routerAddress\":\""+testRoute+"\",") {
		t.Fatalf("page: %s\n%s", p, Render("json"))
	}
	testing.SetRealm(testing.NewUserRealm(alice))
	aborts(t, "not launched", func() { TransferRemote(cross(cur), ethTest, ethWallet, Minimum()) })
}

func TestSetup(cur realm, t *testing.T) {
	fees = hlmailboxFees()
	asMember(cur)
	govinit.InitWithUsers(cross(cur), member)
	testing.SetRealm(testing.NewCodeRealm(testVerifier))
	hlmailbox.RegisterVerifier(cross(cur), verifier2of3{})
	asMember(cur)
	passMailbox(cur, hlmailbox.NewLaunchRequest(cross(cur)))
	// the meter is shown as the launch reset leaves it — empty — before any Claim applied the reset (K22)
	if !strings.Contains(Render("json"), "\"meter\":0,") || !strings.Contains(Render(""), "the bridge meter holds 0 BUBBLE now") {
		t.Fatalf("the meter after Launch: %s", Render("json"))
	}
	asMember(cur)
	passMailbox(cur, hlmailbox.NewSetVerifierRequest(cross(cur), ethTest, testVerifier))
	skipDays(7)
	asRelayer(cur)
	hlmailbox.Execute(cross(cur), 2)
	if !hlmailbox.Live() || hlmailbox.ActiveVerifier(ethTest) != testVerifier {
		t.Fatalf("setup")
	}
}

// TestEchoesStayUTF8 (E4-GS1, E4-RL6): TransferRemote and every builder that reads a caller's string, handed bytes
// that are no UTF-8 or a long run of multi-byte runes, refuse with a text that stays valid UTF-8 however gno cuts it —
// the string quoted and cut short (echo) where this realm refuses it, and refused before p/hlroute reads it
// (mustBeASCII) where p/hlroute would echo it as it came. Nothing is recorded or sent.
func TestEchoesStayUTF8(cur realm, t *testing.T) {
	n, sends := st.Ledger.Last(), st.Sends()
	id := strings.Repeat("ab", 32)
	for _, s := range badStrings {
		for _, c := range []struct {
			name string
			f    func()
		}{
			{"TransferRemote", func() { TransferRemote(cross(cur), ethTest, s, 1000*one) }},
			{"set router", func() { NewSetRouterRequest(cross(cur), s) }},
			{"void: the ids", func() { NewVoidRequest(cross(cur), s, false, "", "") }},
			{"void: a range", func() { NewVoidRequest(cross(cur), "heights:"+s, false, "", "") }},
			{"void: the unpause functions", func() { NewVoidRequest(cross(cur), id, false, s, "") }},
			{"void: the seats", func() { NewVoidRequest(cross(cur), id, false, "", s) }},
			{"restore", func() { NewRestoreRequest(cross(cur), s) }},
			{"credit: the id", func() { NewCreditRequest(cross(cur), s, bob, one) }},
			{"credit: the recipient", func() { NewCreditRequest(cross(cur), id, address(s), one) }},
			{"sweep: the ids", func() { NewSweepRequest(cross(cur), s, bob) }},
			{"sweep: the recipient", func() { NewSweepRequest(cross(cur), id, address(s)) }},
			{"refund", func() { NewRefundRequest(cross(cur), s) }},
			{"write off", func() { NewWriteOffRequest(cross(cur), s) }},
			{"migrate", func() { NewMigrateRequest(cross(cur), "gno.land/r/"+s) }},
		} {
			asMember(cur)
			if msg := refusal(c.f); msg == "" || !chainSafe(msg) {
				t.Errorf("%s handed %d bytes: a refusal of %d bytes, valid UTF-8 %v: %q", c.name, len(s), len(msg), utf8.ValidString(msg), head(msg))
			}
		}
	}
	// where this realm refuses, the bytes come back quoted; where p/hlroute would, the string is refused before it
	asMember(cur)
	if msg := refusal(func() { TransferRemote(cross(cur), ethTest, "\xff\xfe", 1000*one) }); !strings.Contains(msg, `"\xff\xfe"`) {
		t.Errorf("TransferRemote's refusal does not quote the recipient: %q", msg)
	}
	asMember(cur)
	if msg := refusal(func() { NewVoidRequest(cross(cur), "heights:1-\xff", false, "", "") }); !strings.HasPrefix(msg, "hlbubble: the ids holds a byte outside ASCII, at offset 10") {
		t.Errorf("a void's range is not refused before p/hlroute reads it: %q", msg)
	}
	if st.Ledger.Last() != n || st.Sends() != sends {
		t.Fatalf("a refusal recorded or sent something")
	}
}

func hlmailboxFees() address { return realms[2] }

// TestTransferRemote: the allowance covers amount + fee; the lock takes the amount, the fee realm the fee. The amount
// is what backAlice1200 burns back (TestProcessAndClaim: the lock back to 0), a send the minimum must allow.
func TestTransferRemote(cur realm, t *testing.T) {
	send := 1_200 * one
	if send < Minimum() || send+Fee() > 2_000*one {
		t.Fatalf("a send of %d with the fee %d must clear the minimum %d and fit alice's 2,000", send, Fee(), Minimum())
	}
	fund(cur, alice, 2_000*one)
	testing.SetRealm(testing.NewUserRealm(alice))
	aborts(t, "below the minimum", func() { TransferRemote(cross(cur), ethTest, ethWallet, Minimum()-one) })
	testing.SetRealm(testing.NewUserRealm(alice))
	aborts(t, "allowance exceeded", func() { TransferRemote(cross(cur), ethTest, ethWallet, send) })
	testing.SetRealm(testing.NewUserRealm(alice))
	bubble.Approve(cross(cur), Address(), send+Fee())
	// the recipient is an Ethereum address (eth §5.2), refused before any BUBBLE moves: the zero address, this
	// route's own router, a mixed case that is not the EIP-55 checksum, 32 bytes whose top twelve are not zero, a
	// Solana key
	for _, r := range []struct{ to, why string }{
		{"0x0000000000000000000000000000000000000000", "the recipient is the zero address: an ERC-20 refuses to mint to it"},
		{testRoute, "the recipient is this route's own router, its token contract on Ethereum"},
		{"0x0000000000000000000000000000000000000011", "at or below 0xffff: a precompile's"},
		{"0x0BA554Aa0D0566e582dE0238a089Ec102c2e05a4", "token 0 (GNOT) of this deployment on Ethereum: a contract of the bridge"}, // the other token (ER10)
		{"0x0101010101010101010101010101010101010101", "HlAdmin of this deployment on Ethereum"},
		{"0x2525252525252525252525252525252525252525", "the Ed25519Verifier of this deployment on Ethereum"}, // the two the build added (M2-NOTES.md §1 G)
		{"0x1212121212121212121212121212121212121212", "HlAdminExec of this deployment on Ethereum"},
		{"0x000000000000000000000000" + strings.ToLower(testRoute[2:]), "this route's own router"},
		{"0xFb6916095ca1df60bB79Ce92cE3Ea74c37c5d359", "is not its EIP-55 checksum"},
		{"0x" + strings.Repeat("ee", 12) + strings.ToLower(ethWallet[2:]), "top twelve are not zero"},
		{"E588QtVUvresuXq2KoNEwAmoifCzYGpRBdHByN9KQMbi", "the recipient is not an Ethereum address"},
	} {
		testing.SetRealm(testing.NewUserRealm(alice))
		aborts(t, r.why, func() { TransferRemote(cross(cur), ethTest, r.to, send) })
	}
	if Locked() != 0 || bubble.BalanceOf(alice) != 2_000*one {
		t.Fatalf("a refused send moved BUBBLE: locked %d", Locked())
	}
	n0 := hlmailbox.Count()
	testing.SetRealm(testing.NewUserRealm(alice))
	id := TransferRemote(cross(cur), ethTest, ethWallet, send)
	if Locked() != send || Balance() != send || bubble.BalanceOf(alice) != 2_000*one-send-Fee() || bubble.BalanceOf(fees) != Fee() || hlmailbox.Count() != n0+1 {
		t.Fatalf("locked %d balance %d alice %d fees %d", Locked(), Balance(), bubble.BalanceOf(alice), bubble.BalanceOf(fees))
	}
	rawHex, _ := hlmailbox.Message(n0)
	m, _ := hl.ParseMessage(unhex(rawHex))
	self, _ := hl.AddressToBytes32(Address())
	// the recipient and the router as Hyperlane carries an address — twelve zero bytes, then the twenty — decoded
	// here apart from the realm's parser; the amount a big-endian uint256 (1,200 BUBBLE, 1,200,000,000 base units)
	wallet := unhex("000000000000000000000000" + strings.ToLower(ethWallet[2:]))
	route := unhex("000000000000000000000000" + strings.ToLower(testRoute[2:]))
	amount := unhex(strings.Repeat("00", 24) + "0000000047868c00")
	if m.Sender != self || string(m.Recipient[:]) != string(route) || m.Destination != ethTest || string(m.Body) != string(wallet)+string(amount) || idOf(rawHex) != id {
		t.Fatalf("message %+v", m)
	}
	// the rest of the allowance (0) does not cover another
	testing.SetRealm(testing.NewUserRealm(alice))
	aborts(t, "allowance exceeded", func() { TransferRemote(cross(cur), ethTest, ethWallet, Minimum()) })
}

// TestProcessAndClaim: a burn comes back as a claim paid under the meters in BUBBLE; a burn below the fee is swept.
func TestProcessAndClaim(cur realm, t *testing.T) {
	// the vectors' amounts against the compiled fee: carol's 50 below it (swept for that), alice's 1,200 and 500 and
	// dirty bob's 500 at or above it (claimed, or swept for the lock or the recipient, never for their size)
	if 50*one >= Fee() || 500*one < Fee() || 1_200*one < Fee() {
		t.Fatalf("the fee %d no longer sorts the vectors as these tests need", Fee())
	}
	// the lock is 1,200 BUBBLE: under the floor, the cap is 10,000, a quarter 2,500
	asRelayer(cur)
	Process(cross(cur), backAlice1200Meta, backAlice1200Msg)
	asRelayer(cur)
	aborts(t, "not from the router", func() { Process(cross(cur), strangerRouteMeta, strangerRouteMsg) })
	asRelayer(cur)
	Process(cross(cur), backCarol50Meta, backCarol50Msg)
	if st.ClaimOf(idOf(backAlice1200Msg)) == nil || st.SweptOf(idOf(backCarol50Msg)) != 50*one || st.Owed != 1_250*one {
		t.Fatalf("credited: owed %d", st.Owed)
	}
	asRelayer(cur)
	if Claim(cross(cur), 20) != 0 {
		t.Fatalf("paid from an empty meter")
	}
	a0, f0 := bubble.BalanceOf(alice), bubble.BalanceOf(fees)
	skipDays(1)
	asRelayer(cur)
	Claim(cross(cur), 20)
	if bubble.BalanceOf(alice)-a0 != 1_200*one-Fee() || bubble.BalanceOf(fees)-f0 != Fee() || Locked() != 0 || Balance() != 0 {
		t.Fatalf("alice +%d fees +%d locked %d balance %d", bubble.BalanceOf(alice)-a0, bubble.BalanceOf(fees)-f0, Locked(), Balance())
	}
	// a burn above the lock (500 against a lock of 0) cannot be honest: swept, owed, paying nobody
	asRelayer(cur)
	Process(cross(cur), backAlice500Meta, backAlice500Msg)
	if st.SweptOf(idOf(backAlice500Msg)) != 500*one || st.ClaimOf(idOf(backAlice500Msg)) != nil || st.Owed != 550*one {
		t.Fatalf("a burn above the lock: swept %d owed %d", st.SweptOf(idOf(backAlice500Msg)), st.Owed)
	}
	// a sweep payment never exceeds the lock (R11): carol's 50 against a lock of 0
	asMember(cur)
	aborts(t, "the entries sum to 50 BUBBLE, above the lock's 0 BUBBLE", func() { NewSweepRequest(cross(cur), idOf(backCarol50Msg), carol) })
	// a recipient whose top twelve bytes are not zero is no gno address (§13): the credit is swept — owed, paying
	// nobody until GovDAO credits it by id to its owner — never landed on the address its last twenty bytes spell.
	// A send first keeps the lock above the 500, so it is the recipient and not the lock that sweeps it
	fund(cur, bob, 1_150*one+Fee())
	testing.SetRealm(testing.NewUserRealm(bob))
	bubble.Approve(cross(cur), Address(), 1_150*one+Fee())
	testing.SetRealm(testing.NewUserRealm(bob))
	TransferRemote(cross(cur), ethTest, ethWallet, 1_150*one)
	asRelayer(cur)
	Process(cross(cur), dirtyBob500Meta, dirtyBob500Msg)
	if st.SweptOf(idOf(dirtyBob500Msg)) != 500*one || st.ClaimOf(idOf(dirtyBob500Msg)) != nil {
		t.Fatalf("a recipient with dirt in the padding was credited: swept %d", st.SweptOf(idOf(dirtyBob500Msg)))
	}
	b0 := bubble.BalanceOf(bob)
	skipDays(1)
	asRelayer(cur)
	Claim(cross(cur), 20)
	if bubble.BalanceOf(bob)-b0 != 0 || Locked() != 1_150*one {
		t.Fatalf("bob +%d locked %d", bubble.BalanceOf(bob)-b0, Locked())
	}
	sent, paid, owed, voided, lost := Counters()
	if sent != 2_350*one || paid != 1_200*one || owed != 1_050*one || voided != 0 || lost != 0 || sent-paid != Locked() {
		t.Fatalf("counters %d %d %d %d %d", sent, paid, owed, voided, lost)
	}
	if !strings.Contains(Render("json"), "\"symbol\":\"BUBBLE\"") || !strings.Contains(Render(""), "`"+Router()+"`") {
		t.Fatalf("pages")
	}
	_ = carol
	_ = hlroute.Open
	checkWaiting(t, "TestProcessAndClaim")
}

// TestBuilders: one at-once and one queued decision, the BUBBLE amounts, and the sweep paid in BUBBLE.
func TestBuilders(cur realm, t *testing.T) {
	asMember(cur)
	aborts(t, "never goes below the launch floor", func() { NewSetCapFloorRequest(cross(cur), 9_999*one) })
	// a migrate never names a route the mailbox registers already (R31): the GNOT route here, whatever it announced
	asMember(cur)
	aborts(t, "the successor "+hlgnotPath+" is a route already", func() { NewMigrateRequest(cross(cur), hlgnotPath) })
	// a refund waits for kind 6's enrolment of this route (AR1), and a write-off takes only a refunded send
	asMember(cur)
	aborts(t, "no kind 6 has enrolled this route on Solana yet", func() { NewRefundRequest(cross(cur), strings.Repeat("ab", 32)) }) // p/hlroute's own words
	asMember(cur)
	aborts(t, "is not a refunded send: only a send delivered on Solana after its refund is written off", func() { NewWriteOffRequest(cross(cur), strings.Repeat("ab", 32)) })
	asMember(cur)
	fee1 := Fee() - 20*one // a lowering by 20 BUBBLE, the minimum kept: at once
	pass(cur, NewSetFeeRequest(cross(cur), fee1, Minimum()))
	if Fee() != fee1 || st.Ledger.Get(1).Queued() {
		t.Fatalf("a fee lowering waited")
	}
	asMember(cur)
	pass(cur, NewSweepRequest(cross(cur), idOf(backCarol50Msg), carol))
	if !st.Ledger.Get(2).Queued() {
		t.Fatalf("a sweep applied at once")
	}
	skipDays(7)
	asRelayer(cur)
	Execute(cross(cur), 2)
	if bubble.BalanceOf(carol) != 50*one || st.Sweep != 1_000*one || Locked() != 1_100*one || st.SweepOf(idOf(backCarol50Msg)) != nil {
		t.Fatalf("sweep: carol %d sweep %d locked %d", bubble.BalanceOf(carol), st.Sweep, Locked())
	}
	if !strings.Contains(Render("decisions"), "### sweep") {
		t.Fatalf("decisions page")
	}
	// the fee and the cap floor's quarter bound each other: the fee builder refuses a fee above the quarter, the
	// cap-floor builder a floor whose quarter is below the fee, and a queued fee raise finds the floor lowered in
	// between is refused when it would apply
	asMember(cur)
	aborts(t, "never goes above a quarter of the cap floor (2500 BUBBLE)", func() { NewSetFeeRequest(cross(cur), 2_501*one, Minimum()) })
	asMember(cur)
	pass(cur, NewSetCapFloorRequest(cross(cur), 20_000*one))
	skipDays(7)
	asRelayer(cur)
	Execute(cross(cur), st.Ledger.Last())
	asMember(cur)
	pass(cur, NewSetFeeRequest(cross(cur), 5_000*one, Minimum())) // a quarter of 20,000: queued
	raise := st.Ledger.Last()
	asMember(cur)
	pass(cur, NewSetCapFloorRequest(cross(cur), 12_000*one)) // a lowering, at once: the quarter is 3,000 now
	if CapFloor() != 12_000*one || Fee() != fee1 {
		t.Fatalf("cap floor %d fee %d", CapFloor(), Fee())
	}
	skipDays(7)
	asRelayer(cur)
	aborts(t, "above a quarter of the cap floor", func() { Execute(cross(cur), raise) })
	if Fee() != fee1 || !st.Ledger.Get(raise).Pending() {
		t.Fatalf("a fee above the quarter applied: %d", Fee())
	}
	asMember(cur)
	pass(cur, NewSetFeeRequest(cross(cur), 3_000*one, Minimum()))
	skipDays(7)
	asRelayer(cur)
	Execute(cross(cur), st.Ledger.Last())
	asMember(cur)
	aborts(t, "quarter never goes below the fee of 3000 BUBBLE", func() { NewSetCapFloorRequest(cross(cur), 11_000*one) })
	if Fee() != 3_000*one || CapFloor() != 12_000*one {
		t.Fatalf("fee %d cap floor %d", Fee(), CapFloor())
	}
	// R16: a queued sweep payment (the dirty-address burn's 500, to bob), due, waits under a standing payouts flag
	// and goes through after the clear
	asMember(cur)
	pass(cur, NewSweepRequest(cross(cur), idOf(dirtyBob500Msg), bob))
	sweep := st.Ledger.Last()
	skipDays(7)
	g1 := hlmailbox.Seat(1)
	testing.SetRealm(testing.NewUserRealm(g1))
	hlmailbox.SetFlag(cross(cur), pkgPath, hlmailbox.FnPayouts, "")
	asRelayer(cur)
	aborts(t, "payouts are paused", func() { Execute(cross(cur), sweep) })
	if !st.Ledger.Get(sweep).Pending() || st.Sweep != 1_000*one {
		t.Fatalf("a sweep paid under the flag")
	}
	testing.SetRealm(testing.NewUserRealm(g1))
	hlmailbox.ClearFlag(cross(cur), pkgPath, hlmailbox.FnPayouts)
	b0 := bubble.BalanceOf(bob)
	asRelayer(cur)
	Execute(cross(cur), sweep)
	if bubble.BalanceOf(bob)-b0 != 500*one || st.Sweep != 500*one || Locked() != 600*one || st.SweepOf(idOf(dirtyBob500Msg)) != nil {
		t.Fatalf("sweep after the clear: bob +%d sweep %d locked %d", bubble.BalanceOf(bob)-b0, st.Sweep, Locked())
	}
	checkWaiting(t, "TestBuilders")
}

// TestYield (M1k): before the yield every builder's request GovDAO's proxy executes is refused — the one closure
// every builder shares refuses the proxy first, so the eleven requests this state lets a builder make stand for
// the fifteen (a restore, a refund and an apply refund need a voided claim and an enrolment note this test does
// not make; TestApplyRefund runs the refund pair under GovDAO, hlgnot2's drill under the governor) — and nothing
// is recorded; then the governor yields on the mailbox, Govern is closed to it and a GovDAO proposal decides, on
// the same clocks.
func TestYield(cur realm, t *testing.T) {
	// the governor queues a raise, so a cancel can be requested; nothing of it survives this test
	asMember(cur)
	pass(cur, NewSetCapFloorRequest(cross(cur), 13_000*one))
	raise := st.Ledger.Last()
	if !st.Ledger.Get(raise).Queued() || CapFloor() != 12_000*one {
		t.Fatalf("the governor's raise applied at once")
	}
	announced := namespace + "hlbubblem1k"
	testing.SetRealm(testing.NewCodeRealm(announced))
	hlmailbox.AnnounceSuccessor(cross(cur), hlmailbox.SymbolBUBBLE)
	fresh, entry := strings.Repeat("9b", 32), idOf(backAlice500Msg)
	for _, r := range []struct {
		what string
		mk   func() Request
	}{
		{"set cap floor", func() Request { return NewSetCapFloorRequest(cross(cur), 14_000*one) }},
		{"set maximum", func() Request { return NewSetMaximumRequest(cross(cur), 24_000*one) }},
		{"set fee and minimum", func() Request { return NewSetFeeRequest(cross(cur), 2_000*one, Minimum()) }},
		{"set fee floor", func() Request { return NewSetFeeFloorRequest(cross(cur), 30*one) }},
		{"set router", func() Request { return NewSetRouterRequest(cross(cur), "0x"+strings.Repeat("ab", 20)) }},
		{"void", func() Request { return NewVoidRequest(cross(cur), "since:1", true, "", "") }},
		{"credit", func() Request { return NewCreditRequest(cross(cur), fresh, bob, 10*one) }},
		{"sweep", func() Request { return NewSweepRequest(cross(cur), entry, carol) }},
		{"write off", func() Request { return NewWriteOffRequest(cross(cur), strings.Repeat("ab", 32)) }},
		{"migrate", func() Request { return NewMigrateRequest(cross(cur), announced) }},
		{"cancel", func() Request { return NewCancelRequest(cross(cur), raise) }},
	} {
		if r.what == "write off" {
			continue // a write-off names a refunded send, which this state has none of: refused when made, by the builder
		}
		asMember(cur)
		refusedByDAO(cur, t, r.what, r.mk())
	}
	if CapFloor() != 12_000*one || Maximum() != 25_000*one || Fee() != 3_000*one || FeeFloor() != 40*one || st.SweepOf(entry) == nil || Migrated() != "" || !st.Ledger.Get(raise).Pending() || hlmailbox.Delivered(fresh) != 0 {
		t.Fatalf("a refused proposal changed something")
	}
	asMember(cur)
	pass(cur, NewCancelRequest(cross(cur), raise))
	if st.Ledger.Get(raise).State() != "cancelled" {
		t.Fatalf("the governor's cancel")
	}
	if !strings.Contains(Render(""), "- governor: "+string(launchGovernor)+" (until the yield) — it alone decides, by Govern from its own key, and gno.land/r/gov/dao's proposals are refused\n") || !strings.Contains(Render("json"), "\"live\":true,\"governor\":\""+string(launchGovernor)+"\",\"yielded\":0,") {
		t.Fatalf("page: %s\n%s", Render(""), Render("json"))
	}
	// the yield, on the mailbox: this realm reads it
	testing.SetRealm(testing.NewUserRealm(launchGovernor))
	hlmailbox.Yield(cross(cur))
	h := hlmailbox.Yielded()
	if h == 0 || !strings.Contains(Render(""), "- governor: gno.land/r/gov/dao since height "+itoa(h)+" ("+string(launchGovernor)+" yielded)\n") || !strings.Contains(Render("json"), "\"governor\":\""+string(launchGovernor)+"\",\"yielded\":"+itoa(h)+",") {
		t.Fatalf("after the yield: %d\n%s", h, Render(""))
	}
	n := st.Ledger.Last()
	asMember(cur)
	req := NewSetCapFloorRequest(cross(cur), 13_000*one)
	testing.SetRealm(testing.NewUserRealm(launchGovernor))
	aborts(t, "hlbubble: the governor yielded at height "+itoa(h)+", gno.land/r/gov/dao governs since: a decision is a GovDAO proposal, never Govern", func() { Govern(cross(cur), req) })
	if st.Ledger.Last() != n {
		t.Fatalf("Govern recorded after the yield")
	}
	// GovDAO's proposal decides now (pass takes that path from here on): queued as the governor's was, then cancelled
	asMember(cur)
	pass(cur, NewSetCapFloorRequest(cross(cur), 13_000*one))
	raise = st.Ledger.Last()
	if d := st.Ledger.Get(raise); !d.Queued() || d.Due != d.Recorded+decisions.Delay || CapFloor() != 12_000*one {
		t.Fatalf("GovDAO's raise after the yield: %+v", d)
	}
	asMember(cur)
	pass(cur, NewCancelRequest(cross(cur), raise))
	if st.Ledger.Get(raise).State() != "cancelled" || CapFloor() != 12_000*one {
		t.Fatalf("GovDAO's cancel after the yield")
	}
	checkWaiting(t, "TestYield")
}

// TestRouteAddressSwept (R39): every route realm the mailbox registers is a realm of the bridge — a successor's
// address, which its page prints as the holder of the lock and a holder pastes as a deposit address, is refused
// as a send's recipient, and a burn to it is swept and credited to its owner by id, never credited as a claim and
// paid into a realm's account outside every counter. hlgnot7 stands for a successor (registered as a migrate's
// apply registers one); the messages are crafted under a verifier that takes anything.
func TestRouteAddressSwept(cur realm, t *testing.T) {
	anyPath := namespace + "hlanyverifier"
	testing.SetRealm(testing.NewCodeRealm(anyPath))
	hlmailbox.RegisterVerifier(cross(cur), anyVerifier{})
	asMember(cur)
	passMailbox(cur, hlmailbox.NewSetVerifierRequest(cross(cur), ethTest, anyPath))
	skipDays(7)
	asRelayer(cur)
	hlmailbox.Execute(cross(cur), decisionsOf(hlmailbox.Render("json")))
	if hlmailbox.ActiveVerifier(ethTest) != anyPath {
		t.Fatalf("verifier %s", hlmailbox.ActiveVerifier(ethTest))
	}
	succ := namespace + "hlgnot7"
	testing.SetRealm(testing.NewCodeRealm(hlgnotPath))
	hlmailbox.AddRoute(cross(cur), succ)
	sb, _ := hl.AddressToBytes32(chain.PackageAddress(succ))
	fund(cur, alice, 1_000*one)
	testing.SetRealm(testing.NewUserRealm(alice))
	bubble.Approve(cross(cur), Address(), 1_000*one)
	testing.SetRealm(testing.NewUserRealm(alice))
	aborts(t, "a realm of the bridge", func() { TransferRemote(cross(cur), ethTest, hex.EncodeToString(sb[:]), Minimum()) })
	// a burn of 150 to the successor's address: swept, owed, paying nobody
	self, _ := hl.AddressToBytes32(Address())
	m := hl.Message{Version: hl.Version, Nonce: 90, Origin: ethTest, Sender: st.Router, Destination: hlmailbox.LocalDomain(), Recipient: self, Body: hl.TokenMessage(sb, uint64(150*one), nil)}
	raw := m.Bytes()
	idb := hl.ID(raw)
	id := hex.EncodeToString(idb[:])
	owed0, lock0 := st.Owed, Locked()
	asRelayer(cur)
	Process(cross(cur), "", hex.EncodeToString(raw))
	if st.SweptOf(id) != 150*one || st.ClaimOf(id) != nil || st.Owed != owed0+150*one || Locked() != lock0 {
		t.Fatalf("a burn to a successor's address was not swept: swept %d, claim %v", st.SweptOf(id), st.ClaimOf(id))
	}
	// then credited to its owner by id: the entry gone, a claim of 150 to bob in its place
	asMember(cur)
	aborts(t, "outside the bridge's realms", func() { NewCreditRequest(cross(cur), id, chain.PackageAddress(succ), 150*one) })
	asMember(cur)
	pass(cur, NewCreditRequest(cross(cur), id, bob, 150*one))
	skipDays(7)
	asRelayer(cur)
	Execute(cross(cur), st.Ledger.Last())
	if c := st.ClaimOf(id); st.SweepOf(id) != nil || c == nil || c.To != bob || c.By != hlroute.ByVote || c.Amount != 150*one || st.Owed != owed0+150*one {
		t.Fatalf("the credit by id: %+v owed %d", c, st.Owed)
	}
	sent, paid, owed, voided, lost := Counters()
	if sent-paid != Locked() || owed != st.Owed || voided != 0 || lost != 0 {
		t.Fatalf("counters %d %d %d %d %d", sent, paid, owed, voided, lost)
	}
	// a DROPPED route's address stays a realm of the bridge (AT6): hlgnot's migration is re-pointed to hlgnot8, so
	// hlgnot7 is a route no more — a holder who pasted its address while its page printed it as the lock's holder
	// to be is swept and credited by id all the same, and a send to it is refused
	next := namespace + "hlgnot8"
	testing.SetRealm(testing.NewCodeRealm(hlgnotPath))
	hlmailbox.AddRoute(cross(cur), next)
	if hlmailbox.Dropped(succ) != hlgnotPath || hlmailbox.IsRoute(succ) || !hlmailbox.IsRoute(next) {
		t.Fatalf("hlgnot7 is not a dropped route: %q", hlmailbox.Dropped(succ))
	}
	testing.SetRealm(testing.NewUserRealm(alice))
	aborts(t, "a realm of the bridge", func() { TransferRemote(cross(cur), ethTest, hex.EncodeToString(sb[:]), Minimum()) })
	m = hl.Message{Version: hl.Version, Nonce: 91, Origin: ethTest, Sender: st.Router, Destination: hlmailbox.LocalDomain(), Recipient: self, Body: hl.TokenMessage(sb, uint64(120*one), nil)}
	raw = m.Bytes()
	idb = hl.ID(raw)
	id2 := hex.EncodeToString(idb[:])
	owed0 = st.Owed
	asRelayer(cur)
	Process(cross(cur), "", hex.EncodeToString(raw))
	if st.SweptOf(id2) != 120*one || st.ClaimOf(id2) != nil || st.Owed != owed0+120*one {
		t.Fatalf("a burn to a dropped route's address was not swept: swept %d, claim %v", st.SweptOf(id2), st.ClaimOf(id2))
	}
	asMember(cur)
	aborts(t, "outside the bridge's realms", func() { NewCreditRequest(cross(cur), id2, chain.PackageAddress(succ), 120*one) })
	asMember(cur)
	pass(cur, NewCreditRequest(cross(cur), id2, carol, 120*one))
	skipDays(7)
	asRelayer(cur)
	Execute(cross(cur), st.Ledger.Last())
	if c := st.ClaimOf(id2); st.SweepOf(id2) != nil || c == nil || c.To != carol || c.By != hlroute.ByVote || c.Amount != 120*one || st.Owed != owed0+120*one {
		t.Fatalf("the credit by id of the dropped route's burn: %+v owed %d", c, st.Owed)
	}
	sent, paid, owed, voided, lost = Counters()
	if sent-paid != Locked() || owed != st.Owed || voided != 0 || lost != 0 {
		t.Fatalf("counters %d %d %d %d %d", sent, paid, owed, voided, lost)
	}
	checkWaiting(t, "TestRouteAddressSwept")
}

// TestApplyRefund (AT2): a refund's apply is GovDAO's own at-once act, never Execute's — over a send a
// predecessor's page brought (carol's 300, handed to this realm as page 0 would, its BUBBLE with it), the
// enrolment note 14 days old (r/hlgov's own call, stood in for). Last, and past the 35 days TestBuilders ran to,
// since the bubble realm's checkpoints refuse a clock that goes backwards on an account (this realm's, stamped by
// the sweep payment there) and every test function's clock starts over.
func TestApplyRefund(cur realm, t *testing.T) {
	testing.SetRealm(testing.NewCodeRealm(hlgovPath))
	hlmailbox.NoteEnrolled(cross(cur), pkgPath, runtime.ChainHeight())
	skipDays(36)
	sendID := strings.Repeat("e3", 32)
	st.Import(now(), 0, hlmailbox.LaunchedAt(), &hlroute.Snapshot{Page: 0, Lock: 300 * one, SentTotal: 300 * one, Sends: []hlroute.SendItem{{ID: sendID, From: carol, Amount: 300 * one, At: now()}}})
	fund(cur, Address(), 300*one)
	asMember(cur)
	aborts(t, "decision 2 is of kind sweep, not refund: an apply refund names a queued refund decision", func() { NewApplyRefundRequest(cross(cur), 2) })
	asMember(cur)
	pass(cur, NewRefundRequest(cross(cur), sendID))
	refund := st.Ledger.Last()
	asMember(cur)
	aborts(t, "refund decision "+itoa(refund)+" is queued until", func() { NewApplyRefundRequest(cross(cur), refund) })
	skipDays(7)
	c0, l0 := bubble.BalanceOf(carol), Locked()
	asRelayer(cur)
	aborts(t, "a refund is applied by GovDAO's own at-once decision (apply refund) after hlwatch's Solana check, never by Execute", func() { Execute(cross(cur), refund) })
	if bubble.BalanceOf(carol) != c0 || Locked() != l0 || st.SendOf(sendID).Refunded != 0 || !st.Ledger.Get(refund).Pending() {
		t.Fatalf("Execute applied a refund: carol +%d locked %d", bubble.BalanceOf(carol)-c0, Locked())
	}
	asMember(cur)
	pass(cur, NewApplyRefundRequest(cross(cur), refund))
	if bubble.BalanceOf(carol)-c0 != 300*one || Locked() != l0-300*one || st.SendOf(sendID).Refunded == 0 || st.Ledger.Get(refund).State() != "applied" || !strings.Contains(Render("decisions"), "### apply refund") {
		t.Fatalf("the apply: carol +%d locked %d", bubble.BalanceOf(carol)-c0, Locked())
	}
	checkWaiting(t, "TestApplyRefund")
}

// TestSolanaRealmsSwept (E-M1's Q6), last: a burn whose recipient is one of the Solana deployment's six realms — the
// same operator namespace without eth/ — is credited to the sweep queue as one naming a realm of this deployment is,
// and GovDAO's credit by id and sweep payment refuse each as the recipient. The fee is first lowered at once to the
// launch fee, so the lock holds a burn the fee does not sweep: the same burn to carol is a claim (the control), and
// so a sweep below is the realm rule's — for the six, and for the registered and the dropped successor of
// TestRouteAddressSwept, whose burns there the fee of 3,000 BUBBLE swept on its own. The messages are crafted under
// the verifier that takes anything (TestRouteAddressSwept activated it).
func TestSolanaRealmsSwept(cur realm, t *testing.T) {
	if hlmailbox.ActiveVerifier(ethTest) != namespace+"hlanyverifier" {
		t.Fatalf("verifier %s", hlmailbox.ActiveVerifier(ethTest))
	}
	asMember(cur)
	pass(cur, NewSetFeeRequest(cross(cur), launchFee, Minimum()))
	amount := 500 * one
	if Fee() != launchFee || amount < Fee() || amount > Locked() {
		t.Fatalf("the burns carry %d: a claim's amount here must be at or above the fee %d and within the lock %d", amount, Fee(), Locked())
	}
	self, _ := hl.AddressToBytes32(Address())
	nonce := uint32(100)
	burn := func(to address) string {
		k, _ := hl.AddressToBytes32(to)
		m := hl.Message{Version: hl.Version, Nonce: nonce, Origin: ethTest, Sender: st.Router, Destination: hlmailbox.LocalDomain(), Recipient: self, Body: hl.TokenMessage(k, uint64(amount), nil)}
		nonce++
		raw := m.Bytes()
		asRelayer(cur)
		Process(cross(cur), "", hex.EncodeToString(raw))
		idb := hl.ID(raw)
		return hex.EncodeToString(idb[:])
	}
	if id := burn(carol); st.ClaimOf(id) == nil || st.SweptOf(id) != 0 {
		t.Fatalf("the control: a burn to carol was not a claim")
	}
	owed0, sweep0, lock0 := st.Owed, st.Sweep, Locked()
	const sol = "gno.land/r/g1leu8d2vsplhehcfkjg50mwgdpxdkt8tztu95wr/"
	for _, name := range []string{"hlmailbox", "hlverifier", "hlgnot", "hlbubble", "hlfees", "hlgov"} {
		a := chain.PackageAddress(sol + name)
		if isRealm(a) {
			t.Fatalf("the Solana deployment's %s is in this deployment's list: the sweep would not be the Solana rule's", name)
		}
		id := burn(a)
		if st.SweptOf(id) != amount || st.ClaimOf(id) != nil {
			t.Fatalf("a burn to the Solana deployment's %s was not swept: swept %d, claim %v", name, st.SweptOf(id), st.ClaimOf(id))
		}
		asMember(cur)
		aborts(t, "hlbubble: the recipient must be an address outside the bridge's realms", func() { NewCreditRequest(cross(cur), id, a, amount) })
		asMember(cur)
		aborts(t, "hlbubble: the recipient must be an address outside the bridge's realms", func() { NewSweepRequest(cross(cur), id, a) })
	}
	// the registered successor (hlgnot8) and the dropped one (hlgnot7) of TestRouteAddressSwept, now at a fee that
	// does not sweep the burn by itself (R39, AT6)
	for _, p := range []string{namespace + "hlgnot8", namespace + "hlgnot7"} {
		if id := burn(chain.PackageAddress(p)); st.SweptOf(id) != amount || st.ClaimOf(id) != nil {
			t.Fatalf("a burn to %s was not swept", p)
		}
	}
	if st.Owed != owed0+8*amount || st.Sweep != sweep0+8*amount || Locked() != lock0 {
		t.Fatalf("the eight swept: owed %d (was %d), sweep %d (was %d), lock %d (was %d)", st.Owed, owed0, st.Sweep, sweep0, Locked(), lock0)
	}
	sent, paid, owed, voided, lost := Counters()
	if sent-paid != Locked() || owed != st.Owed || lost != 0 {
		t.Fatalf("counters %d %d %d %d %d", sent, paid, owed, voided, lost)
	}
	checkWaiting(t, "TestSolanaRealmsSwept")
}

// TestUnpayableSwept (the round-2 realms audit's E2-RL3; its proof of concept, inverted): a burn whose recipient is
// this deployment's own hlverifier (in no route's list until then, M1-NOTES' F-3) or the zero address (a burn whose
// recipient field was left empty: Hyperlane's transferRemote takes any bytes32) is swept, as one naming a realm of
// the bridge is — never a claim paid into an account nothing moves BUBBLE out of — at an amount the same burn to carol
// takes as a claim (the control); GovDAO's credit by id and sweep payment refuse each as the recipient; a send to
// hlverifier's twenty bytes is refused as a realm of the bridge. Crafted under the verifier that takes anything.
func TestUnpayableSwept(cur realm, t *testing.T) {
	skipDays(120) // past every account checkpoint the earlier test functions stamped (the bubble realm's clock rule)
	// a send first, so the lock holds the burns whatever the fee
	fund(cur, alice, Minimum()+Fee())
	testing.SetRealm(testing.NewUserRealm(alice))
	bubble.Approve(cross(cur), Address(), Minimum()+Fee())
	testing.SetRealm(testing.NewUserRealm(alice))
	TransferRemote(cross(cur), ethTest, ethWallet, Minimum())
	amount := Fee() + one
	if amount > Locked() {
		t.Fatalf("the burns carry %d: a claim's amount here must be within the lock %d", amount, Locked())
	}
	self, _ := hl.AddressToBytes32(Address())
	nonce := uint32(200)
	burn := func(k [32]byte) string {
		m := hl.Message{Version: hl.Version, Nonce: nonce, Origin: ethTest, Sender: st.Router, Destination: hlmailbox.LocalDomain(), Recipient: self, Body: hl.TokenMessage(k, uint64(amount), nil)}
		nonce++
		raw := m.Bytes()
		asRelayer(cur)
		Process(cross(cur), "", hex.EncodeToString(raw))
		idb := hl.ID(raw)
		return hex.EncodeToString(idb[:])
	}
	kc, _ := hl.AddressToBytes32(carol)
	if id := burn(kc); st.ClaimOf(id) == nil || st.SweptOf(id) != 0 {
		t.Fatalf("the control: a burn to carol was not a claim")
	}
	owed0, sweep0, lock0 := st.Owed, st.Sweep, Locked()
	ver := chain.PackageAddress(namespace + "hlverifier")
	zero, _ := hl.Bytes32ToAddress([32]byte{})
	for _, a := range []address{ver, zero} {
		k, _ := hl.AddressToBytes32(a)
		id := burn(k)
		if st.SweptOf(id) != amount || st.ClaimOf(id) != nil {
			t.Fatalf("a burn to %s was not swept: swept %d, claim %v", a, st.SweptOf(id), st.ClaimOf(id))
		}
		asMember(cur)
		aborts(t, "hlbubble: the recipient must be an address outside the bridge's realms", func() { NewCreditRequest(cross(cur), id, a, amount) })
		asMember(cur)
		aborts(t, "hlbubble: the recipient must be an address outside the bridge's realms", func() { NewSweepRequest(cross(cur), id, a) })
	}
	if st.Owed != owed0+2*amount || st.Sweep != sweep0+2*amount || Locked() != lock0 {
		t.Fatalf("the two swept: owed %d (was %d), sweep %d (was %d), lock %d (was %d)", st.Owed, owed0, st.Sweep, sweep0, Locked(), lock0)
	}
	vb, _ := hl.AddressToBytes32(ver)
	testing.SetRealm(testing.NewUserRealm(alice))
	aborts(t, "hlbubble: the recipient is a realm of the bridge", func() { TransferRemote(cross(cur), ethTest, hex.EncodeToString(vb[:]), Minimum()) })
	sent, paid, owed, _, lost := Counters()
	if sent-paid != Locked() || owed != st.Owed || lost != 0 {
		t.Fatalf("counters %d %d %d %d", sent, paid, owed, lost)
	}
	checkWaiting(t, "TestUnpayableSwept")
}

// decisionsOf reads a realm's "decisions" count out of its json: the id of its newest decision.
func decisionsOf(json string) int64 {
	s := json[strings.Index(json, "\"decisions\":")+len("\"decisions\":"):]
	end := 0
	for end < len(s) && s[end] >= '0' && s[end] <= '9' {
		end++
	}
	n, _ := strconv.ParseInt(s[:end], 10, 64)
	return n
}

// TestRouterSwapTargets (the round-2 realms audit's E2-RL7): a router swap never names a contract of the deployment
// but this route's own token — the other token, the mailbox, the client, the gate, HlAdmin and the rest of
// hlmailbox's sixteen mint nothing for this route, so every send after the swap would strand — while the BUBBLE
// token itself, which a swap back names, is taken (or refused as the router already); nothing is recorded.
func TestRouterSwapTargets(cur realm, t *testing.T) {
	n0 := st.Ledger.Last()
	for i := 0; i < hlmailbox.EthContracts; i++ {
		name, k := hlmailbox.EthContractAt(i)
		a := hlmailbox.EthAddress(k)
		asMember(cur)
		if i == hlmailbox.EthToken1 {
			if k == st.Router {
				aborts(t, "that is the router already", func() { NewSetRouterRequest(cross(cur), a) })
			} else if d := NewSetRouterRequest(cross(cur), a).Description(); d != "set router to "+a {
				t.Fatalf("a swap back to the BUBBLE token: %s", d)
			}
			continue
		}
		aborts(t, "hlbubble: the router "+a+" is "+name+" of this deployment, not the BUBBLE token: every send after the swap would strand undelivered, and the token's burns would be refused once the old router's 7 days end", func() { NewSetRouterRequest(cross(cur), strings.ToLower(a)) })
	}
	if st.Ledger.Last() != n0 {
		t.Fatalf("a refused swap recorded something: %d", st.Ledger.Last())
	}
	checkWaiting(t, "TestRouterSwapTargets")
}

// spellings are the four spellings of a gno address IsValid takes: as gno writes it (a), all in upper case, and with
// bech32m's checksum (BIP-350) in lower case and in upper (eth F9, its review's finding 1).
func spellings(a address) []address {
	b, err := hl.AddressToBytes32(a)
	if err != nil {
		panic(err)
	}
	data, err := bech32.ConvertBits(b[12:], 8, 5, true)
	if err != nil {
		panic(err)
	}
	m, err := bech32.EncodeM("g", data)
	if err != nil {
		panic(err)
	}
	return []address{a, address(strings.ToUpper(string(a))), address(m), address(strings.ToUpper(m))}
}

// TestVotesCanonical (the F6 review's follow-up; F9's review's finding 1): the credit and sweep votes read the address
// a member typed in its canonical spelling (hlmailbox.CanonicalAddress) before comparing it with the realm lists —
// IsValid takes four spellings of the same twenty bytes (spellings: as gno writes it, upper case, bech32m's checksum
// in either case). In each of them, each realm of this route's list, every route the mailbox registers, the Solana
// deployment's six and the zero address are refused by both votes, nothing recorded; an owner typed in each other
// spelling is named as gno writes it by both, and paid there — BUBBLE's ledger keys an account by its string, so a
// payment under another spelling would sit under a key nobody spends (a sweep of a zero burn's entry to carol,
// crafted under the verifier that takes anything).
func TestVotesCanonical(cur realm, t *testing.T) {
	skipDays(150) // past every account checkpoint the earlier test functions stamped (the bubble realm's clock rule)
	const sol = "gno.land/r/g1leu8d2vsplhehcfkjg50mwgdpxdkt8tztu95wr/"
	amount := Fee() + one
	if amount > Locked() {
		t.Fatalf("the burn carries %d: within the lock %d, it is swept for its recipient alone", amount, Locked())
	}
	self, _ := hl.AddressToBytes32(Address())
	m := hl.Message{Version: hl.Version, Nonce: 300, Origin: ethTest, Sender: st.Router, Destination: hlmailbox.LocalDomain(), Recipient: self, Body: hl.TokenMessage([32]byte{}, uint64(amount), nil)}
	raw := m.Bytes()
	asRelayer(cur)
	Process(cross(cur), "", hex.EncodeToString(raw))
	idb := hl.ID(raw)
	id := hex.EncodeToString(idb[:])
	if st.SweptOf(id) != amount {
		t.Fatalf("the zero burn was not swept: %d", st.SweptOf(id))
	}
	refused := append([]address{zeroAddress}, realms...)
	for _, p := range hlmailbox.Routes() {
		refused = append(refused, chain.PackageAddress(p))
	}
	for _, name := range []string{"hlmailbox", "hlverifier", "hlgnot", "hlbubble", "hlfees", "hlgov"} {
		refused = append(refused, chain.PackageAddress(sol+name))
	}
	n0 := st.Ledger.Last()
	refuses := func(vote string, to address, f func()) { // the vote refuses the address as its recipient; a failure names it
		t.Helper()
		asMember(cur)
		if revive(f) == nil {
			t.Fatalf("the %s vote took %s as its recipient", vote, to)
		}
		asMember(cur)
		aborts(t, "hlbubble: the recipient must be an address outside the bridge's realms", f)
	}
	for _, a := range refused {
		for i, to := range spellings(a) {
			if !to.IsValid() || i > 0 && to == a {
				t.Fatalf("%s's spelling %d, %s, is no other spelling IsValid takes", a, i, to)
			}
			refuses("credit", to, func() { NewCreditRequest(cross(cur), id, to, amount) })
			refuses("sweep", to, func() { NewSweepRequest(cross(cur), id, to) })
		}
	}
	if st.Ledger.Last() != n0 {
		t.Fatalf("a refused vote recorded something: %d", st.Ledger.Last())
	}
	// the owner in each other spelling: named as gno writes it by both votes, and paid to the account carol spends,
	// nothing under another key (typed with bech32m's checksum in upper case)
	others := spellings(carol)[1:]
	for _, a := range others {
		asMember(cur)
		if d := NewCreditRequest(cross(cur), id, a, amount).Description(); !strings.HasPrefix(d, "credit "+id+" to "+string(carol)+" for ") {
			t.Fatalf("a credit to %s: %s", a, d)
		}
		asMember(cur)
		if d := NewSweepRequest(cross(cur), id, a).Description(); !strings.HasPrefix(d, "pay ") || !strings.HasSuffix(d, " of the sweep queue to "+string(carol)+": "+id) {
			t.Fatalf("a sweep to %s: %s", a, d)
		}
	}
	asMember(cur)
	req := NewSweepRequest(cross(cur), id, others[2])
	c0 := bubble.BalanceOf(carol)
	o0 := make([]int64, len(others))
	for i, a := range others {
		o0[i] = bubble.BalanceOf(a)
	}
	pass(cur, req)
	skipDays(7)
	asRelayer(cur)
	Execute(cross(cur), st.Ledger.Last())
	if bubble.BalanceOf(carol)-c0 != amount || st.SweepOf(id) != nil {
		t.Fatalf("the sweep to carol: +%d, entry %v", bubble.BalanceOf(carol)-c0, st.SweepOf(id))
	}
	for i, a := range others {
		if bubble.BalanceOf(a) != o0[i] {
			t.Fatalf("BUBBLE under the key %s: %d, was %d", a, bubble.BalanceOf(a), o0[i])
		}
	}
	sent, paid, owed, _, lost := Counters()
	if sent-paid != Locked() || owed != st.Owed || lost != 0 {
		t.Fatalf("counters %d %d %d %d", sent, paid, owed, lost)
	}
	checkWaiting(t, "TestVotesCanonical")
}
