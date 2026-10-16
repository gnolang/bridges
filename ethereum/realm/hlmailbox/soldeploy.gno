package hlmailbox

import "chain"

// The Solana deployment's realms (eth/bridge/PLAN.md §4: the two deployments share the operator's namespace and the
// three pure packages, nothing else). A burn on Ethereum may name one of them as its recipient — a holder who pastes
// the Solana route's address, which that route's page prints as the lock's holder — and a claim paid there would land
// in that realm's account outside every counter of either deployment: the Solana fee realm would pay it to its
// treasurer as fees, the others hold it where no code moves it. So the routes sweep such a credit, to be credited by
// id to its owner, as they sweep one naming a realm in their own list (isRealm), and refuse such an address as a
// vote's recipient (E-M1's Q6). A send's recipient is an Ethereum address: no send asks. The six are the Solana
// launch's; a successor a Solana migration names later is not among them.

// solanaNamespace is the Solana deployment's namespace: the operator's, this deployment's without its eth/ segment
// (eth §4, §11.8). The stage step rewrites it as it rewrites namespace; were this deployment staged under another
// namespace than the Solana deployment's, the stage step would write the Solana one here instead — on gnoland-1 the
// Solana realms live under g1zdaz6sypk5tdsjc2fzsqrrn4ttn8qzmnl4waqp, and there init takes that namespace and no other
// (mainnet.gno, E2-RL1). The json shows it, its six realms' addresses and this deployment's own namespace, for
// hlwatch to check.
// Its own line, so one sed on `^const solanaNamespace = "…"` finds it.
const solanaNamespace = "gno.land/r/g1leu8d2vsplhehcfkjg50mwgdpxdkt8tztu95wr/"

// solanaNames are the Solana deployment's six realms, by name under solanaNamespace — never its reference
// successor, hlgnot2, which its launch does not deploy.
var solanaNames = [6]string{"hlmailbox", "hlverifier", "hlgnot", "hlbubble", "hlfees", "hlgov"}

// solanaRealms are their addresses, in solanaNames' order.
var solanaRealms = [6]address{
	chain.PackageAddress(solanaNamespace + "hlmailbox"), chain.PackageAddress(solanaNamespace + "hlverifier"),
	chain.PackageAddress(solanaNamespace + "hlgnot"), chain.PackageAddress(solanaNamespace + "hlbubble"),
	chain.PackageAddress(solanaNamespace + "hlfees"), chain.PackageAddress(solanaNamespace + "hlgov"),
}

// IsSolanaRealm is whether a is the address of one of the Solana deployment's six realms: a route sweeps a credit
// naming one and refuses one as a vote's recipient (E-M1's Q6).
func IsSolanaRealm(a address) bool {
	for _, r := range solanaRealms {
		if r == a {
			return true
		}
	}
	return false
}
