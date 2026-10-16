package hlmailbox

import (
	"chain/runtime"
	"crypto/sha256"
	"strconv"
	"strings"
)

// The mainnet guard (the round-2 realms audit's E2-RL1). The dev tree compiles a test value wherever a launch value
// stands — three signer keys whose ed25519 seeds are public (sha256("hyperlane gno test validator 0|1|2"): anyone
// signs a 2-of-3 checkpoint with them, so a verifier over them credits any forged burn), a governor and five seats no
// key holds (testutils addresses), patterned Ethereum addresses for the sixteen contracts, the dev namespace as the
// Solana deployment's — and the stage step writes the real ones. A line it misses deploys silently: forged credits
// for anyone (the signers), a seat that can never pause, decisions sent to no HlAdmin, a sweep that matches nothing.
// So on gnoland-1, init refuses the deploy while any of them still equals the dev tree's, naming which. The guard holds
// each dev value's sha256, never the value itself, so no stage step's sed — which rewrites the dev values wherever
// they stand — can rewrite the guard along with them. It hashes a key or an Ethereum address as the 32 bytes init
// parses it to, so no respelling (case, padding, order) gets past; a gno address it hashes as its string, after
// requiring the string in its one canonical form (valid bech32, lower case), which every caller's address takes — a
// governor or a seat in any other spelling is one no call ever matches, refused for that alone. Each value is held
// against every dev value of its kind, so a dev value staged on another value's line is refused too. Seat 1 is not
// among the dev values: it is Jae's cold key, a real key the launch may keep; gnodev's test1, the e2e's governor and
// seat 1, whose mnemonic is public, is. And solanaNamespace must be the Solana deployment's own on gnoland-1, where it
// is known: held by its sha256 too, it refuses the dev value and every other. Off gnoland-1 (the e2e's dev chain, a
// Sepolia stage's test chain) the guard reads no dev value. Beside it, launchFault holds the set's own sense (E3-RL2):
// on every chain a signer rule the set can meet and no key in two seats, on gnoland-1 a governor and six filled
// seats too. The other realms guard the launch values they compile
// themselves the same way (r/hlgov its six Ethereum seats and the HlAdmin it reads from here, each route its router),
// keyed on LocalDomain; what they read from here, this guard has already held, since a realm cannot deploy before its
// imports.

// devKeys are the sha256 of the dev tree's three signer keys, each key's 32 bytes: the vectors' test signers 0, 1
// and 2, ed25519.NewKeyFromSeed(sha256("hyperlane gno test validator <i>")).
var devKeys = [...]string{
	"70796c41339e075199c6ec085c82e1f58144e97384096ff13258d0f580a32285",
	"6f5b1fa76f37b759efab316313c1dc71e6490c771f681abdc95b319f85bc66d9",
	"7a61f12375ad2da38603865ea8a9329e6c457c0cbf7505edaea21ff5f6f698ee",
}

// devAddresses are the sha256 of the test gno addresses, each address's bech32 string: the dev tree's, which no key
// holds — the governor, testutils.TestAddress("hl-governor"), and seats 2 to 6, testutils.TestAddress of
// "hl-guardian-2", "hl-guardian-3", "hl-guardian-4", "hl-watch-5" and "hl-watch-6" — and gnodev's test1, the e2e's
// governor and seat 1, whose key anyone recovers from gnodev's published mnemonic.
var devAddresses = [...]string{
	"d34cfea7ed6dd2a35924cb34fc478c74e378bbff86a545b29bb89faad2b2423c",
	"2d9ca01e125093072e0b4189abea0d5355864237853649c4db99e0976588f43d",
	"d357727611cfb54ac1d901fe253f6ac081d605f7744b1152e6d396c0c003c594",
	"08c3a943acf1b24e1efe272b6c9d0db042f2a3a2a0a9f78f8fdfc8777fdc2440",
	"1260f12c7ee07e5221e3943a8a9ebcff0be200c8436b19a90fdf1f6c827075cf",
	"43b44508d640ececb85a6425a64bdae8c32887b56938fd7d870c2d16c8bb46b9",
	"7d79fb46d106c31e857fda2f5582281afd950fc015faccfa004e673e857c3fe8",
}

