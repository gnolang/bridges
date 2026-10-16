package gnoside

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gnolang/bridges/solana/bridge/gno"
	"github.com/gnolang/bridges/solana/bridge/gnolight"
	"github.com/gnolang/bridges/solana/bridge/hl"
)

// fakeNode is a gno node in memory: canned renders.
type fakeNode struct {
	mu      sync.Mutex
	renders map[string]string
	head    gno.Head
}

func (f *fakeNode) RenderAt(_ context.Context, pkg, path string, _ int64) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.renders[pkg+":"+path]
	if !ok {
		return "", fmt.Errorf("fake: no render %s:%s", pkg, path)
	}
	return s, nil
}
func (f *fakeNode) Status(context.Context) (gno.Head, error) { return f.head, nil }
func (f *fakeNode) Block(_ context.Context, h int64) (gno.Head, error) {
	return gno.Head{Height: h, Time: f.head.Time}, nil
}

const mbPath = "gno.land/r/g1leu8d2vsplhehcfkjg50mwgdpxdkt8tztu95wr/eth/hlmailbox"

// The renders decode as the realms write them: the mailbox's rule and flags, a route's marks in every form, a
// message checked against its id, nonce and origin.
func TestRenders(t *testing.T) {
	ctx := context.Background()
	key := strings.Repeat("ab", 32)
	f := &fakeNode{renders: map[string]string{
		mbPath + ":json": `{"domain":1198419796,"count":2,"live":true,"governor":"g1gov","yielded":0,"signers":{"keys":["` + key + `"],"threshold":1,"groupA":[],"groupB":[]},` +
			`"routes":{"gno.land/r/x/eth/hlgnot":{"sends":true,"flags":["sends by seat 1 (g1abc) at 120 naming a,b"],"enrolled":{"height":5,"at":1700000000}}},"clocks":{"factor":8,"delay":75600,"deadline":151200},` +
			`"ethContracts":{"mailbox":"0x0404040404040404040404040404040404040404","hlAdmin":"0x0101010101010101010101010101010101010101"}}`,
		"gno.land/r/x/eth/hlgov:json": `{"address":"g1gov","live":true,"governor":"","yielded":0,"destination":31337,"recipient":"0x0101010101010101010101010101010101010101","former":["0x5aAeb6053F3E94C9b9A09f33669435E7Ef1BeAed"],` +
			`"proxies":{"mailbox":"0x0404040404040404040404040404040404040404","token0":"0x1","token1":"0x2","client":"0x3","gate":"0x4"},"start":1,"last":7,"next":8,"clocks":{"factor":1},"decisions":3}`,
		"r:send/1": `{"id":"1","from":"g1x","amount":9,"refunded":1700000001,"writtenOff":false}`,
		"r:send/2": `{"id":"2","from":"g1x","amount":9,"refunded":"0","writtenOff":true}`,
		"r:send/3": `null`,
	}}
	mb, err := MailboxAt(ctx, f, mbPath, 0)
	if err != nil {
		t.Fatal(err)
	}
	if mb.DAOGoverns() || mb.Clocks.DeadlineDuration() != 151200*time.Second || mb.EthContracts["hlAdmin"] != "0x0101010101010101010101010101010101010101" {
		t.Fatalf("%+v", mb)
	}
	gov, err := GovAt(ctx, f, "gno.land/r/x/eth/hlgov", 0)
	if err != nil || gov.Recipient != "0x0101010101010101010101010101010101010101" || len(gov.Former) != 1 || gov.Proxies["gate"] != "0x4" || gov.Next != 8 || gov.Destination != 31337 {
		t.Fatalf("%+v %v", gov, err)
	}
	r, err := mb.Rule()
	if err != nil || r.Threshold != 1 || len(r.Keys) != 1 {
		t.Fatalf("%+v %v", r, err)
	}
	rf := mb.Routes["gno.land/r/x/eth/hlgnot"]
	fl := rf.StandingFlags()
	if len(fl) != 1 || fl[0].Fn != "sends" || fl[0].Seat != 1 || len(fl[0].IDs) != 2 {
		t.Fatalf("%+v", fl)
	}
	if h, at := rf.Enrollment(); h != 5 || at != 1700000000 {
		t.Fatalf("%d %d", h, at)
	}
	s1, _ := SendAt(ctx, f, "r", "1", 0)
	s2, _ := SendAt(ctx, f, "r", "2", 0)
	s3, err := SendAt(ctx, f, "r", "3", 0)
	if !bool(s1.Refunded) || bool(s2.Refunded) || !bool(s2.WrittenOff) || s3 != nil || err != nil {
		t.Fatalf("%+v %+v %+v %v", s1, s2, s3, err)
	}
	m := hl.Message{Version: 3, Nonce: 1, Origin: hl.DomainGnoTest, Destination: 31337, Body: []byte{1}}
	raw := m.Bytes()
	id := hl.ID(raw)
	good := fmt.Sprintf(`{"nonce":1,"id":%q,"root":%q,"height":77,"message":%q}`, hex.EncodeToString(id[:]), strings.Repeat("00", 32), hex.EncodeToString(raw))
	msg, err := ParseMsg(good, hl.DomainGnoTest, 1)
	if err != nil || msg.Height != 77 || msg.ID != id {
		t.Fatalf("%+v %v", msg, err)
	}
	if _, err := ParseMsg(good, hl.DomainGnoTest, 2); err == nil {
		t.Fatal("a message under another nonce taken")
	}
	if _, err := ParseMsg(good, hl.DomainGnoMainnet, 1); err == nil {
		t.Fatal("a message of another origin taken")
	}
}

