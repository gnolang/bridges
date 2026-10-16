package main

import (
	"crypto/ed25519"
	"fmt"
	"math/big"
	"math/rand/v2"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// gen draws every random choice from one ChaCha8 stream, so a seed fixes the whole set byte for byte.
type gen struct {
	rng  *rand.Rand
	recs []record
	fam  map[string]int
	tors []point // [i]T8, i = 0..7
}

func newGen(seed [32]byte) *gen {
	return &gen{rng: rand.New(rand.NewChaCha8(seed)), fam: map[string]int{}, tors: torsion()}
}

func (g *gen) bytes(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(g.rng.Uint32())
	}
	return b
}

// scalar is a uniform integer in [0, n).
func (g *gen) scalar(n *big.Int) *big.Int {
	v := leInt(g.bytes(64))
	return v.Mod(v, n)
}

func (g *gen) msg() []byte { return g.bytes(g.rng.IntN(301)) }

func (g *gen) add(family string, pk, sig, msg []byte, label string) {
	g.recs = append(g.recs, record{pk: clone(pk), sig: clone(sig), msg: clone(msg), label: family + "." + label})
	g.fam[family]++
}

func clone(b []byte) []byte { return append([]byte{}, b...) }

func flipBit(b []byte, bit int) []byte {
	c := clone(b)
	c[bit/8] ^= 1 << (bit % 8)
	return c
}

func sigOf(r [32]byte, s *big.Int) []byte {
	e := le32(s)
	return append(r[:], e[:]...)
}

func sigBytes(r []byte, s *big.Int) []byte {
	e := le32(s)
	return append(clone(r), e[:]...)
}

// key is a fresh key pair from a random seed, as tm2's GenPrivKey makes one.
func (g *gen) key() ed25519.PrivateKey { return ed25519.NewKeyFromSeed(g.bytes(32)) }

// valid: honest signatures over random messages (half of length 0-300, half of exactly 110 bytes: gnoland-1's
// precommit sign bytes), each followed by four copies with one random bit flipped: in R, in S, in A, in M (in R
// again when M is empty).
func (g *gen) valid(n int) {
	for i := 0; i < n; i++ {
		ln := 110
		if i%2 == 0 {
			ln = g.rng.IntN(301)
		}
		priv := g.key()
		pk := []byte(priv.Public().(ed25519.PublicKey))
		m := g.bytes(ln)
		sig := ed25519.Sign(priv, m)
		id := fmt.Sprintf("%d.len%d", i, ln)
		g.add("valid", pk, sig, m, id)
		b := g.rng.IntN(256)
		g.add("valid", pk, flipBit(sig, b), m, fmt.Sprintf("%s.flipR%d", id, b))
		b = g.rng.IntN(256)
		g.add("valid", pk, flipBit(sig, 256+b), m, fmt.Sprintf("%s.flipS%d", id, b))
		b = g.rng.IntN(256)
		g.add("valid", flipBit(pk, b), sig, m, fmt.Sprintf("%s.flipA%d", id, b))
		if ln > 0 {
			b = g.rng.IntN(8 * ln)
			g.add("valid", pk, sig, flipBit(m, b), fmt.Sprintf("%s.flipM%d", id, b))
		} else {
			b = g.rng.IntN(256)
			g.add("valid", pk, flipBit(sig, b), m, fmt.Sprintf("%s.flipR%d", id, b))
		}
	}
}

// random: random 64-byte signatures under random keys; random R with a canonical S under real keys; random R and a
// canonical S under random 32-byte keys (about half of which are not on the curve).
func (g *gen) random(n int) {
	for i := 0; i < n; i++ {
		switch i % 3 {
		case 0:
			g.add("random", g.bytes(32), g.bytes(64), g.msg(), fmt.Sprintf("%d.all", i))
		case 1:
			pk := []byte(g.key().Public().(ed25519.PublicKey))
			g.add("random", pk, sigBytes(g.bytes(32), g.scalar(orderL)), g.msg(), fmt.Sprintf("%d.realkey", i))
		case 2:
			g.add("random", g.bytes(32), sigBytes(g.bytes(32), g.scalar(orderL)), g.msg(), fmt.Sprintf("%d.anykey", i))
		}
	}
}

// grind draws messages until k = H(R || A || M) satisfies want(k).
func (g *gen) grind(r, pk []byte, want func(k *big.Int) bool) []byte {
	for {
		m := g.msg()
		if want(challenge(r, pk, m)) {
			return m
		}
	}
}

