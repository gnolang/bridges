package hlmailbox

import (
	"chain"
	"strconv"
	"strings"
	"testing"

	"gno.land/p/nt/testutils/v0"
)

// The dev tree's launch values, spelled out here so the guard's golden values never come from the realm: the three
// test signers (the vectors'), the governor and seats 2–6 (testutils addresses), the sixteen contract placeholders
// in ethdeploy.gno's order, and the dev solanaNamespace.
var (
	devSignerKeys = []string{
		"ec4ff84bc5faf45e487f9026f51b72df9f75de612614948c9bb8ed21f7d5d593",
		"44204958a13ca0984fb9bbfb8beecdba35f2d1930a86f551e76669c1c394f65d",
		"9082be72255bbb5af3906569c52055c5dedfce85623f10d12226b4ffff6f1839",
	}
	devGovernor = address("g1dpkz6em0wejhymn0wf047h6lta047h6lvxn4gx")
	test1       = address("g1jg8mtutu9khhfwc4nxmuhcpftf0pajdhfvsqf5") // gnodev's test1: the e2e's governor and seat 1
	devSeats    = []address{"g1dpkz6em4v9exg6tpdcknyh6lta047h6lg2eyz4", "g1dpkz6em4v9exg6tpdcknxh6lta047h6ll43gpz",
		"g1dpkz6em4v9exg6tpdckngh6lta047h6lp6q9gu", "g1dpkz6ampw33kstf4ta047h6lta047h6l20k9es", "g1dpkz6ampw33kstfkta047h6lta047h6l39yjqs"}
	devContractAddrs = []string{"0x0404040404040404040404040404040404040404", "0x0BA554Aa0D0566e582dE0238a089Ec102c2e05a4",
		"0x051D52Fd082d902f8E0CF300a60304D484cf2d07", "0x0303030303030303030303030303030303030303",
		"0x0202020202020202020202020202020202020202", "0x0101010101010101010101010101010101010101",
		"0x0505050505050505050505050505050505050505", "0x0606060606060606060606060606060606060606",
		"0x0707070707070707070707070707070707070707", "0x0808080808080808080808080808080808080808",
		"0x0909090909090909090909090909090909090909", "0x7070707070707070707070707070707070707070",
		"0x8080808080808080808080808080808080808080", "0x9090909090909090909090909090909090909090",
		"0x2525252525252525252525252525252525252525", "0x1212121212121212121212121212121212121212"}
	devContractConsts = []string{"launchEthMailbox", "launchEthToken0", "launchEthToken1", "launchEthClient",
		"launchEthGate", "launchEthHlAdmin", "launchEthProxyAdmin", "launchEthMerkleTreeHook", "launchEthProtocolFee",
		"launchEthMailboxImpl", "launchEthToken0Impl", "launchEthToken1Impl", "launchEthClientImpl", "launchEthGateImpl",
		"launchEthEd25519Verifier", "launchEthHlAdminExec"}
	devSolana = "gno.land/r/g1leu8d2vsplhehcfkjg50mwgdpxdkt8tztu95wr/"
	// the Solana deployment's namespace on gnoland-1 (read there 2026-10-14), what the stage step writes
	liveSolana = "gno.land/r/g1zdaz6sypk5tdsjc2fzsqrrn4ttn8qzmnl4waqp/"
	jaeCold    = address("g1ecsuj0q572jr0dhu29q9njtnmw03hyu7tyyvv6") // seat 1: Jae's cold key, a real key
)

const mainnetRefusal = ": refused on gnoland-1, where the stage step writes the deployed value"

// staged is a launch set as a mainnet stage step writes it: fresh signer keys under the compiled rule of 2,
// governor, seats 2–6, sixteen distinct contract addresses and the live Solana namespace; seat 1 kept (Jae's cold
// key, as compiled).
func staged() launchValues {
	v := launchValues{threshold: launchThreshold, governor: testutils.TestAddress("f6-staged-governor"), solana: liveSolana}
	for i := 0; i < 3; i++ {
		var k Key
		k[0], k[31] = 0xf6, byte(i+1)
		v.signers = append(v.signers, k)
	}
	v.seats[1] = jaeCold
	for n := 2; n <= seatCount; n++ {
		v.seats[n] = testutils.TestAddress("f6-staged-seat-" + strconv.Itoa(n))
	}
	for i := range v.contracts {
		v.contracts[i][12], v.contracts[i][31] = 0xf6, byte(i+1)
	}
	return v
}

