// Package hl is the Go twin of gno.land/p/…/hyperlane/v0: Hyperlane's message, merkle tree, checkpoint digest and
// token message, byte for byte, plus what only the off-chain side does — the signers' ed25519 checkpoint
// signatures for the gno mailbox (nothing signs on Solana: the light client checks gno.land's validators there).
//
// The two packages are checked against the same Hyperlane vectors (hl_test.go here, hyperlane_test.gno there), and
// this package writes the signed messages the gno realm tests replay (gen_test.go).
package hl

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/binary"
	"errors"

	"github.com/btcsuite/btcd/btcutil/bech32"
	"golang.org/x/crypto/sha3"
)

func sha256Sum(b []byte) [32]byte { return sha256.Sum256(b) }

const (
	Version   = 3
	HeaderLen = 77
	TreeDepth = 32
	MaxLeaves = 1<<TreeDepth - 1

	sigLen  = 65
	metaLen = 68

	// DomainGnoMainnet and DomainGnoTest are gno.land's domain ids ("GnoL", "GnoT"); hlmailbox gives itself the
	// first on gnoland-1 and the second on every other chain.
	DomainGnoMainnet = uint32(1198419788)
	DomainGnoTest    = uint32(1198419796)
	// Solana's, from the Hyperlane registry.
	DomainSolanaMainnet = uint32(1399811149)
	DomainSolanaTestnet = uint32(1399811150)
	DomainSolanaDevnet  = uint32(1399811151)
)

func Keccak(parts ...[]byte) [32]byte {
	h := sha3.NewLegacyKeccak256()
	for _, p := range parts {
		h.Write(p)
	}
	var out [32]byte
	h.Sum(out[:0])
	return out
}

// Message is a Hyperlane message.
type Message struct {
	Version     uint8
	Nonce       uint32
	Origin      uint32
	Sender      [32]byte
	Destination uint32
	Recipient   [32]byte
	Body        []byte
}

func (m Message) Bytes() []byte {
	b := make([]byte, 0, HeaderLen+len(m.Body))
	b = append(b, m.Version)
	b = binary.BigEndian.AppendUint32(b, m.Nonce)
	b = binary.BigEndian.AppendUint32(b, m.Origin)
	b = append(b, m.Sender[:]...)
	b = binary.BigEndian.AppendUint32(b, m.Destination)
	b = append(b, m.Recipient[:]...)
	return append(b, m.Body...)
}

func (m Message) ID() [32]byte { return ID(m.Bytes()) }

func ID(raw []byte) [32]byte { return Keccak(raw) }

func ParseMessage(b []byte) (Message, error) {
	if len(b) < HeaderLen {
		return Message{}, errors.New("hl: message shorter than its header")
	}
	var m Message
	m.Version = b[0]
	m.Nonce = binary.BigEndian.Uint32(b[1:5])
	m.Origin = binary.BigEndian.Uint32(b[5:9])
	copy(m.Sender[:], b[9:41])
	m.Destination = binary.BigEndian.Uint32(b[41:45])
	copy(m.Recipient[:], b[45:77])
	m.Body = append([]byte(nil), b[HeaderLen:]...)
	return m, nil
}

// GnoAddressToBytes32 pads a g1… address's twenty bytes with twelve zero bytes in front.
func GnoAddressToBytes32(a string) ([32]byte, error) {
	var out [32]byte
	hrp, data, err := bech32.Decode(a)
	if err != nil {
		return out, err
	}
	bz, err := bech32.ConvertBits(data, 5, 8, false)
	if err != nil {
		return out, err
	}
	if hrp != "g" || len(bz) != 20 {
		return out, errors.New("hl: not a gno address: " + a)
	}
	copy(out[12:], bz)
	return out, nil
}

// Bytes32ToGnoAddress reads the last twenty bytes as a g1… address; the first twelve must be zero (§13:
// Bytes32ToAddress requires them so), else the bytes are no gno address — a raw Solana pubkey, which the realm
// sweeps — and the error says so.
func Bytes32ToGnoAddress(b [32]byte) (string, error) {
	for _, c := range b[:12] {
		if c != 0 {
			return "", errors.New("hl: not a gno address: the top twelve bytes are not zero")
		}
	}
	return bytes20ToGnoAddress(b), nil
}

// bytes20ToGnoAddress encodes the last twenty bytes as a g1… address, whatever the top twelve are.
func bytes20ToGnoAddress(b [32]byte) string {
	data, err := bech32.ConvertBits(b[12:], 8, 5, true)
	if err != nil {
		panic(err)
	}
	s, err := bech32.Encode("g", data)
	if err != nil {
		panic(err)
	}
	return s
}

// GnoPackageAddress is the address gno.land gives a package path: the first twenty bytes of
// sha256("pkgPath:" + path), as a g1… address (chain.PackageAddress).
func GnoPackageAddress(pkgPath string) string {
	var b [32]byte
	sum := sha256Sum([]byte("pkgPath:" + pkgPath))
	copy(b[12:], sum[:20])
	return bytes20ToGnoAddress(b)
}

// Tree is Hyperlane's incremental merkle tree.
type Tree struct {
	Branch [TreeDepth][32]byte
	Count  uint32
}

var zeroes = func() (z [TreeDepth][32]byte) {
	for i := 1; i < TreeDepth; i++ {
		z[i] = Keccak(z[i-1][:], z[i-1][:])
	}
	return z
}()

