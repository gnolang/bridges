package agent

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gagliardetto/solana-go"
	"github.com/gagliardetto/solana-go/rpc"

	"github.com/gnolang/bridges/solana/bridge/gno"
	"github.com/gnolang/bridges/solana/bridge/hl"
	"github.com/gnolang/bridges/solana/bridge/sol"
)

// Lane s4 (2026-10-15): the Ethereum port's third-round fixes of the Go trust path (fold F12) carried to this bridge
// — E3-GT2, the read-twice guard over hlmailbox's delivered count and the counters of an open line (a refund's on the
// machines that read refund marks); E3-GT9, the floor under the Solana snapshot's slot. Every machine's whole round
// runs here against a gno node in memory (a hook lands a transaction between two of the round's reads) and a Solana
// in memory behind JSON-RPC, each of its two providers answering at its own finalized slot.

// ------------------------------------------------------------------------------------------- Solana --

// s4Key is a key from a name.
func s4Key(name string) solana.PublicKey {
	h := sha256.Sum256([]byte(name))
	return solana.PublicKeyFromBytes(h[:])
}

type solVersion struct {
	slot  uint64
	owner solana.PublicKey
	data  []byte
}

type solSigAt struct {
	sig  solana.Signature
	slot uint64
}

type solTxAt struct {
	slot uint64
	raw  []byte
}

// solNet is a Solana in memory behind JSON-RPC: every account's versions by slot, every address's signatures, the
// transactions. Its two providers (httptest servers) answer at their own finalized slots — an account as its last
// version at or below that slot, the signatures and the transactions at or below it — and a provider may answer
// one account from an older slot (pin: a stale backend, or a liar); a getMultipleAccounts is answered from one slot,
// as a node answers it from one bank (the lowest any of its accounts is pinned to), and minContextSlot is honoured
// (lane s5).
type solNet struct {
	mu       sync.Mutex
	at       [2]uint64
	pin      [2]map[solana.PublicKey]uint64
	accounts map[solana.PublicKey][]solVersion
	sigs     map[solana.PublicKey][]solSigAt // oldest first
	txs      map[solana.Signature]solTxAt
	urls     [2]string
	unknown  []string
	// listLag holds provider i's signature listing that many slots behind its accounts (a backend of its own);
	// ignoreMin makes provider i answer an account read below a minContextSlot instead of refusing (a provider that
	// ignores it), ignoreMinList its signature listing (as Alchemy does, E5-SF2; lane s6).
	listLag       [2]uint64
	ignoreMin     [2]bool
	ignoreMinList [2]bool
	// clockBase is the time Solana's clock stamps on slot 0: the Clock sysvar of slot s reads clockBase + 0.4 s × s
	// (slot 100 at s4ClockZero, the gno node's block time, by default); clockLie moves provider i's answer of it.
	clockBase time.Time
	clockLie  [2]time.Duration
}

// lagListing holds provider i's signature listing lag slots behind its finalized slot.
func (n *solNet) lagListing(i int, lag uint64) {
	n.mu.Lock()
	n.listLag[i] = lag
	n.mu.Unlock()
}

func newSolNet(t *testing.T) *solNet {
	n := &solNet{pin: [2]map[solana.PublicKey]uint64{{}, {}}, accounts: map[solana.PublicKey][]solVersion{}, sigs: map[solana.PublicKey][]solSigAt{}, txs: map[solana.Signature]solTxAt{},
		clockBase: s4ClockZero.Add(-100 * 400 * time.Millisecond)}
	for i := range 2 {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { n.serve(i, w, r) }))
		t.Cleanup(srv.Close)
		n.urls[i] = srv.URL
	}
	return n
}

func (n *solNet) clients() [2]*rpc.Client {
	return [2]*rpc.Client{rpc.New(n.urls[0]), rpc.New(n.urls[1])}
}

// slots sets the providers' finalized slots.
func (n *solNet) slots(a, b uint64) {
	n.mu.Lock()
	n.at = [2]uint64{a, b}
	n.mu.Unlock()
}

// pinAt makes provider i answer an account as of an older slot (0 lifts it).
func (n *solNet) pinAt(i int, k solana.PublicKey, slot uint64) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if slot == 0 {
		delete(n.pin[i], k)
		return
	}
	n.pin[i][k] = slot
}

// put writes an account's version from a slot on (nil data: gone from it).
func (n *solNet) put(k solana.PublicKey, slot uint64, owner solana.PublicKey, data []byte) {
	n.mu.Lock()
	defer n.mu.Unlock()
	vs := append(n.accounts[k], solVersion{slot: slot, owner: owner, data: data})
	sort.SliceStable(vs, func(i, j int) bool { return vs[i].slot < vs[j].slot })
	n.accounts[k] = vs
}

func (n *solNet) version(k solana.PublicKey, slot uint64) *solVersion {
	var out *solVersion
	for i := range n.accounts[k] {
		if v := &n.accounts[k][i]; v.slot <= slot {
			out = v
		}
	}
	if out == nil || out.data == nil {
		return nil
	}
	return out
}

// clockAt is the time the Clock sysvar stamps on a slot.
func (n *solNet) clockAt(slot uint64) time.Time {
	return n.clockBase.Add(time.Duration(slot) * 400 * time.Millisecond)
}

func (n *solNet) accountJSON(i int, k solana.PublicKey, slot uint64) any {
	if k.Equals(solana.SysVarClockPubkey) { // the Clock sysvar of the slot asked
		d := make([]byte, 40)
		binary.LittleEndian.PutUint64(d[0:8], slot)
		binary.LittleEndian.PutUint64(d[32:40], uint64(n.clockAt(slot).Add(n.clockLie[i]).Unix()))
		return map[string]any{"data": []string{base64.StdEncoding.EncodeToString(d), "base64"}, "executable": false, "lamports": 1_169_280, "owner": solana.SysVarPubkey.String(), "rentEpoch": 0, "space": 40}
	}
	v := n.version(k, slot)
	if v == nil {
		return nil
	}
	return map[string]any{"data": []string{base64.StdEncoding.EncodeToString(v.data), "base64"}, "executable": false, "lamports": 1_000_000, "owner": v.owner.String(), "rentEpoch": 0, "space": len(v.data)}
}