// devContracts are the sha256 of the dev tree's sixteen Ethereum contract placeholders (ethdeploy.gno), each as the
// 32 bytes ParseEthAddress reads it to, in the list's order: the patterned addresses and, for the two tokens, the
// vectors' test routes.
var devContracts = [EthContracts]string{
	"34ec77b0af7a4c5a6608c2f487a91fb393346daa3d090b8bcf9e676808682737",
	"79a3ac7ee4282caf9ff1dc5f561b2f32ae3959e61a8736ba9fdf87584dd020b4",
	"b07d0aa94b3b84d1dde8acbd0387cf2d6e0043853793d8fb7dde8439c2e3e747",
	"d8055cc8fae5f93a20d17c18550986850df8eaa5cce4cc0464a6cef2b624547f",
	"e3875c04b7e70de5cc9457cff2bc416036b6097180c25e07843f8ca9de2df59a",
	"c1590f5b1894d62193220ba8a3fd239aa81ac47238ebe5479de0384f40fe3ae7",
	"8893cd10b82a398e025cc41c2bc7115f3f3e2f4f4aa49f90d7e34dceae0d036b",
	"f5ffffe74438ec3a40249b7d883fb51109755ac1bc0a0f0fa30e3f6e198e9007",
	"ddb133005cd5476f28f35d52d9f8b6d2acf6ad61c5524ee2748e2871af944bda",
	"ee3b80328bdb738ae2c7260c85fc1ad142828bb55569ca686649de1b49767229",
	"13cb2024e16f98e486ca0bab92df762fe6a56d3ebdabf9d1af2b9a6f59e4ada8",
	"af46e04b8e1544dedf91c6fddd13f158ebf8db943c86d3c80d7e80abaad7802c",
	"50115a883386458e4b8c3065f2c015c17ad77b2ac77c13c42c3c3384d7ade78f",
	"339244f2785fcd372473bf3bb9e58e48e94d6de09fd6207a6387ac291b108f51",
	"4ac8c942c7602d18591071e26fe5b299d954e87bed974c325dfe9656c5d21993",
	"194d9be1d6b08b527e506daab0c78c144d67442bcaf28cdac093bd7ae8585fbf",
}

// devSolanaNamespace is the sha256 of the dev tree's solanaNamespace: the operator's dev namespace, under which the
// Solana deployment's realms do not live on gnoland-1.
const devSolanaNamespace = "50e33a3b95fb261b36d2b95def1c3514747ec71b673febc6438542b53e765b1a"

// mainnetSolanaNamespace is the sha256 of the Solana deployment's namespace on gnoland-1, read there 2026-10-14 (the
// round-2 realms audit: its six realms and pure packages live under g1zdaz6…): on gnoland-1 solanaNamespace is that
// one or nothing, since a launch's six realms never move. A stage step that rewrote the line with this deployment's
// own namespace, say, would leave IsSolanaRealm matching nothing — a burn naming one of the six paid there, never swept.
const mainnetSolanaNamespace = "e0f650b7da3126754c178b66d92507c6c5041e1e542e0046b2c5bbe79037d43d"

// launchValues are the launch values the guard reads, as init has them.
type launchValues struct {
	signers   []Key
	threshold int // launchThreshold: how many of the signers the launch rule asks
	governor  address
	seats     [seatCount + 1]address // by seat number; 0 unused
	contracts [EthContracts][32]byte
	solana    string // solanaNamespace
}

// compiledLaunchValues are this tree's launch values.
func compiledLaunchValues() launchValues {
	return launchValues{
		signers:   parseKeys(launchSigners),
		threshold: launchThreshold,
		governor:  launchGovernor,
		seats:     [seatCount + 1]address{"", launchSeat1, launchSeat2, launchSeat3, launchSeat4, launchSeat5, launchSeat6},
		contracts: ethContracts,
		solana:    solanaNamespace,
	}
}

// mustNotBeDevTree is init's guard over the compiled launch values (launchGuard). It returns the chain id it checked
// at, which init keeps (guardedOn) for the suite to read.
func mustNotBeDevTree() string {
	id := runtime.ChainID()
	if why := launchGuard(id, compiledLaunchValues()); why != "" {
		// echo-ok(why): the guard's own refusal: ASCII words, contract names, EIP-55 and bech32 addresses and pkgpaths, a few hundred bytes at most: far under the cut
		panic(why)
	}
	return id
}

// launchGuard is why the launch values v may not deploy on chain chainID, or "": on gnoland-1 a value still the dev
// tree's (devPlaceholder), and then a set no launch can run on any chain, or an incomplete one on gnoland-1
// (launchFault).
func launchGuard(chainID string, v launchValues) string {
	if why := devPlaceholder(chainID, v); why != "" {
		return why
	}
	return launchFault(chainID, v)
}

