package ethx

import (
	"crypto/ecdsa"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"

	"github.com/gnolang/bridges/solana/bridge/hl"
)

// A machine's Ethereum key is one secp256k1 secret in a file of its own: 64 hex digits (0x optional, a trailing
// newline allowed), mode 0600 or narrower, never empty — hl.CheckKeyFile's rule, which every key file of the
// bridge's machines goes through. The secret is never printed: no error here carries a byte of it.

// LoadKey reads a key file.
func LoadKey(path string) (*ecdsa.PrivateKey, error) {
	if err := hl.CheckKeyFile(path); err != nil {
		return nil, err
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	s := strings.TrimPrefix(strings.TrimSpace(string(b)), "0x")
	if len(s) != 64 {
		return nil, fmt.Errorf("key file %s holds %d characters, not a 32-byte secret in hex", path, len(s))
	}
	raw, err := hex.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("key file %s is not hex", path)
	}
	k, err := crypto.ToECDSA(raw)
	if err != nil {
		return nil, fmt.Errorf("key file %s is not a secp256k1 secret", path)
	}
	return k, nil
}

// NewKeyFile writes a fresh key (mode 0600) and answers its address; it refuses to overwrite a file.
func NewKeyFile(path string) (common.Address, error) {
	if _, err := os.Stat(path); err == nil {
		return common.Address{}, fmt.Errorf("%s exists: refusing to overwrite a key", path)
	} else if !errors.Is(err, os.ErrNotExist) {
		return common.Address{}, err
	}
	k, err := crypto.GenerateKey()
	if err != nil {
		return common.Address{}, err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return common.Address{}, err
	}
	if _, err := f.WriteString(hex.EncodeToString(crypto.FromECDSA(k)) + "\n"); err != nil {
		f.Close()
		return common.Address{}, err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return common.Address{}, err
	}
	if err := f.Close(); err != nil {
		return common.Address{}, err
	}
	return crypto.PubkeyToAddress(k.PublicKey), nil
}

// KeyAddress is a key's address.
func KeyAddress(k *ecdsa.PrivateKey) common.Address { return crypto.PubkeyToAddress(k.PublicKey) }