// A line of migrations is followed to its end and back to its first realm.
func TestMigrationLine(t *testing.T) {
	ctx := context.Background()
	route := func(migratedTo string, importing bool, imported int, lock int64) string {
		return fmt.Sprintf(`{"live":true,"migratedTo":%q,"importing":%v,"imported":%d,"lock":%d}`, migratedTo, importing, imported, lock)
	}
	f := &fakeNode{renders: map[string]string{
		"gno.land/r/a:json": route("gno.land/r/b", false, 0, 0),
		"gno.land/r/b:json": route("gno.land/r/c", false, 3, 0),
		"gno.land/r/c:json": route("", false, 2, 1000),
	}}
	mb := &Mailbox{Routes: map[string]RouteFlags{"gno.land/r/b": {Predecessor: "gno.land/r/a"}, "gno.land/r/c": {Predecessor: "gno.land/r/b"}}}
	rj, _ := RouteAt(ctx, f, "gno.land/r/a", 0)
	mig, err := MigrationOf(ctx, RouteReaderAt(f, 0), "GNOT", "gno.land/r/a", "", rj, mb)
	if err != nil || mig == nil || mig.Line() != "gno.land/r/a → gno.land/r/b → gno.land/r/c" || !mig.Ended() || mig.Live() != "gno.land/r/c" {
		t.Fatalf("%+v %v", mig, err)
	}
	if lock, _, _, _ := mig.Sum(); lock != 1000 {
		t.Fatalf("lock %d", lock)
	}
	// from the last realm, the line back to its first
	rc, _ := RouteAt(ctx, f, "gno.land/r/c", 0)
	back, err := MigrationOf(ctx, RouteReaderAt(f, 0), "GNOT", "gno.land/r/c", "", rc, mb)
	if err != nil || back == nil || back.Predecessor != "gno.land/r/a" || back.Successor != "gno.land/r/c" {
		t.Fatalf("%+v %v", back, err)
	}
	if !Refundable(rc, nil) || Refundable(&Route{}, nil) {
		t.Fatal("refundable")
	}
}

// fakeBlocks is a chain's blocks for the scan: transaction counts and results by height.
type fakeBlocks struct {
	txs     map[int64]int64
	results map[int64][]*gnolight.TxResult
	reads   []int64
}

func (b *fakeBlocks) NumTxs(_ context.Context, from, to int64) (map[int64]int64, error) {
	out := map[int64]int64{}
	for h := from; h <= to; h++ {
		out[h] = b.txs[h]
	}
	return out, nil
}

func (b *fakeBlocks) BlockResults(_ context.Context, h int64) ([]*gnolight.TxResult, error) {
	b.reads = append(b.reads, h)
	return b.results[h], nil
}

// dispatchResult is a transaction's result carrying the Dispatch events of nonces toward a destination.
func dispatchResult(mailbox string, dest uint32, nonces ...uint32) *gnolight.TxResult {
	r := &gnolight.TxResult{}
	var anys [][]byte
	for _, n := range nonces {
		m := hl.Message{Version: 3, Nonce: n, Origin: hl.DomainGnoTest, Destination: dest, Body: []byte{byte(n)}}
		raw := m.Bytes()
		d := gnolight.Dispatch{ID: hl.ID(raw), Nonce: n, Origin: m.Origin, Destination: dest, Hash: gnolight.MessageHash(raw)}
		e := d.ToEvent(mailbox)
		r.Events = append(r.Events, e)
		anys = append(anys, gnolight.Any(gnolight.EventTypeURL, gnolight.EncodeEvent(e)))
	}
	r.Leaf = gnolight.EncodeResult(nil, nil, anys)
	return r
}

