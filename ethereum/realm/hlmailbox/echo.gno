package hlmailbox

import "strconv"

// echoMax is the most bytes of a caller's string a refusal shows (echo).
const echoMax = 64

// echo is a caller's string as a refusal of this realm shows it — a panic's text, an error's (the round-4 audits'
// E4-GS1 and E4-RL6): strconv.QuoteToASCII of at most its first echoMax bytes, and its length when longer. A failed
// transaction's panic text goes into the block's results as it is; a byte that is not UTF-8 there comes back from the
// public RPC's JSON as U+FFFD, the results root rebuilt from that JSON is not the header's, and nothing in the block
// can then be proven to Ethereum — so one failed call a block, from any key, would stop gno.land → Ethereum. The
// quote is ASCII whatever the caller sent, and short: a message that echoes stays far under the 1,024 bytes at which
// gno cuts a panic's text, and its Log line, by byte offset (a cut that could split one of this text's own runes).
// eth/scripts/check-echoes.go holds every panic, error and event of this deployment to it. A plain function over
// values, as ParseEthAddress: the routes and r/hlgov keep their own copy.
func echo(s string) string {
	if len(s) <= echoMax {
		return strconv.QuoteToASCII(s)
	}
	return strconv.QuoteToASCII(s[:echoMax]) + "... (" + strconv.Itoa(len(s)) + " bytes)"
}

// mustBeASCII refuses, with a fixed text, a caller's string holding a byte outside ASCII before it reaches
// p/hlroute's parsers (a payouts flag's range here: hlroute.ParseRange), which echo what they refuse as it came and
// cannot change (shared with the live Solana deployment, immutable): their text around the echo is ASCII, so an ASCII
// argument keeps every such refusal valid UTF-8, cut anywhere (E4-GS1).
func mustBeASCII(what, s string) {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			panic("hlmailbox: the " + what + " holds a byte outside ASCII, at offset " + strconv.Itoa(i) + ": refused before it is read") // echo-ok(what): every caller names its parameter with a constant
		}
	}
}
