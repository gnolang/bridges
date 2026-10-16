package main

import (
	"bytes"
	"crypto/sha512"
	"fmt"
	"math/big"
)

// The edwards25519 group of RFC 8032 §5.1 in affine coordinates over math/big: a second, independent reading of the
// rules Go's crypto/ed25519.Verify follows (eth/bridge/PLAN.md §2, §6.6). It shares no code and no formula with Go's
// implementation or with Ed25519.sol: affine addition with one inversion per step, a square root by Atkin's
// exponent (p+3)/8 rather than Go's SqrtRatio. The generator checks this model's verdict against crypto/ed25519 on
// every record it writes; crypto/ed25519 is the authority, the model a witness that the rules are understood and
// the source of the crafted points (small-order keys and R, mixed-order keys, edge values of S).

var (
	bigZero = big.NewInt(0)
	bigOne  = big.NewInt(1)

	// fieldP is 2^255 - 19.
	fieldP = new(big.Int).Sub(new(big.Int).Lsh(bigOne, 255), big.NewInt(19))
	// orderL is the prime order of the base point, 2^252 + 27742317777372353535851937790883648493.
	orderL = mustBig("7237005577332262213973186563042994240857116359379907606001950938285454250989")
	// curveD is -121665/121666 mod p.
	curveD = fmul(new(big.Int).Sub(fieldP, big.NewInt(121665)), finv(big.NewInt(121666)))
	// sqrtM1 is 2^((p-1)/4) mod p, a square root of -1.
	sqrtM1 = new(big.Int).Exp(big.NewInt(2), new(big.Int).Rsh(new(big.Int).Sub(fieldP, bigOne), 2), fieldP)
	// sqrtExp is (p+3)/8: for a square a, a^((p+3)/8) is a root of a or of -a.
	sqrtExp = new(big.Int).Rsh(new(big.Int).Add(fieldP, big.NewInt(3)), 3)

	// basePoint is B: y = 4/5, x the even root (RFC 8032 §5.1).
	basePoint = func() point {
		y := fmul(big.NewInt(4), finv(big.NewInt(5)))
		x, ok := recoverX(y, 0)
		if !ok {
			panic("model: the base point does not decode")
		}
		return point{x, y}
	}()
	// basePowers[i] = [2^i]B, for the fixed-base multiplications the generator does by the thousand.
	basePowers = func() []point {
		out := make([]point, 256)
		out[0] = basePoint
		for i := 1; i < 256; i++ {
			out[i] = out[i-1].add(out[i-1])
		}
		return out
	}()
)

func mustBig(s string) *big.Int {
	v, ok := new(big.Int).SetString(s, 10)
	if !ok {
		panic("model: bad constant " + s)
	}
	return v
}

func fmod(a *big.Int) *big.Int    { return new(big.Int).Mod(a, fieldP) }
func fadd(a, b *big.Int) *big.Int { return fmod(new(big.Int).Add(a, b)) }
func fsub(a, b *big.Int) *big.Int { return fmod(new(big.Int).Sub(a, b)) }
func fmul(a, b *big.Int) *big.Int { return fmod(new(big.Int).Mul(a, b)) }

func finv(a *big.Int) *big.Int {
	r := new(big.Int).ModInverse(fmod(a), fieldP)
	if r == nil {
		panic("model: inverse of zero")
	}
	return r
}

// fsqrt returns a square root of a when a is a square.
func fsqrt(a *big.Int) (*big.Int, bool) {
	a = fmod(a)
	x := new(big.Int).Exp(a, sqrtExp, fieldP)
	if fmul(x, x).Cmp(a) == 0 {
		return x, true
	}
	x = fmul(x, sqrtM1)
	if fmul(x, x).Cmp(a) == 0 {
		return x, true
	}
	return nil, false
}

// point is an affine point (x, y) with both coordinates reduced mod p.
type point struct{ x, y *big.Int }

