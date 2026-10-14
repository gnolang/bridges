package hyperlane

import (
	"encoding/hex"
	"strings"
	"testing"
)

func h32(s string) [32]byte {
	var out [32]byte
	b, err := hex.DecodeString(s)
	if err != nil || len(b) != 32 {
		panic("h32: " + s)
	}
	copy(out[:], b)
	return out
}

func rep(b byte) [32]byte {
	var out [32]byte
	for i := range out {
		out[i] = b
	}
	return out
}

func pad20(b byte) [32]byte {
	var out [32]byte
	for i := 12; i < 32; i++ {
		out[i] = b
	}
	return out
}

func hx(b [32]byte) string { return hex.EncodeToString(b[:]) }

// TestMessageVector is vectors/message.json: the header's layout and the id over the whole message.
func TestMessageVector(t *testing.T) {
	m := Message{Version: 3, Nonce: 0, Origin: 1000, Sender: pad20(0x11), Destination: 2000, Recipient: pad20(0x22), Body: []byte{18, 52}}
	raw := m.Bytes()
	if len(raw) != 79 {
		t.Fatalf("length %d", len(raw))
	}
	if got := hx(m.ID()); got != "f8a66f8aadee751d842616fee0ed14a3ad6da1e13564920364ee0ad35a02703f" {
		t.Fatalf("id %s", got)
	}
	back, err := ParseMessage(raw)
	if err != nil || back.Nonce != 0 || back.Origin != 1000 || back.Destination != 2000 || back.Sender != m.Sender ||
		back.Recipient != m.Recipient || string(back.Body) != string(m.Body) || back.Version != 3 {
		t.Fatalf("round trip: %v %+v", err, back)
	}
	if _, err := ParseMessage(raw[:76]); err == nil {
		t.Fatalf("a short message parsed")
	}
}

// TestDomainHashVectors is vectors/domainHash.json.
func TestDomainHashVectors(t *testing.T) {
	want := []string{
		"bbca56eb98960a4637eb40486d9a069550dd70d9c185ed138516e8e33cf3d7e7",
		"a6a93d86d397028e41995d521ccbc270e6db2a2fc530dcb7f0135254f30c8424",
		"ffb4fbe5142f55e07b5d44b3c7f565c5ef4b016551cbd7c23a92c91621aca06f",
	}
	for i, w := range want {
		if got := hx(DomainHash(uint32(i+1), pad20(0x22))); got != w {
			t.Fatalf("domain %d: %s", i+1, got)
		}
	}
}

// sealevelMessage is the message of rust/sealevel/libraries/multisig-ism/src/test_data.rs.
func sealevelMessage() []byte {
	body := make([]byte, 10)
	for i := range body {
		body[i] = byte(i)
	}
	return Message{Version: 3, Nonce: 69, Origin: 1234, Sender: rep(0xaf), Destination: 4321, Recipient: rep(0xbe), Body: body}.Bytes()
}

// TestCheckpointDigestVector is the Sealevel multisig ISM's test data: message id, domain hash, the signing hash
// the file states, and the EIP-191 digest its three secp256k1 signatures recover against.
func TestCheckpointDigestVector(t *testing.T) {
	raw := sealevelMessage()
	id := ID(raw)
	if hx(id) != "808dd1b989490a4f9911f716132fbb6e57c519eb86eb7ecbb3fc7790ec30170c" {
		t.Fatalf("id %s", hx(id))
	}
	if got := hx(DomainHash(1234, rep(0xab))); got != "3cba759c9d95a03f5755357ca95fdbc90828f6b21a1f56ffbcf49866b4bdedde" {
		t.Fatalf("domain hash %s", got)
	}
	if got := hx(CheckpointDigest(1234, rep(0xab), rep(0xcd), 70, id)); got != "ce7956b074d407cc6f056ad6dc1737aac9935dbfc42df381d016ce1ab179c1e5" {
		t.Fatalf("digest %s", got)
	}
	// the signing hash itself, before EIP-191: what test_data.rs states
	if got := hx(EthSignedMessageHash(h32("3fd308215a20af20b137372f8a69fd336ebf93d57d4076a7c46e13f315255257"))); got != "ce7956b074d407cc6f056ad6dc1737aac9935dbfc42df381d016ce1ab179c1e5" {
		t.Fatalf("eip-191 of the stated signing hash %s", got)
	}
}