// setChainID sets the chain id the code a test calls reads (runtime.ChainID): the harness runs init at its own
// chain id, "dev", and only a test function's later calls see another.
func setChainID(id string) {
	ctx := testing.GetContext()
	ctx.ChainID = id
	testing.SetContext(ctx)
}

// panicOf runs f and returns what it panicked with, or "": a panic inside this realm crosses no boundary, so revive
// (aborts) never sees it, and a deferred recover does.
func panicOf(f func()) (msg string) {
	defer func() {
		if r := recover(); r != nil {
			msg, _ = r.(string)
		}
	}()
	f()
	return ""
}

// TestMainnetPlaceholders (the round-2 realms audit's E2-RL1): on gnoland-1, init refuses the deploy while a launch
// value is still the dev tree's — any of the three test signers (whose seeds are public), the governor, a seat holding
// a testutils address or gnodev's test1, any of the sixteen contract placeholders on any line and in any spelling,
// the dev solanaNamespace — naming which; and, its review's two (F6), a governor or seat in any but its canonical
// spelling, and any solanaNamespace but the Solana deployment's own on gnoland-1. A tree staged with real values
// passes (seat 1 kept as Jae's cold key, the governor "" for GovDAO from the first block); on every other chain the
// guard reads nothing. The rule is devPlaceholder,
// called with a chain id (the harness runs init at "dev" and cannot run it at another); init's own call,
// mustNotBeDevTree, reads runtime.ChainID, which the test sets for it with testing.SetContext. The json shows this
// deployment's namespace and the Solana one with its six realms.
func TestMainnetPlaceholders(cur realm, t *testing.T) {
	// this tree is the dev tree: refused on gnoland-1, naming the first dev value, and taken everywhere else
	if why := devPlaceholder("gnoland-1", compiledLaunchValues()); why != "hlmailbox: launchSigners holds a dev tree test signer, "+devSignerKeys[0]+" (signer 0), whose ed25519 seed is public"+mainnetRefusal {
		t.Fatalf("the dev tree on gnoland-1: %q", why)
	}
	for _, id := range []string{"dev", "test5", "gnoland-2", "", "staging"} {
		if why := devPlaceholder(id, compiledLaunchValues()); why != "" {
			t.Fatalf("the dev tree on %q: %q", id, why)
		}
	}
	// a staged tree passes, with seat 1 kept and with no governor compiled
	if why := devPlaceholder("gnoland-1", staged()); why != "" {
		t.Fatalf("a staged tree refused: %q", why)
	}
	v := staged()
	v.governor = ""
	if why := devPlaceholder("gnoland-1", v); why != "" {
		t.Fatalf("a staged tree with no governor refused: %q", why)
	}
	// each test signer, in each position among real keys, and spelled as the stage step might copy it (upper case)
	for i, h := range devSignerKeys {
		for pos := 0; pos < 3; pos++ {
			v := staged()
			v.signers[pos] = parseKey(h)
			if pos == 1 {
				v.signers[pos] = parseKey("0x" + strings.ToUpper(h))
			}
			if why := devPlaceholder("gnoland-1", v); why != "hlmailbox: launchSigners holds a dev tree test signer, "+h+" (signer "+strconv.Itoa(pos)+"), whose ed25519 seed is public"+mainnetRefusal {
				t.Fatalf("test signer %d at %d: %q", i, pos, why)
			}
		}
	}
	// the governor, and each seat (1 to 6) holding any of the dev addresses or gnodev's test1
	devs := append([]address{test1, devGovernor}, devSeats...)
	for _, a := range devs {
		v := staged()
		v.governor = a
		if why := devPlaceholder("gnoland-1", v); why != "hlmailbox: launchGovernor is a test address, "+string(a)+", whose key nobody holds or anybody can"+mainnetRefusal {
			t.Fatalf("governor %s: %q", a, why)
		}
		for n := 1; n <= seatCount; n++ {
			v := staged()
			v.seats[n] = a
			if why := devPlaceholder("gnoland-1", v); why != "hlmailbox: launchSeat"+strconv.Itoa(n)+" is a test address, "+string(a)+", whose key nobody holds or anybody can"+mainnetRefusal {
				t.Fatalf("seat %d %s: %q", n, a, why)
			}
		}
	}
	// a governor or a seat in any but its canonical spelling — a staged address in upper case, a dev one in upper
	// case, either with bech32m's checksum (eth F9, its review's finding 2: IsValid takes it, and a dev address so
	// spelled misses the digests), no address at all — is one no caller's address ever equals; an empty seat stays an
	// empty seat
	for _, a := range []address{address(strings.ToUpper(string(jaeCold))), address(strings.ToUpper(string(devGovernor))), spellings(jaeCold)[2], spellings(devGovernor)[2], spellings(devGovernor)[3], "g1notanaddress", "jae"} {
		v := staged()
		v.governor = a
		if why := devPlaceholder("gnoland-1", v); why != "hlmailbox: launchGovernor is "+string(a)+", no gno address in its canonical form (bech32, lower case), so no call would ever be the governor's"+mainnetRefusal {
			t.Fatalf("governor %s: %q", a, why)
		}
		for n := 1; n <= seatCount; n++ {
			v := staged()
			v.seats[n] = a
			if why := devPlaceholder("gnoland-1", v); why != "hlmailbox: launchSeat"+strconv.Itoa(n)+" is "+string(a)+", no gno address in its canonical form (bech32, lower case), so no call would ever be the seat's"+mainnetRefusal {
				t.Fatalf("seat %d %s: %q", n, a, why)
			}
		}
	}
	v = staged()
	v.seats[6] = ""
	if why := devPlaceholder("gnoland-1", v); why != "" {
		t.Fatalf("an empty seat refused: %q", why)
	}
	// each contract placeholder, on its own line and on every other one, in its own spelling and in lower case
	if len(devContractAddrs) != EthContracts || len(devContractConsts) != EthContracts {
		t.Fatalf("the test's list is not the sixteen")
	}
	for j, s := range devContractAddrs {
		for i := 0; i < EthContracts; i++ {
			v := staged()
			spelled := s
			if (i+j)%2 == 1 {
				spelled = strings.ToLower(s)
			}
			k, err := ParseEthAddress(spelled)
			if err != nil {
				t.Fatalf("%s: %v", spelled, err)
			}
			v.contracts[i] = k
			if why := devPlaceholder("gnoland-1", v); why != "hlmailbox: "+devContractConsts[i]+" ("+ethContractNames[i]+") is a dev tree placeholder, "+s+mainnetRefusal {
				t.Fatalf("placeholder %s on line %d: %q", s, i, why)
			}
		}
	}
	v = staged()
	v.solana = devSolana
	if why := devPlaceholder("gnoland-1", v); why != "hlmailbox: solanaNamespace is the dev tree's, "+devSolana+", where no realm of the Solana deployment lives"+mainnetRefusal {
		t.Fatalf("the dev solanaNamespace: %q", why)
	}
	// on gnoland-1 the Solana namespace is the live one or nothing: this deployment's own, the live one without its
	// slash or one character off, the dev one one character off
	for _, ns := range []string{namespace, liveSolana[:len(liveSolana)-1], liveSolana[:len(liveSolana)-2] + "x/", devSolana[:len(devSolana)-2] + "x/", ""} {
		v := staged()
		v.solana = ns
		if why := devPlaceholder("gnoland-1", v); why != "hlmailbox: solanaNamespace is "+ns+", not the Solana deployment's on gnoland-1 (the namespace hashing to e0f650b7da312675...): a burn naming one of its six realms would be paid there, never swept"+mainnetRefusal {
			t.Fatalf("solanaNamespace %q: %q", ns, why)
		}
	}
	// a near miss of a dev contract is a real address: the guard holds the exact ones (0x0404…0405, one bit off the
	// mailbox's placeholder)
	v = staged()
	for i := 12; i < 32; i++ {
		v.contracts[EthMailbox][i] = 0x04
	}
	v.contracts[EthMailbox][31] ^= 1
	if why := devPlaceholder("gnoland-1", v); why != "" {
		t.Fatalf("a near miss refused: %q", why)
	}
	// init's call reads the chain id at deploy: on gnoland-1 this tree stops there, on any other it goes on
	setChainID("gnoland-1")
	if why := panicOf(func() { mustNotBeDevTree() }); why != "hlmailbox: launchSigners holds a dev tree test signer, "+devSignerKeys[0]+" (signer 0), whose ed25519 seed is public"+mainnetRefusal {
		t.Fatalf("init's guard on gnoland-1: %q", why)
	}
	setChainID("dev")
	if why := panicOf(func() { mustNotBeDevTree() }); why != "" {
		t.Fatalf("init's guard on dev: %q", why)
	}
	// the json names both namespaces and the Solana deployment's six realms
	j := Render("json")
	if !strings.Contains(j, ",\"namespace\":\""+namespace+"\",\"solanaNamespace\":\""+devSolana+"\",\"solanaRealms\":{\"hlmailbox\":\""+string(chain.PackageAddress(devSolana+"hlmailbox"))+"\",\"hlverifier\":\""+string(chain.PackageAddress(devSolana+"hlverifier"))+"\",\"hlgnot\":\""+string(chain.PackageAddress(devSolana+"hlgnot"))+"\",\"hlbubble\":\""+string(chain.PackageAddress(devSolana+"hlbubble"))+"\",\"hlfees\":\""+string(chain.PackageAddress(devSolana+"hlfees"))+"\",\"hlgov\":\""+string(chain.PackageAddress(devSolana+"hlgov"))+"\"},\"ethContracts\":") {
		t.Fatalf("json: %s", j)
	}
}

