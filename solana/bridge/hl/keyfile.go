package hl

import (
	"crypto/ed25519"
	"fmt"
	"os"

	"github.com/btcsuite/btcd/btcutil/bech32"
)

// CheckKeyFile refuses a key file another user could read (a mode wider than 0600) or one that holds nothing (a
// power loss mid-write leaves a zero-length file, which is corruption, never a key). Every key file a machine
// loads — a signer's seed, a Solana keypair, goes through it.
func CheckKeyFile(path string) error {
	st, err := os.Stat(path)
	if err != nil {
		return err
	}
	if st.IsDir() {
		return fmt.Errorf("key file %s is a directory", path)
	}
	if mode := st.Mode().Perm(); mode&0o077 != 0 {
		return fmt.Errorf("key file %s has mode %04o: wider than 0600, readable by others; refusing it (chmod 600)", path, mode)
	}
	if st.Size() == 0 {
		return fmt.Errorf("key file %s is empty: corrupt (a write that never finished), not a key", path)
	}
	return nil
}

// GnoPubKeyBech32 is an ed25519 key as gno.land writes a consensus public key (tm2's crypto.PubKeyToBech32):
// the protobuf Any of /tm.PubKeyEd25519 in bech32 under the prefix "gpub". r/gnops/valopers takes it at
// Register, and r/sys/validators carries it per validator.
func GnoPubKeyBech32(pub ed25519.PublicKey) string {
	const typeURL = "/tm.PubKeyEd25519"
	inner := append([]byte{0x0a, byte(len(pub))}, pub...) // field 1: the key bytes
	any := append([]byte{0x0a, byte(len(typeURL))}, []byte(typeURL)...)
	any = append(any, 0x12, byte(len(inner)))
	any = append(any, inner...)
	data, err := bech32.ConvertBits(any, 8, 5, true)
	if err != nil {
		panic(err)
	}
	s, err := bech32.Encode("gpub", data)
	if err != nil {
		panic(err)
	}
	return s
}
