package hlmailbox

import (
	"crypto/bech32"
	"strings"
	"testing"

	"gno.land/p/nt/testutils/v0"
)

// TestCanonicalAddress (eth F9, its review's findings 1 and 2): every spelling IsValid takes of an address — as gno
// writes it, all in upper case, and with bech32m's checksum in either case (spellings, mailbox_test.gno) — reads as
// the one gno writes, and only that one is canonical (init's guard); anything IsValid refuses is no gno address. The
// zero address's bech32m spelling is pinned as an independent BIP-350 encoder writes it (the review's, checked against
// BIP-350's vectors; M1-NOTES' Fold F9), so the tests' encoder is held to it.
func TestCanonicalAddress(cur realm, t *testing.T) {
	zero := address("g1qqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqluuxe")
	if sp := spellings(zero); sp[2] != "g1qqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqq4rvsrm" || sp[3] != "G1QQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQ4RVSRM" {
		t.Fatalf("the zero address's bech32m spellings: %v", sp)
	}
	for _, a := range []address{zero, Address(), testutils.TestAddress("hl-canonical")} {
		for i, s := range spellings(a) {
			if !s.IsValid() || i > 0 && s == a {
				t.Fatalf("%s's spelling %d, %s, is no other spelling IsValid takes", a, i, s)
			}
			if c := CanonicalAddress(s); c != a {
				t.Fatalf("%s reads as %q, want %s", s, c, a)
			}
			if canonical(s) != (i == 0) {
				t.Fatalf("%s canonical: %v", s, canonical(s))
			}
		}
	}
	// no gno address: mixed case, a checksum one character off, padded, another prefix, twenty-one bytes, nothing
	a := testutils.TestAddress("hl-canonical")
	last := "q"
	if strings.HasSuffix(string(a), "q") {
		last = "p"
	}
	data21, _ := bech32.ConvertBits(make([]byte, 21), 8, 5, true)
	data20, _ := bech32.ConvertBits(make([]byte, 20), 8, 5, true)
	long, _ := bech32.Encode("g", data21)
	other, _ := bech32.Encode("cosmos", data20)
	for _, s := range []address{
		address("G" + string(a)[1:]), address(string(a)[:len(a)-1] + last), address(" " + string(a)), address(other), address(long), "", "g1notanaddress",
	} {
		if s.IsValid() {
			t.Fatalf("IsValid takes %q", s)
		}
		if c := CanonicalAddress(s); c != "" || canonical(s) {
			t.Fatalf("%q reads as %q (canonical %v), want no address", s, c, canonical(s))
		}
	}
}
