package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/ethereum/go-ethereum/common"

	"github.com/gnolang/bridges/ethereum/bridge/ethx"
)

// The launch values the deployment is held to (E3-GS4): the record is the deployer's, so each value is taken from a
// source independent of the deployer where one exists, said with its source; the record's stands only where none
// does, said so. a.launch is the record's launch with each such value in its place — what the creation inputs, the
// masked immutables and the state are held to (the state through the governed changes' replay, audit_line.go).
//
//   - the realms' paths: the member's -mailbox-pkgpath, -hlgov, -gnot-route, -bubble-route (nothing on chain names
//     the bridge's namespace); without one the binding is UNVERIFIED (bindings).
//   - the trust root: gno.land's own block and validators at its height (trustRoot); without a gno source UNVERIFIED.
//   - the start sequence: r/hlgov's compiled start while HlAdmin is its first decision recipient (json "start",
//     "recipient", "former"), its counter in every case (counter); while r/hlgov is not up, -start-sequence or a
//     chain's first deployment's 0 (§5.8 step 7) — never the record's.
//   - the seats and watch slots: -seats and -watch (the published keys); r/hlgov's Ethereum seats, as it last set them,
//     against the deployment's once it is up (seatChecks).
//   - the mint limits: the member's -expected-lock-gnot and -expected-lock-bubble compile them exactly (§5.4); without
//     a lock §5.4's ceilings bound them, UNVERIFIED. A config's routes' expectedLock is the operator's word
//     (bridge.json is the machines' config, the operator's file) and is only noted beside them, never taken (E5-CF3
//     item 3: a member who passed the operator's config held the caps to the operator's lock — E4-TL1's threat by
//     another door); the routes' maximums, once they are up, are never above the gate's.
//   - the beneficiary: -beneficiary (the treasurer's published Ethereum address). No realm records it — r/hlfees keeps
//     the treasurer's gno address — and the fee is 0 at launch: the kind 7 that raises it names its beneficiary.
//
// Each of these is the LAUNCH value — what the creation inputs carry and what the governed changes are replayed from
// (audit_line.go) — never the value as it stands after a kind 5, 7 or 12: the launch notice's. Without -seats and
// -watch, -beneficiary, or a token's expected lock the verdict is UNVERIFIED, never PASS (E4-TL1): a deployer's own
// guardians and watch keys, its own beneficiary and the ceilings' day cap agree with its own record everywhere, and
// before the realms go up nothing else holds them.

// launchFlags names the member's flags that carry a launch value, as the help and the failures name them (E4-TL6).
const launchFlags = "-seats, -watch, -beneficiary and -expected-lock-gnot/-bubble are the LAUNCH values (the launch notice's: what the creation inputs carry; a kind 5, 7 or 12 since is replayed from them), never the values as they stand"

