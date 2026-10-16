package hlfees

import "strconv"

// echoMax is the most bytes of a caller's string a refusal shows (echo).
const echoMax = 64

// echo is a caller's string as a refusal of this realm shows it (the round-4 audits' E4-GS1 and E4-RL6):
// strconv.QuoteToASCII of at most its first echoMax bytes, and its length when longer — ASCII whatever the caller sent,
// and far under the 1,024 bytes at which gno cuts a panic's text by byte offset, so a failed transaction's error is
// valid UTF-8 in the block's results and the results root rebuilt from the public RPC's JSON is the header's (a byte
// that is not UTF-8 there would make nothing in the block provable to Ethereum: one failed call a block, from any key,
// would stop gno.land → Ethereum). hlmailbox's echo.gno says more; eth/scripts/check-echoes.go holds every panic, error
// and event of this realm to it.
func echo(s string) string {
	if len(s) <= echoMax {
		return strconv.QuoteToASCII(s)
	}
	return strconv.QuoteToASCII(s[:echoMax]) + "... (" + strconv.Itoa(len(s)) + " bytes)"
}
