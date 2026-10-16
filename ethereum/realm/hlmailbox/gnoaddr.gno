package hlmailbox

import hl "gno.land/p/g1leu8d2vsplhehcfkjg50mwgdpxdkt8tztu95wr/hyperlane/v0"

// CanonicalAddress is a gno address in the one spelling every address gno writes takes — bech32 with BIP-173's
// checksum, prefix g, in lower case: chain.PackageAddress's, a caller's (cur.Previous().Address()), p/hyperlane's
// Bytes32ToAddress — or "" when a is no gno address. IsValid takes three more spellings of the same twenty bytes:
// all in upper case, and either case with bech32m's checksum (BIP-350; tm2's decoder accepts both checksum
// constants), none of which any caller's address or any address list ever equals. So a typed gno address is compared
// with a list, or stored where a caller's address is compared with it, only in this spelling (eth F9, its review's
// findings 1 and 2): the routes' credit and sweep votes, a seat (parseSeatChanges), hlfees' treasurer, and init's
// guard on gnoland-1 (canonical, mainnet.gno). It is the round trip through p/hyperlane: AddressToBytes32 decodes
// any spelling IsValid takes (gno's crypto/bech32: one case, either checksum; then prefix g and twenty bytes), and
// Bytes32ToAddress writes the canonical one. A plain function over values, as ParseEthAddress: it reads and writes no
// state of this realm.
func CanonicalAddress(a address) address {
	b, err := hl.AddressToBytes32(a)
	if err != nil {
		return ""
	}
	c, err := hl.Bytes32ToAddress(b)
	if err != nil {
		return ""
	}
	return c
}
