package agent

import (
	"crypto/ed25519"
	"crypto/sha256"
	"math/big"
	"testing"
)

// The small-order keys (the code audit's E2-ED3): the fourteen encodings Go decodes to a point of order dividing 8 —
// libsodium's seven blocked y values, each with either sign bit — and the torsion subgroup derived here from [L]P
// agrees; every other key is not one.

// edEncode is a point's encoding: y little-endian, x's parity in the top bit.
func edEncode(p edPoint) [32]byte {
	be := p.y.FillBytes(make([]byte, 32))
	var k [32]byte
	for i := range be {
		k[31-i] = be[i]
	}
	k[31] |= byte(p.x.Bit(0)) << 7
	return k
}

func edScalar(p edPoint, n *big.Int) edPoint {
	acc := edPoint{big.NewInt(0), big.NewInt(1)}
	for i := n.BitLen() - 1; i >= 0; i-- {
		acc = acc.add(acc)
		if n.Bit(i) == 1 {
			acc = acc.add(p)
		}
	}
	return acc
}

func TestSmallOrderKeys(t *testing.T) {
	small := map[[32]byte]bool{}
	for _, h := range libsodiumSmallOrder {
		k := key32(t, h)
		for _, sign := range []byte{0, 0x80} {
			k[31] = k[31]&0x7f | sign
			if !smallOrderKey(k) {
				t.Errorf("%x decodes to a point of small order and is not taken as one", k)
			}
			small[k] = true
		}
	}
	if len(small) != 14 {
		t.Fatalf("%d encodings, want the fourteen", len(small))
	}
	// the torsion subgroup from first principles: [L]P for a point P of full order is a point of order 8, and its
	// eight multiples are the eight small-order points, whose canonical encodings are all on the list
	L, _ := new(big.Int).SetString("7237005577332262213973186563042994240857116359379907606001950938285454250989", 10)
	var t8 edPoint
	for seed := 0; ; seed++ {
		h := sha256.Sum256([]byte{byte(seed)})
		p, ok := edDecode(h)
		if !ok {
			continue
		}
		t8 = edScalar(p, L)
		if q := edScalar(t8, big.NewInt(4)); !(q.x.Sign() == 0 && q.y.Cmp(big.NewInt(1)) == 0) {
			break // [4]T is not the identity: T has order 8
		}
	}
	for i := int64(0); i < 8; i++ {
		e := edEncode(edScalar(t8, big.NewInt(i)))
		if !small[e] {
			t.Errorf("[%d]T8 encodes as %x, not on the list", i, e)
		}
	}
	// not small: the base point, ordinary keys, a key of mixed order (B + T8), a key that decodes to no point
	base := key32(t, "5866666666666666666666666666666666666666666666666666666666666666")
	if smallOrderKey(base) {
		t.Error("the base point taken as small order")
	}
	for i := 0; i < 20; i++ {
		pub, _, err := ed25519.GenerateKey(nil)
		if err != nil {
			t.Fatal(err)
		}
		var k [32]byte
		copy(k[:], pub)
		if smallOrderKey(k) {
			t.Errorf("an ordinary key %x taken as small order", k)
		}
	}
	bp, _ := edDecode(base)
	if mixed := edEncode(bp.add(t8)); smallOrderKey(mixed) {
		t.Errorf("a mixed-order key %x taken as small order", mixed)
	}
	for y := byte(2); ; y++ {
		var k [32]byte
		k[0] = y
		if _, ok := edDecode(k); ok {
			continue
		}
		if smallOrderKey(k) {
			t.Errorf("a key that decodes to no point (y = %d) taken as small order", y)
		}
		break
	}
}