// launchFault names the first launch value of v that no launch on chain chainID may have, or "" (the round-3 realms
// audit's E3-RL2: the dev-value guard alone took a degenerate set — no signer, no governor, empty or doubled seats —
// which a stage step whose shell variables came out empty, or that copied one key into two seat lines, writes). On
// every chain: a rule the signers can meet, launchThreshold between 1 and their number (an empty set meets none: no
// credit for the 7 days a rotation takes), and no key in two seats (HlAdmin's launch seats are as strict on
// Ethereum, Guardians.init: no key twice). On gnoland-1 also a governor (the launch is Jae's key, eth §1: "" would
// hand GovDAO the bridge from its first block) and all six seats filled — the guardians' and the watch keys', the
// brake on forged credits, none of which can pause from an empty seat. A dev or Sepolia stage may leave a seat or the
// governor empty; no launch may compile a rule it cannot meet. Seats are compared as written: on gnoland-1
// devPlaceholder has held each to its one canonical spelling already, so one string is one key there.
func launchFault(chainID string, v launchValues) string {
	if v.threshold < 1 || v.threshold > len(v.signers) {
		return "hlmailbox: launchThreshold is " + strconv.Itoa(v.threshold) + " and launchSigners names " + strconv.Itoa(len(v.signers)) + " keys: the launch rule asks between 1 and the number of signers, or no credit is ever verified"
	}
	for n := 1; n <= seatCount; n++ {
		for m := 1; m < n; m++ {
			if v.seats[n] != "" && v.seats[n] == v.seats[m] {
				return "hlmailbox: launchSeat" + strconv.Itoa(m) + " and launchSeat" + strconv.Itoa(n) + " are both " + string(v.seats[n]) + ": a key holds one seat, so the six can pause as six"
			}
		}
	}
	if chainID != mainnetChainID {
		return ""
	}
	const tail = ": refused on " + mainnetChainID + ", where the launch is the governor's and the six seats'"
	if v.governor == "" {
		return "hlmailbox: launchGovernor is empty, which would hand GovDAO the bridge from its first block" + tail
	}
	for n := 1; n <= seatCount; n++ {
		if v.seats[n] == "" {
			return "hlmailbox: launchSeat" + strconv.Itoa(n) + " is empty, a seat nobody can pause from" + tail
		}
	}
	return ""
}

// devPlaceholder names the first launch value of v that is one of the dev tree's on chain chainID, or "": on
// gnoland-1 only, every other chain answering "".
func devPlaceholder(chainID string, v launchValues) string {
	if chainID != mainnetChainID {
		return ""
	}
	const tail = ": refused on " + mainnetChainID + ", where the stage step writes the deployed value"
	for i, k := range v.signers {
		if sha256In(devKeys[:], k[:]) {
			return "hlmailbox: launchSigners holds a dev tree test signer, " + hx(k[:]) + " (signer " + strconv.Itoa(i) + "), whose ed25519 seed is public" + tail
		}
	}
	if v.governor != "" && !canonical(v.governor) {
		return "hlmailbox: launchGovernor is " + string(v.governor) + ", no gno address in its canonical form (bech32, lower case), so no call would ever be the governor's" + tail
	}
	if sha256In(devAddresses[:], []byte(v.governor)) {
		return "hlmailbox: launchGovernor is a test address, " + string(v.governor) + ", whose key nobody holds or anybody can" + tail
	}
	for n := 1; n <= seatCount; n++ {
		if v.seats[n] != "" && !canonical(v.seats[n]) {
			return "hlmailbox: launchSeat" + strconv.Itoa(n) + " is " + string(v.seats[n]) + ", no gno address in its canonical form (bech32, lower case), so no call would ever be the seat's" + tail
		}
		if sha256In(devAddresses[:], []byte(v.seats[n])) {
			return "hlmailbox: launchSeat" + strconv.Itoa(n) + " is a test address, " + string(v.seats[n]) + ", whose key nobody holds or anybody can" + tail
		}
	}
	for i, c := range v.contracts {
		if sha256In(devContracts[:], c[:]) {
			key := ethContractKeys[i]
			return "hlmailbox: launchEth" + strings.ToUpper(key[:1]) + key[1:] + " (" + ethContractNames[i] + ") is a dev tree placeholder, " + EthAddress(c) + tail
		}
	}
	if sha256In([]string{devSolanaNamespace}, []byte(v.solana)) {
		return "hlmailbox: solanaNamespace is the dev tree's, " + v.solana + ", where no realm of the Solana deployment lives" + tail
	}
	if !sha256In([]string{mainnetSolanaNamespace}, []byte(v.solana)) {
		return "hlmailbox: solanaNamespace is " + v.solana + ", not the Solana deployment's on gnoland-1 (the namespace hashing to " + mainnetSolanaNamespace[:16] + "...): a burn naming one of its six realms would be paid there, never swept" + tail
	}
	return ""
}

// canonical is whether a is a gno address in the one spelling a caller's address takes (CanonicalAddress): bech32 —
// not bech32m, which IsValid takes too — in lower case.
func canonical(a address) bool { return a != "" && CanonicalAddress(a) == a }

// sha256In is whether b's sha256 is one of digests.
func sha256In(digests []string, b []byte) bool {
	h := sha256.Sum256(b)
	d := hx(h[:])
	for _, x := range digests {
		if x == d {
			return true
		}
	}
	return false
}
