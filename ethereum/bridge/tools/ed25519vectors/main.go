// Command ed25519vectors writes the vectors that pin eth/bridge/contracts/src/lib/Ed25519.sol to Go's
// crypto/ed25519, the verifier tm2 calls for every precommit (eth/bridge/PLAN.md §2, §5.9, §6.6). Every record
// carries Go's own verdict, and a math/big model of the rules (model.go) must agree with Go on each one before it is
// written. The forge tests in eth/bridge/contracts/test replay the records against the Solidity library; the gates
// and their counts are in eth/bridge/M2a-NOTES.md.
//
//	ed25519vectors sources -rfc8032 F -wycheproof F -speccheck F -out DIR   the published sets (pinned copies)
//	ed25519vectors local -out DIR                                           edges, binding, gas, SHA-512, probes, decode
//	ed25519vectors diff -out DIR [-seed HEX] [-chunk N]                     the differential set (>= 100,000)
//	ed25519vectors consts                                                   the Solidity constants, derived
package main

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
)

// defaultSeed is sha256("eth-m2a ed25519 differential v1"); the notes record the set it produces.
var defaultSeed = sha256.Sum256([]byte("eth-m2a ed25519 differential v1"))

func main() {
	if len(os.Args) < 2 {
		fatalf("usage: ed25519vectors sources|local|diff|consts [flags]")
	}
	fs := flag.NewFlagSet(os.Args[1], flag.ExitOnError)
	out := fs.String("out", "", "output directory")
	rfcPath := fs.String("rfc8032", "", "rfc8032.txt (rfc-editor.org), pinned by sha256")
	wpPath := fs.String("wycheproof", "", "C2SP/wycheproof testvectors_v1/ed25519_test.json, pinned")
	scPath := fs.String("speccheck", "", "novifinancial/ed25519-speccheck cases.json, pinned")
	seedHex := fs.String("seed", hex.EncodeToString(defaultSeed[:]), "32-byte seed of the differential set")
	chunk := fs.Int("chunk", 2000, "records per chunk file")
	fs.Parse(os.Args[2:])
	if *out == "" && os.Args[1] != "consts" {
		fatalf("-out is required")
	}
	switch os.Args[1] {
	case "sources":
		sourceSet("rfc8032", filepath.Join(*out, "rfc8032.vec"), rfc8032(*rfcPath), true, []string{
			"RFC 8032 §7.1, the five Ed25519 tests (rfc-editor.org/rfc/rfc8032.txt, sha256 " + rfc8032SHA256 + ")",
			"each checked at conversion: Go derives the RFC's public key from its secret and the RFC's signature with Sign"})
		sourceSet("wycheproof", filepath.Join(*out, "wycheproof.vec"), wycheproof(*wpPath), false, []string{
			"C2SP/wycheproof " + wycheproofCommit + " testvectors_v1/ed25519_test.json (sha256 " + wycheproofSHA256 + "), every case",
			"verdict: tm2's (a signature that is not 64 bytes refused, else crypto/ed25519.Verify)"})
		sourceSet("speccheck", filepath.Join(*out, "speccheck.vec"), speccheck(*scPath), false, []string{
			"novifinancial/ed25519-speccheck " + speccheckCommit + " cases.json (sha256 " + speccheckSHA256 + "), the 12 cases of",
			"\"Taming the many EdDSAs\" (Chalkias, Garillot, Nikolaenko; SSR 2020), verdict crypto/ed25519.Verify"})
	case "local":
		writeLocal(*out)
	case "diff":
		seed, err := hex.DecodeString(*seedHex)
		if err != nil || len(seed) != 32 {
			fatalf("-seed must be 32 bytes of hex")
		}
		writeDiff(*out, [32]byte(seed), *chunk)
	case "consts":
		printConsts()
	default:
		fatalf("unknown command %q", os.Args[1])
	}
}

