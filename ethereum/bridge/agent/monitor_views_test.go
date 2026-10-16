package agent

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/crypto"

	"github.com/gnolang/bridges/ethereum/bridge/ethx"
)

// The storage layouts' difference before a kind 8 (§0: append-only): an entry kept, one added after the running ones,
// one moved, one inserted inside the running layout, a type changed.
func TestLayoutDiff(t *testing.T) {
	// the types as forge writes them: a struct's members are what an upgrade must keep (E4-GT3)
	entry := func(ast string) string {
		return `"t_struct(Entry)` + ast + `_storage":{"label":"struct C.Entry","numberOfBytes":"64","members":[{"label":"epoch","slot":"0","offset":0,"type":"t_uint64"},{"label":"height","slot":"0","offset":8,"type":"t_uint64"},{"label":"lastResultsHash","slot":"1","offset":0,"type":"t_bytes32"}]},"t_mapping(t_bytes32,t_struct(Entry)` + ast + `_storage)":{"label":"mapping(bytes32 => struct C.Entry)","numberOfBytes":"32"}`
	}
	old := `{"storage":[{"label":"_admin","slot":"0","offset":0,"type":"t_address"},{"label":"_records","slot":"1","offset":0,"type":"t_uint64"},{"label":"_entries","slot":"2","offset":0,"type":"t_mapping(t_bytes32,t_struct(Entry)123_storage)"}],"types":{` + entry("123") + `}}`
	cases := []struct {
		name, new, want string
	}{
		{"append-only", `{"storage":[{"label":"_admin","slot":"0","offset":0,"type":"t_address"},{"label":"_records","slot":"1","offset":0,"type":"t_uint64"},{"label":"_entries","slot":"2","offset":0,"type":"t_mapping(t_bytes32,t_struct(Entry)456_storage)"},{"label":"_new","slot":"3","offset":0,"type":"t_uint256"}],"types":{` + entry("456") + `}}`, "append-only (3 entries kept, 1 added"},
		{"reordered", `{"storage":[{"label":"_records","slot":"0","offset":0,"type":"t_uint64"},{"label":"_admin","slot":"1","offset":0,"type":"t_address"},{"label":"_entries","slot":"2","offset":0,"type":"t_mapping(t_bytes32,t_struct(Entry)123_storage)"}]}`, "LAYOUT DIFFERS"},
		{"inserted", `{"storage":[{"label":"_admin","slot":"0","offset":0,"type":"t_address"},{"label":"_x","slot":"0","offset":20,"type":"t_uint32"},{"label":"_records","slot":"1","offset":0,"type":"t_uint64"},{"label":"_entries","slot":"2","offset":0,"type":"t_mapping(t_bytes32,t_struct(Entry)123_storage)"}]}`, "inside the running layout"},
		{"a type changed", `{"storage":[{"label":"_admin","slot":"0","offset":0,"type":"t_address"},{"label":"_records","slot":"1","offset":0,"type":"t_uint128"},{"label":"_entries","slot":"2","offset":0,"type":"t_mapping(t_bytes32,t_struct(Entry)123_storage)"}]}`, "was _records t_uint64, is _records t_uint128"},
		{"gone", `{"storage":[{"label":"_admin","slot":"0","offset":0,"type":"t_address"}]}`, "is gone"},
	}
	for _, c := range cases {
		if got := LayoutDiff([]byte(old), []byte(c.new)); !strings.Contains(got, c.want) {
			t.Errorf("%s: %s", c.name, got)
		}
	}
}

