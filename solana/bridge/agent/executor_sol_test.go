package agent

import (
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/gagliardetto/solana-go"

	"github.com/gnolang/bridges/solana/bridge/sol"
)

// When the executor attempts Execute on the admin program (§6.5, plan v29): at maturity; a replacement under a
// hold at the later of its 7th day and day 21 of the earliest hold overlapping its pending life, from the hold
// history (AN4: nothing written, no earlier attempt); a cancelled decision at any time; never while a hold or a
// flag defers a decision until the hold ends, nor for an upgrade whose buffer is not staged.
func TestExecuteWhen(t *testing.T) {
	const factor = 8
	now := int64(1_800_000_000)
	admin := solana.MustPublicKeyFromBase58("4RGdtbf8oxCqysLCmVDRKLLZTrTmjPWiLY1xUTiq9HXU")
	other := solana.MustPublicKeyFromBase58("TokenzQdBNbLqP5VEhdkAS6EPFLC1PHnBqCXEpPxuEb")
	week, day := sol.Week(factor), sol.Day/factor
	st := &sol.AdminState{LastSeq: 9}
	flagged := &sol.AdminState{LastSeq: 9}
	flagged.Guardians.Flags[0] = &sol.Flag{Time: now - 10}
	cancelled := &sol.AdminState{LastSeq: 9, Cancelled: []sol.CancelledRange{{From: 5, To: 5}}}
	// the hold history (AN4): a hold that overlapped a week-old decision's pending life and started long ago (its
	// 21st day is past); one that overlapped it and started ten days ago (its 21st day is ahead); one that ended
	// before the decision was delivered (it does not count)
	pastFloor := &sol.AdminState{LastSeq: 9, Holds: []sol.Hold{{Start: now - 30*day, End: now - 5*day}}}
	aheadFloor := &sol.AdminState{LastSeq: 9, Holds: []sol.Hold{{Start: now - 10*day, End: now - 5*day}}}
	before := &sol.AdminState{LastSeq: 9, Holds: []sol.Hold{{Start: now - 30*day, End: now - 8*day}}}
	hold := &sol.Flag{Time: now - 2*day}     // a hold two days old: its 21st day is ahead
	oldHold := &sol.Flag{Time: now - 22*day} // a hold past its 21st day
	refund := solana.MustPublicKeyFromBase58("ATokenGPvbdGVxr1b2hvZbsiqW5xWH25efTNsLJA8knL")
	dec := func(kind byte, delivered int64) *sol.Decision {
		return &sol.Decision{Number: 5, Kind: kind, DeliveredTime: delivered}
	}
	upgrade := func(program solana.PublicKey, staged bool) *sol.Decision {
		d := dec(sol.KindUpgrade, now-week)
		d.Params = sol.EncodeParams(&sol.Params{Kind: sol.KindUpgrade, Program: program, Buffer: other})
		if staged {
			d.RefundKey = &refund
		}
		return d
	}
	cases := []struct {
		name string
		d    *sol.Decision
		st   *sol.AdminState
		hold *sol.Flag
		act  bool
		why  string
	}{
		{"a mature kind 7", dec(7, now-week), st, nil, true, "mature"},
		{"an immature kind 7", dec(7, now-week+10), st, nil, false, "matures at"},
		{"a kind 7 under a hold", dec(7, now-week), st, hold, false, "until it ends"},
		{"a kind 7 under a lone flag", dec(7, now-week), flagged, nil, false, "a flag"},
		{"a kind 5 under a young hold, no history: day 21 of the hold is ahead", dec(5, now-week), st, hold, false, "the floor"},
		{"a kind 5 under a hold, an earlier overlapping hold's day 21 ahead", dec(5, now-week), aheadFloor, hold, false, "the floor"},
		{"a kind 5 under a hold, an earlier overlapping hold's day 21 passed", dec(5, now-week), pastFloor, hold, true, "mature"},
		{"a kind 5 under a hold, the floor passed, its own 7th day not", dec(5, now-10), pastFloor, hold, false, "matures at"},
		{"a kind 5 under a hold, a hold that ended before its delivery does not count", dec(5, now-week), before, hold, false, "the floor"},
		{"a kind 5 under a hold past its 21st day", dec(5, now-week), st, oldHold, true, "mature"},
		{"a kind 5 under a lone flag", dec(5, now-week), flagged, nil, true, "mature"},
		{"a kind 5 with a history and no hold standing: its own 7th day", dec(5, now-week), aheadFloor, nil, true, "mature"},
		{"a cancelled decision", dec(7, now-10), cancelled, nil, true, "cancelled"},
		{"a cancelled decision under a hold", dec(7, now-10), cancelled, hold, true, "cancelled"},
		{"an unstaged upgrade", upgrade(other, false), st, nil, false, "not staged"},
		{"an unstaged upgrade of the admin program itself", upgrade(admin, false), st, nil, true, "mature"},
		{"a staged upgrade", upgrade(other, true), st, nil, true, "mature"},
		{"a pending cancel (executes at once)", dec(1, now), st, nil, true, "mature"},
		{"kind 4, reserved, mature", dec(4, now-week), st, nil, true, "mature"},
		{"kind 4, reserved, immature", dec(4, now-week+1), st, nil, false, "matures at"},
		{"an unpause after its day", dec(2, now-day), st, hold, true, "mature"},
		{"an unpause before its day", dec(2, now-day+1), st, nil, false, "matures at"},
	}
	for _, c := range cases {
		act, why := executeWhen(c.d, c.st, c.hold, admin, factor, now)
		if act != c.act || !strings.Contains(why, c.why) {
			t.Errorf("%s: act %v (%s), want %v (%q)", c.name, act, why, c.act, c.why)
		}
	}
}