func identity() point { return point{big.NewInt(0), big.NewInt(1)} }

func (a point) isIdentity() bool { return a.x.Sign() == 0 && a.y.Cmp(bigOne) == 0 }

func (a point) equal(b point) bool { return a.x.Cmp(b.x) == 0 && a.y.Cmp(b.y) == 0 }

// onCurve checks -x^2 + y^2 = 1 + d x^2 y^2.
func (a point) onCurve() bool {
	x2, y2 := fmul(a.x, a.x), fmul(a.y, a.y)
	return fsub(y2, x2).Cmp(fadd(bigOne, fmul(curveD, fmul(x2, y2)))) == 0
}

// add is the twisted Edwards addition law with a = -1, complete on the whole curve because d is not a square:
// x3 = (x1 y2 + y1 x2) / (1 + d x1 x2 y1 y2), y3 = (y1 y2 + x1 x2) / (1 - d x1 x2 y1 y2).
func (a point) add(b point) point {
	x1x2, y1y2 := fmul(a.x, b.x), fmul(a.y, b.y)
	t := fmul(curveD, fmul(x1x2, y1y2))
	x3 := fmul(fadd(fmul(a.x, b.y), fmul(a.y, b.x)), finv(fadd(bigOne, t)))
	y3 := fmul(fadd(y1y2, x1x2), finv(fsub(bigOne, t)))
	return point{x3, y3}
}

func (a point) neg() point { return point{fsub(bigZero, a.x), new(big.Int).Set(a.y)} }

// mul is [k]a by double-and-add, for any k >= 0 (the generator multiplies by L itself to reach the torsion).
func (a point) mul(k *big.Int) point {
	r := identity()
	for i := k.BitLen() - 1; i >= 0; i-- {
		r = r.add(r)
		if k.Bit(i) == 1 {
			r = r.add(a)
		}
	}
	return r
}

// baseMul is [k]B for k >= 0, reduced mod L first (B has order L).
func baseMul(k *big.Int) point {
	k = new(big.Int).Mod(k, orderL)
	r := identity()
	for i := 0; i < k.BitLen(); i++ {
		if k.Bit(i) == 1 {
			r = r.add(basePowers[i])
		}
	}
	return r
}

// encode is RFC 8032 §5.1.2: y in 32 little-endian bytes, the low bit of x in the top bit of the last byte.
func (a point) encode() [32]byte {
	var out [32]byte
	be := a.y.FillBytes(make([]byte, 32))
	for i := 0; i < 32; i++ {
		out[i] = be[31-i]
	}
	if a.x.Bit(0) == 1 {
		out[31] |= 0x80
	}
	return out
}

// leInt reads b as an unsigned little-endian integer.
func leInt(b []byte) *big.Int {
	be := make([]byte, len(b))
	for i := range b {
		be[len(b)-1-i] = b[i]
	}
	return new(big.Int).SetBytes(be)
}

// le32 writes v (< 2^256) as 32 little-endian bytes.
func le32(v *big.Int) [32]byte {
	var out [32]byte
	be := v.FillBytes(make([]byte, 32))
	for i := 0; i < 32; i++ {
		out[i] = be[31-i]
	}
	return out
}

// recoverX returns the x with -x^2 + y^2 = 1 + d x^2 y^2 whose low bit is sign, or x = 0 whatever the sign when 0 is
// the only root, and false when no x exists.
func recoverX(y *big.Int, sign uint) (*big.Int, bool) {
	y2 := fmul(y, y)
	u := fsub(y2, bigOne)
	v := fadd(fmul(curveD, y2), bigOne)
	x, ok := fsqrt(fmul(u, finv(v)))
	if !ok {
		return nil, false
	}
	if x.Bit(0) == 1 {
		x = fsub(bigZero, x)
	}
	if sign == 1 {
		x = fsub(bigZero, x)
	}
	return x, true
}

