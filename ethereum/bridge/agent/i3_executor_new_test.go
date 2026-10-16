package agent

import (
	"reflect"
	"testing"

	"github.com/gnolang/bridges/ethereum/bridge/gnoside"
)

// The executor reads GovDAO's list from the floor its state keeps (gnoside.ReadProposalsFrom): after a whole reading,
// one past the newest proposal, the open ones, the DAO's realm — and a reading that is not whole keeps the old one.
func TestI3ExecutorKeepsTheProposalsFloor(t *testing.T) {
	w := newWorld(t, 100, "primary")
	w.r.Cfg.Gno.DAO = gnoside.DAOPath
	i3DAOList(w, "")
	w.tick()
	f := w.r.state.ProposalsFloor
	if f == nil || !reflect.DeepEqual(*f, gnoside.ProposalFloor{DAO: gnoside.DAOPath, Next: 6, Open: []int64{5}}) {
		t.Fatalf("the floor after a whole reading: %+v", f)
	}
	i3DAOList(w, "### [Proposal 5: eth/hlgnot: refund](/r/gov/dao:5)")
	w.tick()
	if g := w.r.state.ProposalsFloor; g == nil || !reflect.DeepEqual(*g, *f) {
		t.Fatalf("a reading not whole moved the floor: %+v (was %+v)", g, f)
	}
}
