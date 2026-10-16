package main

import (
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/common"
)

// fill writes each immutable's value into every place of a copy of the build's runtime code: what a deployment's
// code is.
func fill(a *Artifact, vals map[string][32]byte) []byte {
	code := append([]byte{}, a.Runtime...)
	for _, im := range a.Immutables {
		v := vals[im.Name]
		for _, r := range im.Refs {
			copy(code[r.Start:r.Start+r.Length], v[:])
		}
	}
	return code
}

func synthetic() *Artifact {
	rt := make([]byte, 200)
	for i := range rt {
		rt[i] = byte(i)
	}
	return &Artifact{Name: "Synthetic", Runtime: rt, Immutables: []Immutable{
		{ID: "1", Name: "S.mailbox", Refs: []Ref{{Start: 10, Length: 32}, {Start: 100, Length: 32}}},
		{ID: "2", Name: "S.domain", Refs: []Ref{{Start: 50, Length: 32}}},
	}}
}

func TestCodeCheckFailsEachWrongMaskedValue(t *testing.T) {
	a := synthetic()
	mailbox := common.HexToAddress("0x9fE46736679d2D9a65F0992F2272dE9f3c7fa6e0")
	want := map[string]Expect{"S.mailbox": expectAddr(mailbox, "our mailbox"), "S.domain": expectU64(31337, "Ethereum's domain")}
	good := map[string][32]byte{"S.mailbox": want["S.mailbox"].Want, "S.domain": want["S.domain"].Want}
	at := common.HexToAddress("0x01")
	js := JudgeCode("syn", at, fill(a, good), a, want)
	if failures(js) != 0 {
		t.Fatalf("the build with its values failed:\n%s", judgementText(js))
	}
	// a wrong value at an immutable's every place: named, with both values
	stand := common.HexToAddress("0x000000000000000000000000000000000000bEEF")
	bad := map[string][32]byte{"S.mailbox": common.BytesToHash(stand.Bytes()), "S.domain": good["S.domain"]}
	js = JudgeCode("syn", at, fill(a, bad), a, want)
	if failures(js) != 1 || !strings.Contains(judgementText(js), "FAIL syn's masked immutable S.mailbox = 0x000000000000000000000000000000000000bEEF, but it must be 0x9fE46736679d2D9a65F0992F2272dE9f3c7fa6e0 (our mailbox)") {
		t.Fatalf("a stand-in mailbox:\n%s", judgementText(js))
	}
	bad = map[string][32]byte{"S.mailbox": good["S.mailbox"], "S.domain": common.BigToHash(big.NewInt(1))}
	js = JudgeCode("syn", at, fill(a, bad), a, want)
	if failures(js) != 1 || !strings.Contains(judgementText(js), "S.domain = 1, but it must be 31337 (Ethereum's domain)") {
		t.Fatalf("a wrong domain:\n%s", judgementText(js))
	}
	// one place of an immutable changed: its places disagree
	code := fill(a, good)
	code[100+31] ^= 1
	if js = JudgeCode("syn", at, code, a, want); failures(js) != 1 || !strings.Contains(judgementText(js), "different values in its places") {
		t.Fatalf("a split immutable:\n%s", judgementText(js))
	}
	// a byte outside the masks: the code is not the build's
	code = fill(a, good)
	code[5] ^= 0xff
	if js = JudgeCode("syn", at, code, a, want); failures(js) != 1 || !strings.Contains(judgementText(js), "1 bytes differ from the build outside the immutables, the first at offset 5") {
		t.Fatalf("a byte outside the masks:\n%s", judgementText(js))
	}
	// a masked immutable the audit has no value for: it cannot vouch for it
	if js = JudgeCode("syn", at, fill(a, good), a, map[string]Expect{"S.domain": want["S.domain"]}); failures(js) != 1 || !strings.Contains(judgementText(js), "S.mailbox = 0x") {
		t.Fatalf("an immutable with no expectation:\n%s", judgementText(js))
	}
	// another length, or no code at all
	if js = JudgeCode("syn", at, append(fill(a, good), 0), a, want); failures(js) != 1 {
		t.Fatalf("a longer code:\n%s", judgementText(js))
	}
	if js = JudgeCode("syn", at, nil, a, want); failures(js) != 1 || !strings.Contains(judgementText(js), "no code at the address") {
		t.Fatalf("no code:\n%s", judgementText(js))
	}
}

