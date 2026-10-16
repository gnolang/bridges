package agent

import (
	"encoding/hex"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/gnolang/bridges/ethereum/bridge/gnoside"
)

// E4-GT1's pending readings from inside: the lag taken off a reading's excess, and the samples that rule the lag out.

// A lag and a forgery in one reading: the lag found (the honest burn placed once the provider caught up) is taken off
// the reading's excess, and the forgery's part stands once the index's block time passes the reading's.
func TestF15bLagTakenOffAForgedReading(t *testing.T) {
	r := newMonRig(t)
	gnot := gnoside.RealmKey(m3bNS + "hlgnot")
	r.chain.mine(130, 130)
	id := r.chain.burn(m3bGNOT, gnot, r.alice, 300, 120) // honest, credited
	forged := strings.Repeat("ef", 32)                   // forged, 100
	r.gno.set(m3bNS+"hlgnot", "json", m3bRouteJSON(1000, 400, 0, 0))
	r.gno.set(m3bMailPath, "delivered/"+hex.EncodeToString(id[:]), "59") // both credited below gno.land's head (60)
	r.gno.set(m3bMailPath, "delivered/"+forged, "59")
	r.pa.mu.Lock()
	r.pa.lag = 15
	r.pa.mu.Unlock()
	if err := r.poll(); err != nil {
		t.Fatal(err)
	}
	p := r.m.state.Pending["GNOT"]
	if p == nil || p.Excess != 400 {
		t.Fatalf("the reading: %+v", p)
	}
	r.pa.mu.Lock()
	r.pa.lag = 0
	r.pa.mu.Unlock()
	r.step(10 * time.Second)
	if err := r.poll(); err != nil {
		t.Fatal(err)
	}
	if p = r.m.state.Pending["GNOT"]; p == nil || p.lagSum() != 300 || len(r.gnoFlags()) > 0 {
		t.Fatalf("after the lag ended: %+v %v", p, r.gnoFlags())
	}
	r.chain.retime(uint64(r.now.Unix()))
	r.m.state.Samples = nil // the reading of this poll waits too (no sample), so the flag's words are the pending one's
	// and a burn above the index whose delivered mark does not read, counted as credited: the most an honest lag can
	// carry covers this reading's excess (E5-GT1), so it waits rather than being a break at once
	r.chain.mine(140, 130)
	above := r.chain.burn(m3bGNOT, gnot, r.alice, 100, 135)
	r.gno.renderErr = func(pkg, path string) error {
		if pkg == m3bMailPath && path == "delivered/"+hex.EncodeToString(above[:]) {
			return errors.New("timeout")
		}
		return nil
	}
	r.step(10 * time.Second)
	if err := r.poll(); err != nil {
		t.Fatal(err)
	}
	text := r.m.Alarm.Standing()["identity:GNOT:flag"]
	t.Logf("confirmed: %s", text)
	if len(r.gnoFlags()) != 2 || r.snd.count() != 1 || !strings.Contains(text, "less the lag of 300") {
		t.Fatalf("the forgery's part was not flagged: %v %v %q", r.gnoFlags(), r.snd.calls, text)
	}
}

// A delivery the index cannot place that is old — a credit by vote of an id no burn of the index carries, here — never
// delays the next verdict: once the index's finalized block's time has passed the sample that counted it, a later
// break (a forged mint on Ethereum) is flagged at once. Without the samples (a fresh state) the same reading waits while
// the providers cannot vouch for what lies above the index (their heads old), and is flagged at once when both, current
// to their heads, show no burn there that could carry it (E5-GT1).
func TestF15bOldUnplacedDeliveryDelaysNoBreak(t *testing.T) {
	for _, c := range []struct {
		name         string
		fresh, stale bool
	}{{"the samples, the providers' heads old", false, true}, {"no sample, the providers current", true, false}, {"no sample, the providers' heads old", true, true}} {
		r := newMonRig(t)
		r.chain.retime(uint64(r.now.Unix()) - 3600) // the finalized block 100 forty minutes before gno.land's head
		r.gno.set(m3bMailPath, "delivered/"+strings.Repeat("ab", 32), "55")
		if err := r.poll(); err != nil {
			t.Fatal(err)
		}
		if v := r.m.Status()["identity"].(map[string]any)["GNOT"].(map[string]any)["verdict"]; v != "holds" {
			t.Fatalf("an unplaced delivery under a holding identity: %v", v)
		}
		r.step(10 * time.Minute)
		r.chain.mine(400, 400) // the finalized block's time passes the sample's
		if c.stale {
			r.chain.retime(uint64(r.now.Unix()) - 12*400 - 180) // the head, block 400, three minutes old
		}
		r.chain.setSupply(m3bGNOT, 350, 1500)
		if c.fresh {
			r.m.state.Samples = nil
		}
		if err := r.poll(); err != nil {
			t.Fatal(err)
		}
		t.Logf("%s: flags %v %v; pending %v", c.name, r.gnoFlags(), r.snd.calls, r.m.state.Pending)
		switch {
		case !(c.fresh && c.stale) && (len(r.gnoFlags()) != 2 || r.snd.count() != 1):
			t.Fatalf("%s: the break was held back: %v %v %v", c.name, r.gnoFlags(), r.snd.calls, r.m.state.Pending)
		case c.fresh && c.stale && (len(r.gnoFlags()) != 0 || r.m.state.Pending["GNOT"] == nil):
			t.Fatalf("%s: the reading should wait: %v %v", c.name, r.gnoFlags(), r.m.state.Pending)
		}
	}
}

// The pair's finalized block standing still for an hour alarms eth:finality-stall: a pending reading waits on it.
func TestF15bMonitorAlarmsAFinalityStall(t *testing.T) {
	r := newMonRig(t)
	for i := 0; i < 8; i++ {
		if err := r.poll(); err != nil {
			t.Fatal(err)
		}
		r.step(10 * time.Minute)
	}
	if !r.alarmed("eth:finality-stall") {
		t.Fatalf("no stall alarm after 70 minutes at block 100: %v", r.m.Alarm.StandingKeys())
	}
	r.chain.mine(500, 450) // finality resumes: the chain finalized on meanwhile, its finalized block 23 minutes old
	r.step(10 * time.Second)
	if err := r.poll(); err != nil {
		t.Fatal(err)
	}
	if r.alarmed("eth:finality-stall") {
		t.Fatal("the stall alarm outlived the finalized block's move")
	}
}
