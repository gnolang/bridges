package main

import (
	"bufio"
	"crypto/ed25519"
	"crypto/sha256"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strings"

	goed "github.com/gnolang/bridges/ethereum/bridge/tools/ed25519vectors/goedwards25519"
)

// The gates audit round 2 found missing (eth/bridge/audits/r2/ed25519.md, E2-ED1): five planted bugs in the key
// decoding passed every gate, three of them universal forgeries, because no gate paired a key a mis-decoding
// would take with a signature that mis-decoding accepts. Two committed sets close it: probes.vec, the auditor's
// forgery probes against key decoding, through the whole verify; and decode.vec, the decoding itself tested
// directly against Go's edwards25519.Point.SetBytes.

// probeKey is a key the probes try.
type probeKey struct {
	label string
	b     []byte
}

// decodeKeys are the special keys a decoder can get wrong: for d = 0..18 and both sign bits, y = p + d (the lax
// encodings Go reduces), y = d (the canonical small y: the identity at 1, an order-4 point at 0, others large or
// not on the curve) and y = p - 1 - d (the order-2 point at d = 0; the auditor's universal forgery at d = 1, y = p - 2,
// which has no x).
func decodeKeys() []probeKey {
	var keys []probeKey
	for d := int64(0); d <= 18; d++ {
		for s := byte(0); s < 2; s++ {
			for _, f := range []struct {
				name string
				y    *big.Int
			}{
				{fmt.Sprintf("yp%d", d), new(big.Int).Add(fieldP, big.NewInt(d))},
				{fmt.Sprintf("y%d", d), big.NewInt(d)},
				{fmt.Sprintf("yminus%d", d+1), new(big.Int).Sub(fieldP, big.NewInt(d+1))},
			} {
				e := le32(f.y)
				e[31] |= s << 7
				keys = append(keys, probeKey{fmt.Sprintf("%s.s%d", f.name, s), e[:]})
			}
		}
	}
	return keys
}

// probes is the auditor's forgery-probe family (famProbeDecode of the audit's harness) at the repository's scale.
// For a key K, a signature with R = [S]B passes exactly when the verifier decodes K to a point A with [k]A the
// identity, and R = [S]B - [j]T (T of order o, the message ground so that k = j mod o) passes when it decodes K to
// T. Go refuses every probe unless K really is that point: a no-root key decodes to nothing, y >= p to the point of
// y - p, a sign bit to the other root. A verifier that decodes a key as a small-order point when Go does not accepts
// one of them. Keys: decodeKeys, then per round a random string, an off-curve string, an honest key with its sign
// bit flipped and one with y replaced by p - y. Then, under each key, an .asB probe (audit round 3, E3-ED2: below).
func (g *gen) probes(rounds int) {
	keys := decodeKeys()
	for i := 0; i < rounds; i++ {
		keys = append(keys, probeKey{fmt.Sprintf("rand%d", i), g.bytes(32)})
		for {
			b := g.bytes(32)
			if _, ok := decodeLax(b); !ok {
				keys = append(keys, probeKey{fmt.Sprintf("offcurve%d", i), b})
				break
			}
		}
		pk := []byte(g.key().Public().(ed25519.PublicKey))
		keys = append(keys, probeKey{fmt.Sprintf("negA%d", i), flipBit(pk, 255)})
		y := leInt(append(clone(pk[:31]), pk[31]&0x7f))
		e := le32(fmod(new(big.Int).Sub(fieldP, y)))
		keys = append(keys, probeKey{fmt.Sprintf("negY%d", i), e[:]})
	}
	for _, k := range keys {
		s := g.scalar(orderL)
		r := baseMul(s).encode()
		g.add("probe", k.b, sigOf(r, s), g.bytes(g.rng.IntN(41)), k.label+".asIdentity")
		for ti := 1; ti < 8; ti++ {
			t := g.tors[ti]
			o := pointOrder(t)
			j := g.rng.IntN(o)
			rp := baseMul(s).add(t.mul(big.NewInt(int64(j))).neg()).encode()
			m := g.grindShort(rp[:], k.b, func(kk *big.Int) bool { return modEq(kk, j, o) })
			g.add("probe", k.b, sigOf(rp, s), m, fmt.Sprintf("%s.asT%d.ord%d.j%d", k.label, ti, o, j))
		}
	}
	// Audit round 3 (E3-ED2): the probes above aim only at small-order points, and a key read as a known-log point is
	// a universal forgery too — a verifier that takes a key Go refuses as B (in its decoding, or in verify) accepts
	// R = [r]B, S = r + k mod L for every message under that key, and only the direct decode test saw it. Under each key,
	// that pair: Go accepts it only when the key decodes to B (or to B plus a torsion point [k] kills), which no probe
	// key does. Drawn after the probes above, from the same stream, so those come out byte for byte as before.
	for _, k := range keys {
		r := g.scalar(orderL)
		re := baseMul(r).encode()
		m := g.bytes(g.rng.IntN(41))
		s := new(big.Int).Mod(new(big.Int).Add(r, challenge(re[:], k.b, m)), orderL)
		g.add("probe", k.b, sigOf(re, s), m, k.label+".asB")
	}
}