func (n *solNet) serve(i int, w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID     json.RawMessage   `json:"id"`
		Method string            `json:"method"`
		Params []json.RawMessage `json:"params"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	n.mu.Lock()
	defer n.mu.Unlock()
	at := n.at[i]
	slotOf := func(k solana.PublicKey) uint64 {
		if p, ok := n.pin[i][k]; ok {
			return p
		}
		return at
	}
	key := func(raw json.RawMessage) solana.PublicKey {
		var s string
		_ = json.Unmarshal(raw, &s)
		k, _ := solana.PublicKeyFromBase58(s)
		return k
	}
	// minContextSlot, as a node answers it: below the slot its view is at, the error, never an answer
	var opts struct {
		Limit          int    `json:"limit"`
		Before         string `json:"before"`
		Until          string `json:"until"`
		MinContextSlot uint64 `json:"minContextSlot"`
	}
	if len(req.Params) > 1 {
		_ = json.Unmarshal(req.Params[1], &opts)
	}
	notReached := func(view uint64) bool {
		ignores := n.ignoreMin[i]
		if req.Method == "getSignaturesForAddress" {
			ignores = n.ignoreMinList[i]
		}
		if opts.MinContextSlot == 0 || opts.MinContextSlot <= view || ignores {
			return false
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "error": map[string]any{"code": -32016, "message": "Minimum context slot has not been reached", "data": map[string]any{"contextSlot": view}}})
		return true
	}
	var result any
	switch req.Method {
	case "getAccountInfo":
		k := key(req.Params[0])
		s := slotOf(k)
		if notReached(s) {
			return
		}
		result = map[string]any{"context": map[string]any{"slot": s}, "value": n.accountJSON(i, k, s)}
	case "getMultipleAccounts":
		// one call is one bank: every account at the call's one slot — the provider's, or the lowest a pinned account
		// names (a stale backend answered the call)
		var keys []string
		_ = json.Unmarshal(req.Params[0], &keys)
		ctxSlot := at
		for _, ks := range keys {
			k, _ := solana.PublicKeyFromBase58(ks)
			if p, ok := n.pin[i][k]; ok {
				ctxSlot = min(ctxSlot, p)
			}
		}
		if notReached(ctxSlot) {
			return
		}
		vals := []any{}
		for _, ks := range keys {
			k, _ := solana.PublicKeyFromBase58(ks)
			vals = append(vals, n.accountJSON(i, k, ctxSlot))
		}
		result = map[string]any{"context": map[string]any{"slot": ctxSlot}, "value": vals}
	case "getSignaturesForAddress":
		k := key(req.Params[0])
		view := at - min(at, n.listLag[i])
		if notReached(view) {
			return
		}
		list := []any{}
		skipping := opts.Before != ""
		all := n.sigs[k]
		for j := len(all) - 1; j >= 0; j-- { // newest first
			s := all[j]
			if s.slot > view {
				continue
			}
			if skipping {
				skipping = s.sig.String() != opts.Before
				continue
			}
			if opts.Until != "" && s.sig.String() == opts.Until {
				break
			}
			list = append(list, map[string]any{"signature": s.sig.String(), "slot": s.slot, "err": nil, "memo": nil, "blockTime": nil, "confirmationStatus": "finalized"})
			if opts.Limit > 0 && len(list) >= opts.Limit {
				break
			}
		}
		result = list
	case "getTransaction":
		var s string
		_ = json.Unmarshal(req.Params[0], &s)
		sig, _ := solana.SignatureFromBase58(s)
		if tx, ok := n.txs[sig]; ok && tx.slot <= at {
			result = map[string]any{"slot": tx.slot, "blockTime": nil, "version": "legacy",
				"transaction": []string{base64.StdEncoding.EncodeToString(tx.raw), "base64"},
				"meta": map[string]any{"err": nil, "fee": 5000, "preBalances": []any{}, "postBalances": []any{}, "innerInstructions": []any{}, "logMessages": []any{},
					"preTokenBalances": []any{}, "postTokenBalances": []any{}, "rewards": []any{}, "status": map[string]any{"Ok": nil},
					"loadedAddresses": map[string]any{"writable": []any{}, "readonly": []any{}}}}
		}
	case "getBalance":
		result = map[string]any{"context": map[string]any{"slot": at}, "value": 1_000_000_000}
	default:
		n.unknown = append(n.unknown, req.Method)
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "error": map[string]any{"code": -32601, "message": "solNet: no method " + req.Method}})
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result})
}

// mintData is a token mint's account with a supply (82 bytes, initialized).
func mintData(supply uint64) []byte {
	d := make([]byte, 82)
	binary.LittleEndian.PutUint64(d[36:44], supply)
	d[45] = 1
	return d
}

// supply sets a token's synthetic supply from a slot on.
func (n *solNet) supply(token solana.PublicKey, slot, amount uint64) {
	n.put(sol.Synthetic{Program: token}.Mint(), slot, sol.TokenProgram, mintData(amount))
}

// burn is a burn toward gno.land finalized at a slot: the token program's transaction and the dispatch record the
// mailbox wrote in that slot. It answers the message id. The supply is the test's to set.
func (n *solNet) burn(cfg *Config, nonce uint32, slot uint64, to [32]byte, amount uint64) [32]byte {
	r := cfg.Routes[0]
	token, mailbox := r.TokenKey(), cfg.SolMailbox()
	route, _ := hl.GnoAddressToBytes32(hl.GnoPackageAddress(r.Gno))
	msg := hl.Message{Version: 3, Nonce: nonce, Origin: cfg.Solana.Domain, Sender: [32]byte(token), Destination: hl.DomainGnoTest, Recipient: route, Body: hl.TokenMessage(to, amount, nil)}
	unique := s4Key(fmt.Sprintf("unique %d", nonce))
	acct := sol.Mailbox{Program: mailbox}.DispatchedMessage(unique)
	data := append([]byte{1}, dispatchTag...)
	data = binary.LittleEndian.AppendUint32(data, nonce)
	data = binary.LittleEndian.AppendUint64(data, slot)
	data = append(data, unique.Bytes()...)
	data = append(data, msg.Bytes()...)
	n.put(acct, slot, mailbox, data)
	payer := solana.NewWallet()
	ix := solana.NewInstruction(token, solana.AccountMetaSlice{solana.Meta(acct).WRITE()}, []byte{byte(nonce)})
	tx, err := solana.NewTransaction([]solana.Instruction{ix}, solana.Hash{1}, solana.TransactionPayer(payer.PublicKey()))
	if err != nil {
		panic(err)
	}
	if _, err := tx.Sign(func(k solana.PublicKey) *solana.PrivateKey {
		if k.Equals(payer.PublicKey()) {
			return &payer.PrivateKey
		}
		return nil
	}); err != nil {
		panic(err)
	}
	raw, err := tx.MarshalBinary()
	if err != nil {
		panic(err)
	}
	n.mu.Lock()
	n.txs[tx.Signatures[0]] = solTxAt{slot: slot, raw: raw}
	n.sigs[token] = append(n.sigs[token], solSigAt{sig: tx.Signatures[0], slot: slot})
	n.mu.Unlock()
	return hl.ID(msg.Bytes())
}

// processed is the mailbox's processed-message record of a gno.land message, from the slot it was processed in on
// (Hyperlane's layout: initialized, "PROCESSD", the sequence, the id, the slot); the mint with it is the test's.
func (n *solNet) processed(cfg *Config, id [32]byte, slot uint64) {
	data := append([]byte{1}, processedTag...)
	data = binary.LittleEndian.AppendUint64(data, 0)
	data = append(data, id[:]...)
	data = binary.LittleEndian.AppendUint64(data, slot)
	n.put(sol.Mailbox{Program: cfg.SolMailbox()}.ProcessedMessage(id), slot, cfg.SolMailbox(), data)
}

// clientStateData is the light client's state account (the layout sol.ParseClientState reads): the trusted
// height, a set of six validators, an empty record and ring — so a monitor's record rules read it as consistent.
func clientStateData(trustedHeight uint64, lastAccepted int64) []byte {
	const bodyCap8 = 128
	sum := func(s string) []byte { h := sha256.Sum256([]byte(s)); return h[:] }
	b := []byte{sol.ClientStateVersion, 1}
	b = binary.LittleEndian.AppendUint16(b, 0) // ring count
	b = binary.LittleEndian.AppendUint16(b, 0) // ring next
	b = binary.LittleEndian.AppendUint16(b, bodyCap8)
	b = binary.LittleEndian.AppendUint64(b, 0) // no quarantine
	b = binary.LittleEndian.AppendUint64(b, uint64(lastAccepted))
	b = binary.LittleEndian.AppendUint64(b, trustedHeight)
	b = append(b, sum("trusted")...)
	b = append(b, s4Key("admin").Bytes()...)
	b = binary.LittleEndian.AppendUint32(b, 3)
	b = append(b, "dev"...)
	b = binary.LittleEndian.AppendUint32(b, hl.DomainGnoTest)
	b = binary.LittleEndian.AppendUint64(b, trustedHeight)
	b = append(b, sum("trusted")...)
	b = append(b, sum("vals")...)
	b = append(b, sum("vals")...)
	b = binary.LittleEndian.AppendUint64(b, uint64(lastAccepted))
	b = binary.LittleEndian.AppendUint32(b, 6)
	for i := range 6 {
		b = append(b, s4Key(fmt.Sprintf("validator %d", i)).Bytes()...)
		b = binary.LittleEndian.AppendUint64(b, 60)
	}
	b = append(b, sum("vals")...)
	b = append(b, 0) // no next set
	b = append(b, sum("vals")...)
	b = binary.LittleEndian.AppendUint32(b, 0) // no history
	b = binary.LittleEndian.AppendUint64(b, 0) // the record's counter
	b = append(b, make([]byte, 32)...)         // its running hash
	b = binary.LittleEndian.AppendUint64(b, 0) // the ring epoch
	for len(b) < sol.HeadLen+bodyCap8*8 {
		b = append(b, 0)
	}
	return b
}

// ---------------------------------------------------------------------------------------------- gno --

// s4Gno is a gno node in memory whose renders a hook may change after any read (a transaction landing between two
// of a machine's reads); a burn's delivered mark not set reads 0.
type s4Gno struct {
	mu      sync.Mutex
	renders map[string]string
	head    gno.Head
	after   func(pkg, path string)
	evals   int // the vm/qeval reads answered (lane s6)
}

func newS4Gno() *s4Gno {
	return &s4Gno{renders: map[string]string{}, head: gno.Head{Height: 200, Time: time.Unix(1_800_000_000, 0), Hash: "aa"}}
}

func (g *s4Gno) RenderAt(_ context.Context, pkg, path string, _ int64) (string, error) {
	g.mu.Lock()
	s, ok := g.renders[pkg+":"+path]
	if !ok && strings.HasPrefix(path, "delivered/") {
		s, ok = "0", true
	}
	after := g.after
	g.mu.Unlock()
	if after != nil {
		after(pkg, path)
	}
	if !ok {
		return "", fmt.Errorf("s4Gno: no render %s:%s", pkg, path)
	}
	return s, nil
}

func (g *s4Gno) Status(context.Context) (gno.Head, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.head, nil
}

func (g *s4Gno) Block(_ context.Context, h int64) (gno.Head, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return gno.Head{Height: h, Hash: fmt.Sprintf("hash%d", h), Time: g.head.Time}, nil
}

func (g *s4Gno) Call(context.Context, string, string, int64, ...string) (*gno.Result, error) {
	return &gno.Result{Hash: "tx"}, nil
}

func (g *s4Gno) set(key, val string) {
	g.mu.Lock()
	g.renders[key] = val
	g.mu.Unlock()
}

// Query answers the vm/qeval of hlmailbox's delivered ids (doneAnswer).
func (g *s4Gno) Query(_ context.Context, path string, data []byte, _ int64) ([]byte, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.evals++
	return doneAnswer(g.renders, s4Mailbox, path, data)
}

// setHead moves the node's head (a block made at t).
func (g *s4Gno) setHead(height int64, t time.Time) {
	g.mu.Lock()
	g.head = gno.Head{Height: height, Time: t, Hash: fmt.Sprintf("head%d", height)}
	g.mu.Unlock()
}

func (g *s4Gno) hook(fn func(pkg, path string)) {
	g.mu.Lock()
	g.after = fn
	g.mu.Unlock()
}

// The deployment the rigs read: hlmailbox, one route (BUBBLE) and its token program, and the Solana programs.
const (
	s4Mailbox = "gno.land/r/test/hlmailbox"
	s4Route   = "gno.land/r/test/hlbubble"
	s4Succ    = "gno.land/r/test/hlbubble2"
)

var (
	s4Token     = s4Key("token")
	s4SolBox    = s4Key("mailbox")
	s4Alice, _  = hl.GnoAddressToBytes32(hl.GnoPackageAddress("gno.land/r/test/alice"))
	s4Bob, _    = hl.GnoAddressToBytes32(hl.GnoPackageAddress("gno.land/r/test/bob"))
	s4ClockZero = time.Unix(1_800_000_000, 0)
)

// s4ClientState is the light client's state account.
func s4ClientState() solana.PublicKey { return sol.LightClient{Program: s4Key("client")}.State() }

func s4Config(programs bool) *Config {
	cfg := &Config{}
	cfg.Gno.Mailbox = s4Mailbox
	cfg.Solana.Domain = hl.DomainSolanaTestnet
	cfg.Solana.Mailbox = s4SolBox.String()
	cfg.Routes = []Route{{Symbol: "BUBBLE", Gno: s4Route, Token: s4Token.String()}}
	if programs {
		cfg.Solana.Client, cfg.Solana.Pausable, cfg.Solana.Admin = s4Key("client").String(), s4Key("pausable").String(), s4Key("admin").String()
	}
	return cfg
}

func s4MailboxJSON(count uint32, delivered int) string {
	return fmt.Sprintf(`{"domain":%d,"count":%d,"delivered":%d,"live":true,"routes":{}}`, hl.DomainGnoTest, count, delivered)
}

// s4RouteJSON is a route's json: its lock, owed and refund count; imported > 0 makes it a successor.
func s4RouteJSON(lock, owed int64, imported int, refunded int64, migratedTo string) string {
	return fmt.Sprintf(`{"address":"g1route","live":true,"symbol":"BUBBLE","lock":%d,"sentTotal":%d,"owed":%d,"voided":0,"lost":0,"imported":%d,"refunded":%d,"migratedTo":%q}`, lock, lock, owed, imported, refunded, migratedTo)
}

func s4IDHex(id [32]byte) string { return hex.EncodeToString(id[:]) }

// s4Claim is a route's claim/<id> for a credited burn.
func s4Claim(id [32]byte, to [32]byte, amount int64, height int64) string {
	addr, _ := hl.Bytes32ToGnoAddress(to)
	return fmt.Sprintf(`{"id":%q,"to":%q,"amount":%d,"paid":0,"state":"open","by":"verifier","height":%d}`, s4IDHex(id), addr, amount, height)
}

// ------------------------------------------------------------------------------------------- the rigs --

type s4Signer struct {
	t   *testing.T
	cfg *Config
	n   *solNet
	g   *s4Gno
	s   *Signer
}

// newS4Signer is a signer over BUBBLE with 1,000 locked on gno.land and 1,000 minted on Solana.
func newS4Signer(t *testing.T, programs bool) *s4Signer {
	r := &s4Signer{t: t, cfg: s4Config(programs), n: newSolNet(t), g: newS4Gno()}
	r.g.set(s4Mailbox+":json", s4MailboxJSON(0, 0))
	r.g.set(s4Route+":json", s4RouteJSON(1000, 0, 0, 0, ""))
	r.n.supply(s4Token, 1, 1000)
	if programs {
		r.n.put(s4ClientState(), 1, s4Key("client"), clientStateData(90, s4ClockZero.Unix()))
	}
	r.s = r.newSigner(t.TempDir())
	return r
}

func (r *s4Signer) newSigner(store string) *Signer {
	k, err := hl.NewValidatorKey()
	if err != nil {
		r.t.Fatal(err)
	}
	s := &Signer{Cfg: r.cfg, Key: k, Store: store, Every: time.Second, Alarm: &Alarmer{Source: "test"}, gno: r.g, sol: r.n.clients(), now: func() time.Time { return s4ClockZero }}
	if err := s.load(); err != nil {
		r.t.Fatal(err)
	}
	return s
}

// round is one turn of the signer's loop: the identity, then the signing if it may.
func (r *s4Signer) round() {
	ctx := context.Background()
	if err := r.s.checkIdentity(ctx); err != nil {
		r.t.Logf("round: %v", err)
	}
	if r.s.signing() {
		if err := r.s.signSol(ctx); err != nil {
			r.t.Logf("signing: %v", err)
		}
	}
}

func (r *s4Signer) signed(nonce uint32) bool {
	_, err := r.s.readSigned("sol", nonce)
	return err == nil
}

func (r *s4Signer) alarmed(key string) bool {
	_, ok := r.s.Alarm.Standing()[key]
	return ok
}

func (r *s4Signer) cut() string { return fmt.Sprint(r.s.Status()["cut"]) }

type s4Monitor struct {
	t     *testing.T
	cfg   *Config
	n     *solNet
	g     *s4Gno
	m     *Monitor
	clock time.Time
}

// newS4Monitor is a monitor over BUBBLE with 1,000 locked on gno.land and 1,000 minted on Solana; its state is kept
// in a file, so a restart reads it.
func newS4Monitor(t *testing.T, programs bool) *s4Monitor {
	r := &s4Monitor{t: t, cfg: s4Config(programs), n: newSolNet(t), g: newS4Gno(), clock: s4ClockZero}
	r.g.set(s4Mailbox+":json", s4MailboxJSON(0, 0))
	r.g.set(s4Route+":json", s4RouteJSON(1000, 0, 0, 0, ""))
	r.g.set(s4Route+":claims/0", "[]")
	r.n.supply(s4Token, 1, 1000)
	if programs {
		r.n.put(s4ClientState(), 1, s4Key("client"), clientStateData(90, s4ClockZero.Unix()))
	}
	r.m = r.newMonitor(filepath.Join(t.TempDir(), "monitor.json"))
	return r
}

func (r *s4Monitor) newMonitor(state string) *Monitor {
	m := &Monitor{Cfg: r.cfg, State: state, Every: 10 * time.Second, Alarm: &Alarmer{Source: "test"}, own: r.g, sol: r.n.clients(), now: func() time.Time { return r.clock }}
	m.both = twoProviders(m.sol)
	if err := m.init(); err != nil {
		r.t.Fatal(err)
	}
	return m
}

// poll is one poll, its state saved as the loop saves it.
func (r *s4Monitor) poll() {
	if err := r.m.poll(context.Background()); err != nil {
		r.t.Logf("poll: %v", err)
	}
	if err := r.m.save(); err != nil {
		r.t.Fatal(err)
	}
}

func (r *s4Monitor) step(d time.Duration) { r.clock = r.clock.Add(d) }

func (r *s4Monitor) alarmed(key string) bool {
	_, ok := r.m.Alarm.Standing()[key]
	return ok
}

func (r *s4Monitor) cut() string {
	r.m.mu.Lock()
	defer r.m.mu.Unlock()
	return r.m.status.Cut
}

func (r *s4Monitor) verdict() string {
	r.m.mu.Lock()
	defer r.m.mu.Unlock()
	if e, ok := r.m.status.Identity["BUBBLE"].(map[string]any); ok {
		return fmt.Sprint(e["verdict"])
	}
	return ""
}

// s4RouteAddr is the route realm's 32-byte address (a send's sender).
func s4RouteAddr() [32]byte {
	k, _ := hl.GnoAddressToBytes32(hl.GnoPackageAddress(s4Route))
	return k
}

// s4Send is a send of the route toward its token program on Solana (hlmailbox's message n).
func s4Send(n uint32, amount uint64) hl.Message {
	return hl.Message{Version: 3, Nonce: n, Origin: hl.DomainGnoTest, Sender: s4RouteAddr(), Destination: hl.DomainSolanaTestnet, Recipient: [32]byte(s4Token), Body: hl.TokenMessage([32]byte{7}, amount, nil)}
}

// ------------------------------------------------------------------------------------------- E3-GT2 --

// E3-GT2 on the signer: it read the routes, then Solana (seconds), then each burn's delivered mark, with no
// read-twice at all. A credit landing after the route was read and before its burn's mark was in neither owed nor in
// flight — the reading short by its amount — so a forged mint of that amount, burned back, passed the round: the
// halt lifted and the forged burn was signed. hlmailbox's delivered count, read before the routes and again after the
// marks, moves with every credit: the round is a moving cut (no verdict, quiet), the break shows on the next, and
// credits landing round after round alarm at the fifth.
func TestS4SignerCreditBetweenReadsIsAMovingCut(t *testing.T) {
	r := newS4Signer(t, false)
	// bob's honest burn of 300 (nonce 0) at slot 102: the supply 700, 300 in flight; signed once a cut counted it
	honest := r.n.burn(r.cfg, 0, 102, s4Bob, 300)
	r.n.supply(s4Token, 102, 700)
	r.n.slots(103, 103)
	r.round()
	if !r.signed(0) || r.s.Status()["signing"] != true {
		t.Fatalf("round 1: bob's burn signed %v, the status %v", r.signed(0), r.s.Status())
	}
	// the client is fooled: 300 minted at 105, burned straight back (nonce 1) at 106
	r.n.supply(s4Token, 105, 1000)
	r.n.burn(r.cfg, 1, 106, s4Alice, 300)
	r.n.supply(s4Token, 106, 700)
	r.n.slots(110, 110)
	r.round()
	if st := r.s.Status(); st["signing"] == true || !strings.Contains(fmt.Sprint(st["halted"]), "broken") {
		t.Fatalf("round 2 did not halt on the forged mint: %v", st)
	}
	// round 3: bob's credit lands right after the route was read
	var once sync.Once
	r.g.hook(func(pkg, path string) {
		if pkg == s4Route && path == "json" {
			once.Do(func() {
				r.g.set(s4Route+":json", s4RouteJSON(1000, 300, 0, 0, "")) // owed 300: bob's claim
				r.g.set(s4Mailbox+":delivered/"+s4IDHex(honest), "150")
				r.g.set(s4Mailbox+":json", s4MailboxJSON(0, 1))
			})
		}
	})
	r.round()
	if !strings.Contains(r.cut(), "delivered 1 message(s) while the cut was read") || r.signed(1) {
		t.Fatalf("round 3 took a verdict over a credit landing between the route's read and the marks': signing %v, the forged burn (nonce 1) signed %v, the cut %q", r.s.Status()["signing"], r.signed(1), r.cut())
	}
	if r.s.Status()["signing"] == true || r.alarmed("signer:no-verdict") {
		t.Fatalf("round 3: signing %v, alarms %v", r.s.Status()["signing"], r.s.Alarm.Standing())
	}
	// round 4: the reads hold still, the break shows
	r.g.hook(nil)
	r.round()
	if st := r.s.Status(); st["signing"] == true || !strings.Contains(fmt.Sprint(st["halted"]), "broken") || r.signed(1) {
		t.Fatalf("round 4: %v (the forged burn signed %v)", st, r.signed(1))
	}
	// credits landing round after round alarm at the fifth
	n := 1
	r.g.hook(func(pkg, path string) {
		if pkg == s4Route && path == "json" {
			n++
			r.g.set(s4Mailbox+":json", s4MailboxJSON(0, n))
		}
	})
	for i := 1; i <= 5; i++ {
		r.round()
		if got := r.alarmed("signer:no-verdict"); got != (i == 5) || r.s.Status()["signing"] == true {
			t.Fatalf("moving round %d: alarmed %v, signing %v (%s)", i, got, r.s.Status()["signing"], r.cut())
		}
	}
}

// The same race on the monitor: a credit landing between its route's read and its marks' read hid a forged mint of
// its amount for the poll. The poll is a moving cut, no verdict; the next one flags the forged mint.
func TestS4MonitorCreditBetweenReads(t *testing.T) {
	r := newS4Monitor(t, false)
	honest := r.n.burn(r.cfg, 0, 102, s4Bob, 300) // bob's 300, in flight
	r.n.supply(s4Token, 102, 700)
	r.n.slots(103, 103)
	r.poll()
	if v := r.verdict(); v != "holds" {
		t.Fatalf("poll 1: %s (%s)", v, r.cut())
	}
	r.n.supply(s4Token, 105, 1000)        // a forged mint of 300 …
	r.n.burn(r.cfg, 1, 106, s4Alice, 300) // … burned straight back
	r.n.supply(s4Token, 106, 700)
	r.n.slots(110, 110)
	var once sync.Once
	r.g.hook(func(pkg, path string) {
		if pkg == s4Route && path == "json" {
			once.Do(func() {
				r.g.set(s4Route+":json", s4RouteJSON(1000, 300, 0, 0, ""))
				r.g.set(s4Mailbox+":delivered/"+s4IDHex(honest), "150")
				r.g.set(s4Route+":claim/"+s4IDHex(honest), s4Claim(honest, s4Bob, 300, 150))
				r.g.set(s4Mailbox+":json", s4MailboxJSON(0, 1))
			})
		}
	})
	r.step(10 * time.Second)
	r.poll()
	if c := r.cut(); !strings.Contains(c, "delivered 1 message(s) while the cut was read") {
		t.Fatalf("the poll took a verdict over a credit landing mid-read: %s (%s)", r.verdict(), c)
	}
	r.g.hook(nil)
	r.step(10 * time.Second)
	r.poll()
	if v := r.verdict(); v != "BROKEN" || !r.alarmed("identity:BUBBLE:flag") {
		t.Fatalf("the forged mint was not flagged on the next poll: %s, %v", v, r.m.Alarm.Standing())
	}
}

// A send landing between the monitor's mailbox read and its routes' read: in the lock, and not walked — the walk
// stops at the count read first — so a forged mint of its amount hid for the poll. hlmailbox's count, read again
// after the routes and their lines, moved: no verdict; the next poll walks the send and flags the forged mint.
func TestS4MonitorSendBetweenReads(t *testing.T) {
	r := newS4Monitor(t, false)
	r.n.slots(100, 100)
	r.poll()
	if v := r.verdict(); v != "holds" {
		t.Fatalf("poll 1: %s (%s)", v, r.cut())
	}
	r.n.supply(s4Token, 105, 1300)        // a forged mint of 300 …
	r.n.burn(r.cfg, 0, 106, s4Alice, 300) // … burned straight back
	r.n.supply(s4Token, 106, 1000)
	r.n.slots(110, 110)
	var once sync.Once
	r.g.hook(func(pkg, path string) {
		if pkg == s4Mailbox && path == "json" {
			once.Do(func() { // a send of 300 toward Solana: the lock 1,300, hlmailbox's count 1
				r.g.set(s4Mailbox+":json", s4MailboxJSON(1, 0))
				r.g.set(s4Route+":json", s4RouteJSON(1300, 0, 0, 0, ""))
				r.g.set(s4Mailbox+":msg/0", msgRender(s4Send(0, 300), 140))
			})
		}
	})
	r.step(10 * time.Second)
	r.poll()
	if c := r.cut(); !strings.Contains(c, "dispatched 1 message(s) while the routes were read") {
		t.Fatalf("the poll took a verdict over a send landing between the mailbox's read and the route's: %s (%s)", r.verdict(), c)
	}
	r.g.hook(nil)
	r.step(10 * time.Second)
	r.poll()
	if v := r.verdict(); v != "BROKEN" || !r.alarmed("identity:BUBBLE:flag") {
		t.Fatalf("the forged mint was not flagged once the send was walked: %s, %v", v, r.m.Alarm.Standing())
	}
}

// The sibling (the red team's "every counter"): a token whose line of migrations is open is judged over its realms'
// summed counters, each realm read in its own render. Page 0, which moves every counter from the predecessor to the
// successor, landing between the two reads counted the lock twice (the configured predecessor read before it, the
// successor after): the reading short by the whole lock, a forged mint passing — the halt lifted, its burn signed.
// The line is read again with hlmailbox after the marks: its realms' counters moved, no verdict.
func TestS4SignerLineMovedBetweenReads(t *testing.T) {
	r := newS4Signer(t, false)
	line := fmt.Sprintf(`{"domain":%d,"count":0,"delivered":0,"live":true,"routes":{%q:{"successor":%q},%q:{"predecessor":%q}}}`, hl.DomainGnoTest, s4Route, s4Succ, s4Succ, s4Route)
	r.g.set(s4Mailbox+":json", line)
	r.g.set(s4Route+":json", s4RouteJSON(1000, 0, 0, 0, s4Succ)) // migrated to the successor; page 0 not pulled yet
	r.g.set(s4Succ+":json", s4RouteJSON(0, 0, 0, 0, ""))
	r.n.slots(100, 100)
	r.round()
	if r.s.Status()["signing"] != true {
		t.Fatalf("round 1 over the line: %v", r.s.Status())
	}
	r.n.supply(s4Token, 105, 1300)        // a forged mint of 300 …
	r.n.burn(r.cfg, 0, 106, s4Alice, 300) // … burned straight back
	r.n.supply(s4Token, 106, 1000)
	r.n.slots(110, 110)
	r.round()
	if st := r.s.Status(); st["signing"] == true {
		t.Fatalf("round 2 did not halt on the forged mint: %v", st)
	}
	// round 3: page 0 lands right after the predecessor was read
	var once sync.Once
	r.g.hook(func(pkg, path string) {
		if pkg == s4Route && path == "json" {
			once.Do(func() {
				r.g.set(s4Route+":json", s4RouteJSON(0, 0, 0, 0, s4Succ))
				r.g.set(s4Succ+":json", s4RouteJSON(1000, 0, 1, 0, ""))
			})
		}
	})
	r.round()
	if !strings.Contains(r.cut(), "BUBBLE's counters moved while the cut was read") || r.signed(0) {
		t.Fatalf("round 3 took a verdict over page 0 landing between the line's reads: signing %v, the forged burn signed %v, the cut %q", r.s.Status()["signing"], r.signed(0), r.cut())
	}
	r.g.hook(nil)
	r.round()
	if st := r.s.Status(); st["signing"] == true || !strings.Contains(fmt.Sprint(st["halted"]), "broken") || r.signed(0) {
		t.Fatalf("round 4 over the moved line: %v", st)
	}
}

// ------------------------------------------------------------------------------------------- E3-GT9 --

// E3-GT9 on the signer: a provider answers an account at its own finalized slot and the snapshot took the lower of
// the two, with no floor. A forged mint of 300 burned back halted the signer at slot 110; the forger then sent 300
// from gno.land (minted at 112) and burned 300 of his own at 114, so the supply is 1,000 again — and one provider
// stepping back to slot 105, a real finalized slot with the same supply, read the lock with his send against a
// supply without it and his burn out of flight: the identity held, the halt lifted and the forged burn was signed.
// A snapshot below the highest slot a round already read at is no verdict (quiet, alarmed at the fifth round in a
// row), and the floor is in the state: a restart keeps it.
func TestS4FinalizedFloorSigner(t *testing.T) {
	r := newS4Signer(t, false)
	r.n.supply(s4Token, 101, 1300)        // a forged mint of 300 …
	r.n.burn(r.cfg, 0, 102, s4Alice, 300) // … burned straight back
	r.n.supply(s4Token, 102, 1000)
	r.n.slots(110, 110)
	r.round()
	if st := r.s.Status(); st["signing"] == true || !strings.Contains(fmt.Sprint(st["halted"]), "broken") {
		t.Fatalf("the forged mint did not halt the signer at slot 110: %v", st)
	}
	r.g.set(s4Route+":json", s4RouteJSON(1300, 0, 0, 0, "")) // the forger's send of 300 …
	r.n.supply(s4Token, 112, 1300)                           // … minted on Solana
	r.n.burn(r.cfg, 1, 114, s4Alice, 300)                    // and 300 of his own burned
	r.n.supply(s4Token, 114, 1000)
	r.n.slots(120, 120)
	r.round()
	if st := r.s.Status(); st["signing"] == true {
		t.Fatalf("slot 120: %v", st)
	}
	// provider 1 steps back to slot 105, whose supply (1,000) is provider 2's at 125
	r.n.slots(105, 125)
	r.round()
	if !strings.Contains(r.cut(), "below 120, the highest a reading already rested on") || r.signed(0) || r.s.Status()["signing"] == true {
		t.Fatalf("a snapshot below the floor was read: signing %v, the forged burn signed %v, the cut %q", r.s.Status()["signing"], r.signed(0), r.cut())
	}
	if r.alarmed("solana:finalized-floor") {
		t.Fatal("the first step-back alarmed")
	}
	// a restart keeps the floor
	r.s = r.newSigner(r.s.Store)
	for i := 1; i <= 5; i++ {
		r.round()
		if !strings.Contains(r.cut(), "below 120") || r.signed(0) {
			t.Fatalf("after the restart, round %d read below the floor: the forged burn signed %v, the cut %q", i, r.signed(0), r.cut())
		}
		if got := r.alarmed("solana:finalized-floor"); got != (i == 5) {
			t.Fatalf("round %d below the floor: alarmed %v", i, got)
		}
	}
	// both providers at or above the floor again: the break shows, the alarm clears
	r.n.slots(130, 130)
	r.round()
	if st := r.s.Status(); st["signing"] == true || !strings.Contains(fmt.Sprint(st["halted"]), "broken") || r.alarmed("solana:finalized-floor") || r.signed(0) {
		t.Fatalf("back at 130: %v", st)
	}
}

// The snapshot's other step-back: the client's state, read after the supplies, lowered the snapshot's slot to the
// slot provider 1 answered it at. Provider 1 answering it from slot 103 (the trusted header unchanged since, so both
// providers agree on it) put the burns between 103 and the supplies' slot out of flight while the supply had them
// burned: the forger's own burn of 300 at 104 made room for his forged mint, and its burn (at 102) was signed. The
// client's state below the supplies' slot is no verdict now.
func TestS4SignerClientStateBelowTheSupplies(t *testing.T) {
	r := newS4Signer(t, true) // the client's trusted height 90, below the node's head
	r.n.slots(100, 100)
	r.round()
	if r.s.Status()["signing"] != true {
		t.Fatalf("round 1: %v (%s)", r.s.Status(), r.cut())
	}
	r.n.supply(s4Token, 101, 1300)        // a forged mint of 300 …
	r.n.burn(r.cfg, 0, 102, s4Alice, 300) // … burned straight back
	r.n.supply(s4Token, 102, 1000)
	r.n.burn(r.cfg, 1, 104, s4Alice, 300) // and 300 of the forger's own
	r.n.supply(s4Token, 104, 700)
	r.n.slots(110, 110)
	r.n.pinAt(0, s4ClientState(), 103)
	r.round()
	// (round 1's verdict, at slot 100, still signs what it counted: the forged burn at 102 is not among it)
	if !strings.Contains(r.cut(), "the client's state was answered at slot 103, below the supplies' slot 110") || r.signed(0) {
		t.Fatalf("a client state below the supplies' slot lowered the cut: halted %q, the forged burn signed %v, the cut %q", r.s.Status()["halted"], r.signed(0), r.cut())
	}
	r.n.pinAt(0, s4ClientState(), 0)
	r.round()
	if st := r.s.Status(); st["signing"] == true || !strings.Contains(fmt.Sprint(st["halted"]), "broken") || r.signed(0) {
		t.Fatalf("both at the supplies' slot: %v", st)
	}
}

// E3-GT9 on the monitor: its sends' processed marks stay. A step-back to slot 104 (provider 1; the supply there,
// 1,000, is provider 2's at 115) read the lock with the forger's send — marked processed at slot 110 — against a
// supply without its mint, and his own burn at 108 out of flight: the break read as holding and its flag alarm
// cleared. Below the floor is no verdict, alarmed once it has stood two minutes, and a restart keeps the floor.
func TestS4FinalizedFloorMonitor(t *testing.T) {
	r := newS4Monitor(t, false)
	r.n.supply(s4Token, 101, 1300)        // a forged mint of 300 …
	r.n.burn(r.cfg, 0, 102, s4Alice, 300) // … burned straight back
	r.n.supply(s4Token, 102, 1000)
	send := s4Send(0, 300) // the forger's send of 300 from gno.land, processed on Solana at 106
	r.g.set(s4Mailbox+":json", s4MailboxJSON(1, 0))
	r.g.set(s4Route+":json", s4RouteJSON(1300, 0, 0, 0, ""))
	r.g.set(s4Mailbox+":msg/0", msgRender(send, 140))
	r.n.supply(s4Token, 106, 1300)
	r.n.processed(r.cfg, send.ID(), 106)
	r.n.burn(r.cfg, 1, 108, s4Alice, 300) // and 300 of his own burned: the supply 1,000 again
	r.n.supply(s4Token, 108, 1000)
	r.n.slots(110, 110)
	r.poll()
	if v := r.verdict(); v != "BROKEN" || !r.alarmed("identity:BUBBLE:flag") {
		t.Fatalf("poll 1 at slot 110: %s (%s)", v, r.cut())
	}
	r.n.slots(104, 115)
	r.step(10 * time.Second)
	r.poll()
	if c := r.cut(); !strings.Contains(c, "below 110") || !r.alarmed("identity:BUBBLE:flag") {
		t.Fatalf("a step-back below the floor was read: %s (%s), alarms %v", r.verdict(), c, r.m.Alarm.Standing())
	}
	for i := 0; i < 12; i++ { // two minutes of it
		r.step(10 * time.Second)
		r.poll()
	}
	if !r.alarmed("solana:finalized-floor") {
		t.Fatalf("two minutes below the floor, no alarm: %v", r.m.Alarm.Standing())
	}
	// a restart keeps the floor
	r.m = r.newMonitor(r.m.State)
	r.step(10 * time.Second)
	r.poll()
	if c := r.cut(); !strings.Contains(c, "below 110") {
		t.Fatalf("after a restart, a step-back below the floor was read: %s (%s)", r.verdict(), c)
	}
	r.n.slots(120, 120)
	r.step(10 * time.Second)
	r.poll()
	if v := r.verdict(); v != "BROKEN" || r.alarmed("solana:finalized-floor") {
		t.Fatalf("back above the floor: %s (%s), alarms %v", v, r.cut(), r.m.Alarm.Standing())
	}
}

// The in-poll half on this bridge, which a floor cannot reach: the monitor reads the sends' processed marks after its
// snapshot, at the providers' slot then, and keeps them. A send delivered between the supply's read and its mark's
// counted out of flight against a supply without its mint: the reading short by it, a forged mint of its amount
// passing the poll (here the supplies answered at 104, the marks at 110; the forger's send processed at 106). The
// record carries the slot it was processed in: a send processed after the snapshot's slot is in flight against it.
func TestS4MonitorMarkAboveTheSnapshot(t *testing.T) {
	r := newS4Monitor(t, false)
	send := s4Send(0, 300) // the forger's send of 300 from gno.land
	r.g.set(s4Mailbox+":json", s4MailboxJSON(1, 0))
	r.g.set(s4Route+":json", s4RouteJSON(1300, 0, 0, 0, ""))
	r.g.set(s4Mailbox+":msg/0", msgRender(send, 140))
	r.n.supply(s4Token, 102, 1300)        // a forged mint of 300 …
	r.n.burn(r.cfg, 0, 103, s4Alice, 300) // … burned straight back
	r.n.supply(s4Token, 103, 1000)
	r.n.supply(s4Token, 106, 1300) // the send minted …
	r.n.processed(r.cfg, send.ID(), 106)
	mint := sol.Synthetic{Program: s4Token}.Mint()
	r.n.slots(110, 110)
	r.n.pinAt(0, mint, 104) // … after the supplies' read
	r.n.pinAt(1, mint, 104)
	r.poll()
	if v := r.verdict(); v != "BROKEN" || !r.alarmed("identity:BUBBLE:flag") {
		t.Fatalf("a send processed after the snapshot counted out of flight against its supply: %s (%s)", v, r.cut())
	}
	if s := r.m.state.Sends["0"]; s == nil || !s.Processed || s.ProcessedSlot != 106 {
		t.Fatalf("the mark and its slot: %+v", s)
	}
}

// The processed record's slot as Hyperlane writes it (initialized, "PROCESSD", the sequence, the id, the slot);
// anything else reads as no slot — 0, the mark as before, processed at any slot — never a slot a false break would
// rest on.
func TestS4ProcessedSlot(t *testing.T) {
	id := [32]byte{1, 2, 3}
	rec := append([]byte{1}, processedTag...)
	rec = binary.LittleEndian.AppendUint64(rec, 7)
	rec = append(rec, id[:]...)
	rec = binary.LittleEndian.AppendUint64(rec, 106)
	if got := processedSlot(rec, id); got != 106 {
		t.Fatalf("the record's slot: %d", got)
	}
	other := append([]byte{}, rec...)
	copy(other[1:9], dispatchTag)
	uninit := append([]byte{}, rec...)
	uninit[0] = 0
	for name, c := range map[string]struct {
		data []byte
		id   [32]byte
	}{"another id's": {rec, [32]byte{9}}, "short": {rec[:56], id}, "another account's tag": {other, id}, "uninitialized": {uninit, id}, "a bare owner's account": {[]byte{1}, id}} {
		if got := processedSlot(c.data, c.id); got != 0 {
			t.Errorf("%s read as slot %d", name, got)
		}
	}
}

// The monitor's client state below the supplies' slot, as the signer's: the burns between the two slots were out of
// flight while the supply had them burned (the forger's own burn at 104 made room for his forged mint): no verdict.
func TestS4MonitorClientStateBelowTheSupplies(t *testing.T) {
	r := newS4Monitor(t, true)
	r.n.slots(100, 100)
	r.poll()
	if v := r.verdict(); v != "holds" {
		t.Fatalf("poll 1: %s (%s)", v, r.cut())
	}
	r.n.supply(s4Token, 101, 1300)        // a forged mint of 300 …
	r.n.burn(r.cfg, 0, 102, s4Alice, 300) // … burned straight back
	r.n.supply(s4Token, 102, 1000)
	r.n.burn(r.cfg, 1, 104, s4Alice, 300) // and 300 of the forger's own
	r.n.supply(s4Token, 104, 700)
	r.n.slots(110, 110)
	r.n.pinAt(0, s4ClientState(), 103)
	r.step(10 * time.Second)
	r.poll()
	if c := r.cut(); !strings.Contains(c, "the client's state was answered at slot 103, below the supplies' slot 110") || r.verdict() != "holds" {
		t.Fatalf("a client state below the supplies' slot lowered the cut: %s (%s)", r.verdict(), c)
	}
	r.n.pinAt(0, s4ClientState(), 0)
	r.step(10 * time.Second)
	r.poll()
	if v := r.verdict(); v != "BROKEN" || !r.alarmed("identity:BUBBLE:flag") {
		t.Fatalf("both at the supplies' slot: %s (%s)", v, r.cut())
	}
}

// hlwatch reads in the monitors' order and judges the same identity, so the same races: a credit landing between
// its route's read and its marks' read, and a snapshot below a slot it already read at (its state file keeps the
// floor between runs). A moving cut gives no identity this reading (named as not read); below the floor, no reading
// of Solana at all.
func TestS4HlwatchReadsTwiceAndKeepsTheFloor(t *testing.T) {
	cfg, n, g := s4Config(false), newSolNet(t), newS4Gno()
	g.set(s4Mailbox+":json", s4MailboxJSON(0, 0))
	g.set(s4Route+":json", s4RouteJSON(1000, 0, 0, 0, ""))
	g.set(s4Route+":claims/0", "[]")
	n.supply(s4Token, 1, 1000)
	honest := n.burn(cfg, 0, 102, s4Bob, 300)
	n.supply(s4Token, 102, 700)
	providers := n.clients()
	st := &WatchState{}
	st.init()
	ctx := context.Background()
	read := func() (*SolanaView, error) {
		snap, err := TakeSnapshot(ctx, providers, cfg)
		if err != nil {
			t.Fatal(err)
		}
		return ReadSolanaAt(ctx, providers, g, cfg, st, snap, 200, s4ClockZero)
	}
	n.slots(103, 103)
	if v, err := read(); err != nil || v.Identities["BUBBLE"].Lock != 1000 {
		t.Fatalf("the first reading: %v %+v", err, v)
	} else if verdict, _ := v.Identities["BUBBLE"].Verdict(); verdict != Holds {
		t.Fatalf("the first reading: %s", verdict)
	}
	n.supply(s4Token, 105, 1000)      // a forged mint of 300 …
	n.burn(cfg, 1, 106, s4Alice, 300) // … burned straight back
	n.supply(s4Token, 106, 700)
	n.slots(110, 110)
	var once sync.Once
	g.hook(func(pkg, path string) {
		if pkg == s4Route && path == "json" {
			once.Do(func() {
				g.set(s4Route+":json", s4RouteJSON(1000, 300, 0, 0, ""))
				g.set(s4Mailbox+":delivered/"+s4IDHex(honest), "150")
				g.set(s4Mailbox+":json", s4MailboxJSON(0, 1))
			})
		}
	})
	v, err := read()
	if err != nil {
		t.Fatal(err)
	}
	if id, ok := v.Identities["BUBBLE"]; ok || !strings.Contains(strings.Join(v.Err, "; "), "the identity: no verdict: hlmailbox delivered 1 message(s)") {
		verdict, text := id.Verdict()
		t.Fatalf("a reading over a credit landing mid-read judged the identity: %s %s (errors %v)", verdict, text, v.Err)
	}
	g.hook(nil)
	v, err = read()
	if err != nil {
		t.Fatal(err)
	}
	if verdict, text := v.Identities["BUBBLE"].Verdict(); verdict != Broken {
		t.Fatalf("the next reading: %s %s", verdict, text)
	}
	// a provider stepped back below the floor (kept in the state file between runs): no reading of Solana
	n.slots(104, 125)
	if _, err := read(); err == nil || !strings.Contains(err.Error(), "below 110") {
		t.Fatalf("a snapshot below the floor was read: %v", err)
	}
}
