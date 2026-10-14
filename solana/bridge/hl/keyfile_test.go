package hl

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A key file readable by others, or empty, is refused; a 0600 file with content passes.
func TestCheckKeyFile(t *testing.T) {
	dir := t.TempDir()
	wide := filepath.Join(dir, "wide.json")
	if err := os.WriteFile(wide, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := CheckKeyFile(wide); err == nil || !strings.Contains(err.Error(), "wider than 0600") {
		t.Fatalf("a 0644 key file was taken: %v", err)
	}
	empty := filepath.Join(dir, "empty.json")
	if err := os.WriteFile(empty, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := CheckKeyFile(empty); err == nil || !strings.Contains(err.Error(), "corrupt") {
		t.Fatalf("an empty key file was taken, or not named as corruption: %v", err)
	}
	good := filepath.Join(dir, "good.json")
	if err := os.WriteFile(good, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := CheckKeyFile(good); err != nil {
		t.Fatal(err)
	}
	// LoadValidatorKey goes through the same check.
	k, _ := NewValidatorKey()
	p := filepath.Join(dir, "signer.json")
	if err := k.Save(p); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadValidatorKey(p); err != nil {
		t.Fatal(err)
	}
	_ = os.Chmod(p, 0o640)
	if _, err := LoadValidatorKey(p); err == nil {
		t.Fatal("a group-readable signer key was loaded")
	}
}

// The gno bech32 of an ed25519 public key matches tm2's crypto.PubKeyToBech32 (computed with the gno module for
// ed25519.GenPrivKeyFromSecret(00…01), whose seed is sha256 of the secret).
func TestGnoPubKeyBech32(t *testing.T) {
	secret, _ := hex.DecodeString("0000000000000000000000000000000000000000000000000000000000000001")
	seed := sha256.Sum256(secret)
	pub := ed25519.NewKeyFromSeed(seed[:]).Public().(ed25519.PublicKey)
	want := "gpub1pggj7ard9eg82cjtv4u52epjx56nzwgjyg9zqjpqenszet9gp6y8chrdv2ftlwqesq4avpgjef4ek0pcqp798s9qghvecc"
	if got := GnoPubKeyBech32(pub); got != want {
		t.Fatalf("got %s\nwant %s", got, want)
	}
}
