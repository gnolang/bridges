package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/gagliardetto/solana-go"
)

// newSolanaKeyFile writes a new random Solana keypair at path in solana-keygen's JSON form — the 64 bytes of
// the ed25519 private key (seed then public key) as a JSON array of numbers — with mode 0600, and returns its
// public key in base58. It refuses an existing file: a key is made once, where it will live.
func newSolanaKeyFile(path string) (string, error) {
	if _, err := os.Stat(path); err == nil {
		return "", fmt.Errorf("%s exists", path)
	}
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return "", err
	}
	nums := make([]int, len(priv))
	for i, b := range priv {
		nums[i] = int(b)
	}
	body, err := json.Marshal(nums)
	if err != nil {
		return "", err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", err
	}
	if _, err := f.Write(body); err != nil {
		f.Close()
		return "", err
	}
	if err := f.Close(); err != nil {
		return "", err
	}
	return solanaKeyFilePub(path)
}

// solanaKeyFilePub reads a solana-keygen JSON keypair and returns its public key in base58, checking that the
// public half is the one the seed derives.
func solanaKeyFilePub(path string) (string, error) {
	k, err := solana.PrivateKeyFromSolanaKeygenFile(path)
	if err != nil {
		return "", err
	}
	if len(k) != ed25519.PrivateKeySize {
		return "", errors.New("not a 64-byte keypair")
	}
	derived := ed25519.NewKeyFromSeed(k[:32])
	if string(derived[32:]) != string(k[32:]) {
		return "", errors.New("the public half does not match the seed")
	}
	return k.PublicKey().String(), nil
}
