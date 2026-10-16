package main

import (
	"crypto/ed25519"
	"crypto/sha256"
	"fmt"
	"math/big"
)

// Audit round 3's families (the round's Ed25519 report, E3-ED1 and E3-ED3): forging classes whose only witness was
// one committed vector. Each is written into edges.vec after the named cases, from its own seed, so the named cases
// and the sets drawn after them from edges' stream (binding.vec, gas.vec) come out byte for byte as before. Every
// record's label ends in the verdict the rules predict, which the generator checks Go's against (sourceSet, strict).

// edgeFamilies are the three families, in the order edges.vec carries them.
func edgeFamilies() []record {
	var out []record
	out = append(out, specialPairs(newGen(sha256.Sum256([]byte("eth-f11 ed25519 special pairs v1"))), 6)...)
	out = append(out, negRFamily(newGen(sha256.Sum256([]byte("eth-f11 ed25519 negated R v1"))), 32, 2)...)
	out = append(out, laxSmallR(newGen(sha256.Sum256([]byte("eth-f11 ed25519 lax small R v1"))), 4, 2)...)
	return out
}

// edgeRecord is a record of edges.vec: "edge.<label>.<accept|refuse>", the predicted verdict as its want.
func edgeRecord(pk, sig, msg []byte, label string, accept bool) record {
	want, verdict := "invalid", "refuse"
	if accept {
		want, verdict = "valid", "accept"
	}
	return record{pk: clone(pk), sig: clone(sig), msg: clone(msg), label: "edge." + label + "." + verdict, want: want}
}

// shortMsg is a message of 0-40 bytes (committed vectors: short lines).
func (g *gen) shortMsg() []byte { return g.bytes(g.rng.IntN(41)) }

// specialPairs (E3-ED1): a fast path for a special (R, S) pair would be a universal forgery — R = the identity's
// encoding, S = 0 verifying under every key, honest validators' included, for every message — and the class hung on
// one third-party vector (Wycheproof tc15, under Wycheproof's one key): the other families pair special R and S only
// with the identity or small-order keys, where Go accepts. Here every special R — each of the fourteen encodings of the
// eight points of order dividing 8 (the lax ones too), B, and 32 bytes of 0xff (y >= p: never canonical); 32 zero
// bytes are among the fourteen — beside every special S (0, 1, 2, L - 1, 2^252) under `keys` honest keys, each over its
// own message. Go refuses every one: [S]B - [k]A is the point R names only if [k]A is [S]B minus it, which no k an
// honest key's hash gives reaches.
func specialPairs(g *gen, keys int) []record {
	type named struct {
		name string
		enc  [32]byte
	}
	var rs []named
	seen := map[[32]byte]bool{}
	add := func(name string, e [32]byte) {
		if !seen[e] {
			seen[e] = true
			rs = append(rs, named{name, e})
		}
	}
	for ti, t := range g.tors {
		for ei, e := range laxEncodings(t) {
			add(fmt.Sprintf("T%d.ord%d.enc%d", ti, pointOrder(t), ei), e)
		}
	}
	add("B", basePoint.encode())
	add("zero", [32]byte{})
	var ff [32]byte
	for i := range ff {
		ff[i] = 0xff
	}
	add("ff", ff)
	ss := []named{
		{"S0", le32(big.NewInt(0))},
		{"S1", le32(big.NewInt(1))},
		{"S2", le32(big.NewInt(2))},
		{"SLminus1", le32(new(big.Int).Sub(orderL, bigOne))},
		{"S2pow252", le32(new(big.Int).Lsh(bigOne, 252))},
	}
	var out []record
	for n := 0; n < keys; n++ {
		pk := []byte(g.key().Public().(ed25519.PublicKey))
		for _, r := range rs {
			for _, s := range ss {
				sig := append(clone(r.enc[:]), s.enc[:]...)
				out = append(out, edgeRecord(pk, sig, g.shortMsg(), fmt.Sprintf("special.key%d.R_%s.%s", n, r.name, s.name), false))
			}
		}
	}
	return out
}