// grindShort is grind over messages of at most 40 bytes (the probes are committed: short lines).
func (g *gen) grindShort(r, pk []byte, want func(k *big.Int) bool) []byte {
	for {
		m := g.bytes(g.rng.IntN(41))
		if want(challenge(r, pk, m)) {
			return m
		}
	}
}

// writeProbes writes probes.vec.
func writeProbes(path string, seed [32]byte) {
	g := newGen(seed)
	g.probes(6)
	sourceSet("probes", path, g.recs, false, []string{
		"forgery probes against key decoding (audit round 2, E2-ED1: the auditor's famProbeDecode): under each key,",
		"R = [S]B (accepted only if the key decodes to a point A with [k]A the identity) and R = [S]B - [j]T for T of",
		"every order 2, 4, 8, the message ground so that k = j mod ord(T) (accepted only if the key decodes to T);",
		"keys y = p + d, y = d, y = p - 1 - d (d = 0..18, both sign bits) and random, off-curve, sign-flipped and",
		"y-negated ones; Go accepts a probe only when the key really is that point; then, under each key, R = [r]B,",
		"S = r + k mod L (.asB, audit round 3's E3-ED2: accepted only if the key decodes to B, a known-log point)"})
}

// writeDecode writes decode.vec: "<0|1> <key> <x> <y> <label>", Go's edwards25519.Point.SetBytes on the key (the
// copy of Go 1.25.9's in goedwards25519: the package is internal to Go), x and y its affine coordinates as 32-byte
// big-endian hex, both zero when it refuses. The math/big model's decodeLax must agree on every line, verdict and
// coordinates. Keys: y = d, p + d, p - 1 - d and 2^255 - 1 - d for d = 0..60 with both sign bits; every encoding of
// the eight points of order dividing 8; honest keys, their sign-flipped and y-negated forms and mixed-order keys;
// random strings.
func writeDecode(path string, seed [32]byte) {
	g := newGen(seed)
	var keys []probeKey
	seen := map[string]bool{}
	add := func(label string, b []byte) {
		if !seen[string(b)] {
			seen[string(b)] = true
			keys = append(keys, probeKey{label, clone(b)})
		}
	}
	top := new(big.Int).Lsh(bigOne, 255)
	for d := int64(0); d <= 60; d++ {
		for s := byte(0); s < 2; s++ {
			for _, f := range []struct {
				name string
				y    *big.Int
			}{
				{"y", big.NewInt(d)},
				{"yp", new(big.Int).Add(fieldP, big.NewInt(d))},
				{"yminus", new(big.Int).Sub(fieldP, big.NewInt(d+1))},
				{"top", new(big.Int).Sub(top, big.NewInt(d+1))},
			} {
				if f.y.BitLen() > 255 {
					continue
				}
				e := le32(f.y)
				e[31] |= s << 7
				add(fmt.Sprintf("%s%d.s%d", f.name, d, s), e[:])
			}
		}
	}
	for ti, t := range g.tors {
		for ei, e := range laxEncodings(t) {
			add(fmt.Sprintf("T%d.ord%d.enc%d", ti, pointOrder(t), ei), e[:])
		}
	}
	for i := 0; i < 24; i++ {
		pk := []byte(g.key().Public().(ed25519.PublicKey))
		add(fmt.Sprintf("honest%d", i), pk)
		add(fmt.Sprintf("honest%d.negA", i), flipBit(pk, 255))
		y := leInt(append(clone(pk[:31]), pk[31]&0x7f))
		e := le32(fmod(new(big.Int).Sub(fieldP, y)))
		add(fmt.Sprintf("honest%d.negY", i), e[:])
		m := baseMul(g.scalar(orderL)).add(g.tors[1+i%7]).encode()
		add(fmt.Sprintf("mixed%d.T%d", i, 1+i%7), m[:])
	}
	for i := 0; i < 96; i++ {
		add(fmt.Sprintf("random%d", i), g.bytes(32))
	}

	f, err := os.Create(path)
	if err != nil {
		fatalf("%v", err)
	}
	w := bufio.NewWriter(f)
	fmt.Fprintf(w, "# %s\n", strings.Join([]string{
		"the direct test of Ed25519.sol's _decode (audit round 2, E2-ED1): \"<0|1> <key> <x> <y> <label>\", Go's",
		"edwards25519.Point.SetBytes on the key (Go 1.25.9's code, tools/ed25519vectors/goedwards25519), x and y the",
		"affine coordinates as 32-byte big-endian hex, zero when Go refuses the key; the math/big model agrees on every line"}, "\n# "))
	decoded := 0
	for _, k := range keys {
		x, y, ok := goSetBytes(k.b)
		mp, mok := decodeLax(k.b)
		if ok != mok || (ok && (mp.x.Cmp(x) != 0 || mp.y.Cmp(y) != 0)) {
			fatalf("decode %s (%x): Go's SetBytes %v (%x, %x), the model %v", k.label, k.b, ok, x, y, mok)
		}
		v := 0
		if ok {
			v = 1
			decoded++
		}
		fmt.Fprintf(w, "%d %s 0x%064x 0x%064x %s\n", v, hex0x(k.b), x, y, k.label)
	}
	fmt.Fprintf(w, "# %d keys: Go decodes %d, refuses %d\n", len(keys), decoded, len(keys)-decoded)
	if err := w.Flush(); err != nil {
		fatalf("%v", err)
	}
	if err := f.Close(); err != nil {
		fatalf("%v", err)
	}
	fmt.Printf("decode: %d keys, Go decodes %d, refuses %d -> %s\n", len(keys), decoded, len(keys)-decoded, path)
}

// goSetBytes is Go's Point.SetBytes on b: the affine coordinates, or false (and zeros) when it refuses.
func goSetBytes(b []byte) (x, y *big.Int, ok bool) {
	p, err := new(goed.Point).SetBytes(b)
	if err != nil {
		return new(big.Int), new(big.Int), false
	}
	xb, yb := p.Coordinates()
	return leInt(xb), leInt(yb), true
}

// writeDecodeAndProbes writes the two sets into dir, each from its own seed (so the other local sets are unchanged).
func writeDecodeAndProbes(dir string) {
	writeProbes(filepath.Join(dir, "probes.vec"), sha256.Sum256([]byte("eth-f4 ed25519 forgery probes v1")))
	writeDecode(filepath.Join(dir, "decode.vec"), sha256.Sum256([]byte("eth-f4 ed25519 decode v1")))
}