// honest is the launch set the round-3 realms audit staged as an honest gnoland-1 stage writes it (its
// stage_honest.py): Jae's cold key as the governor and as seat 1, the Solana deployment's live seats as 2 to 6, three
// fresh signer keys under the compiled rule of 2, sixteen fresh contracts and the live Solana namespace.
func honest() launchValues {
	v := launchValues{threshold: launchThreshold, governor: jaeCold, solana: liveSolana}
	for i := 0; i < 3; i++ {
		var k Key
		for j := range k {
			k[j] = byte(0xa0 + i)
		}
		v.signers = append(v.signers, k)
	}
	v.seats = [seatCount + 1]address{"", jaeCold, "g16l5nrlrp9pd8rdld7vzylrthfusv30apeq0n6z", "g1acuz6nhl9hyka2a4t9t965kmd8t355hy7ukur5",
		"g1d6nckzj9n8udw8apwrxfg929p8zm330d664ucy", "g1m8d47hlnmp77rpkamxphlx9q6jfvejt89j7hfj", "g1gdudkuvqdxwfncvmualymvyufdj8u7m893czuv"}
	for i := range v.contracts {
		v.contracts[i][12], v.contracts[i][31] = 0xf6, byte(i+1)
	}
	return v
}

// emptied is the same stage with its shell variables come out empty (stage_honest.py's "empty"): no signer key, no
// governor, seats 2 to 6 empty — seat 1 is compiled, as the stage step leaves it.
func emptied() launchValues {
	v := honest()
	v.signers, v.governor = nil, ""
	for n := 2; n <= seatCount; n++ {
		v.seats[n] = ""
	}
	return v
}

