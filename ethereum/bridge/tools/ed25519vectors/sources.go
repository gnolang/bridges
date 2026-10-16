package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
)

// The three published vector sets, each read from a pinned copy (the commit and the file's sha256 are checked, so a
// changed upstream file is a loud error, not a silently different gate).
const (
	rfc8032SHA256    = "ed63657ff389301282b169b0abde9b5dd2c7e4d524fdfa5da6ff3094fc93c4c3" // rfc-editor.org/rfc/rfc8032.txt
	wycheproofCommit = "7a325932231bd7a724c791c8b0d65ea4d6d2f435"                         // C2SP/wycheproof
	wycheproofSHA256 = "752d2ea7d7c6cf4736381b6cbacb61f8182b126ab7cd9b058f00c50084975536" // testvectors_v1/ed25519_test.json
	speccheckCommit  = "65519336fda78a3d016e947df6d82848aca0c9da"                         // novifinancial/ed25519-speccheck
	speccheckSHA256  = "08e47a36d9aead288664930505584f353fff113ab854f2800db1e4f5b3540450" // cases.json
)

func readPinned(path, want string) []byte {
	b, err := os.ReadFile(path)
	if err != nil {
		fatalf("%v", err)
	}
	if got := fmt.Sprintf("%x", sha256.Sum256(b)); got != want {
		fatalf("%s: sha256 %s, pinned %s", path, got, want)
	}
	return b
}

var (
	rfcLength = regexp.MustCompile(`^MESSAGE \(length (\d+) bytes?\):$`)
	hexLine   = regexp.MustCompile(`^[0-9a-f]+$`)
)

// rfc8032 reads the five Ed25519 tests of RFC 8032 §7.1. A field's hex runs over lines until the next label; the
// page breaks inside TEST 1024's message (a footer, a form feed, a header) are not hex and are skipped. Every test
// is checked against the RFC's own statements: the declared message length, the key derived from the secret, and
// the signature Go's Sign makes with it.
func rfc8032(path string) []record {
	text := strings.ReplaceAll(string(readPinned(path, rfc8032SHA256)), "\r", "")
	start := strings.Index(text, "\n7.1.  Test Vectors for Ed25519\n")
	end := strings.Index(text, "\n7.2.  Test Vectors for Ed25519ctx\n")
	if start < 0 || end < start {
		fatalf("%s: §7.1 not found", path)
	}
	var out []record
	for _, part := range strings.Split(text[start:end], "-----TEST ")[1:] {
		lines := strings.Split(part, "\n")
		name := strings.TrimSpace(lines[0])
		fields := map[string]*bytes.Buffer{}
		field, msgLen := "", -1
		for _, raw := range lines[1:] {
			line := strings.TrimSpace(raw)
			switch {
			case line == "ALGORITHM:":
				field = "alg"
			case line == "SECRET KEY:":
				field = "sk"
			case line == "PUBLIC KEY:":
				field = "pk"
			case line == "SIGNATURE:":
				field = "sig"
			case rfcLength.MatchString(line):
				field = "msg"
				msgLen, _ = strconv.Atoi(rfcLength.FindStringSubmatch(line)[1])
			case field == "alg":
				if line != "" && line != "Ed25519" {
					fatalf("TEST %s: algorithm %q", name, line)
				}
			case field != "" && hexLine.MatchString(line):
				if fields[field] == nil {
					fields[field] = new(bytes.Buffer)
				}
				fields[field].Write(mustHex(line))
			}
		}
		get := func(k string) []byte {
			if fields[k] == nil {
				return []byte{}
			}
			return fields[k].Bytes()
		}
		sk, pk, msg, sig := get("sk"), get("pk"), get("msg"), get("sig")
		if len(sk) != 32 || len(pk) != 32 || len(sig) != 64 || len(msg) != msgLen {
			fatalf("TEST %s: sk %d pk %d sig %d msg %d (declared %d)", name, len(sk), len(pk), len(sig), len(msg), msgLen)
		}
		priv := ed25519.NewKeyFromSeed(sk)
		if !bytes.Equal(priv.Public().(ed25519.PublicKey), pk) || !bytes.Equal(ed25519.Sign(priv, msg), sig) {
			fatalf("TEST %s: Go does not derive the RFC's key and signature", name)
		}
		label := "rfc8032.TEST_" + strings.NewReplacer("(", "_", ")", "").Replace(name)
		out = append(out, record{pk: pk, sig: sig, msg: msg, label: label, want: "valid"})
	}
	if len(out) != 5 {
		fatalf("%s: %d tests in §7.1, want 5", path, len(out))
	}
	return out
}

