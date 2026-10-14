// PKGPATH: gno.land/r/g1leu8d2vsplhehcfkjg50mwgdpxdkt8tztu95wr/usebubble
package usebubble

// The realm from outside: every argument a transaction can carry is a string, an address or an int64, and the
// reads answer on a realm that was loaded with no deployer at all (a filetest has none — the admin and the minter
// are named constants precisely so that this is not a dead realm, unlike a realm reading OriginCaller at init).

import (
	"chain/runtime"
	"testing"

	"gno.land/r/g1leu8d2vsplhehcfkjg50mwgdpxdkt8tztu95wr/bubble"
)

func main(cur realm) {
	jae := address("g1ecsuj0q572jr0dhu29q9njtnmw03hyu7tyyvv6")
	println("name:      ", bubble.GetName())
	println("symbol:    ", bubble.GetSymbol())
	println("decimals:  ", bubble.GetDecimals())
	println("supply:    ", bubble.TotalSupply())
	println("premine:   ", bubble.BalanceOf(jae) == bubble.Premined())
	println("admin set: ", bubble.Admin() != "")
	println("minter set:", bubble.Minter() != "")
	println("paused:    ", bubble.Paused())
	println("day cap:   ", bubble.DayCap())
	println("votes:     ", bubble.VotesOf(jae) == bubble.TotalSupply())
	testing.SetHeight(runtime.ChainHeight() + 100000)
	println("epoch > 1: ", bubble.Epoch() > 1)
	println("past total:", bubble.PastTotalVotes(bubble.Epoch()-1) == bubble.TotalSupply())
	r, ok := bubble.KindRules("govern:reserve")
	println("reserve:   ", ok, r.QuorumBps, r.ThresholdBps)
	println("page:      ", len(bubble.Render("")) > 0)
	println("json:      ", len(bubble.Render("json")) > 0)
}

// Output:
// name:       Bubble
// symbol:     BUBBLE
// decimals:   6
// supply:     10000000000000
// premine:    true
// admin set:  true
// minter set: true
// paused:     false
// day cap:    250000000000
// votes:      true
// epoch > 1:  true
// past total: true
// reserve:    true 2000 6600
// page:       true
// json:       true