// expectations fixes a.launch, each value with its source.
func (a *audit) expectations(ctx context.Context) {
	l := a.d.Launch
	l.MailboxPkgPath, l.Hlgov, l.GnotRoute, l.BubbleRoute = a.pkgPath, a.hlgovPath, a.routePath[0], a.routePath[1]
	src := func(what, value, from string) { fmt.Fprintf(a.out, "     %s: %s — %s\n", what, value, from) }
	differs := func(what string, rec, want any) {
		if fmt.Sprint(rec) != fmt.Sprint(want) {
			fmt.Fprintf(a.out, "     NOTE the record's %s is %v; %v is audited\n", what, rec, want)
			a.launchDiffers = append(a.launchDiffers, what)
		}
	}
	admin := a.d.Addr("hlAdmin")

	// the start sequence
	gov := a.govRead(ctx)
	switch g := gov.Gov; {
	case a.cfg.StartSequence != nil:
		l.StartSequence = *a.cfg.StartSequence
		src("the start sequence", fmt.Sprint(l.StartSequence), "-start-sequence")
	case g != nil && strings.EqualFold(g.Recipient, admin.Hex()) && len(g.Former) == 0:
		l.StartSequence = g.Start
		src("the start sequence", fmt.Sprint(l.StartSequence), "r/hlgov's own compiled start (its json: HlAdmin is its first and only decision recipient, so HlAdmin takes r/hlgov's first number, start + 1)")
	case g != nil:
		src("the start sequence", fmt.Sprint(l.StartSequence), fmt.Sprintf("the deployment's record — HlAdmin is not r/hlgov's first recipient (its recipient %s, former %v), so no reading pins where it started; r/hlgov's counter is held to HlAdmin's below", g.Recipient, g.Former))
	case gov.Err != nil && len(a.gnos) > 0:
		a.fail("r/hlgov's json (its counter, start and decision recipient)", gov.Err)
		src("the start sequence", fmt.Sprint(l.StartSequence), "the deployment's record (r/hlgov did not read)")
	default:
		l.StartSequence = 0
		why := "r/hlgov is not on the chain yet"
		if len(a.gnos) == 0 {
			why = "no gno source to read r/hlgov from"
		}
		src("the start sequence", "0", "a chain's first deployment's (§5.8 step 7: "+why+"; -start-sequence for a fresh deployment under a live r/hlgov's predecessor)")
	}
	differs("start sequence", a.d.Launch.StartSequence, l.StartSequence)

	// the seats and watch slots
	if a.cfg.Seats != "" || a.cfg.Watch != "" {
		seats, err1 := addrs4(a.cfg.Seats)
		watch, err2 := addrs2(a.cfg.Watch)
		if err1 != nil || err2 != nil {
			a.check(false, "-seats and -watch: four and two addresses (%v %v)", err1, err2)
		} else {
			for i, s := range seats {
				l.Seats[i] = s.Hex()
			}
			for i, w := range watch {
				l.Watch[i] = w.Hex()
			}
			src("the seats and watch slots", addrList(append(seats[:], watch[:]...)), "-seats and -watch")
		}
	} else {
		src("the seats and watch slots", strings.Join(append(l.Seats[:], l.Watch[:]...), ","), "the deployment's record: give -seats and -watch (the published keys) to hold them; r/hlgov's Ethereum seats are compared with the deployment's below once it is up")
		a.unverify("the guardians and watch keys at launch (%s): the deployment's record alone — give -seats and -watch, the launch notice's published keys (E4-TL1: a deployer's own keys agree with its own record everywhere, and r/hlgov's seats are read only once the realms are up)", strings.Join(append(l.Seats[:], l.Watch[:]...), ","))
	}
	differs("seats and watch slots (-seats, -watch)", strings.Join(append(a.d.Launch.Seats[:], a.d.Launch.Watch[:]...), ","), strings.Join(append(l.Seats[:], l.Watch[:]...), ","))

	// the beneficiary
	if a.cfg.Beneficiary != "" {
		b, err := ethx.ParseAddress(a.cfg.Beneficiary)
		if err != nil {
			a.check(false, "-beneficiary: %v", err)
		} else {
			l.Beneficiary = b.Hex()
			src("the protocol fee's beneficiary", l.Beneficiary, "-beneficiary")
		}
	} else {
		src("the protocol fee's beneficiary", l.Beneficiary, "the deployment's record: no realm records the treasurer's Ethereum address (r/hlfees keeps its gno address), and the fee is 0 at launch — the kind 7 that raises it names its beneficiary; give -beneficiary to hold it")
		a.unverify("the protocol fee's beneficiary at launch (%s): the deployment's record alone — give -beneficiary, the treasurer's published Ethereum address (E4-TL1: no realm records it, and a deployer's own agrees with its own record everywhere)", l.Beneficiary)
	}
	differs("beneficiary (-beneficiary)", common.HexToAddress(a.d.Launch.Beneficiary).Hex(), common.HexToAddress(l.Beneficiary).Hex())

	// the mint limits: compiled from the expected lock (§5.4, E3-CF4) when one is given, else bounded by §5.4's ceilings
	lock, from := a.expectedLock()
	realms := a.cfg.LaunchMaximum
	if realms == 0 {
		realms = launchMaximumDefault
	}
	for i := 0; i < 2; i++ {
		if lock[i] != 0 {
			mx, dc := launchLimits(lock[i])
			src(fmt.Sprintf("token %d's launch limits", i), fmt.Sprintf("%d a message, %d a day", mx, dc), fmt.Sprintf("compiled from its expected lock %d (%s): the day cap the smaller of 100,000 and a tenth of it, the maximum the smaller of 25,000 and the cap (§5.4); the deployer's record says it compiled them from %d", lock[i], from[i], a.d.Launch.ExpectedLock[i]))
			if c, at := a.configLock(i); c != 0 && c != lock[i] {
				a.note("NOTE the operator's config (%s) expects a lock of %d of token %d — not the launch value given (%s %d): the monitors' cap-ratio rule reads the config's", at, c, i, lockFlags[i], lock[i])
			}
			differs(fmt.Sprintf("token %d's launch limits (%s)", i, lockFlags[i]), fmt.Sprintf("%d/%d", a.d.Launch.MaxPerMessage[i], a.d.Launch.DayCap[i]), fmt.Sprintf("%d/%d", mx, dc))
			l.MaxPerMessage[i], l.DayCap[i] = mx, dc
		} else {
			src(fmt.Sprintf("token %d's launch limits", i), fmt.Sprintf("%d a message, %d a day", l.MaxPerMessage[i], l.DayCap[i]), "the deployment's record, bounded by §5.4's ceilings below: give -expected-lock-gnot and -expected-lock-bubble (the launch notice's) to hold them to the lock")
			a.check(l.DayCap[i] <= launchDayCapMax && l.MaxPerMessage[i] <= launchMaximumDefault && l.MaxPerMessage[i] <= l.DayCap[i] && l.MaxPerMessage[i] >= 1,
				"token %d's launch limits %d a message, %d a day are within §5.4's ceilings (a day cap at most 100,000, a maximum at most 25,000 and the cap)", i, l.MaxPerMessage[i], l.DayCap[i])
			theirs := ""
			if c, at := a.configLock(i); c != 0 {
				theirs = fmt.Sprintf("; the operator's config (%s) expects %d — the operator's word, never taken as the launch value (E5-CF3 item 3)", at, c)
			}
			a.unverify("token %d's launch limits (%d a message, %d a day): the deployment's record, bounded only by §5.4's ceilings — give %s, the launch notice's expected lock: the cap is a tenth of it (E4-TL1: at a small lock the ceilings' cap is many times §5.4's bound)%s", i, l.MaxPerMessage[i], l.DayCap[i], lockFlags[i], theirs)
		}
		a.check(l.MaxPerMessage[i] >= realms, "token %d's launch maximum %d a message is at or above the route realm's compiled maximum %d (-launch-maximum, else their source's 25,000: §5.4, the gate's never below gno.land's)", i, l.MaxPerMessage[i], realms)
	}
	a.launch = l
}