// writeLocal writes the small committed sets the unit tests read by name.
func writeLocal(dir string) {
	seed := sha256.Sum256([]byte("eth-m2a ed25519 local v1"))
	g := newGen(seed)
	edges(g)
	edgeRecs := append(g.recs, edgeFamilies()...)
	sourceSet("edges", filepath.Join(dir, "edges.vec"), edgeRecs, true, []string{
		"named edge cases: each label ends in the verdict PLAN.md §6.6 predicts, which Go's must equal (checked here);",
		"after the 25 named ones, audit round 3's families (families.go, each from its own seed): edge.special — every",
		"encoding of the 8 points of order dividing 8, B and 0xff..ff as R beside S = 0, 1, 2, L-1, 2^252 under honest keys",
		"(E3-ED1); edge.negR — R = enc([r]B), S = k a - r under a known secret, prime-order and mixed keys, so R' = -R",
		"(E3-ED3); edge.laxR — a small-order R' (the identity, an order-4 or the order-2 point) in each encoding a lax",
		"decoder maps to it, y + p and a zero x's sign bit, beside its canonical one (E3-ED3)"})

	g.recs, g.fam = nil, map[string]int{}
	g.binding(32)
	// Every one-bit change of an honest key's 32 bytes, beside the unchanged signature and message.
	priv := g.key()
	pk := []byte(priv.Public().(ed25519.PublicKey))
	m := g.bytes(110)
	sig := ed25519.Sign(priv, m)
	for b := 0; b < 256; b++ {
		g.add("binding", flipBit(pk, b), sig, m, fmt.Sprintf("keyflip.bit%d", b))
	}
	bindRecs := g.recs
	for i := range bindRecs {
		bindRecs[i].want = "invalid"
		if filepath.Ext(bindRecs[i].label) == ".honestA2AgainstA2" {
			bindRecs[i].want = "valid"
		}
	}
	sourceSet("binding", filepath.Join(dir, "binding.vec"), bindRecs, true, []string{
		"the key-binding hazard (research: SCL verified a signature made with key A2 against key A's bytes):",
		"forgedAgainstA: R = [r]B, S = r + H(R || A || M) a2 under A; also against A2; honest A2 signatures against A and A2;",
		"keyflip: an honest signature under each of the 256 one-bit changes of its key's bytes"})

	g.recs = nil
	for _, ln := range []int{0, 1, 47, 48, 110, 175, 176, 300} {
		priv := g.key()
		m := g.bytes(ln)
		g.add("gas", priv.Public().(ed25519.PublicKey), ed25519.Sign(priv, m), m, fmt.Sprintf("len%d", ln))
	}
	sourceSet("gas", filepath.Join(dir, "gas.vec"), g.recs, false, []string{
		"honest signatures by message length for the gas measurement (48 and 176 bytes: where SHA-512 needs a second, third block)"})

	writeSHA512(filepath.Join(dir, "sha512.vec"), sha256.Sum256([]byte("eth-m2a sha512 v1")))

	// audit round 2's E2-ED1: the forgery probes against key decoding and the direct decode vectors (probes.go)
	writeDecodeAndProbes(dir)
}

