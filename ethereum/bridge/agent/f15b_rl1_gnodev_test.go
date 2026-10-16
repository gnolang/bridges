package agent

import (
	"context"
	"os"
	"strconv"
	"testing"
	"time"
)

// E4-RL1 measured on a real node (fold-r4 f15b's notes): with F15B_GNODEV_RPC a local gnodev v1.5.1 (the gas-logging
// build) whose realm F15B_GNODEV_REALM renders claims/<from> as the routes do (p/hlroute's ClaimsPage) over 2,000
// waiting claims, the monitor's walk reads its first pages only, each a query the node answers; the pages the old walks
// reached (hlwatch read every page, the monitor ten a poll from where it stopped) run out of query gas there.
func TestF15bClaimsWalkOnGnodev(t *testing.T) {
	rpc, realm := os.Getenv("F15B_GNODEV_RPC"), os.Getenv("F15B_GNODEV_REALM")
	if rpc == "" || realm == "" {
		t.Skip("F15B_GNODEV_RPC and F15B_GNODEV_REALM unset: a measurement on the devbox's gnodev v1.5.1")
	}
	cfg := &Config{}
	cfg.Gno.ChainID = "dev"
	m := &Monitor{Cfg: cfg, own: cfg.SourceClient(rpc)}
	ctx := context.Background()
	start := time.Now()
	claims, complete, err := m.walkClaims(ctx, realm, 0)
	t.Logf("walkClaims: %d claims read in %s, every one: %v, error: %v", len(claims), time.Since(start).Round(time.Millisecond), complete, err)
	if err != nil || len(claims) != 300 || complete {
		t.Fatalf("the bounded walk over 2,000 waiting claims: %d claims, complete %v, %v", len(claims), complete, err)
	}
	for _, at := range []int{1600, 1900} { // what the unbounded walks asked for
		_, err := m.own.RenderAt(ctx, realm, "claims/"+strconv.Itoa(at), 0)
		t.Logf("claims/%d (the old walks' reach): error %v", at, err)
	}
}
