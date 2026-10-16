package hlmailbox

import (
	"encoding/hex"
	"strings"
	"testing"
)

// The Ethereum carry (eth §0, §5.1, §5.2): the launch origin by chain id, and the one parser and writer of Ethereum
// addresses every realm of the deployment uses. The golden values are EIP-55's own examples (eips.ethereum.org/EIPS/
// eip-55), not this realm's output: each is decoded here with encoding/hex alone.

// padded is a 40-hex address, decoded here with encoding/hex and left-padded to 32 bytes: the expected bytes,
// computed apart from ParseEthAddress.
func padded(t *testing.T, h40 string) [32]byte {
	t.Helper()
	b, err := hex.DecodeString(strings.ToLower(h40))
	if err != nil || len(b) != 20 {
		t.Fatalf("bad test address %s", h40)
	}
	var out [32]byte
	copy(out[12:], b)
	return out
}

func parses(t *testing.T, s string, want [32]byte) {
	t.Helper()
	got, err := ParseEthAddress(s)
	if err != nil {
		t.Fatalf("%q refused: %v", s, err)
	}
	if got != want {
		t.Fatalf("%q parsed to %x, want %x", s, got, want)
	}
}

func refused(t *testing.T, s, want string) {
	t.Helper()
	_, err := ParseEthAddress(s)
	if err == nil {
		t.Fatalf("%q parsed; want a refusal containing %q", s, want)
	}
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("%q refused with %q, which lacks %q", s, err.Error(), want)
	}
}

// TestEthereumOrigin: the launch origin is Ethereum's domain — 1 on gnoland-1, the compiled test domain (31337 in
// the tree, a sed-able line of its own) on every other chain — and the page and json name it as Ethereum's.
func TestEthereumOrigin(cur realm, t *testing.T) {
	if ethereumOf("gnoland-1") != 1 || EthereumMainnet != 1 {
		t.Fatalf("gnoland-1's destination: %d", ethereumOf("gnoland-1"))
	}
	for _, id := range []string{"dev", "test12", "gnoland-2", ""} {
		if ethereumOf(id) != 31337 || ethereumOf(id) != testEthereumDomain {
			t.Fatalf("%q's destination: %d", id, ethereumOf(id))
		}
	}
	// init activated the launch verifier for Ethereum's domain and none for Solana's (whatever this suite's other
	// tests have activated since, at their own times)
	if LaunchOrigin() != 31337 || ActiveVerifier(31337) == "" || ActiveVerifier(1399811150) != "" || ActiveVerifier(1399811149) != "" {
		t.Fatalf("the launch origin %d, its verifier %q", LaunchOrigin(), ActiveVerifier(31337))
	}
	if p := Render(""); !strings.Contains(p, "- domain: 1198419796; Ethereum: 31337\n") || !strings.Contains(p, "the GNOT and BUBBLE bridge to Ethereum: messages out to Ethereum") || strings.Contains(p, "Solana") {
		t.Fatalf("page: %s", p)
	}
	if !strings.Contains(Render("json"), "\"launchOrigin\":31337,") {
		t.Fatalf("json: %s", Render("json"))
	}
}

