package main

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gnolang/bridges/solana/bridge/hl"
)

// The Ethereum side (E-M4): the hand-rolled ABI against Foundry's, the config's two shapes, the snapshot, the burns
// (ER5), the builder, the deliveries, the provider's failures and lies, the transfers both ways, the API.

func mustHex(t testing.TB, s string) []byte {
	t.Helper()
	b, err := parseData(s)
	if err != nil {
		t.Fatalf("%.40s: %v", s, err)
	}
	return b
}

// flipCase changes the case of an address's first hex letter: a mixed case that is not its checksum.
func flipCase(a string) string {
	b := []byte(a)
	for i := 2; i < len(b); i++ {
		switch {
		case b[i] >= 'a' && b[i] <= 'f':
			b[i] -= 32
			return string(b)
		case b[i] >= 'A' && b[i] <= 'F':
			b[i] += 32
			return string(b)
		}
	}
	return a
}

func w32(s string) [32]byte {
	b, _ := hex.DecodeString(strings.Repeat(s, 32))
	return [32]byte(b)
}

// a20 is an address of one repeated byte.
func a20(s string) ethAddr {
	w := w32(s)
	return ethAddr(w[:20])
}

// TestEthABI pins every selector and topic, and every encoder and decoder, against Foundry 1.8.5's cast (cast sig,
// cast keccak, cast abi-encode, cast calldata, cast to-check-sum-address on the devbox, 2026-10-14).
func TestEthABI(t *testing.T) {
	vec := map[string]string{} // testdata/eth-abi-vectors.txt: cast's output, verbatim
	raw, err := os.ReadFile("testdata/eth-abi-vectors.txt")
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(raw), "\n") {
		if k, v, ok := strings.Cut(line, " "); ok && !strings.HasPrefix(line, "#") {
			vec[k] = v
		}
	}
	if len(vec) != 12 {
		t.Fatalf("%d vectors", len(vec))
	}
	for _, c := range []struct {
		got  [4]byte
		want string
	}{{selTotalSupply, "18160ddd"}, {selBalanceOf, "70a08231"}, {selQuoteTransfer, "8bd90b82"}, {selTransfer, "81b4e8b4"}, {selNonce, "affed0e0"},
		{selProcessedAt, "07a2fda1"}, {selKeys, "307540f6"}, {selFlags, "64cc4aa5"}, {selHold, "00e0c53f"}, {selLimits, "14b32112"}, {selToken, "fd00ea1c"},
		{selClockFactor, "f219c25d"}, {selTrustedHeight, "e5ee12e4"}, {selTrustedTime, "5ffa9080"}, {selTrustedHash, "a10c4b2a"}, {selQuarantineEnd, "aacef059"},
		{selEntry, "80820b31"}, {selLastSeq, "ec6a43c1"}, {selPending, "e20ccec3"}, {selDecision, "d7cc6ac6"}, {selSetupDone, "0a0cd8c8"}, {selProcess, "7c39d130"},
		{selUpdate, "5df42c5e"}} {
		if hex.EncodeToString(c.got[:]) != c.want {
			t.Errorf("selector %x, want %s", c.got, c.want)
		}
	}
	for _, c := range []struct {
		got  [32]byte
		want string
	}{{topicDispatch, "769f711d20c679153d382254f59892613b58a97cc876b249134ac25c80f9c814"}, {topicProcess, "0d381c2a574ae8f04e213db7cfb4df8df712cdbd427d9868ffef380660ca6574"},
		{topicProcessID, "1cae38cdd3d3919489272725a5ae62a4f48b2989b0dae843d3c279fee18073a9"}, {topicHeaderRecorded, "dc5398ae57b3a999cf084a96bd90c40a779b716637e1cd2652f2865f30b2961d"},
		{topicReceived, "ba20947a325f450d232530e5f5fce293e7963499d5309a07cee84a269f2f15a6"}, {eventTopic("SentTransferRemote(uint32,bytes32,uint256)"), "d229aacb94204188fe8042965fa6b269c62dc5818b21238779ab64bdd17efeec"},
		{hl.Keccak(nil), "c5d2460186f7233c927e7db2dcc703c0e500b653ca82273b7bfad8045d85a470"}} {
		if hex.EncodeToString(c.got[:]) != c.want {
			t.Errorf("topic %x, want %s", c.got, c.want)
		}
	}
	a := a20("11")
	// quoteTransferRemote's Quote[]
	quotesHex := vec["quotes"]
	qs := []quote{{Amount: big.NewInt(100000000000000)}, {Token: a, Amount: big.NewInt(200000000)}, {Token: a, Amount: new(big.Int)}}
	if got := encQuotes(qs); !bytes.Equal(got, mustHex(t, quotesHex)) {
		t.Errorf("encQuotes: %x", got)
	}
	if dq, err := decodeQuotes(mustHex(t, quotesHex)); err != nil || len(dq) != 3 || dq[0].Token != (ethAddr{}) || dq[0].Amount.Uint64() != 100000000000000 || dq[1].Token != a || dq[1].Amount.Uint64() != 200000000 {
		t.Errorf("decodeQuotes: %+v %v", dq, err)
	}
	// HlAdmin's decision(n)
	decHex := vec["decision"]
	params := mustHex(t, "0x00005af3107a40003333333333333333333333333333333333333333333333333333333333333333")
	dec := ethDecision{Number: 1, Kind: 7, DeliveredBlock: 6999700, DeliveredTime: 1799996668, Deliverer: a20("22"), Params: params}
	if got := encDecision(dec); !bytes.Equal(got, mustHex(t, decHex)) {
		t.Errorf("encDecision: %x", got)
	}
	if d, err := decodeDecision(mustHex(t, decHex)); err != nil || d.Number != 1 || d.Kind != 7 || d.DeliveredBlock != 6999700 || d.DeliveredTime != 1799996668 || d.Deliverer != dec.Deliverer || !bytes.Equal(d.Params, params) {
		t.Errorf("decodeDecision: %+v %v", d, err)
	}
	// the client's verify metadata, and the mailbox's process(metadata, message)
	metaHex := vec["meta"]
	meta := encProofMeta(w32("aa"), 3, 1, [][32]byte{w32("bb"), w32("cc")}, mustHex(t, "0xdeadbeef"))
	if !bytes.Equal(meta, mustHex(t, metaHex)) {
		t.Errorf("encProofMeta: %x", meta)
	}
	if pm, err := decodeProofMeta(meta); err != nil || pm.header != w32("aa") || pm.total != 3 || pm.index != 1 || pm.aunts != 2 {
		t.Errorf("decodeProofMeta: %+v %v", pm, err)
	}
	processHex := vec["process"]
	if got := encProcess(meta, mustHex(t, "0x0102030405")); !bytes.Equal(got, mustHex(t, processHex)) {
		t.Errorf("encProcess: %x", got)
	}
	// update(...): the set it carries
	updateHex := vec["update"]
	up := encUpdate([][]byte{{1}, {2, 3}}, 1, w32("44"), 0, []mockPrecommit{{0, 1700000000, 5, w32("55"), w32("66")}, {2, 1700000001, -1, w32("77"), w32("88")}}, []mockVal{{w32("99"), 60}, {w32("ab"), 70}}, nil)
	if !bytes.Equal(up, mustHex(t, updateHex)) {
		t.Errorf("encUpdate: %x", up)
	}
	if set, err := decodeUpdateSet(mustHex(t, updateHex)); err != nil || fmt.Sprint(set) != "[60 70]" {
		t.Errorf("decodeUpdateSet: %v %v", set, err)
	}
	// the gate's flags() and limits(i), the client's entry(hash), the logs' data
	flagsHex := vec["flags"]
	fb := mustHex(t, flagsHex)
	for i, want := range []string{"&{5 1700000000}", "<nil>", "<nil>", "<nil>", "&{7 1700000100}", "<nil>"} {
		if f, err := flagAt(fb, 64*uint64(i)); err != nil || fmt.Sprint(f) != want {
			t.Errorf("flag %d: %v %v, want %s", i, f, err, want)
		}
	}
	limitsHex := vec["limits"]
	if got := encWords(u64Word(25000000000), u64Word(100000000000), u64Word(1000000000), u64Word(1799992800)); !bytes.Equal(got, mustHex(t, limitsHex)) {
		t.Errorf("limits: %x", got)
	}
	entryHex := vec["entry"]
	if got := encWords(u64Word(1), i64Word(1294), w32("cd")); !bytes.Equal(got, mustHex(t, entryHex)) {
		t.Errorf("entry: %x", got)
	}
	hdrHex := vec["hdrdata"]
	if w, _ := wordAt(mustHex(t, hdrHex), 96); !func() bool { b, err := boolWord(w); return b && err == nil }() {
		t.Error("HeaderRecorded's proven flag")
	}
	dispatchHex := vec["dispatchdata"]
	msg := mustHex(t, "0x0102030405060708091011121314151617181920212223242526272829303132333435")
	if got := append(encWords(u64Word(32)), encBytes(msg)...); !bytes.Equal(got, mustHex(t, dispatchHex)) {
		t.Errorf("Dispatch data: %x", got)
	}
	if b, err := bytesAt(mustHex(t, dispatchHex), 0, 0); err != nil || !bytes.Equal(b, msg) {
		t.Errorf("Dispatch data read: %x %v", b, err)
	}
	// transferRemote's calldata; a negative int64
	rcpt := [32]byte(mustHex(t, "0x0000000000000000000000009227edf1f2e5b3f4ebe4f0f4d0d6c5ab4b5f3c21"))
	if got := calldata(selTransfer, u64Word(11155111), rcpt, u64Word(2500000000)); hex0x(got) != vec["transfer"] {
		t.Errorf("transferRemote: %x", got)
	}
	if w := i64Word(-5); hex0x(w[:]) != vec["neg"] {
		t.Errorf("i64Word(-5): %x", w)
	} else if v, err := i64Of(w); err != nil || v != -5 {
		t.Errorf("i64Of: %d %v", v, err)
	}
	// words that lie: a value past 64 bits, an address with its top bytes set, an offset past the data
	if _, err := u64Of(w32("01")); err == nil {
		t.Error("a 256-bit value read as 64 bits")
	}
	if _, err := addrWord(w32("01")); err == nil {
		t.Error("an address with its top twelve bytes set")
	}
	if _, err := bytesAt(mustHex(t, "0x00000000000000000000000000000000000000000000000000000000000000ff"), 0, 0); err == nil {
		t.Error("an offset past the data")
	}
}