// negRFamily (E3-ED3): the comparator's sign rule. Under a key whose secret a is known, R = enc([r]B) and
// S = k a - r (mod L), k = H(R || A || M), make [S]B - [k]A = -[r]B: the point with R's y and the other x, whose
// canonical encoding is R with its sign bit flipped — so only a comparator that takes either sign of a nonzero x
// accepts. (otherR flips the sign bit of an honest R, which changes k: its R' is unrelated, and the class rested on
// Wycheproof tc59.) `prime` prime-order keys A = [a]B; and `mixed` keys A = [a]B + T for each of the seven torsion
// points T, the message ground so that ord(T) divides k (then -[k]T vanishes and R' is -R again). Go refuses all.
func negRFamily(g *gen, prime, mixed int) []record {
	var out []record
	one := func(a *big.Int, ae [32]byte, label string, order int) {
		r := g.scalar(orderL)
		re := baseMul(r).encode()
		m := g.shortMsg()
		if order > 1 {
			m = g.grindShort(re[:], ae[:], func(k *big.Int) bool { return modEq(k, 0, order) })
		}
		k := challenge(re[:], ae[:], m)
		s := new(big.Int).Mod(new(big.Int).Sub(new(big.Int).Mul(k, a), r), orderL)
		out = append(out, edgeRecord(ae[:], sigOf(re, s), m, label, false))
	}
	for i := 0; i < prime; i++ {
		a := g.scalar(orderL)
		one(a, baseMul(a).encode(), fmt.Sprintf("negR.prime.%d", i), 1)
	}
	for ti, t := range g.tors[1:] {
		o := pointOrder(t)
		for i := 0; i < mixed; i++ {
			a := g.scalar(orderL)
			one(a, baseMul(a).add(t).encode(), fmt.Sprintf("negR.mixed.T%d.ord%d.%d", ti+1, o, i), o)
		}
	}
	return out
}

// laxSmallR (E3-ED3): a computed R' of small order in every encoding a lax decoder maps to it — y + p (the identity's
// y = 1 and an order-4 point's y = 0 are the only y below 19, the only ones whose y + p fits 255 bits) and the sign
// bit of a zero x — beside its canonical one: the canonical one accepted, every other refused (a comparator reducing
// R's y mod p, or ignoring the sign of a zero x, accepts one). Under `prime` prime-order keys, S = k a makes R' the
// identity; under `mixed` keys A = [a]B + T for each T of order 4, the message ground so that k = j mod 4 and S = k a,
// R' = -[j]T: the identity (j = 0), an order-4 point (j = 1, 3) or the order-2 point (j = 2).
func laxSmallR(g *gen, prime, mixed int) []record {
	var out []record
	idEnc := laxEncodings(identity())
	for n := 0; n < prime; n++ {
		a := g.scalar(orderL)
		ae := baseMul(a).encode()
		for ri, renc := range idEnc {
			m := g.shortMsg()
			k := challenge(renc[:], ae[:], m)
			s := new(big.Int).Mod(new(big.Int).Mul(k, a), orderL)
			out = append(out, edgeRecord(ae[:], sigOf(renc, s), m, fmt.Sprintf("laxR.prime.%d.identity.enc%d", n, ri), ri == 0))
		}
	}
	for ti, t := range g.tors {
		if pointOrder(t) != 4 {
			continue
		}
		for n := 0; n < mixed; n++ {
			a := g.scalar(orderL)
			ae := baseMul(a).add(t).encode()
			for j := 0; j < 4; j++ {
				rp := t.mul(big.NewInt(int64(j))).neg()
				for ri, renc := range laxEncodings(rp) {
					m := g.grindShort(renc[:], ae[:], func(k *big.Int) bool { return modEq(k, j, 4) })
					k := challenge(renc[:], ae[:], m)
					s := new(big.Int).Mod(new(big.Int).Mul(k, a), orderL)
					label := fmt.Sprintf("laxR.mixed.T%d.%d.j%d.ord%d.enc%d", ti, n, j, pointOrder(rp), ri)
					out = append(out, edgeRecord(ae[:], sigOf(renc, s), m, label, ri == 0))
				}
			}
		}
	}
	return out
}