func modEq(k *big.Int, j, n int) bool {
	return new(big.Int).Mod(k, big.NewInt(int64(n))).Int64() == int64(j)
}

// smallKeys: all 14 encodings of the 8 points of order dividing 8 as the key (8 canonical, 6 that only a lax
// decoder accepts: y >= p, or x = 0 with the sign bit set). With A = T small, [k]A depends only on k mod ord(T), so
// a signature R = [S]B - [j]T verifies (cofactorless) exactly when k = j mod ord(T): Go accepts it, and refuses the
// same R and S over a message where k differs. With S = 0 the computed R' is itself a small-order point, so its
// other encodings (non-canonical R) are written too and must be refused.
func (g *gen) smallKeys(per int) {
	for ti, t := range g.tors {
		o := pointOrder(t)
		for ei, enc := range laxEncodings(t) {
			pk := enc[:]
			id := fmt.Sprintf("T%d.ord%d.enc%d", ti, o, ei)
			for n := 0; n < per; n++ {
				s := g.scalar(orderL)
				j := g.rng.IntN(o)
				r := baseMul(s).add(t.mul(big.NewInt(int64(j))).neg()).encode()
				m := g.grind(r[:], pk, func(k *big.Int) bool { return modEq(k, j, o) })
				g.add("smallkey", pk, sigOf(r, s), m, fmt.Sprintf("%s.%d.match", id, n))
				if o > 1 {
					m = g.grind(r[:], pk, func(k *big.Int) bool { return !modEq(k, j, o) })
					g.add("smallkey", pk, sigOf(r, s), m, fmt.Sprintf("%s.%d.nomatch", id, n))
				}
			}
			for n := 0; n < per/4; n++ {
				j := g.rng.IntN(o)
				rp := t.mul(big.NewInt(int64(j))).neg() // R' = [0]B - [k]T with k = j mod o
				for ri, renc := range laxEncodings(rp) {
					m := g.grind(renc[:], pk, func(k *big.Int) bool { return modEq(k, j, o) })
					g.add("smallkey", pk, sigOf(renc, big.NewInt(0)), m, fmt.Sprintf("%s.S0.%d.R%d", id, n, ri))
				}
			}
		}
	}
}

// mixedKeys: A = [a]B + T for every T of order 2, 4 or 8, signed honestly (R = [r]B, S = r + k a): the
// cofactorless check accepts exactly when ord(T) divides k, so Go accepts some and refuses the rest, where a
// cofactored verifier (or one computing [L-k]A instead of -[k]A) answers otherwise.
func (g *gen) mixedKeys(keysPer, sigsPer int) {
	for ti, t := range g.tors[1:] {
		for ki := 0; ki < keysPer; ki++ {
			a := g.scalar(orderL)
			ae := baseMul(a).add(t).encode()
			pk := ae[:]
			accepted := 0
			for n := 0; n < sigsPer || accepted < 3; n++ {
				r := g.scalar(orderL)
				re := baseMul(r).encode()
				m := g.msg()
				k := challenge(re[:], pk, m)
				s := new(big.Int).Mod(new(big.Int).Add(r, new(big.Int).Mul(k, a)), orderL)
				if t.mul(k).isIdentity() {
					accepted++
				} else if n >= sigsPer {
					continue
				}
				g.add("mixedkey", pk, sigOf(re, s), m, fmt.Sprintf("T%d.ord%d.key%d.%d", ti+1, pointOrder(t), ki, n))
			}
		}
	}
}

// smallR: R a small-order point. Under a prime-order key with S = k a, R' is the identity, so only the canonical
// identity encoding is accepted; under a mixed key A = [a]B + T with S = k a, R' = -[k]T, any encoding of the
// torsion point is offered and only the canonical one, at the right k, is accepted.
func (g *gen) smallR(per int) {
	idEnc := laxEncodings(identity())
	for n := 0; n < per; n++ {
		a := g.scalar(orderL)
		ae := baseMul(a).encode()
		for ri, renc := range idEnc {
			m := g.msg()
			k := challenge(renc[:], ae[:], m)
			s := new(big.Int).Mod(new(big.Int).Mul(k, a), orderL)
			g.add("smallR", ae[:], sigOf(renc, s), m, fmt.Sprintf("prime.%d.R%d", n, ri))
		}
	}
	for ti, t := range g.tors[1:] {
		o := pointOrder(t)
		for n := 0; n < per/2; n++ {
			a := g.scalar(orderL)
			ae := baseMul(a).add(t).encode()
			j := g.rng.IntN(o)
			rp := t.mul(big.NewInt(int64(j))).neg()
			for ri, renc := range laxEncodings(rp) {
				m := g.grind(renc[:], ae[:], func(k *big.Int) bool { return modEq(k, j, o) })
				k := challenge(renc[:], ae[:], m)
				s := new(big.Int).Mod(new(big.Int).Mul(k, a), orderL)
				g.add("smallR", ae[:], sigOf(renc, s), m, fmt.Sprintf("mixed.T%d.%d.R%d", ti+1, n, ri))
			}
		}
	}
}

