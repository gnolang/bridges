package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/gagliardetto/solana-go"
)

// TestSolanaKeyFile: the file is solana-keygen's form (solana-go reads it back to the same public key), mode 0600,
// never overwritten, and two keys differ.
func TestSolanaKeyFile(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.json")
	pub, err := newSolanaKeyFile(a)
	if err != nil {
		t.Fatal(err)
	}
	k, err := solana.PrivateKeyFromSolanaKeygenFile(a)
	if err != nil || k.PublicKey().String() != pub {
		t.Fatalf("read back: %v %v want %s", err, k.PublicKey(), pub)
	}
	if st, _ := os.Stat(a); st.Mode().Perm() != 0o600 {
		t.Errorf("mode %v", st.Mode().Perm())
	}
	if _, err := newSolanaKeyFile(a); err == nil {
		t.Error("an existing file was overwritten")
	}
	b := filepath.Join(dir, "b.json")
	pub2, err := newSolanaKeyFile(b)
	if err != nil || pub2 == pub {
		t.Errorf("a second key: %v %s", err, pub2)
	}
	if p, err := solanaKeyFilePub(b); err != nil || p != pub2 {
		t.Errorf("solpub: %v %s", err, p)
	}
	// a doctored public half is refused
	body, _ := os.ReadFile(b)
	var nums []int
	if err := json.Unmarshal(body, &nums); err != nil || len(nums) != 64 {
		t.Fatalf("the file's form: %v %d", err, len(nums))
	}
	nums[63] ^= 1 // the public half's last byte, flipped
	doctored, _ := json.Marshal(nums)
	bad := filepath.Join(dir, "bad.json")
	os.WriteFile(bad, doctored, 0o600)
	if _, err := solanaKeyFilePub(bad); err == nil {
		t.Error("a mismatched public half was accepted")
	}
}