// artifactsOrSkip loads the real builds when the devbox's directories are named (HLETH_CONTRACTS_OUT, a forge out
// built with --force --ast; HLETH_HYPERLANE_DIR, local/hyperlane-artifacts.sh's output).
func artifactsOrSkip(t *testing.T) Artifacts {
	t.Helper()
	out, hy := os.Getenv("HLETH_CONTRACTS_OUT"), os.Getenv("HLETH_HYPERLANE_DIR")
	if out == "" || hy == "" {
		t.Skip("HLETH_CONTRACTS_OUT and HLETH_HYPERLANE_DIR name the builds (the devbox's)")
	}
	if _, err := os.Stat(filepath.Join(out, "HlAdmin.sol", "HlAdmin.json")); err != nil {
		t.Skip("no HlAdmin build in HLETH_CONTRACTS_OUT")
	}
	a, err := LoadArtifacts(out, hy)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

// On the real builds: HypERC20's every immutable and HlAdmin's every one (its origin and hlgov among them), each
// set wrong in a copy of the code with the others right, fails the code check naming it and both values.
func TestRealBuildsEachWrongImmutableFails(t *testing.T) {
	arts := artifactsOrSkip(t)
	mailbox := common.HexToAddress("0x9fE46736679d2D9a65F0992F2272dE9f3c7fa6e0")
	hy := arts["HypERC20"]
	hyWant := map[string]Expect{
		"MailboxClient.mailbox":        expectAddr(mailbox, "our mailbox"),
		"MailboxClient.localDomain":    expectU64(31337, "Ethereum's domain"),
		"HypERC20._decimals":           expectU64(6, "6"),
		"TokenRouter.scaleNumerator":   expectU64(1, "1/1"),
		"TokenRouter.scaleDenominator": expectU64(1, "1/1"),
	}
	var g common.Hash
	copy(g[12:], common.HexToAddress("0x073f0fda0fda3eab0213bfa0ee329cd80ff4ab71").Bytes())
	ha := arts["HlAdmin"]
	haWant := map[string]Expect{}
	for i, im := range ha.Immutables {
		w := common.BigToHash(big.NewInt(int64(1000 + i)))
		switch im.Name {
		case "HlAdmin.origin":
			w = common.BigToHash(big.NewInt(1198419796))
		case "HlAdmin.hlgov":
			w = g
		}
		haWant[im.Name] = Expect{Kind: kindBytes32, Want: w, What: "the launch's"}
	}
	if _, ok := haWant["HlAdmin.origin"]; !ok {
		t.Fatalf("HlAdmin's build has no origin immutable: %v", ha.Immutables)
	}
	if _, ok := haWant["HlAdmin.hlgov"]; !ok {
		t.Fatalf("HlAdmin's build has no hlgov immutable: %v", ha.Immutables)
	}
	for _, x := range []struct {
		a    *Artifact
		want map[string]Expect
	}{{hy, hyWant}, {ha, haWant}} {
		if len(x.a.Immutables) != len(x.want) {
			t.Fatalf("%s has %d immutables, the test knows %d", x.a.Name, len(x.a.Immutables), len(x.want))
		}
		good := map[string][32]byte{}
		for n, e := range x.want {
			good[n] = e.Want
		}
		if js := JudgeCode(x.a.Name, common.Address{}, fill(x.a, good), x.a, x.want); failures(js) != 0 {
			t.Fatalf("%s with its right values:\n%s", x.a.Name, judgementText(js))
		}
		for _, im := range x.a.Immutables {
			bad := map[string][32]byte{}
			for n, v := range good {
				bad[n] = v
			}
			wrong := common.BigToHash(big.NewInt(0xbad))
			bad[im.Name] = wrong
			js := JudgeCode(x.a.Name, common.Address{}, fill(x.a, bad), x.a, x.want)
			text := judgementText(js)
			e := x.want[im.Name]
			if failures(js) != 1 || !strings.Contains(text, im.Name+" = "+e.Kind.Render(wrong)+", but it must be "+e.Kind.Render(e.Want)) {
				t.Fatalf("%s's %s set wrong:\n%s", x.a.Name, im.Name, text)
			}
		}
	}
}
