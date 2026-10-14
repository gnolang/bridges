package hl

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"testing"
)

func h32(s string) (out [32]byte) {
	b, err := hex.DecodeString(s)
	if err != nil || len(b) != 32 {
		panic("h32 " + s)
	}
	copy(out[:], b)
	return out
}

func rep(b byte) (out [32]byte) {
	for i := range out {
		out[i] = b
	}
	return out
}

func pad20(b byte) (out [32]byte) {
	for i := 12; i < 32; i++ {
		out[i] = b
	}
	return out
}

func hx(b []byte) string { return hex.EncodeToString(b) }

func TestMessageVector(t *testing.T) {
	m := Message{Version: 3, Origin: 1000, Sender: pad20(0x11), Destination: 2000, Recipient: pad20(0x22), Body: []byte{18, 52}}
	if id := m.ID(); hx(id[:]) != "f8a66f8aadee751d842616fee0ed14a3ad6da1e13564920364ee0ad35a02703f" {
		t.Fatalf("id %x", id)
	}
	back, err := ParseMessage(m.Bytes())
	if err != nil || !bytes.Equal(back.Bytes(), m.Bytes()) {
		t.Fatalf("round trip %v", err)
	}
}

func TestDomainHashVectors(t *testing.T) {
	for i, w := range []string{
		"bbca56eb98960a4637eb40486d9a069550dd70d9c185ed138516e8e33cf3d7e7",
		"a6a93d86d397028e41995d521ccbc270e6db2a2fc530dcb7f0135254f30c8424",
		"ffb4fbe5142f55e07b5d44b3c7f565c5ef4b016551cbd7c23a92c91621aca06f",
	} {
		if d := DomainHash(uint32(i+1), pad20(0x22)); hx(d[:]) != w {
			t.Fatalf("domain %d: %x", i+1, d)
		}
	}
}

func sealevelMessage() []byte {
	body := make([]byte, 10)
	for i := range body {
		body[i] = byte(i)
	}
	return Message{Version: 3, Nonce: 69, Origin: 1234, Sender: rep(0xaf), Destination: 4321, Recipient: rep(0xbe), Body: body}.Bytes()
}

// TestSealevelCheckpoint is rust/sealevel/libraries/multisig-ism/src/test_data.rs: the message id and the
// checkpoint digest Hyperlane's validators sign, which the gno verifier's ed25519 signatures are over too.
func TestSealevelCheckpoint(t *testing.T) {
	raw := sealevelMessage()
	id := ID(raw)
	if hx(id[:]) != "808dd1b989490a4f9911f716132fbb6e57c519eb86eb7ecbb3fc7790ec30170c" {
		t.Fatalf("id %x", id)
	}
	d := CheckpointDigest(1234, rep(0xab), rep(0xcd), 70, id)
	if hx(d[:]) != "ce7956b074d407cc6f056ad6dc1737aac9935dbfc42df381d016ce1ab179c1e5" {
		t.Fatalf("digest %x", d)
	}
}

func TestMerkleVectors(t *testing.T) {
	raw, err := os.ReadFile("testdata/merkle.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		TestName     string `json:"testName"`
		ExpectedRoot string `json:"expectedRoot"`
		Leaves       []string
		Proofs       []struct {
			Leaf  string
			Index int
		}
	}
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		var tr Tree
		for i, l := range c.Leaves {
			// the vectors' leaves are EIP-191 personal-message hashes of the strings
			leaf := Keccak([]byte(fmt.Sprintf("\x19Ethereum Signed Message:\n%d%s", len(l), l)))
			if hx(leaf[:]) != c.Proofs[i].Leaf[2:] {
				t.Fatalf("%s: leaf %d", c.TestName, i)
			}
			tr.Insert(leaf)
		}
		if r := tr.Root(); "0x"+hx(r[:]) != c.ExpectedRoot {
			t.Fatalf("%s: root %x", c.TestName, r)
		}
	}
}

func TestGnoAddresses(t *testing.T) {
	// wbubble's own test pins this: bech32 of sha256("pkgPath:" + path)[:20]
	if a := GnoPackageAddress("gno.land/r/g1leu8d2vsplhehcfkjg50mwgdpxdkt8tztu95wr/wbubble"); a != "g1lxgagqwq8c4mdt4agdacpfs3t85m8kja4dkv3k" {
		t.Fatalf("package address %s", a)
	}
	b, err := GnoAddressToBytes32("g1leu8d2vsplhehcfkjg50mwgdpxdkt8tztu95wr")
	if err != nil {
		t.Fatal(err)
	}
	if a, err := Bytes32ToGnoAddress(b); err != nil || a != "g1leu8d2vsplhehcfkjg50mwgdpxdkt8tztu95wr" {
		t.Fatalf("round trip %s %v", a, err)
	}
	// §13: a recipient whose top twelve bytes are not zero is no gno address (K2: a raw Solana pubkey burned to;
	// the realm sweeps it, and the Go reader errors rather than reading its last twenty bytes as an address).
	var raw [32]byte
	copy(raw[:], b[:])
	raw[3] = 1
	if _, err := Bytes32ToGnoAddress(raw); err == nil {
		t.Fatal("a 32-byte key with a non-zero top was read as a gno address")
	}
}

func TestTokenMessage(t *testing.T) {
	b := TokenMessage(rep(0x5a), 1_234_567, nil)
	if hx(b[32:]) != "000000000000000000000000000000000000000000000000000000000012d687" {
		t.Fatalf("%x", b)
	}
	r, a, m, err := ParseTokenMessage(append(b, 'x'))
	if err != nil || r != rep(0x5a) || a != 1_234_567 || string(m) != "x" {
		t.Fatalf("parse")
	}
}

// testEdKey is a deterministic test validator, the same seed the gno tests name.
func testEdKey(i int) ed25519.PrivateKey {
	seed := sha256.Sum256([]byte(fmt.Sprintf("hyperlane gno test validator %d", i)))
	return ed25519.NewKeyFromSeed(seed[:])
}

func TestEd25519Multisig(t *testing.T) {
	raw := sealevelMessage()
	c := Checkpoint{rep(0xab), rep(0xcd), 70}
	var pubs []ed25519.PublicKey
	var sigs []EdSignature
	for i := 0; i < 3; i++ {
		k := testEdKey(i)
		pubs = append(pubs, k.Public().(ed25519.PublicKey))
		sigs = append(sigs, EdSignature{uint8(i), SignEd25519(k, c, raw)})
	}
	// the gno test's validator 0 key and signature, pinned there
	if hx(pubs[0]) != "ec4ff84bc5faf45e487f9026f51b72df9f75de612614948c9bb8ed21f7d5d593" ||
		hx(sigs[0].Sig[:]) != "36e2d383e1581758530598b0969e2272230aa7a87c51c7ff3c71ff60ed5be5a59fe6ee091a971831bff565b728f04aa85b9eb4311c82b54c5da8c36e053e9f0b" {
		t.Fatalf("validator 0 drifted from the gno test's pins")
	}
	if err := VerifyGno(pubs, 2, GnoMetadata(c, []EdSignature{sigs[0], sigs[2]}), raw); err != nil {
		t.Fatal(err)
	}
	if VerifyGno(pubs, 2, GnoMetadata(c, []EdSignature{sigs[2], sigs[0]}), raw) == nil {
		t.Fatal("out of order verified")
	}
	if VerifyGno(pubs, 2, GnoMetadata(c, []EdSignature{sigs[0]}), raw) == nil {
		t.Fatal("one of two verified")
	}
}
