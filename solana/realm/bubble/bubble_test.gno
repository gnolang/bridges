package bubble

import (
	"chain/runtime"
	"strings"
	"testing"
	"time"

	governor "gno.land/p/g1leu8d2vsplhehcfkjg50mwgdpxdkt8tztu95wr/governor/v0"
	grc20votes "gno.land/p/g1leu8d2vsplhehcfkjg50mwgdpxdkt8tztu95wr/grc20votes/v0"
)

// The clock: kourt's r/govern harness. gno resets the height between tests and the checkpoints refuse a
// backwards write, so every test resumes from a high-water mark and moves BOTH height and time together.
var testHeight int64

const clockBaseUnix = int64(1_234_567_890)

func setClock(h int64) {
	ctx := testing.GetContext()
	ctx.Height = h
	ctx.Time = time.Unix(clockBaseUnix+h*5, 0)
	testing.SetContext(ctx)
}

func advanceBlocks(n int64) {
	if h := runtime.ChainHeight(); h > testHeight {
		testHeight = h
	}
	testHeight += n
	setClock(testHeight)
}

func resumeClock()             { advanceBlocks(0) }
func advanceEpochs(n int64)    { advanceBlocks(n * epochBlocks) }
func advanceDays(n int64)      { advanceBlocks(n * 86400 / 5) }

var (
	jae    = address("g1leu8d2vsplhehcfkjg50mwgdpxdkt8tztu95wr") // the deployer in life
	keeper = address("g18dd5mstjqlcg9ngqpg26azeya6wqg7upd9t8xa")
	alice  = address("g1jg8mtutu9khhfwc4nxmuhcpftf0pajdhfvsqf5")
	bob    = address("g1dmt3sa5ucvecxuhf3j6ne5r0e3z4x7h6c03xc0")
	carol  = address("g1akeqsvhucjt8gf5yupyzjxsjd29wv8fayng37c")
)

