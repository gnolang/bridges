package hlbubble

import (
	"crypto/sha256"
	"encoding/hex"

	"gno.land/r/g1leu8d2vsplhehcfkjg50mwgdpxdkt8tztu95wr/eth/hlmailbox"
)

// The mainnet guard (the round-2 realms audit's E2-RL1; hlmailbox's mainnet.gno says why it holds digests): on
// gnoland-1, init refuses a deploy whose launch router is still one of the dev tree's two — the vectors' test routes,
// hlmailbox's token placeholders — naming it; on every other chain it reads nothing. The router must be the BUBBLE HypERC20 proxy's
// address, which the stage step writes.

// devRouters are the sha256 of the dev tree's two routers (r/hlgnot's and r/hlbubble's launchRouter, hlmailbox's
// launchEthToken0 and launchEthToken1), each as the 32 bytes ParseEthAddress reads it to.
var devRouters = [...]string{
	"79a3ac7ee4282caf9ff1dc5f561b2f32ae3959e61a8736ba9fdf87584dd020b4",
	"b07d0aa94b3b84d1dde8acbd0387cf2d6e0043853793d8fb7dde8439c2e3e747",
}

// devPlaceholder is why init refuses router, the launch router as compiled, on the chain whose gno.land domain is
// domain, or "": on gnoland-1 (hlmailbox.DomainMainnet) only.
func devPlaceholder(domain uint32, router [32]byte) string {
	if domain != hlmailbox.DomainMainnet {
		return ""
	}
	h := sha256.Sum256(router[:])
	d := hex.EncodeToString(h[:])
	for _, x := range devRouters {
		if x == d {
			return "hlbubble: launchRouter is a dev tree placeholder, " + hlmailbox.EthAddress(router) + ": refused on gnoland-1, where the stage step writes the deployed value"
		}
	}
	return ""
}