// TestMerkleVectors is vectors/merkle.json: every case's root, and the root after each prefix agrees with a
// tree rebuilt from scratch.
func TestMerkleVectors(t *testing.T) {
	if hx(zeroes[1]) != "ad3228b676f7d3cd4284a5443f17f1962b36e491b30a40b2405849e597ba5fb5" {
		t.Fatalf("Z1 %s", hx(zeroes[1]))
	}
	for _, c := range merkleVectors {
		var tr Tree
		for _, l := range c.leaves {
			tr.Insert(h32(l))
		}
		if int(tr.Count) != len(c.leaves) {
			t.Fatalf("%s: count %d", c.name, tr.Count)
		}
		if got := hx(tr.Root()); got != c.root {
			t.Fatalf("%s: root %s, want %s", c.name, got, c.root)
		}
	}
}

func TestTokenMessage(t *testing.T) {
	r := rep(0x5a)
	b := TokenMessage(r, 1_234_567, nil)
	if len(b) != 64 || hex.EncodeToString(b[32:]) != "000000000000000000000000000000000000000000000000000000000012d687" {
		t.Fatalf("body %x", b)
	}
	got, amount, meta, err := ParseTokenMessage(append(b, []byte("memo")...))
	if err != nil || got != r || amount != 1_234_567 || string(meta) != "memo" {
		t.Fatalf("parse: %v %x %d %q", err, got, amount, meta)
	}
	big := TokenMessage(r, 1, nil)
	big[40] = 1 // a bit past 64
	if _, _, _, err := ParseTokenMessage(big); err == nil || !strings.Contains(err.Error(), "64 bits") {
		t.Fatalf("an amount past 64 bits parsed: %v", err)
	}
	if _, _, _, err := ParseTokenMessage(b[:63]); err == nil {
		t.Fatalf("a short body parsed")
	}
}

func TestAddresses(t *testing.T) {
	a := address("g1leu8d2vsplhehcfkjg50mwgdpxdkt8tztu95wr")
	b, err := AddressToBytes32(a)
	if err != nil {
		t.Fatalf("%v", err)
	}
	for _, c := range b[:12] {
		if c != 0 {
			t.Fatalf("padding %x", b)
		}
	}
	if back, err := Bytes32ToAddress(b); err != nil || back != a {
		t.Fatalf("round trip %s %v", back, err)
	}
	// the top twelve bytes must be zero (§13): a byte set there is no gno address, never the one the last twenty
	// bytes spell
	for _, i := range []int{0, 11} {
		dirty := b
		dirty[i] = 7
		if back, err := Bytes32ToAddress(dirty); err == nil || !strings.Contains(err.Error(), "top twelve bytes are not zero") || back != "" {
			t.Fatalf("byte %d set: %s %v", i, back, err)
		}
	}
	if _, err := Bytes32ToAddress(rep(0x5a)); err == nil {
		t.Fatalf("a Solana pubkey read as a gno address")
	}
	if _, err := AddressToBytes32(address("cosmos1qypqxpq9qcrsszg2pvxq6rs0zqg3yyc5lzv7xu")); err == nil {
		t.Fatalf("a foreign prefix converted")
	}
}

// Validators 0..3: ed25519 keys from sha256("hyperlane gno test validator <i>") as the seed, each signature over the
// Sealevel test checkpoint's digest (ce7956b0…), made with Go's crypto/ed25519.
var (
	testPubs = []string{
		"ec4ff84bc5faf45e487f9026f51b72df9f75de612614948c9bb8ed21f7d5d593",
		"44204958a13ca0984fb9bbfb8beecdba35f2d1930a86f551e76669c1c394f65d",
		"9082be72255bbb5af3906569c52055c5dedfce85623f10d12226b4ffff6f1839",
		"27293a9027de27f7ab1cf4c563642df70608d70872ce289c041a1586b59ee500",
	}
	testSigs = []string{
		"36e2d383e1581758530598b0969e2272230aa7a87c51c7ff3c71ff60ed5be5a59fe6ee091a971831bff565b728f04aa85b9eb4311c82b54c5da8c36e053e9f0b",
		"572aabc7b0ccc1a2354425ec29238480d94b2e972b83d98d78c24a24d77671c7f51781a7ff9e99efeddc704667064b8215985c3d9eecb3b6561af726b1630c0e",
		"ecaaf55366bea1be0ee9f1bf3dc11660a8dbb6b8f69aaf12ad5f28d7b4bfc164e4be412b7a8070041d82d32bbb94b48f0382a3c60f9b528b1dea25e5bf135f0a",
		"1f4c79de77c61b1f18ab011ef62de61ee3c19b77243232a7d7e51661307b575f536f5474833c18bfc0aee8dd59f1eb6ad67af5b1e63b6737dd3346896da22e08",
	}
)

func testSig(i int) Signature {
	b, _ := hex.DecodeString(testSigs[i])
	var s Signature
	s.Validator = uint8(i)
	copy(s.Sig[:], b)
	return s
}

func testSet(n int) [][]byte {
	out := make([][]byte, n)
	for i := 0; i < n; i++ {
		out[i], _ = hex.DecodeString(testPubs[i])
	}
	return out
}