// edgeS: S at and around the bounds. Honest signatures with S + L, S + 2L, S + 8L (the same point, refused for
// S >= L) and with bit 253, 254 or 255 set. Under the identity key (every encoding), R = [S]B verifies for any
// message, so S = 0, 1, L-1 ... are accepted while S = L, L+1, 2^253-1, 2^256-1 ... over the same R are refused.
func (g *gen) edgeS(honest int) {
	for n := 0; n < honest; n++ {
		priv := g.key()
		pk := []byte(priv.Public().(ed25519.PublicKey))
		m := g.msg()
		sig := ed25519.Sign(priv, m)
		s := leInt(sig[32:])
		for _, mult := range []int64{1, 2, 8} {
			s2 := new(big.Int).Add(s, new(big.Int).Mul(orderL, big.NewInt(mult)))
			g.add("edgeS", pk, sigBytes(sig[:32], s2), m, fmt.Sprintf("honest.%d.plus%dL", n, mult))
		}
		for _, bit := range []int{253, 254, 255} {
			s2 := new(big.Int).SetBit(new(big.Int).Set(s), bit, 1)
			g.add("edgeS", pk, sigBytes(sig[:32], s2), m, fmt.Sprintf("honest.%d.bit%d", n, bit))
		}
	}
	two := func(e uint) *big.Int { return new(big.Int).Lsh(bigOne, e) }
	minus := func(a *big.Int, b int64) *big.Int { return new(big.Int).Sub(a, big.NewInt(b)) }
	plus := func(a *big.Int, b int64) *big.Int { return new(big.Int).Add(a, big.NewInt(b)) }
	inRange := []*big.Int{big.NewInt(0), big.NewInt(1), big.NewInt(2), minus(orderL, 1), minus(orderL, 2),
		minus(two(252), 1), two(252), new(big.Int).Sub(orderL, two(128)), g.scalar(orderL), g.scalar(orderL)}
	outRange := []*big.Int{orderL, plus(orderL, 1), plus(orderL, 2), new(big.Int).Add(orderL, two(252)),
		minus(two(253), 1), two(253), minus(two(255), 1), two(255), minus(two(256), 1)}
	for ei, enc := range laxEncodings(identity()) {
		for si, s := range inRange {
			r := baseMul(s).encode()
			for rep := 0; rep < 2; rep++ {
				g.add("edgeS", enc[:], sigOf(r, s), g.msg(), fmt.Sprintf("idkey%d.in%d.%d", ei, si, rep))
				if s2 := new(big.Int).Add(s, orderL); s2.BitLen() <= 256 {
					g.add("edgeS", enc[:], sigOf(r, s2), g.msg(), fmt.Sprintf("idkey%d.in%d.plusL.%d", ei, si, rep))
				}
			}
		}
		for si, s := range outRange {
			r := baseMul(s).encode()
			g.add("edgeS", enc[:], sigOf(r, s), g.msg(), fmt.Sprintf("idkey%d.out%d", ei, si))
		}
	}
}

// laxKeys: every key whose y is written as y + p (y = 0 ... 18, both signs), the decodable ones among them, and keys
// with no x at all, under random signatures and messages; the small-order ones among them also carry crafted
// accepting signatures in smallKeys.
func (g *gen) laxKeys(per int) {
	for delta := int64(0); delta <= 18; delta++ {
		for sign := 0; sign < 2; sign++ {
			e := le32(new(big.Int).Add(fieldP, big.NewInt(delta)))
			if sign == 1 {
				e[31] |= 0x80
			}
			_, ok := decodeLax(e[:])
			for n := 0; n < per; n++ {
				g.add("laxkey", e[:], sigBytes(g.bytes(32), g.scalar(orderL)), g.msg(),
					fmt.Sprintf("yp%d.sign%d.decodes%v.%d", delta, sign, ok, n))
			}
		}
	}
	for n := 0; n < per*4; n++ {
		var e []byte
		for {
			e = g.bytes(32)
			if _, ok := decodeLax(e); !ok {
				break
			}
		}
		g.add("laxkey", e, sigBytes(g.bytes(32), g.scalar(orderL)), g.msg(), fmt.Sprintf("offcurve.%d", n))
	}
}