// Before a kind 8 or 9 a holder reads the target's code: an EIP-7702 account named (its key re-delegates it at will),
// no code named, the hash against the decision's and against a reproducible build.
func TestCodeCheck(t *testing.T) {
	r := newMonRig(t)
	impl := common.HexToAddress("0x000000000000000000000000000000000000d00d")
	eoa7702 := common.HexToAddress("0x000000000000000000000000000000000000d7d7")
	code := []byte{0x60, 0x80, 0x60, 0x40}
	r.chain.mu.Lock()
	r.chain.code[impl] = 1
	r.chain.code[eoa7702] = 1
	r.chain.mu.Unlock()
	codes := map[common.Address][]byte{impl: code, eoa7702: append([]byte{0xef, 0x01, 0x00}, make([]byte, 20)...)}
	for _, p := range []*m3bProvider{r.pa, r.pb} {
		p.codeX = func(a common.Address) hexutil.Bytes { return codes[a] }
	}
	w := &Watch{m: r.m, Builds: map[string]Build{"impl-v2": {Hash: crypto.Keccak256Hash(code)}}}
	at := ethx.AtNumber(100)
	check := func(addr common.Address, want common.Hash, _ bool) string {
		return w.ImplementationCheck(context.Background(), at, m3bAdmin, common.Address{}, addr, want, nil, "")
	}
	got := check(impl, crypto.Keccak256Hash(code), false)
	if !strings.Contains(got, "equals the decision's") || !strings.Contains(got, "is the reproducible build impl-v2") {
		t.Fatalf("a matching implementation: %s", got)
	}
	if got := check(impl, common.Hash{1}, false); !strings.Contains(got, "NOT the decision's") || !strings.Contains(got, VerdictSpent) {
		t.Fatalf("a hash that is not the decision's: %s", got)
	}
	if got := check(eoa7702, common.Hash{}, false); !strings.Contains(got, "EIP-7702 account") || !strings.Contains(got, "MUTABLE") {
		t.Fatalf("an EIP-7702 account: %s", got)
	}
	if got := check(common.HexToAddress("0x1234"), common.Hash{}, false); !strings.Contains(got, "NO CODE") {
		t.Fatalf("an account without code: %s", got)
	}
	w.Builds = map[string]Build{"other": {Hash: common.Hash{2}}}
	if got := check(impl, common.Hash{}, true); !strings.Contains(got, "MATCHES NO REPRODUCIBLE BUILD") || !strings.Contains(got, "LAYOUT NOT CHECKED: no storage layouts given") {
		t.Fatalf("no build matches: %s", got)
	}
}

// The holder's page over a quiet bridge: green; over a break, not, and the page says why.
func TestWatchViewGreenAndNot(t *testing.T) {
	r := newMonRig(t)
	r.m.ReadOnly = true
	w := &Watch{m: r.m, Supermajority: 66.66}
	v, err := w.View(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	text := v.Text("build x")
	for _, want := range []string{"## hlmailbox", "## the identity", "GNOT: lock 1000", "holds", "## Ethereum", "the client: trusted height 50", "matches gno.land's", "pending decisions on HlAdmin"} {
		if !strings.Contains(text, want) {
			t.Errorf("the page lacks %q:\n%s", want, text)
		}
	}
	if ok, why := v.Green(); !ok {
		t.Fatalf("a quiet bridge is not green: %v\n%s", why, text)
	}
	r.chain.mine(110, 110)
	r.chain.setSupply(m3bGNOT, 105, 1300)
	r.step(10 * time.Second)
	v, err = w.View(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if ok, why := v.Green(); ok || !strings.Contains(strings.Join(why, ";"), "GNOT's identity is broken") {
		t.Fatalf("a break is green: %v", why)
	}
	if r.snd.count() != 0 || len(r.gnoFlags()) != 0 {
		t.Fatal("the read-only watch flagged")
	}
}

// DescribeKind writes every kind of §13 in words.
func TestDescribeKind(t *testing.T) {
	addr := ethx.AddressTo32(common.HexToAddress("0x00000000000000000000000000000000000000aa"))
	cases := map[byte][]byte{
		1:  append(append(make([]byte, 8), 0, 1), append([]byte{0}, 0, 0, 0, 0, 0, 0, 0, 5)...),
		2:  {1},
		3:  append([]byte{0}, addr[:]...),
		5:  append([]byte{1, 3}, addr[:]...),
		7:  append([]byte{0, 0, 0, 0, 0, 0, 3, 0xe8}, addr[:]...),
		9:  append(addr[:], make([]byte, 32)...),
		12: {1, 0, 0, 0, 0, 0, 0, 0, 9, 0, 0, 0, 0, 0, 0, 0, 10},
	}
	want := map[byte]string{1: "cancel numbers 5", 2: "unpause HlAdmin", 3: "set token 0's ISM", 5: "seat 3 →", 7: "set protocol fee 1000 wei", 9: "transfer every authority", 12: "9 a message, 10 a day"}
	for k, p := range cases {
		if got := DescribeKind(k, p); !strings.Contains(got, want[k]) {
			t.Errorf("kind %d: %s", k, got)
		}
	}
}