// The scan reads only blocks with transactions, finds the mailbox's Dispatch events (a batch re-emit's several in
// one leaf), marks a block whose leaves do not rebuild, notes r/sys/validators' events, and the index answers each
// message's usable heights.
func TestScanAndIndex(t *testing.T) {
	ctx := context.Background()
	b := &fakeBlocks{txs: map[int64]int64{10: 1, 12: 2, 13: 1, 15: 1, 16: 1}, results: map[int64][]*gnolight.TxResult{
		10: {dispatchResult(mbPath, 31337, 0)},
		12: {dispatchResult(mbPath, 31337, 1), {Err: fmt.Errorf("a failed transaction")}},
		13: {{Err: fmt.Errorf("a failed transaction")}},
		15: {dispatchResult(mbPath, 31337, 0, 1, 2), dispatchResult(mbPath, 1399811149, 3)},
		16: {{Leaf: gnolight.EncodeResult(nil, nil, nil), Events: []*gnolight.Event{{Type: "ValidatorAdded", PkgPath: "gno.land/r/sys/validators/v2"}}}},
	}}
	found, last, err := ScanBlocks(ctx, b, mbPath, 9, 20, 0)
	if err != nil || last != 20 || len(found) != 5 {
		t.Fatalf("%v %d %v", found, last, err)
	}
	if fmt.Sprint(b.reads) != "[10 12 13 15 16]" {
		t.Fatalf("read %v", b.reads)
	}
	if found[1].Buildable || found[2].Buildable || !found[4].Valset || len(found[3].Dispatches) != 4 {
		t.Fatalf("%+v", found)
	}
	var x EventIndex
	for _, be := range found {
		x.Add(be, 31337)
	}
	// a block that cannot be proven from is marked only when it carries one of our messages
	if _, ok := x.Unbuildable[12]; !ok {
		t.Fatal("block 12 (our message beside a failed transaction) not marked")
	}
	if _, ok := x.Unbuildable[13]; ok {
		t.Fatal("block 13 (no message of ours) marked")
	}
	// the leaf each message would be proven from, read from the leaves' own bytes: block 15's first leaf carries
	// nonces 0, 1 and 2
	leaves, _ := gnolight.ResultLeaves(b.results[15])
	for _, n := range []uint32{0, 1, 2} {
		if got, ok := x.LeafOf(n, 15); !ok || got != len(leaves[0]) {
			t.Fatalf("nonce %d's leaf at 15: %d %v, want %d", n, got, ok, len(leaves[0]))
		}
	}
	if _, ok := x.LeafOf(1, 12); ok {
		t.Fatal("a leaf size for a block that cannot be rebuilt")
	}
	if got := x.Of(0, 10); fmt.Sprint(got) != "[10 15]" {
		t.Fatalf("nonce 0: %v", got)
	}
	if got := x.Of(1, 12); fmt.Sprint(got) != "[15]" { // its own block cannot be rebuilt
		t.Fatalf("nonce 1: %v", got)
	}
	if got := x.Of(3, 15); fmt.Sprint(got) != "[15]" || len(x.Heights[3]) != 0 { // another destination: not indexed
		t.Fatalf("nonce 3: %v %v", got, x.Heights[3])
	}
	x.Forget(0)
	if got := x.Of(0, 10); fmt.Sprint(got) != "[10]" {
		t.Fatalf("forgotten: %v", got)
	}
	// a bounded scan stops short and says where
	if _, last, _ := ScanBlocks(ctx, b, mbPath, 9, 20, 3); last != 11 {
		t.Fatalf("bounded: %d", last)
	}
}