func TestEthAddress(t *testing.T) {
	for _, s := range []string{"0x5aAeb6053F3E94C9b9A09f33669435E7Ef1BeAed", "0xfB6916095ca1df60bB79Ce92cE3Ea74c37c5d359", "0xdbF03B407c01E7cD3CBea99509d93f8DDDC8C6FB", "0xD1220A0cf47c7B9Be7A2E6BA89F429762e7b9aDb"} {
		// a machine's address (a wallet's account, a provider's) in any one case, read in its checksum form
		a, err := parseEthWire(strings.ToLower(s))
		if err != nil || a.String() != s {
			t.Errorf("%s: %s %v", s, a, err)
		}
		if _, err := parseEthWire("0x" + strings.ToUpper(s[2:])); err != nil {
			t.Errorf("%s in upper case: %v", s, err)
		}
		// a person's, only in its checksum case (§5.2, E3-CF6: e3_test.go)
		if _, err := parseEthAddress(s); err != nil {
			t.Errorf("%s (its checksum): %v", s, err)
		}
		if _, err := parseEthAddress(strings.ToLower(s)); err == nil {
			t.Errorf("%s typed in lower case taken", s)
		}
	}
	for _, bad := range []string{"0x5aAeb6053F3E94C9b9A09f33669435E7Ef1BeAeD", "5aaeb6053f3e94c9b9a09f33669435e7ef1beaed", "0x5aaeb6053f3e94c9b9a09f33669435e7ef1bea", "0x5aaeb6053f3e94c9b9a09f33669435e7ef1beaeg", ""} {
		if _, err := parseEthAddress(bad); err == nil {
			t.Errorf("%q read as an address", bad)
		}
		if _, err := parseEthWire(bad); err == nil {
			t.Errorf("%q read as a machine's address", bad)
		}
	}
	e := &ethereumChain{}
	if got := e.Address(mockEthAddr("bob").word()); got != mockEthAddr("bob").String() {
		t.Errorf("Address: %s", got)
	}
	if got := e.Address(w32("01")); got != "0x"+strings.Repeat("01", 32) {
		t.Errorf("no address: %s", got)
	}
}