// TestLaunchFault (the round-3 realms audit's E3-RL2): beside the dev values, init refuses a launch set no launch can
// run — on every chain a signer rule the set cannot meet (a threshold below 1 or above the number of keys, an empty set
// among them) and a key in two seats; on gnoland-1 also an empty governor and an empty seat — naming which. The
// audit's honest set deploys on gnoland-1 and on a test chain; its empty set is refused on both, by its rule first.
// The dev values still come first (launchGuard), and the dev tree still deploys on its own chain.
func TestLaunchFault(cur realm, t *testing.T) {
	const tail = ": refused on gnoland-1, where the launch is the governor's and the six seats'"
	rule := func(threshold, n int) string {
		return "hlmailbox: launchThreshold is " + strconv.Itoa(threshold) + " and launchSigners names " + strconv.Itoa(n) + " keys: the launch rule asks between 1 and the number of signers, or no credit is ever verified"
	}
	for _, id := range []string{"gnoland-1", "dev"} {
		if why := launchGuard(id, honest()); why != "" {
			t.Fatalf("the honest set on %s: %q", id, why)
		}
		if why := launchGuard(id, emptied()); why != rule(2, 0) {
			t.Fatalf("the empty set on %s: %q", id, why)
		}
	}
	// the rule, on every chain
	for _, c := range []struct{ keys, threshold int }{{3, 0}, {3, -1}, {3, 4}, {1, 2}, {0, 1}} {
		for _, id := range []string{"gnoland-1", "dev"} {
			v := honest()
			v.signers, v.threshold = v.signers[:c.keys], c.threshold
			if why := launchGuard(id, v); why != rule(c.threshold, c.keys) {
				t.Fatalf("%d keys, threshold %d on %s: %q", c.keys, c.threshold, id, why)
			}
		}
	}
	for _, c := range []struct{ keys, threshold int }{{3, 1}, {3, 3}, {1, 1}} {
		v := honest()
		v.signers, v.threshold = v.signers[:c.keys], c.threshold
		if why := launchGuard("gnoland-1", v); why != "" {
			t.Fatalf("%d keys, threshold %d refused: %q", c.keys, c.threshold, why)
		}
	}
	// a key in two seats, every pair, on every chain; two empty seats are no pair
	for n := 2; n <= seatCount; n++ {
		for m := 1; m < n; m++ {
			for _, id := range []string{"gnoland-1", "dev"} {
				v := honest()
				v.seats[n] = v.seats[m]
				if why := launchGuard(id, v); why != "hlmailbox: launchSeat"+strconv.Itoa(m)+" and launchSeat"+strconv.Itoa(n)+" are both "+string(v.seats[m])+": a key holds one seat, so the six can pause as six" {
					t.Fatalf("seats %d and %d on %s: %q", m, n, id, why)
				}
			}
		}
	}
	v := honest()
	v.seats[5], v.seats[6] = "", ""
	if why := launchGuard("dev", v); why != "" {
		t.Fatalf("two empty seats on dev: %q", why)
	}
	// gnoland-1 alone: a governor and six filled seats (a test chain's stage may leave either empty)
	v = honest()
	v.governor = ""
	if why := launchGuard("gnoland-1", v); why != "hlmailbox: launchGovernor is empty, which would hand GovDAO the bridge from its first block"+tail {
		t.Fatalf("no governor on gnoland-1: %q", why)
	}
	if why := launchGuard("dev", v); why != "" {
		t.Fatalf("no governor on dev: %q", why)
	}
	for n := 1; n <= seatCount; n++ {
		v := honest()
		v.seats[n] = ""
		if why := launchGuard("gnoland-1", v); why != "hlmailbox: launchSeat"+strconv.Itoa(n)+" is empty, a seat nobody can pause from"+tail {
			t.Fatalf("seat %d empty on gnoland-1: %q", n, why)
		}
		if why := launchGuard("dev", v); why != "" {
			t.Fatalf("seat %d empty on dev: %q", n, why)
		}
	}
	// the dev values first: the dev tree is named by its first test signer on gnoland-1, and deploys on its own chain
	if why := launchGuard("gnoland-1", compiledLaunchValues()); why != "hlmailbox: launchSigners holds a dev tree test signer, "+devSignerKeys[0]+" (signer 0), whose ed25519 seed is public"+mainnetRefusal {
		t.Fatalf("the dev tree on gnoland-1: %q", why)
	}
	if why := launchGuard("dev", compiledLaunchValues()); why != "" {
		t.Fatalf("the dev tree on dev: %q", why)
	}
	// a staged set (TestMainnetPlaceholders') passes the whole guard on gnoland-1 too
	if why := launchGuard("gnoland-1", staged()); why != "" {
		t.Fatalf("a staged set: %q", why)
	}
}

// TestInitGuarded (the round-3 realms audit's E3-RL7): init called its guard. guardedOn is what the call returned, the
// chain id it checked the launch values at (the harness runs init at its own, "dev"): a tree whose init lost the call
// — which only a hand-run stage control saw before, the unit suite passing — fails here.
func TestInitGuarded(cur realm, t *testing.T) {
	if guardedOn == "" {
		t.Fatalf("init never ran its guard (mustNotBeDevTree): guardedOn is empty")
	}
	t.Logf("init's guard checked the launch values at chain %q", guardedOn)
}
