package agent

import (
	"context"
	"crypto/sha256"
	"strings"
	"testing"
	"time"
)

// The integration lane I1's tests of the machines (audit round 3's fold, the cross-lane follow-ups): the updater
// and gno.land's header encoding.

// E3-AS3's updater half (fold F12's "needs"): the updater hashes the header it would submit from its fields, as the
// client hashes them, and holds it to the node's hash of the block before anything is sent — a difference (a gno
// upgrade that changed the header's encoding: the chain signs its own hash of a block, which the client would not
// rebuild) sends nothing and alarms gno:encoding, which clears once a header holds again.
func TestUpdaterAlarmsAHeaderEncodingDifference(t *testing.T) {
	w := newUpdWorld(t, 100)
	w.g.dispatch(112, sendMsg(testGNOT, ethGNOT, 500))
	w.g.head = 115
	w.advance(10 * time.Minute)
	w.g.mu.Lock()
	honest := w.g.header(113)
	changed := *honest
	changed.Hash = sha256.Sum256(append([]byte("another encoding of "), honest.Hash[:]...))
	w.g.headers[113] = &changed
	w.g.mu.Unlock()
	err := w.u.tick(context.Background())
	if err == nil || !strings.Contains(err.Error(), "the header's encoding") {
		t.Fatalf("a header whose fields hash otherwise: %v", err)
	}
	if n := len(w.e.sentOf("update")); n != 0 {
		t.Fatalf("%d update(s) sent for a header the client would not rebuild", n)
	}
	if text, ok := w.u.Alarm.Standing()["gno:encoding"]; !ok || !strings.Contains(text, "header 113's fields hash to") {
		t.Fatalf("not alarmed gno:encoding: %v", w.u.Alarm.Standing())
	}
	w.g.mu.Lock()
	w.g.headers[113] = honest
	w.g.mu.Unlock()
	w.advance(3 * time.Minute)
	w.tick(t)
	if len(w.e.sentOf("update")) != 1 || w.e.trusted != 113 {
		t.Fatalf("the header whole again was not submitted: trusted %d", w.e.trusted)
	}
	if _, ok := w.u.Alarm.Standing()["gno:encoding"]; ok {
		t.Fatal("gno:encoding stands after a header held")
	}
}
