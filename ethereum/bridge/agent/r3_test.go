package agent

import (
	"encoding/hex"
	"fmt"
	"strings"
	"testing"

	"github.com/gnolang/bridges/solana/bridge/hl"
	"github.com/gnolang/bridges/ethereum/bridge/ethx"
	"github.com/gnolang/bridges/ethereum/bridge/gnoside"

	"github.com/ethereum/go-ethereum/common"
)

// putMsg puts message 0 on the rig's hlmailbox: from sender to the GNOT token, for amount, dispatched at height h.
func (r *signerRig) putMsg(sender [32]byte, amount uint64, h int64) [32]byte {
	r.t.Helper()
	m := hl.Message{Version: 3, Nonce: 0, Origin: m3bGnoDom, Sender: sender, Destination: m3bEthDom, Recipient: ethx.AddressTo32(m3bGNOT), Body: hl.TokenMessage(ethx.AddressTo32(common.HexToAddress("0x00000000000000000000000000000000000a11ce")), amount, nil)}
	raw := m.Bytes()
	id := hl.ID(raw)
	r.gno.set(m3bMailPath, "msg/0", fmt.Sprintf(`{"nonce":0,"id":%q,"root":"%s","height":%d,"message":%q}`, hex.EncodeToString(id[:]), strings.Repeat("00", 32), h, hex.EncodeToString(raw)))
	r.gno.set(m3bMailPath, "json", `{"domain":1198419796,"live":true,"count":1,"signers":{"keys":[],"threshold":2},"seats":[],"routes":{}}`)
	return id
}

// E3-CF1 (the plan red team's E3-RT1, the Go trust audit's E3-GT1): hlmailbox's Dispatch is open to anyone, and a token
// refuses a message whose sender is not its enrolled router, so a stranger's message to a token never delivers. Counted
// as a send in flight it stood there for good with whatever amount its body said: every signer halted (supply plus in
// flight over the lock) and the monitors flagged, for one transaction's fee. Only a route realm of the token's line
// sends the token; the route's own send still counts.
func TestStrangersMessageIsNoSend(t *testing.T) {
	t.Run("a stranger's message", func(t *testing.T) {
		r := newSignerRig(t)                                           // GNOT supply 1,000 on Ethereum
		r.gno.set(m3bNS+"hlgnot", "json", m3bRouteJSON(1000, 0, 0, 0)) // locked 1,000: nothing in flight
		r.putMsg([32]byte{0xee}, 1, 55)
		r.chain.view(m3bMailbox, "delivered", func(uint64, []any) []any { return []any{false} })
		r.round()
		st := r.status()
		ident := st["identity"].(map[string]any)["GNOT"].(map[string]any)
		if st["signing"] != true || ident["verdict"] != "holds" || ident["undelivered"].(int64) != 0 {
			t.Fatalf("a stranger's message stopped the signer: %v", st)
		}
	})
	t.Run("the route's own send", func(t *testing.T) {
		r := newSignerRig(t)
		r.gno.set(m3bNS+"hlgnot", "json", m3bRouteJSON(1300, 0, 0, 0)) // locked 1,300: the send's 300 in flight
		r.putMsg(gnoside.RealmKey(m3bNS+"hlgnot"), 300, 55)
		r.chain.view(m3bMailbox, "delivered", func(uint64, []any) []any { return []any{false} })
		r.round()
		st := r.status()
		ident := st["identity"].(map[string]any)["GNOT"].(map[string]any)
		if st["signing"] != true || ident["verdict"] != "holds" || ident["undelivered"].(int64) != 300 {
			t.Fatalf("the route's send in flight: %v", st)
		}
	})
}
