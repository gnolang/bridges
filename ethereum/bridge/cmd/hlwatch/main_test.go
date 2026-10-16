package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"

	"github.com/gnolang/bridges/ethereum/bridge/agent"
	"github.com/gnolang/bridges/ethereum/bridge/ethx"
	"github.com/gnolang/bridges/ethereum/bridge/gnoside"
)

// A -build is forge's artifact (the keccak256 of its deployedBytecode) or a file holding the hash; anything else is
// refused, never read as an empty build.
func TestReadBuilds(t *testing.T) {
	dir := t.TempDir()
	code := []byte{0x60, 0x80, 0x60, 0x40, 0x52}
	art := filepath.Join(dir, "Impl.json")
	if err := os.WriteFile(art, []byte(`{"abi":[],"bytecode":{"object":"0x6080604052600a"},"deployedBytecode":{"object":"0x6080604052","immutableReferences":{}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	hash := filepath.Join(dir, "succ.hash")
	want := common.HexToHash("0x1111111111111111111111111111111111111111111111111111111111111111")
	if err := os.WriteFile(hash, []byte(want.Hex()+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := readBuilds(builds{"impl": art, "succ": hash})
	if err != nil {
		t.Fatal(err)
	}
	if got["impl"].Hash != crypto.Keccak256Hash(code) || got["impl"].Art == nil || len(got["impl"].Art.Creation) != 7 || got["succ"].Hash != want || got["succ"].Art != nil {
		t.Fatalf("the builds: %v", got)
	}
	bad := filepath.Join(dir, "bad.json")
	_ = os.WriteFile(bad, []byte(`{"bytecode":{"object":"0x00"}}`), 0o600)
	if _, err := readBuilds(builds{"bad": bad}); err == nil {
		t.Fatal("an artifact without a deployedBytecode was taken")
	}
	var b builds = builds{}
	if err := b.Set("nolabel"); err == nil {
		t.Fatal("a -build without LABEL= was taken")
	}
}

// watch alarms on a flag set anywhere, a decision pending on either chain, an identity's verdict changed — and clears
// what is gone.
func TestWatchDiff(t *testing.T) {
	a := &agent.WatchView{Mailbox: &gnoside.Mailbox{Routes: map[string]gnoside.RouteFlags{}}, Identity: map[string]string{"GNOT": "holds: GNOT: lock 1"}}
	b := &agent.WatchView{
		Mailbox:  &gnoside.Mailbox{Routes: map[string]gnoside.RouteFlags{"gno.land/r/x/eth/hlgnot": {Flags: []string{"credits by seat 1 (g1abc) at 7"}}}},
		Gate:     &ethx.GateState{Flags: [6]ethx.Flag{5: {BlockNumber: 90}}},
		Admin:    &ethx.AdminState{},
		Identity: map[string]string{"GNOT": "BROKEN: the right side …"},
		Pending:  []agent.PendingGno{{Realm: "gno.land/r/x/eth/hlgov", Decision: gnoside.Decision{ID: 3, Kind: "set fee", Text: "…"}}},
	}
	al := &agent.Alarmer{Source: "test"}
	diff(al, a, b)
	st := al.Standing()
	for _, k := range []string{"watch:flag:gno.land/r/x/eth/hlgnot:credits:1", "watch:flag:gate:6", "watch:identity:GNOT", "watch:pending:gno.land/r/x/eth/hlgov#3"} {
		if _, ok := st[k]; !ok {
			t.Errorf("no alarm %s: %v", k, st)
		}
	}
	diff(al, b, a)
	if _, ok := al.Standing()["watch:flag:gate:6"]; ok {
		t.Fatal("a flag gone stays alarmed")
	}
	if !strings.HasPrefix(verdictOf("BROKEN: x"), "BROKEN") || verdictOf("holds: y") != "holds" {
		t.Fatal("verdictOf")
	}
}

// E3-GT7: a kind 8 typed by hand names its proxy and its call (-call none for a bare upgrade): `check` said "no call
// (ProxyAdmin.upgrade)" of a decision whose call it was never shown, and held the build to no proxy.
func TestTypedSpec(t *testing.T) {
	impl := "0x000000000000000000000000000000000000D00d" // its EIP-55 form: one typed in one case is refused (E3-CF6(b))
	proxy := "0x000000000000000000000000000000000000b0b0"
	for _, c := range []struct {
		name                 string
		kind                 uint
		target, proxy, call  string
		wantErr, wantCallHex string
	}{
		{"a kind 8 without its proxy and call", 8, impl, "", "", "-proxy and -call are needed", ""},
		{"a kind 8 without its call", 8, impl, proxy, "", "-proxy and -call are needed", ""},
		{"a bare upgrade, said so", 8, impl, proxy, "none", "", ""},
		{"an upgrade and call", 8, impl, proxy, "0xdeadbeef", "", "deadbeef"},
		{"a call that is no hex", 8, impl, proxy, "0xzz", "-call is calldata in hex", ""},
		{"a kind 9 with a proxy", 9, impl, proxy, "", "a kind 8's", ""},
		{"no target", 8, "", proxy, "none", "-proposal ID", ""},
	} {
		s, err := typedSpec(c.kind, c.target, "", c.proxy, c.call)
		switch {
		case c.wantErr != "" && (err == nil || !strings.Contains(err.Error(), c.wantErr)):
			t.Errorf("%s: want %q, got %v", c.name, c.wantErr, err)
		case c.wantErr == "" && err != nil:
			t.Errorf("%s: %v", c.name, err)
		case c.wantErr == "" && fmt.Sprintf("%x", s.Call) != c.wantCallHex:
			t.Errorf("%s: the call %x", c.name, s.Call)
		}
	}
}