// liveShape is the live site's config as deployed (bridge/site/site.example.json's shape, the M5 form: one gno
// section, Solana alone), with values that check.
func liveShape() string {
	return `{"gno":{"rpc":"https://rpc.gno.land:443","chainId":"gnoland-1","mailbox":"` + mockNS + `hlmailbox","fees":"` + mockNS + `hlfees","gov":"` + mockNS + `hlgov","bubble":"` + mockNS + `bubble","realmLink":"https://gno.land/%s","txLink":"","gasWanted":60000000},
	"chains":[{"id":"solana","name":"Solana","rpc":"https://api.mainnet-beta.solana.com","domain":1399811149,"explorer":"https://explorer.solana.com","cluster":"",
	"tokens":[{"symbol":"GNOT","program":"` + mockPub("gnot").String() + `","realm":"` + mockNS + `hlgnot"},{"symbol":"BUBBLE","program":"` + mockPub("bubble").String() + `","realm":"` + mockNS + `hlbubble"}],
	"programs":{"mailbox":"` + mockPub("mailbox").String() + `","pausable":"` + mockPub("pausable").String() + `","admin":"` + mockPub("admin").String() + `","client":"` + mockPub("client").String() + `"},
	"signers":["https://bridge.gno.world/signer/1","https://bridge.gno.world/signer/2","https://bridge.gno.world/signer/3"]}]}`
}

func loadText(t *testing.T, text string) (*Config, error) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "site.json")
	if err := os.WriteFile(p, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	return LoadConfig(p)
}

func TestEthConfig(t *testing.T) {
	// the live shape loads as it did: Solana's realms are the shared section's, and the realms refused as a
	// recipient are the same six in the same order, with the deployment's hlverifier after r/hlgov (round 3's E3-CF6)
	c, err := loadText(t, liveShape())
	if err != nil {
		t.Fatal(err)
	}
	ch := c.Chain("solana")
	if r := ch.Realms(); r.Mailbox != mockNS+"hlmailbox" || r.Fees != mockNS+"hlfees" || r.Gov != mockNS+"hlgov" || ch.Gno != nil || ch.Kind != "solana" {
		t.Errorf("the live shape's realms: %+v", r)
	}
	if got := strings.Join(c.Realms(), " "); got != strings.Join([]string{mockNS + "hlmailbox", mockNS + "hlfees", mockNS + "hlgov", mockNS + "hlverifier", mockNS + "bubble", mockNS + "hlgnot", mockNS + "hlbubble"}, " ") {
		t.Errorf("the live shape's realms: %s", got)
	}
	// the new shape: Ethereum beside, with realms of its own; its addresses in their checksum case (one case is
	// refused: e3_test.go)
	both := mockConfig()
	eth := mockEthChain()
	both.Chains = append(both.Chains, eth)
	b, _ := json.Marshal(both)
	c, err = loadText(t, string(b))
	if err != nil {
		t.Fatal(err)
	}
	e := c.Chain("ethereum")
	if e.Programs.Mailbox != mockEthAddr("mailbox").String() || e.ChainID != uint64(mockEthDomain) || e.LogRange != defaultLogRange || e.Tokens[1].Mint != mockEthAddr("BUBBLE").String() || e.Realms().Mailbox != mockEthNS("hlmailbox") {
		t.Errorf("the Ethereum chain: %+v", e)
	}
	if rs := c.Realms(); len(rs) != 13 || rs[3] != mockNS+"hlverifier" || rs[4] != mockEthNS("hlmailbox") || rs[7] != mockEthNS("hlverifier") {
		t.Errorf("both deployments' realms: %v", rs)
	}
	mutate := func(f func(c *Config)) error {
		b, _ := json.Marshal(both)
		var x Config
		_ = json.Unmarshal(b, &x)
		f(&x)
		return x.check()
	}
	for _, tc := range []struct {
		name string
		f    func(c *Config)
		want string
	}{
		{"Ethereum on Solana's realms", func(c *Config) { c.Chains[1].Gno = nil }, "same gno.land mailbox"},
		{"no mailbox anywhere", func(c *Config) { c.Gno.Mailbox, c.Chains[1].Gno.Mailbox = "", "" }, "no gno.land mailbox"},
		{"a realm outside gno.land/r/", func(c *Config) { c.Chains[1].Gno.Gov = "gno.land/p/x" }, "not under gno.land/r/"},
		{"a wrong checksum", func(c *Config) { c.Chains[1].Programs.Admin = flipCase(c.Chains[1].Programs.Admin) }, "EIP-55"},
		{"a base58 contract", func(c *Config) { c.Chains[1].Programs.Client = mockPub("client").String() }, "programs.client"},
		{"the zero address", func(c *Config) { c.Chains[1].Programs.Pausable = "0x" + strings.Repeat("0", 40) }, "zero address"},
		{"no ProxyAdmin", func(c *Config) { c.Chains[1].Programs.ProxyAdmin = "" }, "programs.proxyAdmin"},
		{"another token", func(c *Config) { c.Chains[1].Tokens[0].Symbol = "USDC" }, "only GNOT and BUBBLE"},
		{"a mint not its contract", func(c *Config) { c.Chains[1].Tokens[0].Mint = mockEthAddr("x").String() }, "its own mint"},
		{"a cluster", func(c *Config) { c.Chains[1].Cluster = "sepolia" }, "cluster"},
		{"an unknown kind", func(c *Config) { c.Chains[1].Kind = "cosmos" }, "not implemented"},
	} {
		if err := mutate(tc.f); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v, want an error naming %q", tc.name, err, tc.want)
		}
	}
	// the two example files load once their FILLs are replaced
	for _, f := range []string{"site.example.json", "site.example.ethereum.json"} {
		raw, err := os.ReadFile(filepath.Join(siteDir, f))
		if err != nil {
			t.Fatal(err)
		}
		text := string(raw)
		for _, kv := range [][2]string{{`"FILL: the GNOT token program id from hlsol setup"`, `"` + mockPub("gnot").String() + `"`}, {`"FILL: the BUBBLE token program id"`, `"` + mockPub("bubble").String() + `"`},
			{`"FILL: our mailbox program id"`, `"` + mockPub("mailbox").String() + `"`}, {`"FILL: the pausable ISM program id"`, `"` + mockPub("pausable").String() + `"`},
			{`"FILL: the admin program id"`, `"` + mockPub("admin").String() + `"`}, {`"FILL: the light-client ISM program id"`, `"` + mockPub("client").String() + `"`}} {
			text = strings.ReplaceAll(text, kv[0], kv[1])
		}
		for i := 0; strings.Contains(text, `"FILL`); i++ {
			j := strings.Index(text, `"FILL`)
			k := strings.Index(text[j+1:], `"`) + j + 2
			fill := text[j:k]
			repl := `"https://provider.example/key"`
			if strings.Contains(fill, "address") || strings.Contains(fill, "0x") {
				repl = `"` + mockEthAddr(fmt.Sprint("fill ", i)).String() + `"`
			}
			text = text[:j] + repl + text[k:]
		}
		if _, err := loadText(t, text); err != nil {
			t.Errorf("%s: %v", f, err)
		}
	}
}

