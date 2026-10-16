package hlmailbox

import (
	"crypto/keccak256"
	"encoding/hex"
	"errors"
	"strings"
)

// Ethereum addresses, read and written in the one place every realm of this deployment uses (eth/bridge/PLAN.md §0,
// §5.2, §13): the routes parse a recipient and a router with ParseEthAddress, r/hlgov every address a decision
// carries, and every page shows an address with EthAddress. Both are plain functions over values: they read and
// write no state of this realm, so a route or r/hlgov calling them (a non-crossing call, borrow rule #1 of the
// Interrealm Specification v2 §4.1, which runs the body under this realm's storage context) can change nothing here,
// and what they return is a value the caller copies — an array or a string, never a pointer into this realm.

// ParseEthAddress reads an Ethereum address the way a person or a tool writes one, and returns it as Hyperlane
// carries an address — left-padded with twelve zero bytes to 32 (eth §0, §5.2):
//
//   - "0x" and 40 hex digits: all lower case, all upper case, or mixed case that must be the address's EIP-55
//     checksum exactly (a mixed-case address with one letter's case wrong is a typo the checksum caught, refused);
//   - 64 hex digits, with or without "0x", whose first 24 are zero (an address already as 32 bytes; any case).
//
// Anything else is refused: 40 hex digits without "0x", another length, a non-hex digit, and 32 bytes whose top
// twelve are not zero (Hyperlane's bytes32ToAddress would revert the delivery for good, eth §5.2). The zero address
// parses — kind 3 names it on purpose — so each caller refuses it where the plan does. Surrounding spaces are
// trimmed. A refusal quotes what it was given (echo: ASCII, at most 64 bytes of it), never its bytes as they came
// (E4-GS1: every route's TransferRemote and every builder that reads an address hands its refusal on).
func ParseEthAddress(s string) ([32]byte, error) {
	var out [32]byte
	s = strings.TrimSpace(s)
	switch {
	case len(s) == 42 && s[:2] == "0x":
		h := s[2:]
		b, err := hex.DecodeString(h)
		if err != nil || len(b) != 20 {
			return out, errors.New("not an Ethereum address: " + echo(s) + " is not 0x and 40 hex digits")
		}
		if hasHexLetter(h, 'a', 'f') && hasHexLetter(h, 'A', 'F') && eip55(b) != h {
			return out, errors.New("not an Ethereum address: " + echo(s) + " mixes upper and lower case but is not its EIP-55 checksum (a typo?): write it as the wallet shows it, or all in one case")
		}
		copy(out[12:], b)
		return out, nil
	case len(s) == 64 || len(s) == 66 && s[:2] == "0x":
		b, err := hex.DecodeString(strings.TrimPrefix(s, "0x"))
		if err != nil || len(b) != 32 {
			return out, errors.New("not an Ethereum address: " + echo(s) + " is not 64 hex digits")
		}
		for _, c := range b[:12] {
			if c != 0 {
				return out, errors.New("not an Ethereum address: " + echo(s) + " is 32 bytes whose top twelve are not zero (Hyperlane's bytes32ToAddress would revert the delivery for good)")
			}
		}
		copy(out[:], b)
		return out, nil
	}
	return out, errors.New("not an Ethereum address: " + echo(s) + " (want 0x and 40 hex digits — mixed case only as its EIP-55 checksum — or 64 hex digits whose first 24 are zero)")
}

// EthAddress writes 32 bytes the way every page of this deployment shows an Ethereum address: "0x" and the low
// twenty bytes in their EIP-55 checksum case, when the top twelve are zero; otherwise (no Ethereum address) "0x" and
// all 64 hex digits in lower case, so nothing is ever shown truncated.
func EthAddress(b [32]byte) string {
	for _, c := range b[:12] {
		if c != 0 {
			return "0x" + hex.EncodeToString(b[:])
		}
	}
	return "0x" + eip55(b[12:])
}

// eip55 is a 20-byte address's 40 hex digits in EIP-55 case: a letter is upper case where the matching nibble of
// keccak256 of the lower-case hex (as ASCII) is 8 or more.
func eip55(addr []byte) string {
	lower := hex.EncodeToString(addr)
	h := keccak256.Sum256([]byte(lower))
	out := []byte(lower)
	for i := 0; i < len(out); i++ {
		c := out[i]
		if c < 'a' || c > 'f' {
			continue
		}
		nib := h[i/2] >> 4
		if i%2 == 1 {
			nib = h[i/2] & 0x0f
		}
		if nib >= 8 {
			out[i] = c - 'a' + 'A'
		}
	}
	return string(out)
}

// hasHexLetter is whether s holds a letter between lo and hi.
func hasHexLetter(s string, lo, hi byte) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= lo && s[i] <= hi {
			return true
		}
	}
	return false
}