// A failed Execute is sorted: the program's clock answers are waits, LayoutFixed is refused for good, NeedsExtend
// (3026 in M2j's table) asks for a growth step, anything else (a code past the table among them) fails toward
// parking.
func TestJudgeExecuteErr(t *testing.T) {
	custom := func(code uint32) error {
		return errors.New("send: transaction failed: InstructionError(1, Custom(" + strconv.FormatUint(uint64(code), 10) + "))")
	}
	hexErr := func(s string) error { return errors.New("simulation failed: custom program error: " + s) }
	cases := []struct {
		err  error
		want string
	}{
		{custom(3012), "wait"}, {custom(3013), "wait"}, {hexErr("0xbc4"), "wait"},
		{custom(2017), "refused"}, {hexErr("0x7e1"), "refused"},
		{custom(3026), "extend"}, {hexErr("0xbd2"), "extend"},
		{custom(3023), "fail"}, {errors.New("dial tcp: connection refused"), "fail"}, {custom(3035), "fail"}, {custom(3027), "fail"},
	}
	for _, c := range cases {
		if got := judgeExecuteErr(c.err); got != c.want {
			t.Errorf("%v: %s, want %s", c.err, got, c.want)
		}
	}
}

// A cancel's ranges are read past the salt its parameters start with (v27 §13); one whose channel byte is not 0
// (M2j: the one channel), or that does not parse, is spent by the program and closes nothing here.
func TestCancelRangesPastTheSalt(t *testing.T) {
	params := sol.EncodeParams(&sol.Params{Kind: sol.KindCancel, Salt: 0xdeadbeefcafef00d, Cancel: []sol.CancelRange{{Channel: 0, From: 2, To: 2}, {Channel: 0, From: 5, To: 5}}})
	body := sol.DecisionBody(sol.KindCancel, 3, params)
	rs := cancelRanges(body[9:])
	if len(rs) != 2 || rs[0].From != 2 || rs[0].To != 2 || rs[1].From != 5 {
		t.Fatalf("ranges %+v", rs)
	}
	if cancelRanges(body[17:]) != nil {
		t.Fatal("the bytes past the salt parsed as a cancel")
	}
	rng := sol.EncodeParams(&sol.Params{Kind: sol.KindCancel, Salt: 9, Cancel: []sol.CancelRange{{Channel: 0, From: 4, To: 7}}})
	if rs := cancelRanges(rng); len(rs) != 1 || rs[0].From != 4 || rs[0].To != 7 {
		t.Fatalf("a range: %+v", rs)
	}
	mixed := sol.EncodeParams(&sol.Params{Kind: sol.KindCancel, Salt: 1, Cancel: []sol.CancelRange{{Channel: 0, From: 2, To: 2}, {Channel: 1, From: 1, To: 1}}})
	if cancelRanges(mixed) != nil {
		t.Fatal("a cancel naming channel 1 is spent, not closed")
	}
	if cancelRanges(nil) != nil || cancelRanges([]byte{0, 0, 0, 0, 0, 0, 0}) != nil {
		t.Fatal("a short body parsed")
	}
}
