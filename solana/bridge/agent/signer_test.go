package agent

import (
	"strings"
	"testing"

	"github.com/gnolang/bridges/solana/bridge/hl"
)

// A signer signs once per message and refuses two messages for one nonce: the second stops it (it keeps serving
// and does not exit) and the status endpoint says why.
func TestSignerRefusesTwoMessagesForOneNonce(t *testing.T) {
	k, err := hl.NewValidatorKey()
	if err != nil {
		t.Fatal(err)
	}
	s := &Signer{Cfg: &Config{}, Key: k, Store: t.TempDir(), Alarm: &Alarmer{Source: "test"}}
	if err := s.load(); err != nil {
		t.Fatal(err)
	}
	verdictNow(s)
	first := Signed{Nonce: 3, ID: "aa"}
	fresh, err := s.put("sol", first)
	if err != nil || !fresh {
		t.Fatalf("first put: %v %v", fresh, err)
	}
	fresh, err = s.put("sol", first)
	if err != nil || fresh {
		t.Fatalf("the same message again: fresh %v err %v", fresh, err)
	}
	if !s.signing() {
		t.Fatal("halted after a harmless repeat")
	}
	_, err = s.put("sol", Signed{Nonce: 3, ID: "bb"})
	if err == nil || !strings.Contains(err.Error(), "REFUSING") {
		t.Fatalf("a second message for nonce 3 was taken: %v", err)
	}
	if s.signing() {
		t.Fatal("still signing after a nonce conflict")
	}
	st := s.Status()
	if st["signing"] != false || st["nonceConflict"] != true || !strings.Contains(st["halted"].(string), "nonce conflict") {
		t.Fatalf("status does not say why: %v", st)
	}
	// The identity holding again does not lift a nonce conflict.
	s.resume()
	if s.signing() {
		t.Fatal("a nonce conflict was lifted by resume")
	}
	// The first signature is still served.
	if b, err := s.readSigned("sol", 3); err != nil || b.ID != "aa" {
		t.Fatalf("the first signature is gone: %v %v", b, err)
	}
}

// The refusal rule halts and resumes with the identity; the status says the reason.
func TestSignerHaltResume(t *testing.T) {
	k, _ := hl.NewValidatorKey()
	s := &Signer{Cfg: &Config{}, Key: k, Store: t.TempDir(), Alarm: &Alarmer{Source: "test"}}
	_ = s.load()
	verdictNow(s)
	if !s.signing() {
		t.Fatal("not signing at the start")
	}
	s.halt("the supply identity is broken")
	if s.signing() || !strings.Contains(s.Status()["halted"].(string), "broken") {
		t.Fatal("not halted")
	}
	s.resume()
	if !s.signing() {
		t.Fatal("did not resume once the identity held")
	}
	// a round with no verdict (a reading the node cannot give yet) alarms and never stops a signer that was signing
	s.unverified("own node's head is below the client's trusted height")
	if !s.signing() {
		t.Fatal("a no-verdict round stopped a signer that was signing")
	}
	if _, ok := s.Alarm.Standing()["signer:no-verdict"]; !ok {
		t.Fatal("no alarm for the no-verdict round")
	}
	// but a signer never signs before its first verdict
	fresh := &Signer{Cfg: &Config{}, Key: k, Store: t.TempDir(), Alarm: &Alarmer{Source: "test"}}
	_ = fresh.load()
	fresh.unverified("the order never held")
	if fresh.signing() {
		t.Fatal("signing before the first verdict")
	}
}

// §5.6's stop when the client runs ahead: the trusted height T more than an hour of blocks past both the own
// node's head and a second source's stops the signer, which says why; the stop clears when the identity holds
// at a height at or above T. T above the head inside the allowance, or past it on the own node alone (the own
// node lags), or with no second source to compare, is a reading the node cannot give yet: an alarm, never a stop.
func TestSignerStopsWhenTheClientRunsAhead(t *testing.T) {
	const allow = DefaultAheadBlocks
	if (&Config{}).AheadAllowance() != 1050 {
		t.Fatal("the default allowance is an hour of blocks at 3.43 s")
	}
	c := &Config{}
	c.Gno.AheadBlocks = 300
	if c.AheadAllowance() != 300 {
		t.Fatal("a configured allowance")
	}
	cases := []struct {
		name           string
		T, own, second uint64
		secondOK       bool
		want           aheadVerdict
		why            string
	}{
		{"inside the allowance", 2000, 1500, 0, false, aheadInside, "judged at the head"},
		{"exactly the allowance (not more than an hour)", 2000 + allow, 2000, 2000, true, aheadInside, "judged at the head"},
		{"past it on the own node, the second source near T", 5000, 1000, 4500, true, aheadOwnLags, "own node lags"},
		{"past it on the own node, the second source exactly at the allowance", 5000, 1000, 5000 - allow, true, aheadOwnLags, "own node lags"},
		{"past it on both", 5000, 1000, 1200, true, aheadStop, "ahead of its chain"},
		{"past it on both by one block", 5000, 1000, 5000 - allow - 1, true, aheadStop, "ahead of its chain"},
		{"no second source", 5000, 1000, 0, false, aheadNoSecond, "no second source"},
	}
	for _, c := range cases {
		v, why := judgeAhead(c.T, c.own, c.second, c.secondOK, allow)
		if v != c.want || !strings.Contains(why, c.why) {
			t.Errorf("%s: %d (%s), want %d (%q)", c.name, v, why, c.want, c.why)
		}
	}
	k, _ := hl.NewValidatorKey()
	s := &Signer{Cfg: &Config{}, Key: k, Store: t.TempDir(), Alarm: &Alarmer{Source: "test"}}
	_ = s.load()
	verdictNow(s)
	s.applyAhead(judgeAhead(5000, 1000, 1200, true, allow))
	if s.signing() {
		t.Fatal("still signing with the client ahead of its chain")
	}
	st := s.Status()
	if !strings.Contains(st["halted"].(string), "ahead of its chain") || !strings.Contains(st["cut"].(string), "trusted height 5000") {
		t.Fatalf("the status does not say why: %v", st)
	}
	if _, ok := s.Alarm.Standing()["signer:halt"]; !ok {
		t.Fatal("no halt alarm")
	}
	// the identity holding at a height at or above T clears the stop
	s.resume()
	if !s.signing() {
		t.Fatal("the stop did not clear once the identity held")
	}
	// the other verdicts never stop a signer that was signing
	for _, in := range []struct {
		T, own, second uint64
		ok             bool
	}{{2000, 1500, 0, false}, {5000, 1000, 4500, true}, {5000, 1000, 0, false}} {
		s.applyAhead(judgeAhead(in.T, in.own, in.second, in.ok, allow))
		if !s.signing() {
			t.Fatalf("stopped on a reading the node cannot give yet: T %d own %d second %d", in.T, in.own, in.second)
		}
		if _, ok := s.Alarm.Standing()["signer:no-verdict"]; !ok {
			t.Fatal("no alarm for the no-verdict round")
		}
	}
	// a nonce conflict is not cleared by the identity, the ahead stop is
	s.nonceHalt = true
	s.applyAhead(judgeAhead(5000, 1000, 1200, true, allow))
	s.resume()
	if s.signing() {
		t.Fatal("a nonce conflict lifted by the identity")
	}
}
