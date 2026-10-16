package agent

import (
	"strings"
	"testing"
	"time"
)

// The integration lane I1's tests of the machines (audit round 3's fold, the cross-lane follow-ups): the delivery
// age's words.

// delivery:age says what moves a send that has waited seven hours, and no longer offers the sender's own delivery
// (withdrawn with the site's offer: fold F14, E3-FC5 — no tool of ours builds one, and it needs the header carried
// first): the send waits for its fees' header or the six-hour draw, and the operator may buy its header outside the
// budget.
func TestDeliveryAgeOffersNoSelfDelivery(t *testing.T) {
	w := newUpdWorld(t, 100)
	w.u.Cfg.Eth.Prices = map[string]float64{"GNOT": 0.0000001} // its fee covers nothing, the budget holds nothing
	w.g.dispatch(105, sendMsg(testGNOT, ethGNOT, 500))
	w.g.head = 110
	w.advance(time.Hour)
	w.tick(t)
	w.advance(6*time.Hour + time.Minute)
	w.tick(t)
	text, ok := w.u.Alarm.Standing()["delivery:age"]
	if !ok {
		t.Fatalf("seven hours and no delivery:age: %v", w.u.Alarm.StandingKeys())
	}
	for _, gone := range []string{"own delivery", "Mailbox.process"} {
		if strings.Contains(text, gone) {
			t.Fatalf("delivery:age still offers the sender's own delivery (%q): %s", gone, text)
		}
	}
	for _, want := range []string{"its fees (a lower base fee, more sends to share the header)", "the six-hour draw", "the operator may buy its header outside the budget"} {
		if !strings.Contains(text, want) {
			t.Fatalf("delivery:age does not say %q: %s", want, text)
		}
	}
}