// wycheproof reads every case of ed25519_test.json; "want" keeps Wycheproof's own result to compare with Go's.
func wycheproof(path string) []record {
	var doc struct {
		NumberOfTests int `json:"numberOfTests"`
		TestGroups    []struct {
			PublicKey struct {
				PK string `json:"pk"`
			} `json:"publicKey"`
			Tests []struct {
				TcID    int    `json:"tcId"`
				Comment string `json:"comment"`
				Msg     string `json:"msg"`
				Sig     string `json:"sig"`
				Result  string `json:"result"`
			} `json:"tests"`
		} `json:"testGroups"`
	}
	if err := json.Unmarshal(readPinned(path, wycheproofSHA256), &doc); err != nil {
		fatalf("%s: %v", path, err)
	}
	var out []record
	for _, g := range doc.TestGroups {
		pk := mustHex(g.PublicKey.PK)
		for _, t := range g.Tests {
			out = append(out, record{pk: pk, sig: mustHex(t.Sig), msg: mustHex(t.Msg),
				label: fmt.Sprintf("wycheproof.tc%d", t.TcID), want: t.Result})
		}
	}
	if len(out) != doc.NumberOfTests {
		fatalf("%s: %d tests read, the file says %d", path, len(out), doc.NumberOfTests)
	}
	return out
}

// speccheck reads the twelve cases of "Taming the many EdDSAs" (Chalkias, Garillot, Nikolaenko, SSR 2020).
func speccheck(path string) []record {
	var doc []struct {
		Message   string `json:"message"`
		PubKey    string `json:"pub_key"`
		Signature string `json:"signature"`
	}
	if err := json.Unmarshal(readPinned(path, speccheckSHA256), &doc); err != nil {
		fatalf("%s: %v", path, err)
	}
	var out []record
	for i, c := range doc {
		out = append(out, record{pk: mustHex(c.PubKey), sig: mustHex(c.Signature), msg: mustHex(c.Message),
			label: fmt.Sprintf("speccheck.case%d", i)})
	}
	if len(out) != 12 {
		fatalf("%s: %d cases, want 12", path, len(out))
	}
	return out
}

// sourceSet writes one set with Go's verdicts and reports, for the notes, every case where the source states a
// result of its own that differs from Go's; strict sets (RFC 8032, the named edges, the binding cases) state the
// verdict the rules predict, and a difference there stops the generator.
func sourceSet(name, out string, recs []record, strict bool, header []string) {
	v := verdicts(recs)
	var differ []string
	lengths := 0
	for i, r := range recs {
		if len(r.sig) != 64 {
			lengths++
		}
		if r.want == "" {
			continue
		}
		goSays := "invalid"
		if v[i] {
			goSays = "valid"
		}
		if r.want != goSays {
			differ = append(differ, fmt.Sprintf("%s: source says %s, Go says %s", r.label, r.want, goSays))
		}
	}
	if strict && len(differ) > 0 {
		fatalf("%s: Go's verdict differs from the predicted one: %v", name, differ)
	}
	header = append(header,
		fmt.Sprintf("%d records: Go accepts %d, refuses %d (of which %d by length: the signature is not 64 bytes)",
			len(recs), countTrue(v), len(recs)-countTrue(v), lengths))
	if len(differ) == 0 {
		header = append(header, "where the source states a result, Go's verdict agrees with it on every case")
	}
	for _, d := range differ {
		header = append(header, "DIFFERS "+d)
	}
	writeVec(out, header, recs, v)
	fmt.Printf("%s: %d records, Go accepts %d, refuses %d (%d by length), %d differ from the source's result -> %s\n",
		name, len(recs), countTrue(v), len(recs)-countTrue(v), lengths, len(differ), out)
	for _, d := range differ {
		fmt.Println("  " + d)
	}
}

// fipsSHA512 holds the FIPS 180-4 examples (NIST's SHA512.pdf one- and two-block samples, and the empty and
// million-'a' messages of the earlier FIPS 180-2 appendix C), each checked against crypto/sha512 before use.
var fipsSHA512 = []struct{ name, msg, digest string }{
	{"empty", "", "cf83e1357eefb8bdf1542850d66d8007d620e4050b5715dc83f4a921d36ce9ce47d0d13c5d85f2b0ff8318d2877eec2f63b931bd47417a81a538327af927da3e"},
	{"abc", "abc", "ddaf35a193617abacc417349ae20413112e6fa4e89a97ea20a9eeee64b55d39a2192992a274fc1a836ba3c23a3feebbd454d4423643ce80e2a9ac94fa54ca49f"},
	{"two-block", "abcdefghbcdefghicdefghijdefghijkefghijklfghijklmghijklmnhijklmnoijklmnopjklmnopqklmnopqrlmnopqrsmnopqrstnopqrstu", "8e959b75dae313da8cf4f72814fc143f8f7779c6eb9f7fa17299aeadb6889018501d289e4900f7e4331b99dec4b5433ac7d329eeb6dd26545e96e55b874be909"},
}

// fipsMillionA is SHA-512 of 1,000,000 bytes of 'a' (FIPS 180-2 appendix C.3); the forge test builds the message.
const fipsMillionA = "e718483d0ce769644e2e42c7bc15b4638e1f98b13b2044285632a803afa973ebde0ff244877ea60a4cb0432ce577c31beb009c5c2c49aa2e4eadb217ad8cc09b"

func checkFIPS() {
	for _, f := range fipsSHA512 {
		if got := hex.EncodeToString(sha512Sum([]byte(f.msg))); got != f.digest {
			fatalf("FIPS %s: crypto/sha512 gives %s", f.name, got)
		}
	}
	if got := hex.EncodeToString(sha512Sum(bytes.Repeat([]byte{'a'}, 1_000_000))); got != fipsMillionA {
		fatalf("FIPS million-a: crypto/sha512 gives %s", got)
	}
}
