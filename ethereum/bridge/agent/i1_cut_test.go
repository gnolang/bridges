package agent

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/common"

	"github.com/gnolang/bridges/solana/bridge/hl"
	"github.com/gnolang/bridges/ethereum/bridge/ethx"
	"github.com/gnolang/bridges/ethereum/bridge/gnoside"
)

// The integration lane I1's tests of the machines (audit round 3's fold, the cross-lane follow-ups): the monitor's
// read-twice cut over an open line of migrations.

// The monitor's hlmailbox count is read again after the routes AND their lines of migrations (E3-GT2 as the Solana port
// found it: bridge be66bc47). A token's open line is read after its route — the successor's json, and so its lock —
// and the count's second read came before it, so a send to the line landing between the two was in the lock and not
// counted in flight: a forged mint of its amount passed the poll unseen. Read after the line, the cut moves and is
// taken again at once (round 6, E6-GT1), which sees the forged mint in the same poll (broken: flagged).
func TestMonitorCountReadAfterTheLines(t *testing.T) {
	r := newMonRig(t) // GNOT supply 1,000 on Ethereum
	r.chain.view(m3bMailbox, "delivered", func(uint64, []any) []any { return []any{false} })
	r.chain.setSupply(m3bGNOT, 1, 1300) // a forged mint of 300
	succ := m3bNS + "hlgnot2"
	// GNOT's line is open: hlgnot migrated to hlgnot2 (its counters moved there at page 0), whose import has ended
	r.gno.set(m3bNS+"hlgnot", "json", `{"live":false,"lock":0,"owed":0,"voided":0,"lost":0,"fee":5000000,"migratedTo":"`+succ+`"}`)
	// a send of 300 through the successor: its lock holds it — the send lands before the line is read …
	r.gno.set(succ, "json", `{"live":true,"lock":1300,"owed":0,"voided":0,"lost":0,"fee":5000000,"imported":1}`)
	m := hl.Message{Version: 3, Nonce: 0, Origin: m3bGnoDom, Sender: gnoside.RealmKey(succ), Destination: m3bEthDom, Recipient: ethx.AddressTo32(m3bGNOT), Body: hl.TokenMessage(ethx.AddressTo32(common.HexToAddress("0x00000000000000000000000000000000000a11ce")), 300, nil)}
	raw := m.Bytes()
	id := hl.ID(raw)
	r.gno.set(m3bMailPath, "msg/0", fmt.Sprintf(`{"nonce":0,"id":%q,"root":"%s","height":55,"message":%q}`, hex.EncodeToString(id[:]), strings.Repeat("00", 32), hex.EncodeToString(raw)))
	landed := false
	r.gno.mu.Lock()
	r.gno.afterRender = func(pkg, path string) {
		if pkg == succ && path == "json" && !landed {
			landed = true // … and hlmailbox's count holds it from the line's read on
			var mj map[string]any
			_ = json.Unmarshal([]byte(m3bMailboxJSON(nil, 2, []string{"", "", "", "", monWatchGno, ""})), &mj)
			mj["count"] = 1
			b, _ := json.Marshal(mj)
			r.gno.set(m3bMailPath, "json", string(b))
		}
	}
	r.gno.mu.Unlock()
	if err := r.poll(); err != nil {
		t.Fatal(err)
	}
	ident, _ := r.m.Status()["identity"].(map[string]any)["GNOT"].(map[string]any)
	if !r.alarmed("identity:GNOT:flag") || len(r.gnoFlags()) == 0 || ident["inflightOut"] != float64(300) {
		t.Fatalf("the cut over a send to the open line landing in its window was not taken again, or the forged mint not flagged: %v (identity %v)", r.m.Alarm.StandingKeys(), ident)
	}
}
