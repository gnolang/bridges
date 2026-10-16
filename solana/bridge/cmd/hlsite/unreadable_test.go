package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/gnolang/bridges/solana/bridge/gnolight"
	"github.com/gnolang/bridges/solana/bridge/hl"
)

// bigMsgs is manyMsgs' mailbox of 45 sends where nonce 40 is a stranger's message too big to render (E3-RL1):
// its msg/40 and every msgs/ page holding it fail as the node fails them (out of query gas), and its header reads
// — the mailbox's record of it (vm/qeval) and that block's Dispatch event, its id and hash (block_results).
type bigMsgs struct {
	*manyMsgs
	big     uint32
	msg     hl.Message
	headers int
}

var errOutOfGas = errors.New(`gno query vm/qrender: map[@type:/abci.StringError value:out of gas in location: CPUCycles] types.OutOfGasError{Descriptor:"CPUCycles"}`)

func newBigMsgs(g *mockGno) *bigMsgs {
	m := &bigMsgs{manyMsgs: &manyMsgs{mockGno: g}, big: 40}
	m.msg = hl.Message{Version: hl.Version, Nonce: m.big, Origin: mockGnoDomain, Sender: [32]byte{0xee}, Destination: mockSolDomain, Body: []byte(strings.Repeat("x", 50_000))}
	copy(m.msg.Recipient[:], mockPub("gnot").Bytes())
	return m
}

func (m *bigMsgs) RenderAt(ctx context.Context, pkgPath, path string, height int64) (string, error) {
	switch {
	case strings.HasPrefix(path, "msgs/"):
		from, _ := strconv.Atoi(strings.TrimPrefix(path, "msgs/"))
		if uint32(from) <= m.big && m.big < uint32(from)+20 {
			return "", errOutOfGas
		}
	case strings.HasPrefix(path, "msg/"):
		n, _ := strconv.Atoi(strings.TrimPrefix(path, "msg/"))
		if uint32(n) == m.big {
			return "", errOutOfGas
		}
		msg := mockSendMessage(uint32(n), int64(n+1)*one)
		return fmt.Sprintf(`{"nonce":%d,"id":%q,"root":"%s","height":%d,"message":%q}`, n, idOf(msg), strings.Repeat("00", 32), 100+n, hex.EncodeToString(msg.Bytes())), nil
	}
	return m.manyMsgs.RenderAt(ctx, pkgPath, path, height)
}

func (m *bigMsgs) Query(_ context.Context, path string, data []byte, _ int64) ([]byte, error) {
	m.headers++
	if path == "vm/qeval" && strings.Contains(string(data), fmt.Sprintf("sent.Get(nonceKey(%d)).(*sentMsg)", m.big)) {
		raw := m.msg.Bytes()
		return []byte(fmt.Sprintf("(%q string)\n(%d int64)\n(%d int)", hex.EncodeToString(raw[:hl.HeaderLen]), 100+m.big, len(raw))), nil
	}
	return nil, errors.New("fake: qeval")
}

func (m *bigMsgs) Request(_ context.Context, method string, params map[string]any, result any) error {
	m.headers++
	if method != "block_results" || params["height"] != strconv.Itoa(int(100+m.big)) {
		return errors.New("fake: request")
	}
	raw := m.msg.Bytes()
	d := gnolight.Dispatch{ID: hl.ID(raw), Nonce: m.big, Origin: m.msg.Origin, Destination: m.msg.Destination, Sender: m.msg.Sender, Recipient: m.msg.Recipient, Hash: gnolight.MessageHash(raw)}
	e := d.ToEvent(mockNS + "hlmailbox")
	var attrs []map[string]string
	for _, a := range e.Attributes {
		attrs = append(attrs, map[string]string{"key": a.Key, "value": a.Value})
	}
	ev := map[string]any{"@type": gnolight.EventTypeURL, "type": e.Type, "attrs": attrs, "pkg_path": e.PkgPath}
	b, _ := json.Marshal(map[string]any{"height": params["height"], "results": map[string]any{"deliver_tx": []any{map[string]any{"ResponseBase": map[string]any{"Error": nil, "Events": []any{ev}}}}}})
	return json.Unmarshal(b, result)
}

// E3-RL1 on the site: a stranger's message too big to render puts every msgs/ page holding it past the node's query
// gas, and the activity's dispatches stopped there (an error, no newer send listed) until twenty more messages
// pushed it out of the listing. The page that cannot be read is read message by message; the one whose own page
// cannot be read is listed from its header, as "other", and the sends around it as before.
func TestActivityPastAnUnreadableMessage(t *testing.T) {
	s, g, _ := mockServer(false, siteDir, fixedNow)
	big := newBigMsgs(g)
	s.gno = big
	ch, other := &s.cfg.Chains[0], s.others["solana"]
	st := s.activityNow(ctx, ch, other)
	for _, e := range st.errors {
		if strings.Contains(e, "dispatches") {
			t.Fatalf("the dispatches stopped at the stranger's message: %v", st.errors)
		}
	}
	if len(st.dispatch) != activityList || st.count != 45 {
		t.Fatalf("the listing holds the newest %d of 45: %d, count %d", activityList, len(st.dispatch), st.count)
	}
	raw := big.msg.Bytes()
	sum := gnolight.MessageHash(raw)
	if d := st.dispatch[40]; d == nil || d.Kind != "other" || d.Height != 140 || d.Hash != hex.EncodeToString(sum[:]) || d.ID != hex.EncodeToString(func() []byte { id := hl.ID(raw); return id[:] }()) {
		t.Fatalf("the stranger's message, from its header: %+v", st.dispatch[40])
	}
	for _, n := range []uint32{39, 41, 44} {
		if d := st.dispatch[n]; d == nil || d.Kind != "send" || d.Amount != int64(n+1)*one {
			t.Fatalf("the send #%d beside it: %+v", n, d)
		}
	}
	if big.headers != 2 {
		t.Fatalf("the header read %d times, want once (the record and the block)", big.headers/2)
	}
}
