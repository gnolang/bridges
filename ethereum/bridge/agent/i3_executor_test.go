package agent

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/gnolang/bridges/ethereum/bridge/gnoside"
)

// i3DAOList renders GovDAO's list in the relayer's fake: one item (#5, a refund made with hlgnot's builder, armed), its
// heading as impl v0 writes it or — form — in another form this reader does not parse.
func i3DAOList(w *testWorld, form string) {
	heading := "### [Prop #5 - eth/hlgnot: refund](/r/gov/dao:5)"
	if form != "" {
		heading = form
	}
	w.g.renders[gnoside.DAOPath+":"] = "# GovDAO\n## Members\n[> Go to Memberstore <](/r/gov/dao/memberstore/v0)\n## Proposals\n" + heading + "\nAuthor: g1jg8mtutu9khhfwc4nxmuhcpftf0pajdhfvsqf5\n\nStatus: ACTIVE\n\nTiers eligible to vote: T1, T2, T3\n\n---\n\n"
	w.g.renders[gnoside.DAOPath+":5"] = fmt.Sprintf("## Prop #5 - eth/hlgnot: refund\nAuthor: g1jg8mtutu9khhfwc4nxmuhcpftf0pajdhfvsqf5\n\nrefund\n\nExecutor created in: `%s`\n\n---\n\n### Stats\n- YES PERCENT: 70%%\n", testGNOT)
}

// The relayer's executor read GovDAO's list as hlwatch did before E5-GT2 — a list it could not parse was a list of no
// proposals, in silence (F17b's Q3): it finished nothing, said nothing, and forgot when it first saw each proposal open,
// so the deadline it waits for began again once the list read. A list not read whole is said — proposals:unread, once it
// has stood two minutes — and the first sightings are kept; a reading of it whole clears the alarm.
func TestI3ExecutorSaysAListNotReadWhole(t *testing.T) {
	w := newWorld(t, 100, "primary")
	w.r.Cfg.Gno.DAO = gnoside.DAOPath
	i3DAOList(w, "")
	w.tick()
	first, seen := w.r.state.ProposalsSeen["5"]
	if !seen {
		t.Fatalf("the open bridge proposal #5 was not seen: %v (%s)", w.r.state.ProposalsSeen, w.r.last["proposals"])
	}
	// GovDAO's render changes its item heading: nothing parses
	i3DAOList(w, "### [Proposal 5: eth/hlgnot: refund](/r/gov/dao:5)")
	for i := 0; i < 3; i++ {
		w.advance(time.Minute)
		w.tick()
	}
	alarm, alarmed := w.r.Alarm.Standing()["proposals:unread"]
	kept, still := w.r.state.ProposalsSeen["5"]
	t.Logf("three minutes over a list that does not parse: alarm %v %q; #5's first sighting kept %v", alarmed, alarm, still)
	if !alarmed || !strings.Contains(alarm, "shows no item this reader parses") {
		t.Fatalf("a GovDAO list the executor cannot parse read as no proposals, in silence: alarms %v", w.r.Alarm.StandingKeys())
	}
	if !still || kept != first {
		t.Fatalf("the list not read whole made the executor forget when it first saw #5 open: %v (was %d)", w.r.state.ProposalsSeen, first)
	}
	// read whole again: the alarm ends, the first sighting stands
	i3DAOList(w, "")
	w.advance(time.Minute)
	w.tick()
	if _, alarmed := w.r.Alarm.Standing()["proposals:unread"]; alarmed || w.r.state.ProposalsSeen["5"] != first {
		t.Fatalf("read whole again: alarm %v, #5 first seen %d (was %d)", alarmed, w.r.state.ProposalsSeen["5"], first)
	}
	if note, ok := w.r.last["proposals:unread"]; ok {
		t.Fatalf("read whole again, the status still says the list is not: %q", note)
	}
}
