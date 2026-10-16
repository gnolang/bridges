package hlgov

import (
	"strings"
	"testing"
)

// TestRefusedDomain (the round-2 realms audit's E2-RL7): kind 6's domain rule read where the harness cannot run it —
// at gnoland-1 (this chain's domain 1198419788, Ethereum's 1), where gno.land's other domain is 1198419796, and at a
// Sepolia stage (1198419796, Ethereum's 11155111): Ethereum's own domain and gno.land's other one refused, this
// chain's own and another chain's taken (TestDeploymentTargets drives the builder on this test chain).
func TestRefusedDomain(cur realm, t *testing.T) {
	for _, c := range []struct {
		domain, local, origin uint32
		want                  string
	}{
		{1, 1198419788, 1, "hlgov: domain 1 is Ethereum's own: a token enrolled for it would dispatch every burn there"},
		{1198419796, 1198419788, 1, "hlgov: domain 1198419796 is gno.land's other domain (this chain's is 1198419788): no client on Ethereum proves it"},
		{1198419788, 1198419788, 1, ""},
		{10, 1198419788, 1, ""},
		{31337, 1198419788, 1, ""},
		{11155111, 1198419796, 11155111, "hlgov: domain 11155111 is Ethereum's own"},
		{1198419788, 1198419796, 11155111, "hlgov: domain 1198419788 is gno.land's other domain (this chain's is 1198419796)"},
		{1198419796, 1198419796, 11155111, ""},
		{1, 1198419796, 11155111, ""},
	} {
		if why := refusedDomain(c.domain, c.local, c.origin); (c.want == "" && why != "") || !strings.HasPrefix(why, c.want) {
			t.Fatalf("domain %d at %d (Ethereum %d): %q", c.domain, c.local, c.origin, why)
		}
	}
}
