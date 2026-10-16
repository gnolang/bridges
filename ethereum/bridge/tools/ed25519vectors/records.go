package main

import (
	"bufio"
	"crypto/ed25519"
	"encoding/hex"
	"fmt"
	"os"
	"runtime"
	"strings"
	"sync"
)

// A record is one verification the forge tests replay against Ed25519.sol: the 32-byte key, the signature (64 bytes
// except where a source carries another length, which tm2 refuses before it calls Verify), the message and a label.
type record struct {
	pk    []byte
	sig   []byte
	msg   []byte
	label string
	// want is set where the source states an expected verdict of its own (RFC 8032: valid; Wycheproof's "result"),
	// to report where it differs from Go's.
	want string
}

// goVerdict is what tm2 decides: PubKeyEd25519.VerifyBytes refuses a signature that is not 64 bytes and otherwise
// returns golang.org/x/crypto/ed25519.Verify, which is crypto/ed25519.Verify (tm2/pkg/crypto/ed25519/ed25519.go,
// golang.org/x/crypto v0.53.0 ed25519/ed25519.go).
func goVerdict(r record) bool {
	if len(r.pk) != ed25519.PublicKeySize {
		panic(fmt.Sprintf("%s: a key of %d bytes (tm2's keys are [32]byte)", r.label, len(r.pk)))
	}
	if len(r.sig) != ed25519.SignatureSize {
		return false
	}
	return ed25519.Verify(ed25519.PublicKey(r.pk), r.msg, r.sig)
}

// verdicts computes Go's verdict for every record, in parallel, and checks the math/big model agrees with each; a
// disagreement stops the generator (the model, not Go, would be wrong, and every crafted record came from it).
func verdicts(recs []record) []bool {
	out := make([]bool, len(recs))
	bad := make([]string, len(recs))
	workers := runtime.NumCPU() / 2
	if workers < 1 {
		workers = 1
	}
	var wg sync.WaitGroup
	next := make(chan int, 1024)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range next {
				g := goVerdict(recs[i])
				m := len(recs[i].sig) == 64 && modelVerify(recs[i].pk, recs[i].msg, recs[i].sig)
				out[i] = g
				if g != m {
					bad[i] = fmt.Sprintf("%s: crypto/ed25519 %v, model %v (pk %x sig %x msg %x)",
						recs[i].label, g, m, recs[i].pk, recs[i].sig, recs[i].msg)
				}
			}
		}()
	}
	for i := range recs {
		next <- i
	}
	close(next)
	wg.Wait()
	for _, b := range bad {
		if b != "" {
			fatalf("the model disagrees with crypto/ed25519: %s", b)
		}
	}
	return out
}

func hex0x(b []byte) string { return "0x" + hex.EncodeToString(b) }

// writeVec writes records as the forge tests read them: '#' lines are comments; every other line is
// "<0|1> <pk> <sig> <msg> <label>", Go's verdict first, byte strings as 0x-prefixed hex ("0x" when empty).
func writeVec(path string, header []string, recs []record, verdict []bool) {
	f, err := os.Create(path)
	if err != nil {
		fatalf("%v", err)
	}
	w := bufio.NewWriterSize(f, 1<<20)
	for _, h := range header {
		fmt.Fprintf(w, "# %s\n", h)
	}
	for i, r := range recs {
		if strings.ContainsAny(r.label, " \t\n") || r.label == "" {
			fatalf("bad label %q", r.label)
		}
		v := 0
		if verdict[i] {
			v = 1
		}
		fmt.Fprintf(w, "%d %s %s %s %s\n", v, hex0x(r.pk), hex0x(r.sig), hex0x(r.msg), r.label)
	}
	if err := w.Flush(); err != nil {
		fatalf("%v", err)
	}
	if err := f.Close(); err != nil {
		fatalf("%v", err)
	}
}

func countTrue(v []bool) (n int) {
	for _, b := range v {
		if b {
			n++
		}
	}
	return n
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "ed25519vectors: "+format+"\n", args...)
	os.Exit(1)
}

func mustHex(s string) []byte {
	b, err := hex.DecodeString(s)
	if err != nil {
		fatalf("bad hex %q: %v", s, err)
	}
	return b
}
