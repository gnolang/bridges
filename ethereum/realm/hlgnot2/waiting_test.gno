package hlgnot2

import "testing"

// TestRenderGasRouteJSON: the json every machine reads the route by (E4-RL2: every page the machines read has a
// probe). The route's other pages are probed at their bounds in hlbubble, whose suite never migrates: the same code —
// p/hlroute's pages and waiting.gno, byte for byte the same in the three routes (eth/scripts/gnotest.sh checks).
func TestRenderGasRouteJSON(cur realm, t *testing.T) {
	if p := Render("json"); len(p) < 800 {
		t.Fatalf("json: %s", p)
	}
}