func ethOf(s *server) *ethereumChain { return s.others["ethereum"].(*ethereumChain) }

func TestEthSnapshot(t *testing.T) {
	s, _, _, me := mockBoth(false, siteDir, fixedNow)
	e := ethOf(s)
	// the supplies are read at the finalized block, every call pinned to its hash
	var pinned []string
	var mu sync.Mutex
	me.hook = func(method string, p []json.RawMessage) (json.RawMessage, *rpcError, bool) {
		if method == "eth_call" && strings.Contains(string(p[0]), hex.EncodeToString(selTotalSupply[:])) {
			mu.Lock()
			pinned = append(pinned, string(p[1]))
			mu.Unlock()
		}
		return nil, nil, false
	}
	snap := e.Snapshot(ctx, fixedNow())
	fin := mockEthBlockHash(me.finalized())
	if len(pinned) != 2 || pinned[0] != `{"blockHash":"`+hex0x(fin[:])+`"}` || pinned[1] != pinned[0] {
		t.Errorf("the supplies' block: %v (finalized %x)", pinned, fin)
	}
	if len(snap.Errors) > 0 {
		t.Fatalf("errors: %v", snap.Errors)
	}
	// the done world: the out mint's block is not final yet (a mint counts once its block is), the burn's is
	if snap.Slot != me.finalized() || snap.Head != mockEthHead || snap.Supplies["GNOT"] != 499_880*one || snap.Supplies["BUBBLE"] != 1_200_000*one {
		t.Errorf("the cut: block %d head %d supplies %v", snap.Slot, snap.Head, snap.Supplies)
	}
	l := snap.Limits["BUBBLE"]
	if l.MaxPerMessage != uint64(25_000*one) || l.DayCap != uint64(100_000*one) || l.Used != 0 {
		t.Errorf("limits: %+v (the bucket drained what was charged two hours ago)", l)
	}
	if snap.Pausable == nil || snap.Admin == nil || len(snap.Pausable.Keys) != 6 || snap.Pausable.Keys[0] != mockEthAddr("g1").String() || snap.Admin.Keys[4] != mockEthAddr("watch5").String() || snap.Pausable.Hold || snap.Pausable.Flags[0] != nil {
		t.Errorf("guardians: %+v %+v", snap.Pausable, snap.Admin)
	}
	if c := snap.Client; c == nil || c.TrustedHeight != uint64(ethOutHeight+1) || c.ClockFactor != 1 || c.QuarantineEnd != 0 || c.TrustedTime != fixedNow().Add(-6*mockBlock).Unix() || c.Validators != 0 {
		t.Errorf("client before the activity: %+v", snap.Client)
	}
	if snap.LastSeq != 1 || !snap.SetupDone || len(snap.Pending) != 1 {
		t.Fatalf("HlAdmin: last %d setup %v pending %+v", snap.LastSeq, snap.SetupDone, snap.Pending)
	}
	p := snap.Pending[0]
	if p.Number != 1 || p.Kind != 7 || p.KindName != "set protocol fee" || p.Text != "set the protocol fee to 0.0001 ETH, beneficiary "+mockEthAddr("treasurer").String() || p.MaturesAt != p.Delivered+7*86400 || p.Deferred != "" {
		t.Errorf("pending: %+v", p)
	}
	// the set and the last header's time come from the update the activity read
	s.activityNow(ctx, s.cfg.Chain("ethereum"), e)
	snap = e.Snapshot(ctx, fixedNow())
	if c := snap.Client; c.Validators != 6 || fmt.Sprint(c.Powers) != "[60 60 60 60 60 60]" || c.LastAccepted != fixedNow().Unix() {
		t.Errorf("client after the activity: %+v", c)
	}
	// a hold: two guardians' flags on both objects; the 7-day decision waits for its end
	me.hold = true
	snap = e.Snapshot(ctx, fixedNow())
	if !snap.Pausable.Hold || !snap.Admin.Hold || snap.Admin.HoldSince == nil || snap.Pausable.Flags[1] == nil || len(snap.Pending) != 1 || !strings.Contains(snap.Pending[0].Deferred, "until the hold ends") {
		t.Errorf("hold: %+v %+v %+v", snap.Pausable, snap.Admin, snap.Pending)
	}
	// the provider on another chain is refused before anything is read
	_, _, _, me2 := mockBoth(false, siteDir, fixedNow)
	me2.hook = func(method string, p []json.RawMessage) (json.RawMessage, *rpcError, bool) {
		if method == "eth_chainId" {
			return json.RawMessage(`"0x1"`), nil, true
		}
		return nil, nil, false
	}
	e2 := newEthereum(mockEthChainChecked(t), me2, mockGnoDomain, nil)
	snap = e2.Snapshot(ctx, fixedNow())
	if len(snap.Errors) != 1 || !strings.Contains(snap.Errors[0], "serves chain 1, not Ethereum's 11155111") || len(snap.Supplies) != 0 {
		t.Errorf("another chain: %v", snap.Errors)
	}
}

// mockEthChainChecked is the Ethereum chain's config as the check leaves it.
func mockEthChainChecked(t *testing.T) ChainConfig {
	c := mockConfig()
	c.Chains = append(c.Chains, mockEthChain())
	if err := c.check(); err != nil {
		t.Fatal(err)
	}
	return *c.Chain("ethereum")
}

