package agent

import (
	"encoding/hex"
	"fmt"
	"testing"
	"time"

	"github.com/gnolang/bridges/ethereum/bridge/gnoside"
)

// A fee a decision lowers past a dust burn makes it a delivery the relayers bring (TestI3RelayerJudgesDustAgain…): the
// monitors count it waiting again, its wait from the last poll that found it dust — not from when it was first indexed,
// hours ago, which would raise `age` the moment the fee fell, before any relayer could act.
func TestI3MonitorDustBecomesADeliveryWhenTheFeeFalls(t *testing.T) {
	r := newMonRig(t)
	r.gno.set(m3bNS+"hlgnot", "json", i3Route(1000, 0, 100))
	if err := r.poll(); err != nil {
		t.Fatal(err)
	}
	r.chain.mine(110, 110)
	dust := r.chain.burn(m3bGNOT, gnoside.RealmKey(m3bNS+"hlgnot"), r.alice, 99, 105)
	r.step(10 * time.Second)
	if err := r.poll(); err != nil {
		t.Fatal(err)
	}
	rec := r.m.state.Index.Burns[hex.EncodeToString(dust[:])]
	rec.Seen = r.now.Add(-3 * time.Hour).Unix()
	r.step(10 * time.Second)
	if err := r.poll(); err != nil {
		t.Fatal(err)
	}
	if i3Num(r.m.Status(), "dust", "GNOT") != 1 || r.alarmed("age") {
		t.Fatalf("under the fee of 100: dust %v, alarms %v", r.m.Status()["dust"], r.m.Alarm.StandingKeys())
	}
	r.gno.set(m3bNS+"hlgnot", "json", i3Route(1000, 0, 50)) // GovDAO lowers the fee past it
	r.step(10 * time.Second)
	if err := r.poll(); err != nil {
		t.Fatal(err)
	}
	st := r.m.Status()
	t.Logf("the fee lowered to 50: waiting %v, dust %v; alarms %v", st["waiting"], st["dust"], r.m.Alarm.StandingKeys())
	if i3Num(st, "waiting", "ethereum→gno GNOT") != 1 || i3Num(st, "dust", "GNOT") != 0 || r.alarmed("age") {
		t.Fatalf("a delivery since the fee fell: waiting %v, age %v (its wait counted from its first sighting three hours ago)", st["waiting"], r.alarmed("age"))
	}
	rec.DustAt = r.now.Add(-2*time.Hour - time.Minute).Unix() // two hours since the fee fell, still uncredited
	r.step(10 * time.Second)
	if err := r.poll(); err != nil {
		t.Fatal(err)
	}
	if !r.alarmed("age") {
		t.Fatalf("a delivery two hours waiting since the fee fell raised no age alarm: %v", r.m.Alarm.StandingKeys())
	}
}

// The relayer keeps at most maxLeft dust burns pending: past it the oldest is forgotten (a stranger's pile does not grow
// its state file without end), and the mark of dust no longer pending goes with it.
func TestI3RelayerBoundsTheDustItKeeps(t *testing.T) {
	j := &inboundJob{st: inboundState{Left: map[string]int64{"gone": 100}}}
	var pending []inboundBurn
	for i := 0; i < maxLeft+5; i++ {
		b := inboundBurn{ID: fmt.Sprintf("%064x", i), Nonce: uint32(i)}
		pending = append(pending, b)
		j.st.Left[b.ID] = 100
	}
	honest := inboundBurn{ID: fmt.Sprintf("%064x", 99_999), Nonce: 99_999}
	pending = append(pending, honest)
	kept := j.boundLeft(pending)
	if len(kept) != maxLeft+1 || len(j.st.Left) != maxLeft || kept[0].Nonce != 5 || kept[len(kept)-1].ID != honest.ID {
		t.Fatalf("the dust kept: %d pending (first #%d), %d left", len(kept), kept[0].Nonce, len(j.st.Left))
	}
	if _, ok := j.st.Left["gone"]; ok {
		t.Fatal("the mark of dust no longer pending stays")
	}
}

// markDoubt: only a delivery the index cannot place, under a BROKEN reading or a pending one, puts a cut's marks in doubt.
func TestI3MarkDoubt(t *testing.T) {
	for _, c := range []struct {
		unplaced int
		broken   bool
		pending  int
		want     bool
	}{{0, true, 1, false}, {1, false, 0, false}, {1, true, 0, true}, {2, false, 1, true}} {
		if got := markDoubt(c.unplaced, c.broken, c.pending); got != c.want {
			t.Fatalf("markDoubt(%d, %v, %d) = %v", c.unplaced, c.broken, c.pending, got)
		}
	}
}