// decodeLax is the decoding Go's edwards25519.Point.SetBytes performs: the top bit of the last byte is the sign of
// x and is ignored for y; y is the other 255 bits reduced mod p (so y >= p is accepted); the sign of a zero x is
// ignored; a y with no x is refused.
func decodeLax(b []byte) (point, bool) {
	if len(b) != 32 {
		return point{}, false
	}
	yb := make([]byte, 32)
	copy(yb, b)
	sign := uint(yb[31] >> 7)
	yb[31] &= 0x7f
	y := fmod(leInt(yb))
	x, ok := recoverX(y, sign)
	if !ok {
		return point{}, false
	}
	return point{x, y}, true
}

// challenge is k = SHA-512(R || A || M) as a 64-byte little-endian integer, reduced mod L.
func challenge(r, pk, msg []byte) *big.Int {
	h := sha512.New()
	h.Write(r)
	h.Write(pk)
	h.Write(msg)
	return new(big.Int).Mod(leInt(h.Sum(nil)), orderL)
}

// modelVerify is PLAN.md §6.6 read literally: the signature is 64 bytes; the top three bits of its last byte are
// clear and S < L; A decodes laxly from the key's own 32 bytes; k is hashed over the key's bytes as given; the
// canonical encoding of [S]B - [k]A, computed in the full group (cofactorless), equals R's 32 bytes.
func modelVerify(pk, msg, sig []byte) bool {
	if len(pk) != 32 || len(sig) != 64 || sig[63]&0xe0 != 0 {
		return false
	}
	a, ok := decodeLax(pk)
	if !ok {
		return false
	}
	s := leInt(sig[32:])
	if s.Cmp(orderL) >= 0 {
		return false
	}
	k := challenge(sig[:32], pk, msg)
	r := baseMul(s).add(a.neg().mul(k))
	enc := r.encode()
	return bytes.Equal(enc[:], sig[:32])
}

// torsion returns the eight points of order dividing 8, [i]T8 for i = 0..7, T8 a point of order exactly 8 reached
// as [L]Q for the first decodable y = 2, 3, ... whose [L]Q has order 8.
func torsion() []point {
	for y := int64(2); ; y++ {
		x, ok := recoverX(big.NewInt(y), 0)
		if !ok {
			continue
		}
		t8 := point{x, big.NewInt(y)}.mul(orderL)
		if t8.mul(big.NewInt(4)).isIdentity() {
			continue
		}
		out := make([]point, 8)
		out[0] = identity()
		for i := 1; i < 8; i++ {
			out[i] = out[i-1].add(t8)
		}
		if !out[7].add(t8).isIdentity() {
			panic("model: the torsion point is not of order 8")
		}
		return out
	}
}

// pointOrder is the order of a point of the torsion subgroup: 1, 2, 4 or 8.
func pointOrder(t point) int {
	q := t
	for n := 1; n <= 8; n *= 2 {
		if q.isIdentity() {
			return n
		}
		q = q.add(q)
	}
	panic("model: not a torsion point")
}

// laxEncodings lists every 32-byte string that decodeLax maps to the point: y and, when it stays below 2^255,
// y + p; the sign bit of x's parity, and both signs when x = 0.
func laxEncodings(a point) [][32]byte {
	ys := []*big.Int{a.y}
	if yp := new(big.Int).Add(a.y, fieldP); yp.BitLen() <= 255 {
		ys = append(ys, yp)
	}
	signs := []uint{a.x.Bit(0)}
	if a.x.Sign() == 0 {
		signs = []uint{0, 1}
	}
	var out [][32]byte
	for _, y := range ys {
		for _, s := range signs {
			e := le32(y)
			if s == 1 {
				e[31] |= 0x80
			}
			got, ok := decodeLax(e[:])
			if !ok || !got.equal(a) {
				panic(fmt.Sprintf("model: encoding %x does not decode to its point", e))
			}
			out = append(out, e)
		}
	}
	return out
}