func TestEthBurn(t *testing.T) {
	s, _, _, me := mockBoth(true, siteDir, fixedNow)
	e := ethOf(s)
	bs, err := e.Burn(ctx, mockEthBurnTx)
	if err != nil || len(bs) != 1 {
		t.Fatalf("the burn in flight: %+v %v", bs, err)
	}
	b := bs[0]
	inBlock := me.blockAt(fixedNow().Unix() - 300)
	if b.ID != mockEthInID || b.Token != "BUBBLE" || b.Amount != 2500*one || b.To != mockRecipient || b.Nonce != 4 || b.Slot != inBlock || b.Time != me.timeOf(inBlock) || b.Finalized != me.finalized() || b.Sig != mockEthBurnTx || b.BadTo {
		t.Errorf("the burn: %+v", b)
	}
	// ER5: a stranger's dispatch toward gno.land is no burn
	if bs, err := e.Burn(ctx, mockEthTx("stranger")); err != nil || len(bs) != 0 {
		t.Errorf("a stranger's dispatch read as a burn: %+v %v", bs, err)
	}
	ch := s.cfg.Chain("ethereum")
	if _, err := s.resolve(ctx, ch, e, mockEthTx("stranger")); !errors.Is(err, errUnknownTransfer) || !strings.Contains(err.Error(), "made no burn toward gno.land on the bridge's token contracts") {
		t.Errorf("tracking a stranger's dispatch: %v", err)
	}
	// a failed transaction, one no block holds, a block taken back
	if _, err := e.Burn(ctx, mockEthTx("failed")); err == nil || !strings.Contains(err.Error(), "failed on Ethereum") {
		t.Errorf("a failed transaction: %v", err)
	}
	if _, err := e.Burn(ctx, mockEthTx("nothing")); !errors.Is(err, errUnknownTransfer) || !strings.Contains(err.Error(), "no transaction") {
		t.Errorf("an unknown transaction: %v", err)
	}
	me.hook = func(method string, p []json.RawMessage) (json.RawMessage, *rpcError, bool) {
		if method == "eth_getBlockByNumber" && str(p, 0) == qty(inBlock) {
			other := hl.Keccak([]byte("a block taken back"))
			return json.RawMessage(fmt.Sprintf(`{"number":"%s","hash":"%s","timestamp":"0x1"}`, qty(inBlock), hex0x(other[:]))), nil, true
		}
		return nil, nil, false
	}
	if _, err := e.Burn(ctx, mockEthBurnTx); !errors.Is(err, errUnknownTransfer) || !strings.Contains(err.Error(), "taken back") {
		t.Errorf("a reorganized receipt: %v", err)
	}
	me.hook = nil
	// burnOf: only our mailbox's dispatch from one of our tokens, to that token's route, toward gno.land, whose
	// message says the same
	l := ethLog{Address: mockEthAddr("mailbox"), Topics: [][32]byte{topicDispatch, mockEthAddr("BUBBLE").word(), u64Word(uint64(mockGnoDomain)), gnoAddr32(mockEthNS("hlbubble"))}, Data: append(encWords(u64Word(32)), encBytes(mockEthInMessage().Bytes())...)}
	if _, ok := e.burnOf(l); !ok {
		t.Fatal("the real burn's log")
	}
	for name, mut := range map[string]func(l *ethLog){
		"another emitter":   func(l *ethLog) { l.Address = mockEthAddr("BUBBLE") },
		"a stranger sender": func(l *ethLog) { l.Topics[1] = mockEthAddr("stranger").word() },
		"another route":     func(l *ethLog) { l.Topics[3] = gnoAddr32(mockEthNS("hlgnot")) },
		"another domain":    func(l *ethLog) { l.Topics[2] = u64Word(1) },
		"a lying message": func(l *ethLog) {
			l.Data = append(encWords(u64Word(32)), encBytes(mockEthBurnMessageOf(4, mockEthAddr("GNOT"), 1).Bytes())...)
		},
		"a Solana-side path": func(l *ethLog) { l.Topics[3] = gnoAddr32(mockNS + "hlbubble") },
		"removed":            func(l *ethLog) { l.Removed = true },
	} {
		x := l
		x.Topics = append([][32]byte{}, l.Topics...)
		mut(&x)
		if _, ok := e.burnOf(x); ok {
			t.Errorf("%s read as a burn", name)
		}
	}
}

func TestEthDeliveryOf(t *testing.T) {
	s, _, _, me := mockBoth(true, siteDir, fixedNow)
	e := ethOf(s)
	id := func(m hl.Message) [32]byte { return m.ID() }
	d, err := e.DeliveryOf(ctx, id(mockEthPMessage()))
	pBlock := me.blockAt(fixedNow().Add(time.Duration(ethPHeight-mockHead)*mockBlock).Unix() + 100)
	if err != nil || d == nil || d.Sig != mockEthTx("P delivery") || d.Slot != pBlock || d.Time != me.timeOf(pBlock) || d.Link != "https://sepolia.etherscan.io/tx/"+mockEthTx("P delivery") {
		t.Errorf("the history's delivery: %+v %v", d, err)
	}
	if d, err := e.DeliveryOf(ctx, id(mockEthOutMessage())); err != nil || d != nil {
		t.Errorf("not delivered yet: %+v %v", d, err)
	}
	// the record stands but the log names a block the provider no longer holds: delivered, without its transaction
	me.hook = func(method string, p []json.RawMessage) (json.RawMessage, *rpcError, bool) {
		if method == "eth_getLogs" {
			other := hl.Keccak([]byte("taken back"))
			pid := id(mockEthPMessage())
			return json.RawMessage(fmt.Sprintf(`[{"address":"%s","topics":["%s","%s"],"data":"0x","blockNumber":"%s","blockHash":"%s","transactionHash":"%s","logIndex":"0x1","removed":false}]`,
				mockEthAddr("mailbox"), hex0x(topicProcessID[:]), hex0x(pid[:]), qty(pBlock), hex0x(other[:]), mockEthTx("elsewhere"))), nil, true
		}
		return nil, nil, false
	}
	if d, err := e.DeliveryOf(ctx, id(mockEthPMessage())); err != nil || d == nil || d.Sig != "" || d.Slot != pBlock || d.Link != "https://sepolia.etherscan.io/address/"+mockEthAddr("mailbox").String() {
		t.Errorf("a log of a block taken back: %+v %v", d, err)
	}
}

