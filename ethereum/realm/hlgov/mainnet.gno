package hlgov

import (
	"crypto/sha256"
	"strconv"

	"gno.land/r/g1leu8d2vsplhehcfkjg50mwgdpxdkt8tztu95wr/eth/hlmailbox"
)

// The mainnet guard (the round-2 realms audit's E2-RL1; hlmailbox's mainnet.gno says why it holds digests): on
// gnoland-1, init refuses a deploy whose Ethereum seats are still the dev tree's, or whose decision recipient is the
// dev tree's HlAdmin, naming which; on every other chain it reads nothing. The recipient and the five proxies are
// hlmailbox's compiled contracts, which hlmailbox's own guard has held before this realm could deploy; the recipient
// is held here again, being where every decision of this realm goes.

// devEthSeats are the sha256 of the dev tree's six Ethereum seats (launchEthSeat1 to launchEthSeat6, the patterned
// addresses), each as the 32 bytes ParseEthAddress reads it to.
var devEthSeats = [...]string{
	"f5088ec0ab9381a2d6f2e98445d5a44b8e01513144e88b1165c496cb4fa49802",
	"01b0538fe8b24dfec8c19d5dd1a61a0e70fcc61a3e88d943c5912c6100b5eb69",
	"04fd09391ff8fb44ccef1c4675c93f560d6ab1b22b234e2c58d819a34f68d76b",
	"5c17edeb2b54db1e788a604ba8496c245e6ec3555a00120f62f78e98ef8a03a4",
	"b142efd13abe4d4d22035425ec450b267f7a7db3fa0682040ad8310ff850700b",
	"bb4100618e7d40a0d835cbfdba5489cb614b645ec4b27e3414387b605e66ffc9",
}

// devHlAdmin is the sha256 of the dev tree's HlAdmin (hlmailbox's launchEthHlAdmin), as 32 bytes.
const devHlAdmin = "c1590f5b1894d62193220ba8a3fd239aa81ac47238ebe5479de0384f40fe3ae7"

// devPlaceholder names the first of this realm's launch values that is one of the dev tree's, on the chain whose
// gno.land domain is domain, or "": on gnoland-1 (hlmailbox.DomainMainnet) only. Each seat is held against all six
// dev seats, so a dev seat staged on another seat's line is refused too.
func devPlaceholder(domain uint32, seats [seatCount + 1][32]byte, hlAdmin [32]byte) string {
	if domain != hlmailbox.DomainMainnet {
		return ""
	}
	const tail = ": refused on gnoland-1, where the stage step writes the deployed value"
	for n := 1; n <= seatCount; n++ {
		if isDev(devEthSeats[:], seats[n]) {
			return "hlgov: launchEthSeat" + strconv.Itoa(n) + " is a dev tree placeholder, " + ea(seats[n]) + tail
		}
	}
	if isDev([]string{devHlAdmin}, hlAdmin) {
		return "hlgov: the decision recipient is the dev tree's HlAdmin, " + ea(hlAdmin) + tail
	}
	return ""
}

// isDev is whether k's sha256 is one of digests.
func isDev(digests []string, k [32]byte) bool {
	h := sha256.Sum256(k[:])
	d := hx(h[:])
	for _, x := range digests {
		if x == d {
			return true
		}
	}
	return false
}