// The node's listing: tm2's blockchain route twenty metas a request, highest first; a height it does not answer for
// is an error.
func TestNodeNumTxs(t *testing.T) {
	var asked []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		var body struct {
			Params map[string]string `json:"params"`
		}
		_ = json.NewDecoder(req.Body).Decode(&body)
		lo, hi := body.Params["minHeight"], body.Params["maxHeight"]
		asked = append(asked, lo+"-"+hi)
		var metas []string
		var l, h int64
		fmt.Sscan(lo, &l)
		fmt.Sscan(hi, &h)
		for x := h; x >= l; x-- {
			if x == 33 && strings.Contains(req.URL.Path, "hole") {
				continue
			}
			metas = append(metas, fmt.Sprintf(`{"header":{"height":"%d","num_txs":"%d"}}`, x, x%3))
		}
		fmt.Fprintf(w, `{"jsonrpc":"2.0","id":1,"result":{"last_height":"100","block_metas":[%s]}}`, strings.Join(metas, ","))
	}))
	defer srv.Close()
	n := Node{Client: &gno.Client{RPC: srv.URL}}
	got, err := n.NumTxs(context.Background(), 1, 45)
	if err != nil || len(got) != 45 || got[44] != 2 || got[45] != 0 {
		t.Fatalf("%v %v", got, err)
	}
	if fmt.Sprint(asked) != "[26-45 6-25 1-5]" {
		t.Fatalf("asked %v", asked)
	}
	hole := Node{Client: &gno.Client{RPC: srv.URL + "/hole"}}
	if _, err := hole.NumTxs(context.Background(), 30, 40); err == nil {
		t.Fatal("a height the node skipped read as a block without transactions")
	}
}

// A batch re-emit is one transaction of Reemit MsgCalls, one a nonce in the order given — never a MsgRun, which
// gnoland-1's run_submitters refuses (E3-AS1): gnokey writes the first message, the others copy it with their own
// nonce, the whole is signed under the key's account number and sequence (auth/accounts), simulated (broadcast
// -dry-run), then signed again with the measured gas and a fifth more and broadcast; the height and hash read back.
func TestReemitTransaction(t *testing.T) {
	const caller = "g1jg8mtutu9khhfwc4nxmuhcpftf0pajdhfvsqf5"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		var body struct {
			Method string            `json:"method"`
			Params map[string]string `json:"params"`
		}
		_ = json.NewDecoder(req.Body).Decode(&body)
		if body.Method != "abci_query" || body.Params["path"] != "auth/accounts/"+caller {
			http.Error(w, "unexpected "+body.Method+" "+body.Params["path"], 400)
			return
		}
		data, _ := json.Marshal(map[string]any{"BaseAccount": map[string]any{"address": caller, "account_number": "7", "sequence": "42"}, "attributes": "0"})
		fmt.Fprintf(w, `{"jsonrpc":"2.0","id":1,"result":{"response":{"ResponseBase":{"Error":null,"Data":%q}}}}`, base64.StdEncoding.EncodeToString(data))
	}))
	defer srv.Close()
	dir := t.TempDir()
	logf := filepath.Join(dir, "calls.log")
	txs := filepath.Join(dir, "txs.log")
	script := `#!/bin/sh
echo "$@" >> ` + logf + `
case "$1" in
maketx) echo '{"msg":[{"@type":"/vm.m_call","caller":"` + caller + `","send":"","max_deposit":"","pkg_path":"` + mbPath + `","func":"Reemit","args":["3"]}],"fee":{"gas_wanted":"1","gas_fee":"1ugnot"},"signatures":null,"memo":""}' ;;
sign) cat "$3" >> ` + txs + `; echo >> ` + txs + `; echo "Tx successfully signed" ;;
broadcast) for a in "$@"; do [ "$a" = -dry-run ] && { echo 'OK!'; echo 'GAS USED:   1000000'; echo 'HEIGHT:     0'; exit 0; }; done
  echo 'OK!'; echo 'GAS USED:   1000000'; echo 'HEIGHT:     55'; echo 'TX HASH:    abc123' ;;
esac
`
	bin := filepath.Join(dir, "gnokey")
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	r := &Runner{Client: &gno.Client{RPC: srv.URL, ChainID: "dev", Gnokey: bin, Key: "relayer", GasFee: "1000ugnot"}, TempDir: dir}
	res, err := r.Reemit(context.Background(), mbPath, []uint32{3, 7}, 45_000_000)
	if err != nil || res.Height != 55 || res.Hash != "abc123" {
		t.Fatalf("%+v %v", res, err)
	}
	b, _ := os.ReadFile(logf)
	log := string(b)
	if strings.Contains(log, "maketx run") || strings.Count(log, "maketx call") != 1 || strings.Count(log, "sign -tx-path") != 2 || !strings.Contains(log, "-account-number 7 -account-sequence 42") || strings.Count(log, "broadcast -remote") != 2 || !strings.Contains(log, "-dry-run") {
		t.Fatal(log)
	}
	signed, _ := os.ReadFile(txs)
	lines := strings.Split(strings.TrimSpace(string(signed)), "\n")
	if len(lines) != 2 {
		t.Fatalf("%d transactions signed: %s", len(lines), signed)
	}
	var tx struct {
		Msg []struct {
			Type    string   `json:"@type"`
			Caller  string   `json:"caller"`
			PkgPath string   `json:"pkg_path"`
			Func    string   `json:"func"`
			Args    []string `json:"args"`
		} `json:"msg"`
		Fee struct {
			GasWanted string `json:"gas_wanted"`
			GasFee    string `json:"gas_fee"`
		} `json:"fee"`
	}
	if err := json.Unmarshal([]byte(lines[1]), &tx); err != nil {
		t.Fatal(err)
	}
	if len(tx.Msg) != 2 || tx.Msg[0].Args[0] != "3" || tx.Msg[1].Args[0] != "7" || tx.Msg[1].Type != "/vm.m_call" || tx.Msg[1].Func != "Reemit" || tx.Msg[1].PkgPath != mbPath || tx.Msg[1].Caller != caller {
		t.Fatalf("the transaction's messages: %+v", tx.Msg)
	}
	if tx.Fee.GasWanted != "2200000" { // the simulation's 1,000,000, a fifth more and a million (gno.GasFor)
		t.Fatalf("the broadcast's gas: %+v", tx.Fee)
	}
	// a refusal is a CallError, sorted by gno.Deterministic
	_ = os.WriteFile(bin, []byte("#!/bin/sh\ncase \"$1\" in maketx) echo '{\"msg\":[{\"@type\":\"/vm.m_call\",\"caller\":\""+caller+"\",\"pkg_path\":\""+mbPath+"\",\"func\":\"Reemit\",\"args\":[\"9\"]}]}' ;; sign) ;; *) echo 'panic: hlmailbox: no message with nonce 9'; exit 1 ;; esac\n"), 0o755)
	_, err = r.Reemit(context.Background(), mbPath, []uint32{9}, 45_000_000)
	if err == nil || !gno.Deterministic(err) {
		t.Fatalf("%v", err)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 3 { // the script and its two logs
		t.Fatalf("the transaction files were not removed: %d entries", len(entries))
	}
}