func TestEthBurnTx(t *testing.T) {
	s, _, _, me := mockBoth(false, siteDir, fixedNow)
	e := ethOf(s)
	ch := s.cfg.Chain("ethereum")
	bubble := *ch.Token("BUBBLE")
	alice := mockEthAddr("alice")
	bt, err := e.BurnTx(ctx, bubble, strings.ToLower(alice.String()), mockRecipient, uint64(2500*one))
	if err != nil {
		t.Fatal(err)
	}
	rcpt, _ := hl.GnoAddressToBytes32(mockRecipient)
	want := calldata(selTransfer, u64Word(uint64(mockGnoDomain)), rcpt, u64Word(uint64(2500*one)))
	if bt.From != alice.String() || bt.To != mockEthAddr("BUBBLE").String() || bt.Data != hex0x(want) || bt.Value != "0x5af3107a4000" || bt.ChainID != "0xaa36a7" || bt.Tx != "" {
		t.Errorf("the call: %+v", bt)
	}
	for _, tc := range []struct {
		sender, to string
		amount     uint64
		want       string
	}{
		{alice.String(), hl.GnoPackageAddress(mockEthNS("hlbubble")), 1, "bridge realm"},
		{alice.String(), strings.ToUpper(hl.GnoPackageAddress(mockEthNS("hlmailbox"))), 1, "bridge realm"},
		{alice.String(), hl.GnoPackageAddress(mockNS + "hlgnot"), 1, "bridge realm"}, // the Solana deployment's route: refused here too
		{alice.String(), "g1nope", 1, "gno.land address"},
		{"nope", mockRecipient, 1, "not an Ethereum address"},
		{flipCase(alice.String()), mockRecipient, 1, "EIP-55"},
		{alice.String(), mockRecipient, 0, "above zero"},
		{alice.String(), mockRecipient, uint64(20_000 * one), "holds 10000 BUBBLE on Ethereum, less than 20000"},
		{mockEthAddr("bob").String(), mockRecipient, 1, "holds 0 BUBBLE"},
	} {
		if _, err := e.BurnTx(ctx, bubble, tc.sender, tc.to, tc.amount); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s → %s × %d: %v, want %q", tc.sender, tc.to, tc.amount, err, tc.want)
		}
	}
	// a token whose quote asks more than the amount, or its gas in tokens, is not built for
	for name, q := range map[string][]quote{
		"a fee recipient": {{Amount: big.NewInt(1)}, {Token: mockEthAddr("BUBBLE"), Amount: big.NewInt(2500*one + 1)}, {Token: mockEthAddr("BUBBLE"), Amount: new(big.Int)}},
		"a fee hook":      {{Token: mockEthAddr("BUBBLE"), Amount: big.NewInt(5)}, {Token: mockEthAddr("BUBBLE"), Amount: big.NewInt(2500 * one)}},
		"an external fee": {{Amount: big.NewInt(1)}, {Token: mockEthAddr("BUBBLE"), Amount: big.NewInt(2500 * one)}, {Token: mockEthAddr("BUBBLE"), Amount: big.NewInt(9)}},
	} {
		q := q
		me.hook = func(method string, p []json.RawMessage) (json.RawMessage, *rpcError, bool) {
			if method == "eth_call" && strings.Contains(string(p[0]), hex.EncodeToString(selQuoteTransfer[:])) {
				b, _ := json.Marshal(hex0x(encQuotes(q)))
				return b, nil, true
			}
			return nil, nil, false
		}
		if _, err := e.BurnTx(ctx, bubble, alice.String(), mockRecipient, uint64(2500*one)); err == nil || !strings.Contains(err.Error(), "builds no such transfer") {
			t.Errorf("%s: %v", name, err)
		}
	}
	// the provider down: unavailable (503), not the request's fault
	me.hook = func(method string, p []json.RawMessage) (json.RawMessage, *rpcError, bool) {
		return nil, &rpcError{Code: -32000, Message: "over capacity"}, true
	}
	var down unavailable
	if _, err := e.BurnTx(ctx, bubble, alice.String(), mockRecipient, 1); !errors.As(err, &down) {
		t.Errorf("the provider down: %v", err)
	}
}

// TestEthProviderHTTP drives the HTTP client against a provider that answers, fails, refuses and echoes its key:
// no error carries the URL's key, and the status and the log say what failed without it.
func TestEthProviderHTTP(t *testing.T) {
	const key = "S3CRETkey0123456789abcdef"
	me := newMockEth(newMockWorld(fixedNow, true))
	var mode string
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		m := mode
		mu.Unlock()
		if !strings.Contains(r.URL.Path, key) || r.URL.Query().Get("apikey") != key+"q" {
			http.Error(w, "unauthorized", 401)
			return
		}
		switch m {
		case "500":
			http.Error(w, "internal error for key "+key, 500)
		case "echo":
			_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": 1, "error": map[string]any{"code": -32000, "message": "rate limited: project " + key + " over its quota at https://user:" + key + "@x.example/v3/" + key}})
		case "garbage":
			_, _ = w.Write([]byte("<html>not json</html>"))
		default:
			me.ServeHTTP(w, r)
		}
	}))
	defer srv.Close()
	p := newEthHTTP(srv.URL + "/v3/" + key + "?apikey=" + key + "q")
	var id string
	if err := p.Call(ctx, "eth_chainId", nil, &id); err != nil || id != "0xaa36a7" {
		t.Fatalf("eth_chainId over HTTP: %q %v", id, err)
	}
	e := newEthereum(mockEthChainChecked(t), p, mockGnoDomain, nil)
	if snap := e.Snapshot(ctx, fixedNow()); len(snap.Errors) > 0 || snap.Supplies["BUBBLE"] != 1_200_000*one {
		t.Errorf("a snapshot over HTTP: %v %v", snap.Errors, snap.Supplies)
	}
	for m, want := range map[string]string{"500": "HTTP 500", "echo": "rate limited", "garbage": "not JSON-RPC"} {
		mu.Lock()
		mode = m
		mu.Unlock()
		err := p.Call(ctx, "eth_blockNumber", nil, nil)
		if err == nil || !strings.Contains(err.Error(), want) || strings.Contains(err.Error(), key) {
			t.Errorf("%s: %v", m, err)
		}
	}
	// nothing answers: the error names no URL
	srv.Close()
	if err := p.Call(ctx, "eth_blockNumber", nil, nil); err == nil || strings.Contains(err.Error(), key) || !strings.Contains(err.Error(), "did not answer") {
		t.Errorf("nothing answers: %v", err)
	}
	// through the API: the gno side still answers, Ethereum's parts are named as unread, the key nowhere
	var logs bytes.Buffer
	log.SetOutput(&logs)
	defer log.SetOutput(os.Stderr)
	s, _, _, _ := mockBoth(false, siteDir, fixedNow)
	s.others["ethereum"] = newEthereum(*s.cfg.Chain("ethereum"), p, mockGnoDomain, nil)
	rr := httptest.NewRecorder()
	s.handler().ServeHTTP(rr, httptest.NewRequest("GET", "/api/ethereum/status", nil))
	if rr.Code != 200 || !strings.Contains(rr.Body.String(), `"identity":"unread"`) || !strings.Contains(rr.Body.String(), "the provider") || strings.Contains(rr.Body.String(), key) {
		t.Errorf("the status with the provider down: %d %.400s", rr.Code, rr.Body.String())
	}
	rr = httptest.NewRecorder()
	s.handler().ServeHTTP(rr, httptest.NewRequest("POST", "/api/ethereum/burn-tx", strings.NewReader(`{"token":"GNOT","sender":"`+mockEthAddr("alice").String()+`","recipient":"`+mockRecipient+`","amount":1}`)))
	if rr.Code != 503 || strings.Contains(rr.Body.String(), key) {
		t.Errorf("the builder with the provider down: %d %s", rr.Code, rr.Body.String())
	}
	if strings.Contains(logs.String(), key) {
		t.Errorf("the log carries the key:\n%s", logs.String())
	}
	_ = url.PathEscape
}