// lockFlags are the flags of each token's expected lock: token 0 is the -gnot-route's (HlAdmin's route(0)), 1 the
// -bubble-route's.
var lockFlags = [2]string{"-expected-lock-gnot", "-expected-lock-bubble"}

// expectedLock is the lock the launch notice expects of each token, and where each comes from: the member's own flag
// alone (-expected-lock-gnot for token 0 — the -gnot-route's, HlAdmin's route(0) — and -expected-lock-bubble for
// token 1), never the record's symbols (E4-TL1: a record naming token 0 "BUBBLE" would compile GNOT's limits from
// BUBBLE's lock) nor a config's expectedLock (E5-CF3 item 3: bridge.json is the operator's file; configLock notes it).
func (a *audit) expectedLock() ([2]uint64, [2]string) {
	var out [2]uint64
	var from [2]string
	for i := 0; i < 2; i++ {
		if a.cfg.ExpectedLock[i] != 0 {
			out[i], from[i] = a.cfg.ExpectedLock[i], lockFlags[i]
		}
	}
	return out, from
}

// configLock is the lock the operator's config expects of token i — its route whose token is that token's address
// (the record's, held on chain to the token whose router for gno.land is the member's route path) — and where it says
// so; 0 when it names none. Only ever noted (E5-CF3 item 3).
func (a *audit) configLock(i int) (uint64, string) {
	if a.cf == nil {
		return 0, ""
	}
	token := a.d.Addr(fmt.Sprintf("token%d", i))
	for _, r := range a.cf.Routes {
		if t, err := ethx.ParseAddress(r.Token); err == nil && t == token && r.ExpectedLock != 0 {
			return r.ExpectedLock, fmt.Sprintf("-config's route of token %d's address %s, its realm %s", i, token.Hex(), r.Gno)
		}
	}
	return 0, ""
}