// A route's send leaves its own fee in its SentTransferRemote event beside its Dispatch, in one transaction: the scan
// keeps it by message (with the realm that said it) and the index by nonce; an event in another transaction than the
// Dispatch it names, or with a fee that is no number, is not taken.
func TestScanKeepsEachSendsOwnFee(t *testing.T) {
	ctx := context.Background()
	send := dispatchResult(mbPath, 31337, 0)
	id := send.Events[0].Attributes[0].Value
	sent := func(id, fee string) *gnolight.Event {
		return &gnolight.Event{Type: SentTransferRemote, PkgPath: "gno.land/r/x/eth/hlgnot", Attributes: []gnolight.Attribute{
			{Key: "destination", Value: "31337"}, {Key: "amount", Value: "10"}, {Key: "fee", Value: fee}, {Key: "id", Value: id}}}
	}
	send.Events = append(send.Events, sent(id, "5000000"))
	other := dispatchResult(mbPath, 31337, 1)
	stray := &gnolight.TxResult{Leaf: gnolight.EncodeResult(nil, nil, nil), Events: []*gnolight.Event{sent(other.Events[0].Attributes[0].Value, "1")}}
	bad := dispatchResult(mbPath, 31337, 2)
	bad.Events = append(bad.Events, sent(bad.Events[0].Attributes[0].Value, "five"))
	b := &fakeBlocks{txs: map[int64]int64{10: 1, 11: 2, 12: 1}, results: map[int64][]*gnolight.TxResult{10: {send}, 11: {other, stray}, 12: {bad}}}
	found, _, err := ScanBlocks(ctx, b, mbPath, 10, 12, 0)
	if err != nil || len(found) != 3 {
		t.Fatalf("%v %v", found, err)
	}
	var x EventIndex
	for _, be := range found {
		x.Add(be, 31337)
	}
	if f, ok := x.FeeOf(0); !ok || f.Fee != 5_000_000 || f.Realm != "gno.land/r/x/eth/hlgnot" {
		t.Fatalf("the send's own fee: %+v %v", f, ok)
	}
	if f, ok := x.FeeOf(1); ok {
		t.Fatalf("a fee from another transaction than the dispatch's: %+v", f)
	}
	if f, ok := x.FeeOf(2); ok {
		t.Fatalf("a fee that is no number: %+v", f)
	}
	x.Forget(0)
	if _, ok := x.FeeOf(0); ok {
		t.Fatal("an ended message's fee kept")
	}
}