func TestEthResolve(t *testing.T) {
	s, _, _, _ := mockBoth(true, siteDir, fixedNow)
	ch, e := s.cfg.Chain("ethereum"), ethOf(s)
	done := func(w []walkStep) string { return doneKeys(w) }
	// out, by its message id (0x and in capitals: still an id, not a transaction)
	for stage, want := range []string{"lock final", "lock final header", "lock final header proof mint"} {
		if stage > 0 {
			if err := s.world.step("ethereum", "out"); err != nil {
				t.Fatal(err)
			}
		}
		tr, err := s.resolve(ctx, ch, e, "0x"+strings.ToUpper(mockEthOutID))
		if err != nil {
			t.Fatal(err)
		}
		if tr.Direction != "out" || tr.Kind != "message" || tr.Token != "GNOT" || tr.Amount != 120*one || tr.To != mockEthAddr("bob").String() || done(tr.Walk) != want || tr.Height != ethOutHeight || tr.Nonce != 2 {
			t.Errorf("stage %d: %s %+v", stage, done(tr.Walk), tr)
		}
		if stage == 2 && (tr.State != "delivered" || tr.Steps[1].Link != "https://sepolia.etherscan.io/tx/"+mockEthTx("out delivery") || tr.Proof == nil || tr.Proof.Height != ethOutHeight+1 || tr.Proof.Total != 3 || tr.Proof.Index != 1 || tr.Walk[2].Height != ethOutHeight+1) {
			t.Errorf("delivered: %+v %+v", tr.Steps, tr.Proof)
		}
	}
	// out, by the wallet's gno.land transaction hash
	if tr, err := s.resolve(ctx, ch, e, mockEthGnoTx); err != nil || tr.Kind != "gnotx" || tr.ID != mockEthOutID {
		t.Errorf("by gno tx: %+v %v", tr, err)
	}
	// in, by the Ethereum transaction hash: burned (its block not final), final, the signers, the credit, paid
	for stage, want := range []string{"burn", "burn", "burn", "burn vouch", "burn vouch deliver credit", "burn vouch deliver credit", "burn vouch deliver credit pay"} {
		if stage > 0 {
			if err := s.world.step("ethereum", "in"); err != nil {
				t.Fatal(err)
			}
		}
		tr, err := s.resolve(ctx, ch, e, mockEthBurnTx)
		if err != nil {
			t.Fatal(err)
		}
		if tr.Direction != "in" || tr.Kind != "signature" || tr.ID != mockEthInID || tr.Amount != 2500*one || tr.To != mockRecipient || done(tr.Walk) != want || tr.Nonce != 4 || tr.Finality == nil || tr.Finality.Final != (stage >= 1) {
			t.Errorf("stage %d: %s %+v %+v", stage, done(tr.Walk), tr, tr.Finality)
		}
		if stage == 0 && !strings.Contains(tr.Steps[1].Text, "waiting for Ethereum to finalize block") {
			t.Errorf("stage 0: %s", tr.Steps[1].Text)
		}
		if wantV := [][]int{{}, {}, {1}, {1, 2}}; stage < 4 && fmt.Sprint(tr.Vouched) != fmt.Sprint(wantV[stage]) {
			t.Errorf("stage %d: vouched %v, want %v", stage, tr.Vouched, wantV[stage])
		}
	}
	// in, by its message id; the history's burn by its hash
	if tr, err := s.resolve(ctx, ch, e, mockEthInID); err != nil || tr.State != "paid" || tr.Finality == nil {
		t.Errorf("by id: %+v %v", tr, err)
	}
	if tr, err := s.resolve(ctx, ch, e, mockEthTx("burn Q")); err != nil || tr.State != "paid" || tr.Nonce != 2 {
		t.Errorf("the history's burn: %+v %v", tr, err)
	}
	// a hash that is nothing: no id, no transaction there, none on gno.land
	if _, err := s.resolve(ctx, ch, e, "0x"+strings.Repeat("ab", 32)); !errors.Is(err, errUnknownTransfer) || !strings.Contains(err.Error(), "no message id of the bridge") {
		t.Errorf("nothing: %v", err)
	}
}

