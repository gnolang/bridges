package hlmailbox

import (
	"chain"
	"testing"

	"gno.land/p/nt/testutils/v0"
)

// TestSolanaRealms (E-M1's Q6): IsSolanaRealm answers for the Solana deployment's six realms — the operator's
// namespace without eth/, spelled out here — and for nothing else: not this deployment's own six (the routes' own
// lists hold those, this deployment's hlverifier among them since F6: M1-NOTES.md's F1 finding F-3, E2-RL3), not the
// Solana deployment's reference successor (its launch never deploys one), not the BUBBLE token realm (each route's
// own list holds it), not a user, not the empty address.
func TestSolanaRealms(cur realm, t *testing.T) {
	const sol = "gno.land/r/g1leu8d2vsplhehcfkjg50mwgdpxdkt8tztu95wr/"
	for _, name := range []string{"hlmailbox", "hlverifier", "hlgnot", "hlbubble", "hlfees", "hlgov"} {
		if !IsSolanaRealm(chain.PackageAddress(sol + name)) {
			t.Fatalf("%s%s is not one of the Solana deployment's realms", sol, name)
		}
		if IsSolanaRealm(chain.PackageAddress(namespace + name)) {
			t.Fatalf("this deployment's %s answers as the Solana deployment's", name)
		}
	}
	for _, p := range []string{sol + "hlgnot2", sol + "bubble", sol + "eth", namespace + "hlgnot2", pkgPath} {
		if IsSolanaRealm(chain.PackageAddress(p)) {
			t.Fatalf("%s answers as one of the Solana deployment's six", p)
		}
	}
	if IsSolanaRealm(testutils.TestAddress("hl-solana-user")) || IsSolanaRealm("") {
		t.Fatalf("a user or the empty address answers as a Solana realm")
	}
	// the dev tree's two namespaces are the operator's one, with and without eth/ (eth §4)
	if solanaNamespace != sol || solanaNamespace+"eth/" != namespace {
		t.Fatalf("the namespaces: %s, %s", solanaNamespace, namespace)
	}
}