// TestParseEthAddress: every accepted form, every refused one.
func TestParseEthAddress(cur realm, t *testing.T) {
	// EIP-55's mixed-case examples: accepted, and written back exactly so
	for _, a := range []string{
		"0x5aAeb6053F3E94C9b9A09f33669435E7Ef1BeAed",
		"0xfB6916095ca1df60bB79Ce92cE3Ea74c37c5d359",
		"0xdbF03B407c01E7cD3CBea99509d93f8DDDC8C6FB",
		"0xD1220A0cf47c7B9Be7A2E6BA89F429762e7b9aDb",
	} {
		want := padded(t, a[2:])
		parses(t, a, want)
		if EthAddress(want) != a {
			t.Fatalf("EthAddress(%x) = %s, want EIP-55's %s", want, EthAddress(want), a)
		}
		// the same address all in lower case and all in upper case: no checksum to check, the same bytes
		parses(t, "0x"+strings.ToLower(a[2:]), want)
		parses(t, "0x"+strings.ToUpper(a[2:]), want)
		// one letter's case flipped: a typo the checksum catches
		for i := 2; i < len(a); i++ {
			c := a[i]
			var flipped byte
			switch {
			case c >= 'a' && c <= 'f':
				flipped = c - 'a' + 'A'
			case c >= 'A' && c <= 'F':
				flipped = c - 'A' + 'a'
			default:
				continue
			}
			bad := a[:i] + string(flipped) + a[i+1:]
			if bad == "0x"+strings.ToLower(a[2:]) || bad == "0x"+strings.ToUpper(a[2:]) {
				continue // a flip that leaves one case throughout is no longer mixed: accepted as such
			}
			refused(t, bad, "is not its EIP-55 checksum")
		}
	}
	// EIP-55's all-caps and all-lower examples: accepted as they are
	for _, a := range []string{
		"0x52908400098527886E0F7030069857D2E4169EE7",
		"0x8617E340B3D01FA5F11F306F4090FD50E238070D",
		"0xde709f2102306220921060314715629080e2fb77",
		"0x27b1fdb04752bbc536007a920d24acb045561c26",
	} {
		parses(t, a, padded(t, a[2:]))
	}
	// 64 hex digits whose first 24 are zero, with or without 0x, any case: the same bytes
	a := "0x5aAeb6053F3E94C9b9A09f33669435E7Ef1BeAed"
	want := padded(t, a[2:])
	parses(t, "000000000000000000000000"+strings.ToLower(a[2:]), want)
	parses(t, "0x000000000000000000000000"+strings.ToLower(a[2:]), want)
	parses(t, "0x000000000000000000000000"+a[2:], want)
	parses(t, "  "+a+"\n", want)
	// the zero address parses (kind 3 names it); its callers refuse it where the plan does
	parses(t, "0x0000000000000000000000000000000000000000", [32]byte{})
	if EthAddress([32]byte{}) != "0x0000000000000000000000000000000000000000" {
		t.Fatalf("the zero address written %s", EthAddress([32]byte{}))
	}
	// refused: 40 hex without 0x, 0X, other lengths, non-hex, a 32-byte value whose top twelve bytes are not zero
	refused(t, strings.ToLower(a[2:]), "not an Ethereum address")
	refused(t, "0X"+strings.ToLower(a[2:]), "not an Ethereum address")
	refused(t, a[:41], "not an Ethereum address")
	refused(t, a+"0", "not an Ethereum address")
	refused(t, "0x"+strings.Repeat("0", 39)+"g", "is not 0x and 40 hex digits")
	refused(t, "0x"+strings.Repeat("a", 63)+"z", "is not 64 hex digits")
	refused(t, "", "not an Ethereum address")
	refused(t, "g1leu8d2vsplhehcfkjg50mwgdpxdkt8tztu95wr", "not an Ethereum address")
	refused(t, "E588QtVUvresuXq2KoNEwAmoifCzYGpRBdHByN9KQMbi", "not an Ethereum address") // a Solana key in base58
	refused(t, "01"+strings.Repeat("00", 11)+strings.ToLower(a[2:]), "top twelve are not zero")
	refused(t, "0x"+strings.Repeat("00", 11)+"01"+strings.ToLower(a[2:]), "top twelve are not zero")
	refused(t, strings.Repeat("ab", 32), "top twelve are not zero")
	// a value whose top twelve bytes are not zero is written whole, never truncated to an address
	var k [32]byte
	for i := range k {
		k[i] = 0xab
	}
	if EthAddress(k) != "0x"+strings.Repeat("ab", 32) {
		t.Fatalf("a non-address written %s", EthAddress(k))
	}
}