func TestEthAPI(t *testing.T) {
	s, _, _, _ := mockBoth(false, siteDir, fixedNow)
	h := s.handler()
	do := func(method, path, body string) *httptest.ResponseRecorder {
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, httptest.NewRequest(method, path, strings.NewReader(body)))
		return rr
	}
	var chains struct {
		Chains []map[string]any `json:"chains"`
	}
	rr := do("GET", "/api/chains", "")
	if rr.Code != 200 || json.Unmarshal(rr.Body.Bytes(), &chains) != nil || len(chains.Chains) != 2 {
		t.Fatalf("chains: %d %s", rr.Code, rr.Body.String())
	}
	sol, eth := chains.Chains[0], chains.Chains[1]
	if _, ok := sol["kind"]; ok || sol["contracts"] != nil || sol["gno"] != nil {
		t.Errorf("Solana's entry grew: %v", sol)
	}
	cs, _ := eth["contracts"].(map[string]any)
	if eth["kind"] != "ethereum" || eth["chainId"] != "0xaa36a7" || len(cs) != 16 || cs["proxyAdmin"] != mockEthAddr("proxyAdmin").String() || cs["GNOT"] != mockEthAddr("GNOT").String() || eth["gno"] == nil {
		t.Errorf("Ethereum's entry: %v", eth)
	}
	for _, p := range []string{"/api/ethereum/status", "/api/ethereum/activity", "/api/ethereum/transfer/" + mockEthBurnTx, "/api/ethereum/transfer/" + mockEthOutID} {
		if rr := do("GET", p, ""); rr.Code != 200 || !strings.Contains(rr.Header().Get("Content-Security-Policy"), "connect-src 'self'") {
			t.Errorf("%s: %d %.300s", p, rr.Code, rr.Body.String())
		}
	}
	rr = do("POST", "/api/ethereum/burn-tx", `{"token":"BUBBLE","sender":"`+mockEthAddr("alice").String()+`","recipient":"`+mockRecipient+`","amount":"2500000000"}`)
	if rr.Code != 200 || !strings.Contains(rr.Body.String(), `"value":"0x5af3107a4000"`) || strings.Contains(rr.Body.String(), `"tx"`) || strings.Contains(rr.Body.String(), "lastValidBlockHeight") {
		t.Errorf("the builder: %d %s", rr.Code, rr.Body.String())
	}
	if rr := do("POST", "/api/ethereum/burn-tx", `{"token":"BUBBLE","sender":"`+mockEthAddr("alice").String()+`","recipient":"`+hl.GnoPackageAddress(mockEthNS("hlgnot"))+`","amount":1}`); rr.Code != 400 || !strings.Contains(rr.Body.String(), "bridge realm") {
		t.Errorf("a realm: %d %s", rr.Code, rr.Body.String())
	}
	if rr := do("GET", "/api/ethereum/transfer/"+strings.Repeat("x", 200), ""); rr.Code != 400 || !strings.Contains(rr.Body.String(), "Ethereum transaction hash") {
		t.Errorf("a long reference: %d %s", rr.Code, rr.Body.String())
	}
	if rr := do("GET", "/api/ethereum/transfer/0x"+strings.Repeat("cd", 32), ""); rr.Code != 404 {
		t.Errorf("an unknown hash: %d %s", rr.Code, rr.Body.String())
	}
}

// TestEthKinds: every decision kind's name and the words for its parameters.
func TestEthKinds(t *testing.T) {
	be := func(v uint64) []byte {
		return []byte{byte(v >> 56), byte(v >> 48), byte(v >> 40), byte(v >> 32), byte(v >> 24), byte(v >> 16), byte(v >> 8), byte(v)}
	}
	addr := mockEthAddr("new").word()
	ones := w32("01")
	cat := func(parts ...[]byte) []byte { return bytes.Join(parts, nil) }
	for _, c := range []struct {
		kind   byte
		params []byte
		want   string
	}{
		{1, cat(be(7), []byte{0, 2}, []byte{0}, be(3), []byte{0}, be(4)), "cancel #3, #4 (salt 0x0000000000000007)"},
		{1, cat(be(7), []byte{1, 0}, be(3), be(9)), "cancel #3 to #9"},
		{2, []byte{0}, "unpause the gate"},
		{2, []byte{1}, "unpause HlAdmin"},
		{3, cat([]byte{1}, addr[:]), "set BUBBLE's ISM to " + mockEthAddr("new").String()},
		{4, nil, "reserved (kind 4): spent"},
		{5, cat([]byte{1, 5}, addr[:]), "set guardians and watch keys: seat 5 → " + mockEthAddr("new").String()},
		{6, cat([]byte{0}, []byte{0x47, 0x6e, 0x6f, 0x4c}, addr[:]), "enroll GNOT's router for domain 1198419788"},
		{7, cat(be(mockProtocolFee), addr[:]), "set the protocol fee to 0.0001 ETH"},
		{8, cat(addr[:], addr[:], addr[:]), "upgrade " + mockEthAddr("new").String()},
		{9, cat(addr[:], addr[:]), "hand every authority to " + mockEthAddr("new").String()},
		{12, cat([]byte{0}, be(uint64(25_000*one)), be(uint64(100_000*one))), "set GNOT's mint limits: 25000 a transfer, 100000 a day"},
		{7, []byte{1, 2}, "set protocol fee: the parameters do not parse"},
		{3, cat([]byte{0}, ones[:]), "(no address: spent)"},
		{13, nil, "unknown kind 13"},
	} {
		if got := describeEthParams(c.kind, c.params); !strings.Contains(got, c.want) {
			t.Errorf("kind %d: %q, want %q", c.kind, got, c.want)
		}
	}
	if ethKindName(10) != "reserved" || ethKindName(12) != "set mint limits" || ethKindName(0) != "unknown" || !ethSevenDay(12) || ethSevenDay(10) || ethSevenDay(2) {
		t.Error("the kind table")
	}
}

var _ = context.Background

// TestSolanaNotABurn: a Solana transaction that burned nothing is answered in the words it always was (the answer is
// now typed, so that an Ethereum reference stops there rather than trying gno.land).
func TestSolanaNotABurn(t *testing.T) {
	s, _, _ := mockServer(false, siteDir, fixedNow)
	sig := mockSig("delivery P").String()
	_, err := s.resolve(ctx, &s.cfg.Chains[0], s.others["solana"], sig)
	if !errors.Is(err, errUnknownTransfer) || err.Error() != "unknown transfer: transaction "+sig+" made no burn toward gno.land on the bridge's token programs" {
		t.Errorf("%v", err)
	}
}