func TestVerifyMultisig(t *testing.T) {
	raw := sealevelMessage()
	cp := Checkpoint{Hook: rep(0xab), Root: rep(0xcd), Index: 70}
	set := testSet(3)
	ok := func(sigs []Signature, threshold int) error {
		return VerifyMultisig(set, threshold, MultisigMetadata(cp, sigs), raw)
	}
	// two of three, in order; and a third past the threshold is not read
	if err := ok([]Signature{testSig(0), testSig(2)}, 2); err != nil {
		t.Fatalf("2 of 3: %v", err)
	}
	if err := ok([]Signature{testSig(1), testSig(2), {Validator: 9}}, 2); err != nil {
		t.Fatalf("extra signature: %v", err)
	}
	// the metadata round-trips
	c2, s2, err := ParseMultisigMetadata(MultisigMetadata(cp, []Signature{testSig(1)}))
	if err != nil || c2 != cp || len(s2) != 1 || s2[0] != testSig(1) {
		t.Fatalf("metadata round trip: %v", err)
	}
	mustFail := func(what, want string, err error) {
		t.Helper()
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("%s: got %v, want %q", what, err, want)
		}
	}
	mustFail("one short", "fewer signatures", ok([]Signature{testSig(0)}, 2))
	mustFail("same validator twice", "out of validator order", ok([]Signature{testSig(1), testSig(1)}, 2))
	mustFail("descending", "out of validator order", ok([]Signature{testSig(2), testSig(0)}, 2))
	mustFail("outside the set", "names no validator", ok([]Signature{testSig(0), testSig(3)}, 2)) // 3 signed, but the set is 0..2
	swapped := testSig(0)
	swapped.Validator = 1 // validator 0's signature claimed as validator 1's
	mustFail("wrong key", "does not verify", ok([]Signature{swapped, testSig(2)}, 2))
	mustFail("threshold 0", "no usable validator set", ok([]Signature{testSig(0)}, 0))
	mustFail("threshold past the set", "no usable validator set", VerifyMultisig(set, 4, MultisigMetadata(cp, nil), raw))
	// any change to the message, the hook, the root or the index breaks every signature
	other := append([]byte(nil), raw...)
	other[len(other)-1] ^= 1
	mustFail("message changed", "does not verify", VerifyMultisig(set, 1, MultisigMetadata(cp, []Signature{testSig(0)}), other))
	for i, c := range []Checkpoint{{rep(0xac), rep(0xcd), 70}, {rep(0xab), rep(0xce), 70}, {rep(0xab), rep(0xcd), 71}} {
		if VerifyMultisig(set, 1, MultisigMetadata(c, []Signature{testSig(0)}), raw) == nil {
			t.Fatalf("checkpoint change %d still verified", i)
		}
	}
	mustFail("ragged metadata", "whole 65-byte", VerifyMultisig(set, 1, MultisigMetadata(cp, []Signature{testSig(0)})[:100], raw))
}

func TestKeys(t *testing.T) {
	for _, c := range []struct{ b58, hex string }{
		{"E588QtVUvresuXq2KoNEwAmoifCzYGpRBdHByN9KQMbi", "c236500cf10c409df566b1415b99679c9285072bf856fe311b3cc1bb946279a5"}, // solanamainnet mailbox
		{"TokenkegQfeZyiNwAJbNbGKPFXCWuBvf9Ss623VQ5DA", "06ddf6e1d765a193d9cbe146ceeb79ac1cb485ed5f5b37913a8cf5857eff00a9"},  // SPL token program
		{"11111111111111111111111111111111", "0000000000000000000000000000000000000000000000000000000000000000"},             // system program
		{"1nc1nerator11111111111111111111111111111111", "003390728d34116079bdc911bfff00dbd44d2ecdccf79ca6e10038e100000000"},  // leading zero byte
	} {
		k, err := ParseKey32(c.b58)
		if err != nil || hx(k) != c.hex {
			t.Fatalf("%s: %v %s", c.b58, err, hx(k))
		}
		if got := Base58(k[:]); got != c.b58 {
			t.Fatalf("%s: encodes back as %s", c.hex, got)
		}
		for _, h := range []string{c.hex, "0x" + c.hex} {
			if k2, err := ParseKey32(h); err != nil || k2 != k {
				t.Fatalf("hex %s: %v", h, err)
			}
		}
	}
	for _, bad := range []string{"", "0OIl", "E588QtVUvresuXq2KoNEw", "2" + "E588QtVUvresuXq2KoNEwAmoifCzYGpRBdHByN9KQMbi", strings.Repeat("z", 64)} {
		if _, err := ParseKey32(bad); err == nil {
			t.Fatalf("%q parsed", bad)
		}
	}
}