// edges are the named cases behind M2a-NOTES.md's statements, each label ending in the verdict the rules predict.
func edges(g *gen) {
	expect := func(pk, sig, msg []byte, label string, accept bool) {
		want, verdict := "invalid", "refuse"
		if accept {
			want, verdict = "valid", "accept"
		}
		g.recs = append(g.recs, record{pk: clone(pk), sig: clone(sig), msg: clone(msg), label: "edge." + label + "." + verdict, want: want})
	}
	id := laxEncodings(identity()) // canonical, sign bit set, y = p + 1, y = p + 1 with the sign bit
	idR := id[0]
	m := []byte("gnoland-1 precommit")
	expect(id[0][:], sigOf(idR, big.NewInt(0)), m, "S0.identityKey.canonicalR", true)
	expect(id[0][:], sigOf(id[1], big.NewInt(0)), m, "S0.identityKey.R_signbit", false)
	expect(id[0][:], sigOf(id[2], big.NewInt(0)), m, "S0.identityKey.R_yp", false)
	expect(id[0][:], sigOf(idR, orderL), m, "SequalsL.identityKey", false)
	lm1 := new(big.Int).Sub(orderL, bigOne)
	expect(id[0][:], sigOf(baseMul(lm1).encode(), lm1), m, "SLminus1.identityKey", true)
	s := g.scalar(orderL)
	rs := baseMul(s).encode()
	expect(id[1][:], sigOf(rs, s), m, "key.identity_signbit", true)
	expect(id[2][:], sigOf(rs, s), m, "key.identity_yp", true)
	expect(id[3][:], sigOf(rs, s), m, "key.identity_yp_signbit", true)
	sPlusL := new(big.Int).Add(s, orderL)
	expect(id[2][:], sigOf(rs, sPlusL), m, "key.identity_yp.SplusL", false)

	// Small-order keys, crafted: R = [S]B - [j]T, the message ground so that k = j mod ord(T).
	crafted := func(t point, enc [32]byte, label string) {
		o := pointOrder(t)
		j := 1 % o
		s := g.scalar(orderL)
		r := baseMul(s).add(t.mul(big.NewInt(int64(j))).neg()).encode()
		msg := g.grind(r[:], enc[:], func(k *big.Int) bool { return modEq(k, j, o) })
		expect(enc[:], sigOf(r, s), msg, label+".kmatch", true)
		if o > 1 {
			msg = g.grind(r[:], enc[:], func(k *big.Int) bool { return !modEq(k, j, o) })
			expect(enc[:], sigOf(r, s), msg, label+".knomatch", false)
		}
	}
	var t2, t4, t8 point
	for _, t := range g.tors {
		switch pointOrder(t) {
		case 2:
			t2 = t
		case 4:
			t4 = t
		case 8:
			t8 = t
		}
	}
	crafted(t2, laxEncodings(t2)[1], "key.order2_signbit") // (0, -1) with the sign bit set
	crafted(t4, laxEncodings(t4)[1], "key.order4_yp")      // (+-i, 0) written with y = p
	crafted(t8, laxEncodings(t8)[0], "key.order8")

	// Mixed-order key A = [a]B + T8 signed honestly: cofactorless accepts only when 8 | k.
	a := g.scalar(orderL)
	ae := baseMul(a).add(t8).encode()
	for _, want := range []bool{false, true} {
		for {
			r := g.scalar(orderL)
			re := baseMul(r).encode()
			msg := g.msg()
			k := challenge(re[:], ae[:], msg)
			if t8.mul(k).isIdentity() != want {
				continue
			}
			sv := new(big.Int).Mod(new(big.Int).Add(r, new(big.Int).Mul(k, a)), orderL)
			label := "mixed.order8.k_not_multiple_of_8"
			if want {
				label = "mixed.order8.k_multiple_of_8"
			}
			expect(ae[:], sigOf(re, sv), msg, label, want)
			break
		}
	}

	// Small-order R under a prime-order key: S = k a makes R' the identity.
	a = g.scalar(orderL)
	pe := baseMul(a).encode()
	for i, label := range []string{"smallR.identity_canonical", "smallR.identity_signbit"} {
		k := challenge(id[i][:], pe[:], m)
		expect(pe[:], sigOf(id[i], new(big.Int).Mod(new(big.Int).Mul(k, a), orderL)), m, label, i == 0)
	}

	// A key with no x; an honest signature with S's top bit set, with S + L, with R's sign flipped; and on 110 bytes.
	var off []byte
	for y := int64(2); ; y++ {
		e := le32(big.NewInt(y))
		if _, ok := decodeLax(e[:]); !ok {
			off = e[:]
			break
		}
	}
	priv := g.key()
	pk := []byte(priv.Public().(ed25519.PublicKey))
	m110 := g.bytes(110)
	sig := ed25519.Sign(priv, m110)
	expect(off, sig, m110, "key.offcurve", false)
	expect(pk, sig, m110, "honest.len110", true)
	expect(pk, flipBit(sig, 511), m110, "honest.S_bit255", false)
	expect(pk, sigBytes(sig[:32], new(big.Int).Add(leInt(sig[32:]), orderL)), m110, "honest.SplusL", false)
	expect(pk, flipBit(sig, 255), m110, "honest.negR", false)
	expect(flipBit(pk, 255), sig, m110, "honest.negA", false)
}

// printConsts derives every constant Ed25519.sol uses, for review against the source.
func printConsts() {
	h := func(v *big.Int) string { return fmt.Sprintf("0x%064x", v) }
	b2 := basePoint.add(basePoint)
	b3 := b2.add(basePoint)
	fmt.Println("P        ", h(fieldP))
	fmt.Println("L        ", h(orderL))
	fmt.Println("D        ", h(curveD))
	fmt.Println("D2       ", h(fadd(curveD, curveD)))
	fmt.Println("SQRT_M1  ", h(sqrtM1))
	fmt.Println("BX       ", h(basePoint.x))
	fmt.Println("BY       ", h(basePoint.y))
	fmt.Println("BT       ", h(fmul(basePoint.x, basePoint.y)))
	fmt.Println("2B       ", h(b2.x), h(b2.y))
	fmt.Println("3B       ", h(b3.x), h(b3.y))
	fmt.Println("2^256%L  ", h(new(big.Int).Mod(new(big.Int).Lsh(bigOne, 256), orderL)))
	enc := basePoint.encode()
	fmt.Println("B enc    ", hex.EncodeToString(enc[:]))
}
