package agent

import (
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/common"

	"github.com/gnolang/bridges/ethereum/bridge/ethx"
	"github.com/gnolang/bridges/ethereum/bridge/gnoside"
)

// daoProposals renders GovDAO's list and each open proposal's page in the fake: id → executor realm, title, text.
func (r *monRig) daoProposals(props map[int64][3]string) {
	var ids []int64
	for id := range props {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	var list strings.Builder
	list.WriteString("# GovDAO\n\n")
	if len(ids) == 0 {
		list.WriteString("No proposals yet.\n\n") // as GovDAO's render says it (a page of no item that does not is not read: E5-GT2)
	}
	for _, id := range ids {
		p := props[id]
		fmt.Fprintf(&list, "### [Prop #%d - %s](/r/gov/dao:%d)\n\nAuthor: g1jg8mtutu9khhfwc4nxmuhcpftf0pajdhfvsqf5\n\nStatus: ACTIVE\n\n", id, p[1], id)
		r.gno.set(gnoside.DAOPath, fmt.Sprint(id), fmt.Sprintf("## Prop #%d - %s\nAuthor: g1jg8mtutu9khhfwc4nxmuhcpftf0pajdhfvsqf5\n\n%s\n\nExecutor created in: `%s`\n\n---\n\n### Stats\n- **Proposal is open for votes**\n- YES PERCENT: 0%%\n", id, p[1], p[2], p[0]))
	}
	r.gno.set(gnoside.DAOPath, "", list.String())
}

// kind6Body is r/hlgov's kind 6 enrolling a realm as a token's router for gno.land's domain.
func kind6Body(n uint64, token byte, realm string) []byte {
	p := []byte{token}
	p = binary.BigEndian.AppendUint32(p, m3bGnoDom)
	k := gnoside.RealmKey(realm)
	return body(6, n, append(p, k[:]...)...)
}

func (r *monRig) crossAlarm(t *testing.T, key string) string {
	t.Helper()
	text, ok := r.m.Alarm.Standing()[key]
	if !ok {
		t.Fatalf("no alarm %s: %v", key, r.m.Alarm.StandingKeys())
	}
	return text
}

// E4-CF5: a successor's refund proposed while the token's router on Ethereum is still the predecessor (the kind 6 that
// enrolls the successor pending on HlAdmin) is NOT ENROLLED ON ETHEREUM — alarmed, the page not green — and an id
// Ethereum's mailbox processed already is DELIVERED; once the router is the successor and nothing is delivered, the
// finding ends.
func TestF15bRefundGateOnEthereum(t *testing.T) {
	r := newMonRig(t)
	route := m3bNS + "hlgnot"
	id1, id2 := strings.Repeat("a1", 32), strings.Repeat("b2", 32)
	r.chain.view(m3bGNOT, "routers", func(uint64, []any) []any { return []any{gnoside.RealmKey(m3bNS + "hlgnot-old")} })
	r.chain.view(m3bMailbox, "delivered", func(_ uint64, in []any) []any { k := in[0].([32]byte); return []any{hex.EncodeToString(k[:]) == id1} })
	r.chain.mine(110, 110)
	r.decide(1, kind6Body(1, 0, route), 101, kind6Body(1, 0, route), true) // the kind 6, pending on HlAdmin
	r.gno.set(m3bMailPath, "json", strings.Replace(m3bMailboxJSON(nil, 2, []string{"", "", "", "", monWatchGno, ""}), `"routes":{}`, fmt.Sprintf(`"routes":{"%s":{"enrolled":{"height":10,"at":%d}}}`, route, r.now.Unix()-20*86400), 1))
	r.daoProposals(map[int64][3]string{5: {route, "eth/hlgnot: refund", "refund 300 GNOT of sends imported from the predecessor (14 days after kind 6's dispatch enrolled this realm) to their senders: " + id1 + "," + id2}})
	v, ok, why := r.view(t)
	text := r.crossAlarm(t, "refund:gate:proposal:5")
	t.Logf("refund:gate:proposal:5: %s", text)
	if ok || !strings.Contains(text, "NOT ENROLLED ON ETHEREUM") || !strings.Contains(text, "pending on HlAdmin as #1") || !strings.Contains(text, id1+" DELIVERED") || !strings.Contains(text, id2+" undelivered") {
		t.Fatalf("the gate: green %v %s; %s", ok, why, text)
	}
	if len(v.Proposals) != 1 || !strings.Contains(v.Proposals[0].Text, "NOT ENROLLED ON ETHEREUM") {
		t.Fatalf("the proposal's line does not carry the gate: %+v", v.Proposals)
	}
	// the kind 6 executed: the router is the successor; nothing delivered — the finding ends
	r.chain.view(m3bGNOT, "routers", func(uint64, []any) []any { return []any{gnoside.RealmKey(route)} })
	r.chain.view(m3bMailbox, "delivered", func(uint64, []any) []any { return []any{false} })
	r.mu.Lock()
	delete(r.pending, 1)
	r.mu.Unlock()
	r.view(t)
	if r.alarmed("refund:gate:proposal:5") {
		t.Fatalf("the finding stands once enrolled: %s", r.m.Alarm.Standing()["refund:gate:proposal:5"])
	}
}

// A queued refund decision is read every poll until it applies, and GovDAO's apply proposal against it too.
func TestF15bQueuedRefundAndItsApply(t *testing.T) {
	r := newMonRig(t)
	route := m3bNS + "hlgnot"
	id1 := strings.Repeat("a1", 32)
	r.chain.view(m3bGNOT, "routers", func(uint64, []any) []any { return []any{gnoside.RealmKey(m3bNS + "hlgnot-old")} })
	refund := "refund 300 GNOT of sends imported from the predecessor (14 days after kind 6's dispatch enrolled this realm) to their senders: " + id1
	r.gno.set(route, "decisions.json/1", fmt.Sprintf(`[{"id":1,"kind":"refund","text":%q,"state":"pending","made":%d,"recorded":%d,"due":%d}]`, refund, r.now.Unix()-86400, r.now.Unix()-86400, r.now.Unix()+6*86400))
	r.gno.set(route, "decisions.json/2", "[]")
	r.daoProposals(map[int64][3]string{6: {route, "eth/hlgnot: apply refund", "apply refund decision 1 at once (" + refund + "), after hlwatch's check that the token's router on Ethereum is this realm"}})
	_, ok, _ := r.view(t)
	queued := r.crossAlarm(t, "refund:gate:queued:"+route+"#1")
	apply := r.crossAlarm(t, "refund:gate:apply:6")
	t.Logf("queued: %s\napply: %s", queued, apply)
	if ok || !strings.Contains(queued, "NOT ENROLLED ON ETHEREUM") || !strings.Contains(queued, "NO NOTE") || !strings.Contains(apply, "applies "+route+"'s queued refund #1, not mature") {
		t.Fatal("the queued refund and its apply are not gated")
	}
}

// A route's per-message maximum raised above the gate's is read before the vote (§5.4: the kind 12 first).
func TestF15bMaximumAboveTheGate(t *testing.T) {
	r := newMonRig(t)
	route := m3bNS + "hlgnot"
	r.daoProposals(map[int64][3]string{
		7: {route, "eth/hlgnot: set maximum", "set maximum to 30000 GNOT (now 25000 GNOT); raise the Ethereum limit (kind 12) first; a send above the Ethereum maximum strands"},
		8: {route, "eth/hlgnot: set maximum", "set maximum to 20000.5 GNOT (now 25000 GNOT); raise the Ethereum limit (kind 12) first; a send above the Ethereum maximum strands"},
	})
	_, ok, _ := r.view(t)
	text := r.crossAlarm(t, "maximum:proposal:7")
	t.Logf("maximum:proposal:7: %s", text)
	if ok || !strings.Contains(text, "RAISE ABOVE THE ETHEREUM MAXIMUM: 30000000000 base units against the gate's 25000000000") || r.alarmed("maximum:proposal:8") {
		t.Fatalf("the maximum check: green %v; 8 alarmed %v", ok, r.alarmed("maximum:proposal:8"))
	}
}

// r/hlgov's recipient switch proposed while HlAdmin's kind 9 is unexecuted alarms; r/hlgov dispatching to an HlAdmin
// that handed every authority on (retired) alarms RETIRED.
func TestF15bRecipientSwitchNotes(t *testing.T) {
	r := newMonRig(t)
	gov := m3bNS + "hlgov"
	succ := common.HexToAddress("0x00000000000000000000000000000000000000aa")
	r.gno.set(gov, "json", fmt.Sprintf(`{"last":0,"next":1,"recipient":%q}`, m3bAdmin.Hex()))
	r.daoProposals(map[int64][3]string{8: {gov, "eth/hlgov: set decision recipient", fmt.Sprintf("set decision recipient to %s, replacing %s (applies only while %s is the recipient)", succ.Hex(), m3bAdmin.Hex(), m3bAdmin.Hex())}})
	_, ok, _ := r.view(t)
	text := r.crossAlarm(t, "switch:proposal:8")
	t.Logf("switch:proposal:8: %s", text)
	if ok || !strings.Contains(text, "UNEXECUTED") {
		t.Fatalf("the switch proposed before the kind 9: green %v %s", ok, text)
	}
	// the kind 9 executed: HlAdmin's successor is succ (inherited), and r/hlgov still dispatches to the retired one
	r.chain.view(m3bAdmin, "successor", func(uint64, []any) []any { return []any{succ} })
	r.chain.view(succ, "predecessor", func(uint64, []any) []any { return []any{m3bAdmin} })
	r.chain.view(succ, "inherited", func(uint64, []any) []any { return []any{true} })
	r.daoProposals(map[int64][3]string{})
	r.view(t)
	retired := r.crossAlarm(t, "switch:retired")
	t.Logf("switch:retired: %s", retired)
	if !strings.Contains(retired, "RETIRED") || r.alarmed("switch:proposal:8") {
		t.Fatalf("a retired recipient: %s; the closed proposal still alarmed: %v", retired, r.alarmed("switch:proposal:8"))
	}
	_ = ethx.Admin
}

// A cross-chain finding whose reading did not run this time (GovDAO's list unread) stands as it was; it ends only once a
// reading that ran no longer finds it.
func TestF15bCrossFindingStandsOverAnUnreadList(t *testing.T) {
	r := newMonRig(t)
	route := m3bNS + "hlgnot"
	r.daoProposals(map[int64][3]string{7: {route, "eth/hlgnot: set maximum", "set maximum to 30000 GNOT (now 25000 GNOT); raise the Ethereum limit (kind 12) first; a send above the Ethereum maximum strands"}})
	r.view(t)
	r.crossAlarm(t, "maximum:proposal:7")
	r.gno.mu.Lock()
	delete(r.gno.renders, gnoside.DAOPath+":") // the list does not read
	r.gno.mu.Unlock()
	r.view(t)
	r.crossAlarm(t, "maximum:proposal:7")
	r.daoProposals(map[int64][3]string{}) // read again: the proposal is gone
	r.view(t)
	if r.alarmed("maximum:proposal:7") {
		t.Fatal("the finding outlived its proposal")
	}
}