// otherR: honest signatures whose R is replaced by -R (the sign bit flipped: the same y, the other x).
func (g *gen) otherR(n int) {
	for i := 0; i < n; i++ {
		priv := g.key()
		pk := []byte(priv.Public().(ed25519.PublicKey))
		m := g.msg()
		g.add("negR", pk, flipBit(ed25519.Sign(priv, m), 255), m, fmt.Sprintf("%d", i))
	}
}

// binding: a key's bytes beside another key's signature. An honest signature by A2 checked against A; and the
// forgery SCL accepted (hazard reproduced in the research): R = [r]B, S = r + H(R || A || M) a2, i.e. A2's secret
// with A's bytes hashed in, checked against A (and against A2).
func (g *gen) binding(n int) {
	for i := 0; i < n; i++ {
		a2 := g.scalar(orderL)
		a2e := baseMul(a2).encode()
		victim := []byte(g.key().Public().(ed25519.PublicKey))
		m := g.msg()
		r := g.scalar(orderL)
		re := baseMul(r).encode()
		k := challenge(re[:], victim, m)
		s := new(big.Int).Mod(new(big.Int).Add(r, new(big.Int).Mul(k, a2)), orderL)
		g.add("binding", victim, sigOf(re, s), m, fmt.Sprintf("%d.forgedAgainstA", i))
		g.add("binding", a2e[:], sigOf(re, s), m, fmt.Sprintf("%d.forgedAgainstA2", i))
		k2 := challenge(re[:], a2e[:], m)
		s2 := new(big.Int).Mod(new(big.Int).Add(r, new(big.Int).Mul(k2, a2)), orderL)
		g.add("binding", victim, sigOf(re, s2), m, fmt.Sprintf("%d.honestA2AgainstA", i))
		g.add("binding", a2e[:], sigOf(re, s2), m, fmt.Sprintf("%d.honestA2AgainstA2", i))
	}
}

// diffSet is the differential set (eth/bridge/M2a-NOTES.md, gate 4) for one seed, in a fixed order.
func diffSet(seed [32]byte) *gen {
	g := newGen(seed)
	g.valid(20000)
	g.random(10000)
	g.smallKeys(48)
	g.mixedKeys(5, 40)
	g.smallR(24)
	g.edgeS(500)
	g.laxKeys(10)
	g.otherR(500)
	g.binding(250)
	return g
}

func writeDiff(dir string, seed [32]byte, chunk int) {
	g := diffSet(seed)
	v := verdicts(g.recs)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		fatalf("%v", err)
	}
	old, _ := filepath.Glob(filepath.Join(dir, "chunk-*.vec"))
	for _, f := range old {
		os.Remove(f)
	}
	chunks := (len(g.recs) + chunk - 1) / chunk
	for c := 0; c < chunks; c++ {
		lo, hi := c*chunk, min((c+1)*chunk, len(g.recs))
		writeVec(filepath.Join(dir, fmt.Sprintf("chunk-%04d.vec", c)),
			[]string{fmt.Sprintf("differential chunk %d of %d: records %d-%d of %d, seed %x", c, chunks, lo, hi-1, len(g.recs), seed)},
			g.recs[lo:hi], v[lo:hi])
	}
	// The manifest: three numbers the forge test reads (chunks, records, records per chunk), then the families.
	accept := map[string]int{}
	for i, r := range g.recs {
		if v[i] {
			accept[strings.SplitN(r.label, ".", 2)[0]]++
		}
	}
	var fams []string
	for f := range g.fam {
		fams = append(fams, f)
	}
	sort.Strings(fams)
	var b strings.Builder
	fmt.Fprintf(&b, "%d\n%d\n%d\n", chunks, len(g.recs), chunk)
	fmt.Fprintf(&b, "# seed %x; Go accepts %d of %d\n", seed, countTrue(v), len(g.recs))
	for _, f := range fams {
		fmt.Fprintf(&b, "# %-9s %6d records, Go accepts %6d\n", f, g.fam[f], accept[f])
	}
	if err := os.WriteFile(filepath.Join(dir, "manifest.txt"), []byte(b.String()), 0o644); err != nil {
		fatalf("%v", err)
	}
	fmt.Print(b.String())
}