// TestEthereumContracts (ER10): the deployment's Ethereum contracts are compiled here, one list of the sixteen —
// each named, each on the page and in the json — and RefusedEthRecipient refuses as a send's recipient each of them,
// the zero address, the route's own router and every address at or below 0xffff (the precompiles 0x01–0x11 and
// 0x100), and nothing else. The placeholders are spelled out here, so the golden values never come from the realm.
func TestEthereumContracts(cur realm, t *testing.T) {
	want := []struct{ name, key, addr string }{
		{"the mailbox", "mailbox", "0x0404040404040404040404040404040404040404"},
		{"token 0 (GNOT)", "token0", "0x0BA554Aa0D0566e582dE0238a089Ec102c2e05a4"},
		{"token 1 (BUBBLE)", "token1", "0x051D52Fd082d902f8E0CF300a60304D484cf2d07"},
		{"the light client", "client", "0x0303030303030303030303030303030303030303"},
		{"the gate", "gate", "0x0202020202020202020202020202020202020202"},
		{"HlAdmin", "hlAdmin", "0x0101010101010101010101010101010101010101"},
		{"the ProxyAdmin", "proxyAdmin", "0x0505050505050505050505050505050505050505"},
		{"the MerkleTreeHook", "merkleTreeHook", "0x0606060606060606060606060606060606060606"},
		{"the ProtocolFee hook", "protocolFee", "0x0707070707070707070707070707070707070707"},
		{"the mailbox's implementation", "mailboxImpl", "0x0808080808080808080808080808080808080808"},
		{"token 0's implementation", "token0Impl", "0x0909090909090909090909090909090909090909"},
		{"token 1's implementation", "token1Impl", "0x7070707070707070707070707070707070707070"},
		{"the light client's implementation", "clientImpl", "0x8080808080808080808080808080808080808080"},
		{"the gate's implementation", "gateImpl", "0x9090909090909090909090909090909090909090"},
		// the two the build added (M2-NOTES.md §1 G): sixteen deployments, eleven codes and five proxies
		{"the Ed25519Verifier", "ed25519Verifier", "0x2525252525252525252525252525252525252525"},
		{"HlAdminExec", "hlAdminExec", "0x1212121212121212121212121212121212121212"},
	}
	if len(want) != EthContracts {
		t.Fatalf("%d contracts, not %d", EthContracts, len(want))
	}
	var router [32]byte // a router that is none of them, so each contract is refused as a contract
	router[31] = 0x42
	router[12] = 0x42
	p, j := Render(""), Render("json")
	for i, w := range want {
		name, k := EthContractAt(i)
		if name != w.name || k != padded(t, w.addr[2:]) || EthAddress(k) != w.addr || EthContractName(k) != w.name {
			t.Fatalf("contract %d: %q %s, want %q %s", i, name, EthAddress(k), w.name, w.addr)
		}
		why := RefusedEthRecipient(k, router)
		if why != w.addr+", "+w.name+" of this deployment on Ethereum: a contract of the bridge, where what is minted nobody can move" {
			t.Fatalf("contract %d refused as %q", i, why)
		}
		if !strings.Contains(p, "- "+w.name+": `"+w.addr+"`\n") || !strings.Contains(j, q(w.key)+":"+q(w.addr)) {
			t.Fatalf("contract %d not on the page or in the json", i)
		}
	}
	if n, k := EthContractAt(EthContracts); n != "" || k != ([32]byte{}) {
		t.Fatalf("a contract past the list")
	}
	if n, _ := EthContractAt(-1); n != "" || EthContractName(router) != "" {
		t.Fatalf("a contract before the list, or a router named")
	}
	// the zero address, the route's own router, and the low range: at or below 0xffff, the boundary just above it free
	if why := RefusedEthRecipient([32]byte{}, router); why != "the zero address: an ERC-20 refuses to mint to it, so the delivery would revert for good" {
		t.Fatalf("the zero address: %q", why)
	}
	if why := RefusedEthRecipient(router, router); why != "this route's own router, its token contract on Ethereum: what it mints to itself nobody can move" {
		t.Fatalf("the router: %q", why)
	}
	for _, low := range []string{"0000000000000000000000000000000000000001", "0000000000000000000000000000000000000011",
		"0000000000000000000000000000000000000100", "000000000000000000000000000000000000ffff", "0000000000000000000000000000000000001000"} {
		k := padded(t, low)
		// (the message writes the address in its EIP-55 case, so 0x…ffff's letters are compared in any case)
		if why := RefusedEthRecipient(k, router); !strings.EqualFold(why, "0x"+low+", at or below 0xffff: a precompile's or an address the protocol reserves, where what is minted nobody can move") {
			t.Fatalf("0x%s: %q", low, why)
		}
	}
	for _, free := range []string{"0000000000000000000000000000000000010000", "1000000000000000000000000000000000000000", "5aaeb6053f3e94c9b9a09f33669435e7ef1beaed"} {
		if why := RefusedEthRecipient(padded(t, free), router); why != "" {
			t.Fatalf("0x%s refused: %q", free, why)
		}
	}
}

// TestEthereumContractsDistinct: the deploy refuses a list in which two lines share an address — a stage step that
// wrote one record field twice, which a proxy written as both tokens and both routes' routers would slip past the
// routes' own init checks: the rule hlmailbox's init applies, on the compiled list (all distinct) and on copies of it
// with token 1 written as token 0, HlAdminExec as HlAdmin, and the last line as the first.
func TestEthereumContractsDistinct(cur realm, t *testing.T) {
	if why := duplicateContract(ethContracts); why != "" {
		t.Fatalf("the compiled list: %s", why)
	}
	for _, c := range []struct {
		from, to int
		want     string
	}{
		{EthToken0, EthToken1, "hlmailbox: token 0 (GNOT) and token 1 (BUBBLE) are both 0x0BA554Aa0D0566e582dE0238a089Ec102c2e05a4: every contract of the deployment has its own address"},
		{EthHlAdmin, EthHlAdminExec, "hlmailbox: HlAdmin and HlAdminExec are both 0x0101010101010101010101010101010101010101: every contract of the deployment has its own address"},
		{EthMailbox, EthHlAdminExec, "hlmailbox: the mailbox and HlAdminExec are both 0x0404040404040404040404040404040404040404: every contract of the deployment has its own address"},
	} {
		cs := ethContracts
		cs[c.to] = cs[c.from]
		if why := duplicateContract(cs); why != c.want {
			t.Fatalf("line %d written as line %d: %q", c.to, c.from, why)
		}
	}
}
