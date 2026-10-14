package hl

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
)

// ValidatorKey is a signer's ed25519 key for checkpoints of Solana messages, which the gno mailbox checks (the
// signers sign nothing on Solana, PLAN.md §5.6). The file holds the seed in hex; it is the signer's alone (mode
// 0600). A file from before the plan's shape also carries a secp256k1 secret, which is ignored.
type ValidatorKey struct {
	Ed ed25519.PrivateKey
}

type validatorKeyFile struct {
	Ed25519 string `json:"ed25519_seed"`
}

func NewValidatorKey() (*ValidatorKey, error) {
	seed := make([]byte, ed25519.SeedSize)
	if _, err := rand.Read(seed); err != nil {
		return nil, err
	}
	return &ValidatorKey{Ed: ed25519.NewKeyFromSeed(seed)}, nil
}

func (k *ValidatorKey) Save(path string) error {
	f := validatorKeyFile{Ed25519: hex.EncodeToString(k.Ed.Seed())}
	b, _ := json.MarshalIndent(f, "", "  ")
	return os.WriteFile(path, append(b, '\n'), 0o600)
}

// LoadValidatorKey reads a signer's key file, refusing one readable by others or empty (CheckKeyFile).
func LoadValidatorKey(path string) (*ValidatorKey, error) {
	if err := CheckKeyFile(path); err != nil {
		return nil, err
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var f validatorKeyFile
	if err := json.Unmarshal(b, &f); err != nil {
		return nil, fmt.Errorf("key file %s is corrupt: %w", path, err)
	}
	seed, err := hex.DecodeString(f.Ed25519)
	if err != nil || len(seed) != ed25519.SeedSize {
		return nil, errors.New("hl: validator key: ed25519_seed is not 32 bytes of hex")
	}
	return &ValidatorKey{Ed: ed25519.NewKeyFromSeed(seed)}, nil
}

// EdPublic is the signer's key as hlmailbox lists it.
func (k *ValidatorKey) EdPublic() ed25519.PublicKey { return k.Ed.Public().(ed25519.PublicKey) }