// seatChecks holds the line's seats and watch slots to r/hlgov's Ethereum seats (E3-GS4) — the one realm that keeps
// them, setting each when it dispatches a kind 5 — once r/hlgov is up: each seat the line holds must be r/hlgov's,
// unless the last kind 5 r/hlgov dispatched to the line for that seat has not run on it (pending, spent, cancelled,
// not delivered yet), which is said, not failed. A launch seat of the deployer's own choosing fails here.
func (a *audit) seatChecks(ctx context.Context) {
	gov := a.govRead(ctx)
	switch {
	case len(a.gnos) == 0 || gov.Err != nil:
		return // the trust root is UNVERIFIED, or r/hlgov's json failed above
	case gov.Absent:
		a.note("r/hlgov (%s) is not on the chain yet: its Ethereum seats are held to the deployment's once it is", a.hlgovPath)
		return
	}
	mirror, err := a.govSeats(ctx)
	if err != nil {
		a.fail("r/hlgov's Ethereum seats (its page)", err)
		return
	}
	f := a.line
	var unrun map[int]*unrunSeat
	for s := 0; s < 6; s++ {
		if mirror[s] == f.Seats[s] {
			a.check(true, "seat %d is %s, r/hlgov's Ethereum seat as it last set it", s+1, mirror[s].Hex())
			continue
		}
		if unrun == nil {
			unrun = a.unrunSeats(ctx)
		}
		if u := unrun[s+1]; u != nil && u.key == mirror[s] {
			a.note("seat %d: the line holds %s; r/hlgov's seat is %s, set by its decision #%d (kind 5), which has %s on the line", s+1, f.Seats[s].Hex(), mirror[s].Hex(), u.n, u.state)
			continue
		}
		a.check(false, "seat %d: the line holds %s, but r/hlgov's Ethereum seat is %s, and no kind 5 r/hlgov sent the line that has not run explains it", s+1, f.Seats[s].Hex(), mirror[s].Hex())
	}
}

// unrunSeat is the last kind 5 r/hlgov dispatched to the line for a seat that did not run there.
type unrunSeat struct {
	n     uint64
	key   common.Address
	state string
}

// unrunSeats reads every decision r/hlgov dispatched after the line's start and keeps, for each seat, the last kind 5
// naming it — when that one did not run on the line (a later executed one makes the two agree again).
func (a *audit) unrunSeats(ctx context.Context) map[int]*unrunSeat {
	f, g := a.line, a.gov.Gov
	ends := map[uint64]string{}
	for _, x := range f.Decisions {
		ends[x.N] = x.End
	}
	last := map[int]*unrunSeat{}
	for n := a.launch.StartSequence + 1; n <= g.Last; n++ {
		gd, err := a.govDecision(ctx, n)
		if err != nil {
			a.fail(fmt.Sprintf("r/hlgov's decision/%d", n), err)
			continue
		}
		if gd == nil {
			continue
		}
		kind, p, err := govBody(gd, n)
		if err != nil || kind != kindGuardians || len(p) == 0 {
			continue
		}
		state, recorded := ends[n]
		switch {
		case !recorded:
			state = "not been delivered yet"
		case state == "":
			state = "not matured yet (pending)"
		default:
			state = map[string]string{"executed": "run", "spent": "been spent", "cancelled": "been cancelled"}[state]
		}
		for i := 0; i < int(p[0]) && len(p) >= 1+33*(i+1); i++ {
			at := 1 + 33*i
			last[int(p[at])] = &unrunSeat{n: n, key: common.BytesToAddress(p[at+13 : at+33]), state: state}
		}
	}
	for s, u := range last {
		if u.state == "run" {
			delete(last, s)
		}
	}
	return last
}
