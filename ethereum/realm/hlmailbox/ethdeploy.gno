package hlmailbox

// The Ethereum deployment's contracts (eth/bridge/PLAN.md §5.8): every address the setup step creates — the five
// contracts behind a proxy, the plain ones, and the five proxies' launch implementations, sixteen in all (M2-NOTES.md
// §1 G: the eleven codes and the five proxies) — compiled here, the one list the deployment keeps of them. A route
// refuses each as a send's recipient (the red team's ER10: tokens minted to a contract of the bridge are stuck
// there), and r/hlgov takes from it HlAdmin, the first decision recipient, and the five proxies its kind 8 may
// upgrade. PLACEHOLDERS until the Ethereum side is deployed (eth §5.8: it is, and audited, before the realms are
// uploaded): patterned addresses, and for the two tokens the routes' own launch routers (r/hlgnot's and r/hlbubble's
// launchRouter, which the stage step swaps for the same deployed proxies). Each on a line of its own, so the stage
// step's sed finds it; each read by ParseEthAddress when the realm is deployed, so a malformed one fails the deploy,
// and on gnoland-1 a placeholder left in place fails it too (mainnet.gno).
const (
	launchEthMailbox         = "0x0404040404040404040404040404040404040404" // Hyperlane's Mailbox (proxy)
	launchEthToken0          = "0x0BA554Aa0D0566e582dE0238a089Ec102c2e05a4" // HypERC20 GNOT (proxy): r/hlgnot's router
	launchEthToken1          = "0x051D52Fd082d902f8E0CF300a60304D484cf2d07" // HypERC20 BUBBLE (proxy): r/hlbubble's router
	launchEthClient          = "0x0303030303030303030303030303030303030303" // GnoLightClient (proxy)
	launchEthGate            = "0x0202020202020202020202020202020202020202" // PausableGate (proxy)
	launchEthHlAdmin         = "0x0101010101010101010101010101010101010101" // HlAdmin (plain, immutable)
	launchEthProxyAdmin      = "0x0505050505050505050505050505050505050505" // OpenZeppelin's ProxyAdmin (plain)
	launchEthMerkleTreeHook  = "0x0606060606060606060606060606060606060606" // Hyperlane's MerkleTreeHook (plain)
	launchEthProtocolFee     = "0x0707070707070707070707070707070707070707" // Hyperlane's ProtocolFee hook (plain)
	launchEthMailboxImpl     = "0x0808080808080808080808080808080808080808" // the mailbox's implementation at launch
	launchEthToken0Impl      = "0x0909090909090909090909090909090909090909" // token 0's implementation at launch
	launchEthToken1Impl      = "0x7070707070707070707070707070707070707070" // token 1's implementation at launch
	launchEthClientImpl      = "0x8080808080808080808080808080808080808080" // the light client's implementation at launch
	launchEthGateImpl        = "0x9090909090909090909090909090909090909090" // the gate's implementation at launch
	launchEthEd25519Verifier = "0x2525252525252525252525252525252525252525" // Ed25519Verifier (plain, immutable): the client's ED25519()
	launchEthHlAdminExec     = "0x1212121212121212121212121212121212121212" // HlAdminExec (plain, immutable): HlAdmin's exec(), its CREATE
)

// The deployment's contracts by index (EthContractAt): the five behind a proxy first, in r/hlgov's kind-8 order; the
// two the build added (M2-NOTES.md §1 G) last, so every earlier index keeps its place.
const (
	EthMailbox = iota
	EthToken0
	EthToken1
	EthClient
	EthGate
	EthHlAdmin
	EthProxyAdmin
	EthMerkleTreeHook
	EthProtocolFee
	EthMailboxImpl
	EthToken0Impl
	EthToken1Impl
	EthClientImpl
	EthGateImpl
	EthEd25519Verifier
	EthHlAdminExec
	EthContracts // how many
)

// ethContractNames name the contracts in the index's order, as pages and refusals say them.
var ethContractNames = [EthContracts]string{
	"the mailbox", "token 0 (GNOT)", "token 1 (BUBBLE)", "the light client", "the gate",
	"HlAdmin", "the ProxyAdmin", "the MerkleTreeHook", "the ProtocolFee hook",
	"the mailbox's implementation", "token 0's implementation", "token 1's implementation",
	"the light client's implementation", "the gate's implementation",
	"the Ed25519Verifier", "HlAdminExec",
}