// reset gives a test the realm as deployed by jae, with the premine in, and the keeper named minter.
func reset() {
	resumeClock()
	ledger = grc20votes.NewLedger(tokenName, tokenSymbol, tokenDecimals, epochBlocks)
	ledger.SetID(tokenID)
	engine = governor.New(ledger, ledger)
	installKinds()
	admin, minter, paused, dayCap = jae, keeper, false, 250_000*one
	premined, referralMinted, reserveMinted, mintedDay, dayKey, lastBatch, batches, reserveLast = 0, 0, 0, 0, 0, "", 0, 0
	turnouts, noted, askedKinds = nil, map[int64]bool{}, map[int64]string{}
	ledger.Mint(premineTo, premine)
	premined = premine
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

func TestPremineAndIdentity(cur realm, t *testing.T) {
	reset()
	if GetName() != "Bubble" || GetSymbol() != "BUBBLE" || GetDecimals() != 6 {
		t.Fatalf("identity: %s %s %d", GetName(), GetSymbol(), GetDecimals())
	}
	if TotalSupply() != premine || BalanceOf(premineTo) != premine || Premined() != premine {
		t.Fatalf("premine: supply %d, jae %d", TotalSupply(), BalanceOf(premineTo))
	}
	if premine+ReferralPool+Reserve != MaxSupply {
		t.Fatalf("the pools do not add up to the max")
	}
	if VotesOf(premineTo) != premine {
		t.Fatalf("the premine votes itself: %d", VotesOf(premineTo))
	}
}

func TestAdminHandsAndRenounces(cur realm, t *testing.T) {
	reset()
	testing.SetRealm(testing.NewUserRealm(alice))
	aborts(t, "only the admin", func() { SetMinter(cross(cur), alice) })
	testing.SetRealm(testing.NewUserRealm(jae))
	SetAdmin(cross(cur), keeper)
	if Admin() != keeper {
		t.Fatalf("admin: %s", Admin())
	}
	testing.SetRealm(testing.NewUserRealm(jae))
	aborts(t, "only the admin", func() { SetPaused(cross(cur), true) })
	testing.SetRealm(testing.NewUserRealm(keeper))
	SetDayCap(cross(cur), 50_000*one)
	aborts(t, "only lower", func() { SetDayCap(cross(cur), 60_000*one) })
	RenounceAdmin(cross(cur))
	if Admin() != "" {
		t.Fatalf("admin not gone: %s", Admin())
	}
	testing.SetRealm(testing.NewUserRealm(keeper))
	aborts(t, "given up", func() { SetAdmin(cross(cur), keeper) })
	// the mint goes on under the minter
	Mint(cross(cur), alice, 100*one)
	if BalanceOf(alice) != 100*one {
		t.Fatalf("alice: %d", BalanceOf(alice))
	}
}

func TestMintWalls(cur realm, t *testing.T) {
	reset()
	testing.SetRealm(testing.NewUserRealm(alice))
	aborts(t, "only the minter", func() { Mint(cross(cur), alice, one) })
	testing.SetRealm(testing.NewUserRealm(jae)) // the admin may mint too, until it is gone
	Mint(cross(cur), alice, one)
	testing.SetRealm(testing.NewUserRealm(keeper))
	aborts(t, "between 1 and", func() { Mint(cross(cur), alice, 0) })
	aborts(t, "between 1 and", func() { Mint(cross(cur), alice, MaxEntry+1) })
	aborts(t, "not an address", func() { Mint(cross(cur), address("nobody"), one) })
	testing.SetRealm(testing.NewUserRealm(jae))
	SetPaused(cross(cur), true)
	testing.SetRealm(testing.NewUserRealm(keeper))
	aborts(t, "paused", func() { Mint(cross(cur), alice, one) })
	testing.SetRealm(testing.NewUserRealm(jae))
	SetPaused(cross(cur), false)
	// the day cap: 250,000 a day, in 50,000 entries
	testing.SetRealm(testing.NewUserRealm(keeper))
	for i := 0; i < 4; i++ {
		Mint(cross(cur), bob, MaxEntry)
	}
	Mint(cross(cur), carol, MaxEntry-one) // one BUBBLE was minted to alice already today
	aborts(t, "over today's cap", func() { Mint(cross(cur), carol, one) })
	minted, ceiling := MintedToday()
	if minted != ceiling {
		t.Fatalf("today: %d of %d", minted, ceiling)
	}
	advanceDays(1)
	Mint(cross(cur), carol, one)
	if m, _ := MintedToday(); m != one {
		t.Fatalf("a new day did not reset: %d", m)
	}
	if ReferralMinted() != 250_000*one+one || TotalSupply() != premine+ReferralMinted() {
		t.Fatalf("pool accounting: %d, supply %d", ReferralMinted(), TotalSupply())
	}
	// nobody as minter
	testing.SetRealm(testing.NewUserRealm(jae))
	SetMinter(cross(cur), "")
	testing.SetRealm(testing.NewUserRealm(keeper))
	aborts(t, "nobody may mint", func() { Mint(cross(cur), alice, one) })
}

func TestMintManyAllOrNothing(cur realm, t *testing.T) {
	reset()
	testing.SetRealm(testing.NewUserRealm(keeper))
	aborts(t, "eight digits", func() { MintMany(cross(cur), "1", alice.String()+":1") })
	aborts(t, "eight digits", func() { MintMany(cross(cur), "2026092a", alice.String()+":1") })
	MintMany(cross(cur), "20260925", alice.String()+":"+itoa(5*one)+","+bob.String()+":"+itoa(3*one))
	if BalanceOf(alice) != 5*one || BalanceOf(bob) != 3*one || Batches() != 1 || LastBatch() != "20260925" {
		t.Fatalf("batch: alice %d bob %d n %d last %s", BalanceOf(alice), BalanceOf(bob), Batches(), LastBatch())
	}
	aborts(t, "not after", func() { MintMany(cross(cur), "20260925", alice.String()+":1") })
	aborts(t, "not after", func() { MintMany(cross(cur), "20260101", alice.String()+":1") })
	// a bad third entry: the first two are not minted either (the abort undoes the transaction; here the test sees the panic)
	aborts(t, "entry 3 "+carol.String()+": an entry is between", func() {
		MintMany(cross(cur), "20260926", alice.String()+":"+itoa(one)+","+bob.String()+":"+itoa(one)+","+carol.String()+":"+itoa(dayCap))
	})
	aborts(t, "entry 2 "+alice.String()+": twice", func() {
		MintMany(cross(cur), "20260926", alice.String()+":1,"+alice.String()+":1")
	})
	aborts(t, "entry 1: expected", func() { MintMany(cross(cur), "20260926", "junk") })
	aborts(t, "bad amount", func() { MintMany(cross(cur), "20260926", alice.String()+":x") })
	aborts(t, "1 to 100 entries", func() { MintMany(cross(cur), "20260926", "") })
	if Batches() != 1 {
		t.Fatalf("a refused batch counted: %d", Batches())
	}
	// five full entries fill a fresh day; the sixth is over the cap and the whole batch is refused (last, because the test harness keeps what a panicked call wrote)
	advanceDays(1)
	full := ""
	for _, a := range []address{alice, bob, carol, jae, keeper} {
		full += a.String() + ":" + itoa(MaxEntry) + ","
	}
	aborts(t, "entry 6 "+premineTo.String()+": over today's cap", func() {
		MintMany(cross(cur), "20260926", full+premineTo.String()+":1")
	})
}

func TestReferralPoolEnds(cur realm, t *testing.T) {
	reset()
	testing.SetRealm(testing.NewUserRealm(jae))
	SetDayCap(cross(cur), 100_000*one) // unchanged; the pool is the wall here
	referralMinted = ReferralPool - one // as if every era had paid out
	testing.SetRealm(testing.NewUserRealm(keeper))
	Mint(cross(cur), alice, one)
	aborts(t, "referral pool is spent", func() { Mint(cross(cur), alice, one) })
	if ReserveMinted() != 0 {
		t.Fatalf("the reserve moved: %d", ReserveMinted())
	}
}

// stake gives the premine address n more BUBBLE straight from the ledger (as if referrals had paid out), so a vote
// can meet the absolute floors (kinds.gno): 20M for a setting, 33M for the reserve.
func stake(n int64) { ledger.Mint(premineTo, n); referralMinted += n }

// pass drives a proposal through: proposed by the premine address, voted yes with it, settled, delayed, executed.
func pass(cur realm, t *testing.T, kind, payload string) int64 {
	t.Helper()
	if BalanceOf(premineTo) < 35_000_000*one {
		stake(35_000_000*one - BalanceOf(premineTo))
	}
	advanceEpochs(2) // a sealed epoch with the stake in it
	testing.SetRealm(testing.NewUserRealm(premineTo))
	id := Propose(cross(cur), kind, payload, "test: "+kind)
	testing.SetRealm(testing.NewUserRealm(premineTo))
	Vote(cross(cur), id, "yes")
	r, _ := KindRules(kind)
	advanceBlocks(r.VotingBlocks + 1)
	testing.SetRealm(testing.NewUserRealm(alice))
	Settle(cross(cur), id)
	if s := State(id); s != "succeeded" {
		t.Fatalf("%s: state %s, want succeeded", kind, s)
	}
	advanceBlocks(r.DelayBlocks + 1)
	testing.SetRealm(testing.NewUserRealm(alice))
	Execute(cross(cur), id)
	return id
}

func TestGovernanceTakesTheAdminsPowers(cur realm, t *testing.T) {
	reset()
	id := pass(cur, t, "govern:minter", alice.String())
	if State(id) != "executed" || Minter() != alice {
		t.Fatalf("minter by vote: %s, minter %s", State(id), Minter())
	}
	pass(cur, t, "govern:pause", "on")
	if !Paused() {
		t.Fatalf("not paused")
	}
	pass(cur, t, "govern:pause", "off")
	pass(cur, t, "govern:daycap", "250000")
	if DayCap() != 250_000*one {
		t.Fatalf("day cap: %d", DayCap())
	}
	pass(cur, t, "govern:admin", none)
	if Admin() != "" {
		t.Fatalf("admin not destroyed: %s", Admin())
	}
	testing.SetRealm(testing.NewUserRealm(alice))
	Mint(cross(cur), bob, one) // the vote's minter mints
	if BalanceOf(bob) != one {
		t.Fatalf("bob: %d", BalanceOf(bob))
	}
}

func TestReserveMovesOnlyByVote(cur realm, t *testing.T) {
	reset()
	if r, ok := KindRules("govern:reserve"); !ok || r.QuorumBps != 2000 || r.ThresholdBps != 6600 {
		t.Fatalf("reserve rules: %v %v", r, ok)
	}
	before := ReferralMinted()
	pass(cur, t, "govern:reserve", carol.String()+" 1000")
	if BalanceOf(carol) != 1000*one || ReserveMinted() != 1000*one || ReferralMinted() != before+35_000_000*one-premine {
		t.Fatalf("reserve: carol %d, reserve %d, referrals %d", BalanceOf(carol), ReserveMinted(), ReferralMinted())
	}
	advanceEpochs(2)
	testing.SetRealm(testing.NewUserRealm(premineTo))
	aborts(t, "between 1 and 2000000", func() { Propose(cross(cur), "govern:reserve", carol.String()+" 2000001", "too much") })
	aborts(t, "expected \"g1", func() { Propose(cross(cur), "govern:reserve", "1000", "no address") })
	aborts(t, "less than ninety days ago", func() { Propose(cross(cur), "govern:reserve", carol.String()+" 1000", "too soon") })
	advanceDays(91)
	pass(cur, t, "govern:reserve", carol.String()+" 2000000")
	if ReserveMinted() != 2_001_000*one {
		t.Fatalf("reserve after the gap: %d", ReserveMinted())
	}
}

func TestTheBarFollowsTurnout(cur realm, t *testing.T) {
	reset() // kourt's terms: a fifth of the supply turns out; with nothing decided yet the bar is that fifth
	advanceEpochs(2)
	if f, want := quorumFloor("govern:minter"), TotalSupply()/5; f != want {
		t.Fatalf("the first bar: %d, want a fifth %d", f, want)
	}
	if quorumFloor("govern:signal") != 0 {
		t.Fatalf("a signal has a bar")
	}
	// the premine (all of the supply) decides alone at launch — kourt's system, Jae's call; the admin is his cold key
	id := pass(cur, t, "govern:minter", alice.String())
	if State(id) != "executed" || Minter() != alice {
		t.Fatalf("launch vote: %s, minter %s", State(id), Minter())
	}
	// that vote turned out 35M of 45M: the bar rises to the mean turnout, capped at a third of the supply
	if MeanTurnout() != BalanceOf(premineTo) {
		t.Fatalf("mean turnout: %d, want %d", MeanTurnout(), BalanceOf(premineTo))
	}
	advanceEpochs(2)
	if f, want := quorumFloor("govern:pause"), TotalSupply()/3; f != want {
		t.Fatalf("the bar after a big vote: %d, want a third %d", f, want)
	}
	// a signal's turnout is not remembered
	pass(cur, t, "govern:signal", "hello")
	if len(turnouts) != 1 {
		t.Fatalf("a signal was noted: %v", turnouts)
	}
}

func TestSignalAndRender(cur realm, t *testing.T) {
	reset()
	id := pass(cur, t, "govern:signal", "should the moon wear a hat")
	if State(id) != "executed" || TotalSupply() != premine+ReferralMinted() || ReserveMinted() != 0 {
		t.Fatalf("signal: %s, supply %d (a signal mints nothing)", State(id), TotalSupply())
	}
	advanceEpochs(2)
	testing.SetRealm(testing.NewUserRealm(premineTo))
	aborts(t, "control characters", func() { Propose(cross(cur), "govern:signal", "a\nb", "sneaky") })
	page := Render("")
	for _, want := range []string{"BUBBLE", "supply: " + whole(TotalSupply()) + " of 100000000", "admin: " + jae.String(), "minter: " + keeper.String(), "turnout bar follows the holders"} {
		if !strings.Contains(page, want) {
			t.Fatalf("page lacks %q:\n%s", want, page)
		}
	}
	j := Render("json")
	for _, want := range []string{"\"symbol\":\"BUBBLE\"", "\"premine\":" + itoa(premine), "\"dayCap\":" + itoa(250_000*one), "\"minter\":\"" + keeper.String() + "\""} {
		if !strings.Contains(j, want) {
			t.Fatalf("json lacks %q: %s", want, j)
		}
	}
	me := Render("me/" + premineTo.String())
	if !strings.Contains(me, "\"balance\":"+itoa(BalanceOf(premineTo))) || !strings.Contains(me, "\"votes\":"+itoa(VotesOf(premineTo))) {
		t.Fatalf("me: %s", me)
	}
}

func TestTransfersAndDelegation(cur realm, t *testing.T) {
	reset()
	testing.SetRealm(testing.NewUserRealm(premineTo))
	Transfer(cross(cur), alice, 10*one)
	testing.SetRealm(testing.NewUserRealm(alice))
	Approve(cross(cur), bob, 4*one)
	testing.SetRealm(testing.NewUserRealm(bob))
	TransferFrom(cross(cur), alice, carol, 4*one)
	testing.SetRealm(testing.NewUserRealm(alice))
	Delegate(cross(cur), carol)
	if BalanceOf(alice) != 6*one || BalanceOf(carol) != 4*one || VotesOf(carol) != 10*one || VotesOf(alice) != 0 {
		t.Fatalf("alice %d carol %d votes carol %d alice %d", BalanceOf(alice), BalanceOf(carol), VotesOf(carol), VotesOf(alice))
	}
	testing.SetRealm(testing.NewUserRealm(carol))
	Burn(cross(cur), one)
	if TotalSupply() != premine-one {
		t.Fatalf("burn: %d", TotalSupply())
	}
}
