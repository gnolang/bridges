package agent

import (
	"encoding/hex"
	"strings"
	"sync"
	"testing"

	"github.com/gnolang/bridges/ethereum/bridge/gnoside"
)

// The review of the integration lane I3's follow-ups, finding 4 (the relayer's dust, as the Solana relayer's 1aea555f
// left it too): a left burn someone else Processes at his own cost — the design's own escape hatch — was never read
// again while it stayed below the fee, held pending and in Left for good; and while a migration's successor imported,
// dust was not judged at all, so every tick read each pending dust burn's mark.

// i3MarkReads counts the renders of an id's delivered mark in the inbound rig's fake node.
func i3MarkReads(r *inboundRig, id string) func() int {
	var mu sync.Mutex
	n := 0
	r.gno.mu.Lock()
	r.gno.afterRender = func(_, path string) {
		if path == "delivered/"+id {
			mu.Lock()
			n++
			mu.Unlock()
		}
	}
	r.gno.mu.Unlock()
	return func() int {
		mu.Lock()
		defer mu.Unlock()
		v := n
		n = 0
		return v
	}
}

// A burn left below the fee that someone else Processes is dropped once the credit moves hlmailbox's delivered count.
func TestI3RelayerDropsDustSomeoneDelivered(t *testing.T) {
	r := newInboundRig(t)
	dust := r.chain.burn(m3bGNOT, gnoside.RealmKey(m3bNS+"hlgnot"), r.alice, 99, 80) // below the fee of 100
	id := hex.EncodeToString(dust[:])
	if err := r.tick(); err != nil {
		t.Fatal(err)
	}
	if _, left := r.job.st.Left[id]; !left || len(r.job.st.Pending) != 1 {
		t.Fatalf("the dust is not left pending: pending %d, left %v", len(r.job.st.Pending), r.job.st.Left)
	}
	r.gno.set(m3bMailPath, "delivered/"+id, "70") // a stranger Processes it at his own cost: the count moves to 1
	for i := 0; i < 2; i++ {
		if err := r.tick(); err != nil {
			t.Fatal(err)
		}
	}
	_, left := r.job.st.Left[id]
	t.Logf("delivered by someone else: pending %d, left %v; Process calls %d", len(r.job.st.Pending), left, len(r.gno.callsOf("Process")))
	if len(r.job.st.Pending) != 0 || left {
		t.Fatalf("dust someone else delivered stays pending (%d) and left (%v)", len(r.job.st.Pending), left)
	}
	if len(r.gno.callsOf("Process")) != 0 {
		t.Fatalf("the relayer Processed the dust: %v", r.gno.callsOf("Process"))
	}
}

// While a migration's successor imports, a burn below the successor's fee is judged as dust as any other: kept left, no
// read of its own a tick.
func TestI3RelayerJudgesDustDuringAnImport(t *testing.T) {
	r := newInboundRig(t)
	r.gno.set(m3bNS+"hlgnot", "json", `{"live":true,"lock":0,"migratedTo":"`+m3bNS+`hlgnot2"}`)
	r.gno.set(m3bNS+"hlgnot2", "json", `{"live":true,"lock":1000,"importing":true,"imported":1,"fee":100}`)
	dust := r.chain.burn(m3bGNOT, gnoside.RealmKey(m3bNS+"hlgnot"), r.alice, 99, 80)
	id := hex.EncodeToString(dust[:])
	reads := i3MarkReads(r, id)
	for i := 0; i < 3; i++ {
		if err := r.tick(); err != nil {
			t.Fatal(err)
		}
	}
	_, left := r.job.st.Left[id]
	n := reads()
	t.Logf("three ticks while hlgnot2 imports: the dust's mark read %d times, left %v; note %q", n, left, r.host.notes["process:"+id])
	if !left || n > 1 {
		t.Fatalf("dust waiting on an import is not judged as dust: left %v, its mark read %d times in three ticks", left, n)
	}
	if strings.Contains(r.host.notes["process:"+id], "waits:") {
		t.Fatalf("the dust is noted as waiting for the import: %q", r.host.notes["process:"+id])
	}
}