// ethContracts are the compiled addresses, 32 bytes each, in the index's order: written once, at deploy.
var ethContracts = distinctContracts([EthContracts][32]byte{
	mustEth(launchEthMailbox), mustEth(launchEthToken0), mustEth(launchEthToken1), mustEth(launchEthClient),
	mustEth(launchEthGate), mustEth(launchEthHlAdmin), mustEth(launchEthProxyAdmin), mustEth(launchEthMerkleTreeHook),
	mustEth(launchEthProtocolFee), mustEth(launchEthMailboxImpl), mustEth(launchEthToken0Impl),
	mustEth(launchEthToken1Impl), mustEth(launchEthClientImpl), mustEth(launchEthGateImpl),
	mustEth(launchEthEd25519Verifier), mustEth(launchEthHlAdminExec),
})

// distinctContracts refuses the deploy when two contracts of the list share an address — a stage step that wrote one
// record field into two lines: the sixteen are distinct deployments, and a proxy written as both tokens (with both
// routes' routers) would pass both routes' init checks against one token.
func distinctContracts(cs [EthContracts][32]byte) [EthContracts][32]byte {
	if why := duplicateContract(cs); why != "" {
		// echo-ok(why): the guard's own refusal: words, contract names, EIP-55 and bech32 addresses, a few hundred bytes at most: far under the cut
		panic(why)
	}
	return cs
}

// duplicateContract names the first two contracts of cs at one address, or "" when every address is its own.
func duplicateContract(cs [EthContracts][32]byte) string {
	for i := range cs {
		for j := i + 1; j < len(cs); j++ {
			if cs[i] == cs[j] {
				return "hlmailbox: " + ethContractNames[i] + " and " + ethContractNames[j] + " are both " + EthAddress(cs[i]) + ": every contract of the deployment has its own address"
			}
		}
	}
	return ""
}

// mustEth reads a compiled Ethereum address, refusing the deploy on a malformed or zero one.
func mustEth(s string) [32]byte {
	k, err := ParseEthAddress(s)
	if err != nil {
		// echo-ok(err): ParseEthAddress's refusal, which echoes what it read (echo, E4-GS1)
		panic("hlmailbox: " + err.Error())
	}
	if k == ([32]byte{}) {
		panic("hlmailbox: a contract of the deployment at the zero address")
	}
	return k
}

// EthContractAt is the deployment's contract at index i (EthMailbox … EthHlAdminExec): its name and its address, 32
// bytes left-padded as Hyperlane carries an address; "" and zeros past the list.
func EthContractAt(i int) (name string, addr [32]byte) {
	if i < 0 || i >= EthContracts {
		return "", addr
	}
	return ethContractNames[i], ethContracts[i]
}

// EthContractName is the name of the deployment's contract at k, "" when k is none of them.
func EthContractName(k [32]byte) string {
	for i, c := range ethContracts {
		if c == k {
			return ethContractNames[i]
		}
	}
	return ""
}

// lowEthAddress is whether k, an Ethereum address, is at or below 0xffff: the precompiles (0x01–0x11 today, 0x100
// P256VERIFY) and the range the protocol reserves for more, the zero address with them.
func lowEthAddress(k [32]byte) bool {
	for _, c := range k[:30] {
		if c != 0 {
			return false
		}
	}
	return true
}

// RefusedEthRecipient is why a route refuses k, an Ethereum address ParseEthAddress read, as the recipient of a
// send — or "" when it does not (eth §5.2; the red team's ER10): the zero address (an ERC-20 refuses to mint to it,
// so the delivery would revert for good), the route's own router (its token contract), an address at or below
// 0xffff (a precompile's or one the protocol reserves), and every contract of this deployment (the list above).
// Each would leave what is minted where nobody can move it. router is the route's router now, which the list holds
// at launch and a router swap may change. The reason is the words after "the recipient is ".
func RefusedEthRecipient(k, router [32]byte) string {
	switch {
	case k == ([32]byte{}):
		return "the zero address: an ERC-20 refuses to mint to it, so the delivery would revert for good"
	case k == router:
		return "this route's own router, its token contract on Ethereum: what it mints to itself nobody can move"
	case lowEthAddress(k):
		return EthAddress(k) + ", at or below 0xffff: a precompile's or an address the protocol reserves, where what is minted nobody can move"
	}
	if name := EthContractName(k); name != "" {
		return EthAddress(k) + ", " + name + " of this deployment on Ethereum: a contract of the bridge, where what is minted nobody can move"
	}
	return ""
}