func (t *Tree) Insert(leaf [32]byte) {
	if t.Count >= MaxLeaves {
		panic("hl: merkle tree full")
	}
	t.Count++
	size, node := t.Count, leaf
	for i := 0; i < TreeDepth; i++ {
		if size&1 == 1 {
			t.Branch[i] = node
			return
		}
		node = Keccak(t.Branch[i][:], node[:])
		size >>= 1
	}
	panic("hl: merkle insert fell through")
}

func (t *Tree) Root() [32]byte {
	var cur [32]byte
	for i := 0; i < TreeDepth; i++ {
		if (t.Count>>uint(i))&1 == 1 {
			cur = Keccak(t.Branch[i][:], cur[:])
		} else {
			cur = Keccak(cur[:], zeroes[i][:])
		}
	}
	return cur
}

func DomainHash(origin uint32, hook [32]byte) [32]byte {
	var o [4]byte
	binary.BigEndian.PutUint32(o[:], origin)
	return Keccak(o[:], hook[:], []byte("HYPERLANE"))
}

// CheckpointDigest is what every signer signs for one message (Hyperlane's digest, kept so the gno verifier and
// its Go twin agree).
func CheckpointDigest(origin uint32, hook, root [32]byte, index uint32, messageID [32]byte) [32]byte {
	d := DomainHash(origin, hook)
	var i [4]byte
	binary.BigEndian.PutUint32(i[:], index)
	return EthSignedMessageHash(Keccak(d[:], root[:], i[:], messageID[:]))
}

func EthSignedMessageHash(h [32]byte) [32]byte {
	return Keccak([]byte("\x19Ethereum Signed Message:\n32"), h[:])
}

func TokenMessage(recipient [32]byte, amount uint64, metadata []byte) []byte {
	b := make([]byte, 0, 64+len(metadata))
	b = append(b, recipient[:]...)
	b = append(b, make([]byte, 24)...)
	b = binary.BigEndian.AppendUint64(b, amount)
	return append(b, metadata...)
}

func ParseTokenMessage(b []byte) (recipient [32]byte, amount uint64, metadata []byte, err error) {
	if len(b) < 64 {
		return recipient, 0, nil, errors.New("hl: token message shorter than 64 bytes")
	}
	for _, c := range b[32:56] {
		if c != 0 {
			return recipient, 0, nil, errors.New("hl: token amount past 64 bits")
		}
	}
	copy(recipient[:], b[:32])
	return recipient, binary.BigEndian.Uint64(b[56:64]), append([]byte(nil), b[64:]...), nil
}

// Checkpoint is the head of multisig metadata.
type Checkpoint struct {
	Hook  [32]byte
	Root  [32]byte
	Index uint32
}

func (c Checkpoint) head() []byte {
	b := make([]byte, 0, metaLen)
	b = append(b, c.Hook[:]...)
	b = append(b, c.Root[:]...)
	return binary.BigEndian.AppendUint32(b, c.Index)
}

// EdSignature is one validator's ed25519 signature for the gno mailbox, and its index in the gno validator set.
type EdSignature struct {
	Validator uint8
	Sig       [64]byte
}

// GnoMetadata is metadata for hlmailbox.Process: the checkpoint, then validator byte ‖ ed25519 signature each,
// validators in increasing order.
func GnoMetadata(c Checkpoint, sigs []EdSignature) []byte {
	b := c.head()
	for _, s := range sigs {
		b = append(b, s.Validator)
		b = append(b, s.Sig[:]...)
	}
	return b
}

// SignEd25519 is a validator's signature on the checkpoint of message raw, for the gno mailbox.
func SignEd25519(key ed25519.PrivateKey, c Checkpoint, raw []byte) [64]byte {
	m, err := ParseMessage(raw)
	if err != nil {
		panic(err)
	}
	d := CheckpointDigest(m.Origin, c.Hook, c.Root, c.Index, ID(raw))
	var out [64]byte
	copy(out[:], ed25519.Sign(key, d[:]))
	return out
}

// VerifyGno is hlmailbox's check, run off chain: a relayer runs it before paying for a transaction that would fail.
func VerifyGno(validators []ed25519.PublicKey, threshold int, metadata, raw []byte) error {
	if threshold < 1 || threshold > len(validators) {
		return errors.New("hl: no usable validator set")
	}
	m, err := ParseMessage(raw)
	if err != nil {
		return err
	}
	if len(metadata) < metaLen || (len(metadata)-metaLen)%sigLen != 0 {
		return errors.New("hl: metadata is not 68 bytes and whole 65-byte signatures")
	}
	var c Checkpoint
	copy(c.Hook[:], metadata[0:32])
	copy(c.Root[:], metadata[32:64])
	c.Index = binary.BigEndian.Uint32(metadata[64:68])
	n := (len(metadata) - metaLen) / sigLen
	if n < threshold {
		return errors.New("hl: fewer signatures than the threshold")
	}
	d := CheckpointDigest(m.Origin, c.Hook, c.Root, c.Index, ID(raw))
	last := -1
	for i := 0; i < threshold; i++ {
		o := metaLen + sigLen*i
		v := int(metadata[o])
		if v <= last || v >= len(validators) {
			return errors.New("hl: signatures out of validator order, or naming no validator")
		}
		if !ed25519.Verify(validators[v], d[:], metadata[o+1:o+sigLen]) {
			return errors.New("hl: a signature does not verify")
		}
		last = v
	}
	return nil
}
