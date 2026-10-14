package agent

import (
	"crypto/ed25519"
	"crypto/sha256"
	"fmt"
	"testing"

	"github.com/gnolang/bridges/solana/bridge/hl"
)

func testKeys(n int) ([]ed25519.PrivateKey, []ed25519.PublicKey) {
	var priv []ed25519.PrivateKey
	var pub []ed25519.PublicKey
	for i := 0; i < n; i++ {
		seed := sha256.Sum256([]byte(fmt.Sprintf("hyperlane gno test validator %d", i)))
		k := ed25519.NewKeyFromSeed(seed[:])
		priv = append(priv, k)
		pub = append(pub, k.Public().(ed25519.PublicKey))
	}
	return priv, pub
}

func sigsOf(idx ...int) map[int][64]byte {
	out := map[int][64]byte{}
	for _, i := range idx {
		var s [64]byte
		s[0] = byte(i + 1)
		out[i] = s
	}
	return out
}

func indexes(sigs []hl.EdSignature) []int {
	var out []int
	for _, s := range sigs {
		out = append(out, int(s.Validator))
	}
	return out
}

func same(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// The relayer sends exactly the signatures the rule needs (M1-NOTES §5.1: the verifier checks every signature
// present, so an extra bad one would refuse the message): the lowest-indexed ones up to the threshold, or one
// of each group.
func TestPickSignaturesThreshold(t *testing.T) {
	_, pub := testKeys(3)
	rule := Rule{Keys: pub, Threshold: 2}
	cases := []struct {
		have []int
		want []int
		err  bool
	}{
		{[]int{0, 1, 2}, []int{0, 1}, false},
		{[]int{1, 2}, []int{1, 2}, false},
		{[]int{2, 0}, []int{0, 2}, false},
		{[]int{2}, nil, true},
		{nil, nil, true},
		{[]int{0, 7}, nil, true}, // 7 names no signer
	}
	for _, c := range cases {
		got, err := pickSignatures(rule, sigsOf(c.have...))
		if (err != nil) != c.err {
			t.Errorf("have %v: err %v", c.have, err)
			continue
		}
		if !c.err && !same(indexes(got), c.want) {
			t.Errorf("have %v: picked %v, want %v", c.have, indexes(got), c.want)
		}
	}
}

func TestPickSignaturesTwoGroups(t *testing.T) {
	_, pub := testKeys(4)
	rule := Rule{Keys: pub, GroupA: []int{0}, GroupB: []int{1, 2, 3}}
	cases := []struct {
		have []int
		want []int
		err  bool
	}{
		{[]int{0, 1, 2, 3}, []int{0, 1}, false},
		{[]int{0, 3}, []int{0, 3}, false},
		{[]int{1, 2, 3}, nil, true}, // nobody of group A
		{[]int{0}, nil, true},       // nobody of group B
	}
	for _, c := range cases {
		got, err := pickSignatures(rule, sigsOf(c.have...))
		if (err != nil) != c.err {
			t.Errorf("have %v: err %v", c.have, err)
			continue
		}
		if !c.err && !same(indexes(got), c.want) {
			t.Errorf("have %v: picked %v, want %v", c.have, indexes(got), c.want)
		}
		if !c.err && !satisfied(rule, indexes(got)) {
			t.Errorf("have %v: the pick does not satisfy the rule", c.have)
		}
	}
}

// A signer's signature is ed25519 over Hyperlane's checkpoint digest of the message, and hlmailbox's verifier
// (its Go twin here) accepts the metadata the relayer builds from the picked signatures.
func TestSignatureRule(t *testing.T) {
	priv, pub := testKeys(3)
	m := hl.Message{Version: 3, Nonce: 7, Origin: hl.DomainSolanaTestnet, Destination: hl.DomainGnoTest, Body: hl.TokenMessage([32]byte{1}, 5, nil)}
	raw := m.Bytes()
	cp := hl.Checkpoint{Hook: hl.MustKey32("75HBBLae3ddeneJVrZeyrDfv6vb7SMC3aCpBucSXS5aR"), Index: 7}
	have := map[int][64]byte{}
	for i := range priv {
		sig := hl.SignEd25519(priv[i], cp, raw)
		have[i] = sig
		if !verifyOne(pub[i], m.Origin, cp, hl.ID(raw), sig[:]) {
			t.Fatalf("signer %d's signature does not verify under its own key", i)
		}
		if verifyOne(pub[(i+1)%3], m.Origin, cp, hl.ID(raw), sig[:]) {
			t.Fatalf("signer %d's signature verifies under another key", i)
		}
	}
	rule := Rule{Keys: pub, Threshold: 2}
	picked, err := pickSignatures(rule, have)
	if err != nil {
		t.Fatal(err)
	}
	meta := hl.GnoMetadata(cp, picked)
	if err := hl.VerifyGno(pub, 2, meta, raw); err != nil {
		t.Fatalf("hlmailbox's check refuses the relayer's metadata: %v", err)
	}
	if len(meta) != 68+2*65 {
		t.Fatalf("metadata carries %d bytes: not exactly two signatures", len(meta))
	}
	// A signature over another message is refused.
	other := m
	other.Nonce = 8
	bad := hl.SignEd25519(priv[0], cp, other.Bytes())
	if verifyOne(pub[0], m.Origin, cp, hl.ID(raw), bad[:]) {
		t.Fatal("a signature over another message verifies")
	}
}
