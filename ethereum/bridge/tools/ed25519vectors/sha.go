package main

import (
	"bufio"
	"crypto/sha512"
	"fmt"
	"math/rand/v2"
	"os"
)

func sha512Sum(b []byte) []byte {
	h := sha512.Sum512(b)
	return h[:]
}

// writeSHA512 writes "<msg> <digest> <label>" lines: the FIPS 180-4 examples, then crypto/sha512 on three random
// messages of every length 0-300, all-zero and all-0xff messages around every padding boundary (111/112 bytes: the
// last length whose padding fits one block; 239/240 for two), and a few long messages.
func writeSHA512(path string, seed [32]byte) {
	checkFIPS()
	rng := rand.New(rand.NewChaCha8(seed))
	f, err := os.Create(path)
	if err != nil {
		fatalf("%v", err)
	}
	w := bufio.NewWriter(f)
	fmt.Fprintf(w, "# SHA-512 vectors: \"<message> <digest> <label>\", the digest from Go's crypto/sha512 (FIPS ones as published)\n")
	n := 0
	put := func(m []byte, label string) {
		fmt.Fprintf(w, "%s %s %s\n", hex0x(m), hex0x(sha512Sum(m)), label)
		n++
	}
	for _, v := range fipsSHA512 {
		put([]byte(v.msg), "fips."+v.name)
	}
	for ln := 0; ln <= 300; ln++ {
		for i := 0; i < 3; i++ {
			m := make([]byte, ln)
			for j := range m {
				m[j] = byte(rng.Uint32())
			}
			put(m, fmt.Sprintf("random.len%d.%d", ln, i))
		}
	}
	for _, ln := range []int{0, 1, 63, 64, 111, 112, 113, 127, 128, 129, 175, 176, 239, 240, 241, 255, 256, 257, 383, 384} {
		zero, ones := make([]byte, ln), make([]byte, ln)
		for j := range ones {
			ones[j] = 0xff
		}
		put(zero, fmt.Sprintf("zero.len%d", ln))
		put(ones, fmt.Sprintf("ff.len%d", ln))
	}
	for _, ln := range []int{512, 1023, 1087, 2000, 4096} {
		m := make([]byte, ln)
		for j := range m {
			m[j] = byte(rng.Uint32())
		}
		put(m, fmt.Sprintf("long.len%d", ln))
	}
	if err := w.Flush(); err != nil {
		fatalf("%v", err)
	}
	f.Close()
	fmt.Printf("sha512: %d vectors -> %s\n", n, path)
}
