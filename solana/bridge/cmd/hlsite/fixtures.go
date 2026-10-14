package main

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gagliardetto/solana-go"
	"github.com/gagliardetto/solana-go/rpc"

	"github.com/gnolang/bridges/solana/bridge/gno"
	"github.com/gnolang/bridges/solana/bridge/gnolight"
	"github.com/gnolang/bridges/solana/bridge/hl"
	"github.com/gnolang/bridges/solana/bridge/sol"
)

// The mock deployment (-mock, and the tests): a gno node and a Solana provider from canned data in the realms'
// and the programs' exact shapes — the mailbox's and the routes' json as bridge/local/e2e.sh reads them
// (M3-NOTES.md, M3l), the pausable ISM's, the admin program's, the client's and the mailbox's accounts in the
// layouts bridge/sol parses, the transactions as the programs' own instruction builders make them — so the readers
// run the same code they run against a chain.
//
// The world (M7) is a history and two transfers in flight:
//
//	history: r/hlgov's decision #3 sent (nonce 0) and enacted on Solana; 25 GNOT test1 → alice (nonce 1), a header
//	         past its block, its proof, its mint; alice's 500 BUBBLE burned back (Solana nonce 8), vouched by all
//	         three signers, credited and paid
//	out:     10 GNOT test1 → alice (nonce 2, block 1290): 0 locked · 1 the header of block 1291, whose results
//	         hash commits to it, accepted · 2 the proof staged against that header in the client's ring · 3 minted
//	in:      200 BUBBLE alice → test1 (Solana nonce 9): 0 burned · 1 signer 1 vouches · 2 signer 2 too · 3 credited
//	         · 4 half paid (signer 3 vouches too) · 5 paid in full
//
// -mock starts both transfers at 0 and POST /api/mock/step moves them (the browser suite); the tests' and the
// preview's world is done. The figures move with the stages so that the identity holds at every one: a send in
// flight is a gap until its mint, a burn until its credit.

const (
	mockNS        = "gno.land/r/g1leu8d2vsplhehcfkjg50mwgdpxdkt8tztu95wr/"
	mockGovernor  = "g1dpkz6em0wejhymn0wf047h6lta047h6lvxn4gx"
	mockSender    = "g1jg8mtutu9khhfwc4nxmuhcpftf0pajdhfvsqf5" // gnodev's test1
	mockRecipient = "g1jg8mtutu9khhfwc4nxmuhcpftf0pajdhfvsqf5"
	mockSolDomain = uint32(1399811150)
	mockGnoDomain = uint32(1198419796)
	one           = int64(1_000_000)
	mockHead      = int64(1300)             // gno.land's head when the world starts
	mockBlock     = 3400 * time.Millisecond // and a block after that
	mockSlot      = uint64(400_000)         // Solana's slot when the world starts (0.4 s a slot)
	// the history's blocks
	decisionHeight = int64(300)
	pHeight        = int64(600)
	qCreditHeight  = int64(900)
	outHeight      = int64(1290)
)

// mockKey is a deterministic keypair for a name (the local run's hlsol keygen, the same idea: anyone can derive it).
func mockKey(name string) solana.PrivateKey {
	s := sha256.Sum256([]byte("hlsite mock: " + name))
	return solana.PrivateKey(ed25519.NewKeyFromSeed(s[:]))
}

func mockPub(name string) solana.PublicKey { return mockKey(name).PublicKey() }

func sum256(s string) []byte { h := sha256.Sum256([]byte(s)); return h[:] }

func mockSig(name string) solana.Signature {
	h := sha512.Sum512([]byte("hlsite mock signature: " + name))
	return solana.SignatureFromBytes(h[:])
}

// the world's messages: gno.land's three dispatches and the two burns toward it
func gnoAddr32(pkgPath string) [32]byte {
	b, _ := hl.GnoAddressToBytes32(hl.GnoPackageAddress(pkgPath))
	return b
}

func mockSendMessage(nonce uint32, amount int64) hl.Message {
	alice := mockPub("alice")
	var to [32]byte
	copy(to[:], alice.Bytes())
	m := hl.Message{Version: hl.Version, Nonce: nonce, Origin: mockGnoDomain, Sender: gnoAddr32(mockNS + "hlgnot"), Destination: mockSolDomain, Body: hl.TokenMessage(to, uint64(amount), nil)}
	copy(m.Recipient[:], mockPub("gnot").Bytes())
	return m
}

func mockOutMessage() hl.Message { return mockSendMessage(2, 10*one) }
func mockPMessage() hl.Message   { return mockSendMessage(1, 25*one) }

// mockDecisionMessage is r/hlgov's decision #3, kind 7 (set the protocol fee), to the admin program.
func mockDecisionMessage() hl.Message {
	body := []byte{sol.KindSetFee}
	body = binary.BigEndian.AppendUint64(body, 3)
	body = append(body, feeParams(1000, mockPub("treasurer"))...)
	m := hl.Message{Version: hl.Version, Nonce: 0, Origin: mockGnoDomain, Sender: gnoAddr32(mockNS + "hlgov"), Destination: mockSolDomain, Body: body}
	copy(m.Recipient[:], mockPub("admin").Bytes())
	return m
}

func mockBurnMessageOf(nonce uint32, amount int64) hl.Message {
	rcpt, _ := hl.GnoAddressToBytes32(mockRecipient)
	msg := hl.Message{Version: hl.Version, Nonce: nonce, Origin: mockSolDomain, Destination: mockGnoDomain, Body: hl.TokenMessage(rcpt, uint64(amount), nil)}
	copy(msg.Sender[:], mockPub("bubble").Bytes())
	msg.Recipient = gnoAddr32(mockNS + "hlbubble")
	return msg
}

// mockBurnMessage is the burn in flight: 200 BUBBLE for test1, Solana nonce 9. Its id is mockInID.
func mockBurnMessage() hl.Message { return mockBurnMessageOf(9, 200*one) }

// mockQMessage is the history's burn: 500 BUBBLE for test1, Solana nonce 8.
func mockQMessage() hl.Message { return mockBurnMessageOf(8, 500*one) }

func idOf(m hl.Message) string { id := m.ID(); return hex.EncodeToString(id[:]) }

// the known transfers and the wallet's transaction hash
var (
	mockOutID     = idOf(mockOutMessage())
	mockInID      = idOf(mockBurnMessage())
	mockPID       = idOf(mockPMessage())
	mockQID       = idOf(mockQMessage())
	mockDecID     = idOf(mockDecisionMessage())
	mockGnoTx     = base64.StdEncoding.EncodeToString(sum256("hlsite mock gno tx"))
	mockBurnTx    = mockSig("burn in").String()
	mockBlockhash = solana.HashFromBytes(sum256("hlsite mock blockhash"))
)

// mockConfig is the mock deployment's config.
func mockConfig() *Config {
	var c Config
	c.Gno.RPC, c.Gno.ChainID = "http://127.0.0.1:26657", "dev"
	c.Gno.Mailbox, c.Gno.Fees, c.Gno.Gov, c.Gno.Bubble = mockNS+"hlmailbox", mockNS+"hlfees", mockNS+"hlgov", mockNS+"bubble"
	c.Gno.RealmLink, c.Gno.TxLink, c.Gno.GasWanted = "https://gno.land/%s", "https://gnoscan.io/transactions/details?txhash=%s", defaultGasWanted
	ch := ChainConfig{ID: "solana", Kind: "solana", Name: "Solana", RPC: "http://127.0.0.1:8899", Domain: mockSolDomain, Explorer: "https://explorer.solana.com", Cluster: "devnet",
		Signers: []string{"https://signers.mock/1", "https://signers.mock/2", "https://signers.mock/3"}}
	ch.Programs.Mailbox, ch.Programs.Pausable, ch.Programs.Admin, ch.Programs.Client = mockPub("mailbox").String(), mockPub("pausable").String(), mockPub("admin").String(), mockPub("client").String()
	ch.Tokens = []TokenConfig{{Symbol: "GNOT", Program: mockPub("gnot").String(), Realm: mockNS + "hlgnot"}, {Symbol: "BUBBLE", Program: mockPub("bubble").String(), Realm: mockNS + "hlbubble"}}
	c.Chains = []ChainConfig{ch}
	if err := c.check(); err != nil {
		panic(err)
	}
	return &c
}

// ---------------------------------------------------------------- the world --

type mockWorld struct {
	mu      sync.Mutex
	now     func() time.Time
	start   time.Time
	out, in int
	down    bool             // signer 3 does not answer
	at      map[string]int64 // when a stage was reached ("out1", "in3"): the events' times
}

func newMockWorld(now func() time.Time, done bool) *mockWorld {
	w := &mockWorld{now: now, start: now(), at: map[string]int64{}}
	if done {
		w.out, w.in = 3, 5
		for _, k := range []string{"out1", "out2", "out3", "in1", "in2", "in3", "in4", "in5"} {
			w.at[k] = w.start.Unix()
		}
	}
	return w
}

func (w *mockWorld) step(flow string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	switch flow {
	case "out":
		if w.out >= 3 {
			return errors.New("the send is minted already")
		}
		w.out++
		w.at["out"+strconv.Itoa(w.out)] = w.now().Unix()
	case "in":
		if w.in >= 5 {
			return errors.New("the claim is paid already")
		}
		w.in++
		w.at["in"+strconv.Itoa(w.in)] = w.now().Unix()
	case "down":
		w.down = !w.down
	case "reset": // the browser suite's next run starts from the same world
		w.out, w.in, w.down, w.at = 0, 0, false, map[string]int64{}
	default:
		return errors.New("flow: out, in, down or reset")
	}
	return nil
}

func (w *mockWorld) stages() map[string]any {
	w.mu.Lock()
	defer w.mu.Unlock()
	return map[string]any{"out": w.out, "in": w.in, "down": w.down}
}

func (w *mockWorld) get() (out, in int, down bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.out, w.in, w.down
}

func (w *mockWorld) when(key string) int64 {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.at[key]
}

func (w *mockWorld) head() int64 { return mockHead + int64(w.now().Sub(w.start)/mockBlock) }

func (w *mockWorld) blockTime(h int64) time.Time {
	return w.start.Add(time.Duration(h-mockHead) * mockBlock)
}

func (w *mockWorld) heightAt(t int64) int64 {
	return mockHead + int64(time.Unix(t, 0).Sub(w.start)/mockBlock)
}

func (w *mockWorld) slotAt(t int64) uint64 { return uint64(int64(mockSlot) + (t-w.start.Unix())*5/2) }

// ------------------------------------------------------------------ gno --

// mockGno is a gno node with the realms' renders as the realms build them.
type mockGno struct {
	w       *mockWorld
	mu      sync.Mutex
	renders map[string]int // the renders asked for, by path up to its first slash: what a read costs the node
	// flags are standing flags on a route, as hlmailbox lists them ("fn:seat:addr:at" strings); paused the
	// route's paused functions
	flags  map[string][]string
	paused map[string]string
}

func newMockGno(w *mockWorld) *mockGno {
	return &mockGno{w: w, flags: map[string][]string{}, paused: map[string]string{}, renders: map[string]int{}}
}

func (m *mockGno) Status(ctx context.Context) (gno.Head, error) {
	h := m.w.head()
	return m.Block(ctx, h)
}

func (m *mockGno) Block(ctx context.Context, height int64) (gno.Head, error) {
	return gno.Head{Height: height, Time: m.w.blockTime(height), Hash: hex.EncodeToString(sum256("block " + strconv.FormatInt(height, 10)))}, nil
}

func (m *mockGno) GasPrice(ctx context.Context) (gno.GasPrice, error) {
	return gno.GasPrice{Gas: 1000, Amount: 1, Denom: "ugnot"}, nil
}

func (m *mockGno) TxEvents(ctx context.Context, hash string) (*gnoTx, error) {
	if hash != mockGnoTx {
		return nil, errors.New("gno rpc tx: tx (" + hash + ") not found")
	}
	return &gnoTx{Height: outHeight, Events: []gnoEvent{{Type: "SentTransferRemote", PkgPath: mockNS + "hlgnot", Attrs: map[string]string{"id": mockOutID, "amount": "10000000", "from": mockSender, "destination": strconv.FormatUint(uint64(mockSolDomain), 10)}}}}, nil
}

// mockMsgs are hlmailbox's dispatched messages by nonce, as "msgs/" renders them.
func (m *mockGno) mockMsgs() []msgJSON {
	var out []msgJSON
	for _, x := range []struct {
		msg    hl.Message
		height int64
	}{{mockDecisionMessage(), decisionHeight}, {mockPMessage(), pHeight}, {mockOutMessage(), outHeight}} {
		out = append(out, msgJSON{Nonce: x.msg.Nonce, ID: idOf(x.msg), Height: x.height, Message: hex.EncodeToString(x.msg.Bytes())})
	}
	return out
}

func (m *mockGno) RenderAt(ctx context.Context, pkgPath, path string, height int64) (string, error) {
	realm := strings.TrimPrefix(pkgPath, mockNS)
	m.mu.Lock()
	m.renders[strings.SplitN(path, "/", 2)[0]]++
	m.mu.Unlock()
	_, in, _ := m.w.get()
	switch {
	case realm == "hlmailbox" && path == "json":
		return m.mailboxJSON(), nil
	case realm == "hlmailbox" && strings.HasPrefix(path, "msgs/"):
		from, err := strconv.Atoi(strings.TrimPrefix(path, "msgs/"))
		if err != nil {
			return "[]", nil
		}
		var page []msgJSON
		for _, x := range m.mockMsgs() {
			if int(x.Nonce) >= from && len(page) < 20 {
				page = append(page, x)
			}
		}
		if page == nil {
			return "[]", nil
		}
		b, _ := json.Marshal(page)
		return string(b), nil
	case (realm == "hlgnot" || realm == "hlbubble") && path == "json":
		return m.routeJSON(realm), nil
	case path == "decisions.json":
		if realm == "hlfees" {
			t := m.w.start.Unix()
			return fmt.Sprintf(`[{"id":1,"kind":"set treasurer","text":"set treasurer to %s","state":"pending","made":%d,"recorded":%d,"due":%d,"applied":0,"cancelled":0,"note":"queued 7 days"}]`, mockSender, t-3600, t-3600, t-3600+7*86400), nil
		}
		return "[]", nil
	case realm == "hlgov" && path == "json":
		return fmt.Sprintf(`{"address":"%s","governor":"%s","yielded":0,"recipient":"%s","former":[],"last":3,"next":4}`, hl.GnoPackageAddress(pkgPath), mockGovernor, hex.EncodeToString(mockPub("admin").Bytes())), nil
	case strings.HasPrefix(path, "send/"):
		id := strings.TrimPrefix(path, "send/")
		switch {
		case realm == "hlgnot" && id == mockOutID:
			return fmt.Sprintf(`{"id":"%s","from":"%s","amount":%d,"at":%d,"imported":false,"refunded":0,"writtenOff":0}`, mockOutID, mockSender, 10*one, m.w.blockTime(outHeight).Unix()), nil
		case realm == "hlgnot" && id == mockPID:
			return fmt.Sprintf(`{"id":"%s","from":"%s","amount":%d,"at":%d,"imported":false,"refunded":0,"writtenOff":0}`, mockPID, mockSender, 25*one, m.w.blockTime(pHeight).Unix()), nil
		}
		return "null", nil
	case strings.HasPrefix(path, "claim/"):
		id := strings.TrimPrefix(path, "claim/")
		switch {
		case realm == "hlbubble" && id == mockQID:
			return fmt.Sprintf(`{"id":"%s","to":"%s","amount":%d,"paid":%d,"state":"done","by":"signers","at":%d,"height":%d,"seq":1}`, mockQID, mockRecipient, 500*one, 500*one, m.w.blockTime(qCreditHeight).Unix(), qCreditHeight), nil
		case realm == "hlbubble" && id == mockInID && in >= 3:
			paid, state := int64(0), "open"
			switch {
			case in >= 5:
				paid, state = 200*one, "done"
			case in == 4:
				paid = 100 * one
			}
			at := m.w.when("in3")
			return fmt.Sprintf(`{"id":"%s","to":"%s","amount":%d,"paid":%d,"state":"%s","by":"signers","at":%d,"height":%d,"seq":2}`, mockInID, mockRecipient, 200*one, paid, state, at, m.w.heightAt(at)), nil
		}
		return "null", nil
	case realm == "hlmailbox" && strings.HasPrefix(path, "delivered/"):
		switch strings.TrimPrefix(path, "delivered/") {
		case mockQID:
			return strconv.FormatInt(qCreditHeight, 10), nil
		case mockInID:
			if in >= 3 {
				return strconv.FormatInt(m.w.heightAt(m.w.when("in3")), 10), nil
			}
		}
		return "0", nil
	case strings.HasPrefix(path, "claims/") || strings.HasPrefix(path, "sweeps/") || strings.HasPrefix(path, "refunds/"):
		return "[]", nil
	}
	return "", fmt.Errorf("mock: no render for %s:%s", pkgPath, path)
}

func (m *mockGno) mailboxJSON() string {
	t := m.w.start.Unix()
	_, in, _ := m.w.get()
	delivered := 1
	if in >= 3 {
		delivered = 2
	}
	route := func(realm string) string {
		flags := "[]"
		if fs := m.flags[realm]; len(fs) > 0 {
			q := make([]string, len(fs))
			for i, f := range fs {
				q[i] = strconv.Quote(f)
			}
			flags = "[" + strings.Join(q, ",") + "]"
		}
		p := m.paused[realm]
		has := func(fn string) string { return strconv.FormatBool(strings.Contains(p, fn)) }
		return fmt.Sprintf(`%q:{"epoch":%d,"epochAt":%d,"sends":%s,"credits":%s,"payouts":%s,"flags":%s,"predecessor":"","successor":"","enrolled":0,"enrolledAt":0,"pulled":false,"dropped":[]}`, mockNS+realm, t-86400, t-86400, has("sends"), has("credits"), has("payouts"), flags)
	}
	return `{"domain":` + strconv.FormatUint(uint64(mockGnoDomain), 10) + `,"hook":"` + hex.EncodeToString(sum256("hook")) + `","address":"` + hl.GnoPackageAddress(mockNS+"hlmailbox") + `","count":3,"root":"` + hex.EncodeToString(sum256("root")) + `","delivered":` + strconv.Itoa(delivered) + `,"live":true,"launchedAt":` + strconv.FormatInt(t-86400, 10) +
		`,"governor":"` + mockGovernor + `","yielded":0,"launchOrigin":` + strconv.FormatUint(uint64(mockSolDomain), 10) + `,"clocks":{"factor":1,"delay":604800,"deadline":1209600},"decisions":4,"signers":{"keys":["` + hex.EncodeToString(mockPub("s0").Bytes()) + `","` + hex.EncodeToString(mockPub("s1").Bytes()) + `","` + hex.EncodeToString(mockPub("s2").Bytes()) + `"],"threshold":2,"groupA":[],"groupB":[]},` +
		`"verifiers":{"` + strconv.FormatUint(uint64(mockSolDomain), 10) + `":{"pkgpath":"` + mockNS + `hlverifier","registered":true}},"registered":["` + mockNS + `hlverifier"],"registeredCount":1,"seats":["` + mockGovernor + `","g1aeddl3g5hr8v5y8vx3s4x9m7qeq9h4u6xmq6wh","g1manfred47kzduec920z88wfr64y5asmjd4q3pz","","` + mockSender + `",""],` +
		`"routes":{` + route("hlbubble") + "," + route("hlgnot") + `},"droppedRoutes":{},"successors":[],"successorCount":0,"successorRoutes":{}}`
}

// routeJSON is a route's figures at the world's stages: GNOT's lock holds the send in flight from the first
// stage (Solana's supply takes it at the mint); BUBBLE's holds the burn in flight until it is credited, then owes
// it, then pays it out — and 300 owed to another recipient all along.
func (m *mockGno) routeJSON(realm string) string {
	_, in, _ := m.w.get()
	sym, lock, fee, minimum, floor := "GNOT", 1_000_000*one, 5*one, 10*one, 10_000*one
	owed, openClaims := int64(0), 0
	if realm == "hlbubble" {
		sym, fee, minimum = "BUBBLE", 100*one, 200*one
		lock, owed, openClaims = 2_500_200*one, 300*one, 1
		switch {
		case in >= 5:
			lock = 2_500_000 * one
		case in == 4:
			lock, owed, openClaims = 2_500_100*one, 400*one, 2
		case in == 3:
			owed, openClaims = 500*one, 2
		}
	}
	cap := max(lock/10, floor)
	return fmt.Sprintf(`{"address":%q,"live":true,"governor":%q,"yielded":0,"balance":%d,"paused":%q,"symbol":%q,"domain":%d,"router":%q,"oldRouter":%q,"oldUntil":0,"lock":%d,"sentTotal":%d,"paidTotal":%d,"owed":%d,"voided":0,"lost":0,"sweep":0,"cap":%d,"meter":%d,"capFloor":%d,"fee":%d,"feeFloor":%d,"minimum":%d,"maximum":%d,"openClaims":%d,"claims":7,"sends":12,"refunded":0,"refundedTotal":0,"writtenOff":0,"writtenOffTotal":0,"migratedTo":"","importing":false,"imported":0,"clocks":{"factor":1,"delay":604800,"deadline":1209600},"decisions":0}`,
		hl.GnoPackageAddress(mockNS+realm), mockGovernor, lock, m.paused[realm], sym, mockSolDomain, hex.EncodeToString(mockPub(strings.ToLower(sym)).Bytes()), strings.Repeat("00", 32), lock, lock+700*one, 700*one, owed, cap, cap/3, floor, fee, fee/2, minimum, 25_000*one, openClaims)
}

// --------------------------------------------------------------- solana --

// mockSol is a Solana provider from canned accounts and transactions.
type mockSol struct {
	mu       sync.Mutex
	w        *mockWorld
	accounts map[solana.PublicKey]mockAccount
	supplies map[solana.PublicKey]int64 // a test's override of a mint's supply
	txs      []*mockTx
	progs    sol.Programs
	calls    map[string]int // the RPC calls made, by method: what a read costs a public provider
	delay    time.Duration  // a slow provider: every account batch waits this long
}

func (m *mockSol) count(method string) {
	m.mu.Lock()
	m.calls[method]++
	m.mu.Unlock()
}

type mockAccount struct {
	owner solana.PublicKey
	data  []byte
}

// mockTx is a transaction the provider serves once its stage is reached: its signature is listed under every
// account it names.
type mockTx struct {
	sig     solana.Signature
	at      func() int64 // its block time
	reached func() bool
	ixs     []solana.Instruction
	payer   solana.PublicKey
	keys    []solana.PublicKey
}

func (t *mockTx) slot(w *mockWorld) uint64 { return w.slotAt(t.at()) }

func mockPrograms() sol.Programs {
	return sol.Programs{Mailbox: mockPub("mailbox"), Tokens: [2]solana.PublicKey{mockPub("gnot"), mockPub("bubble")}, Client: mockPub("client"), Pausable: mockPub("pausable"), Admin: mockPub("admin")}
}

func newMockSol(w *mockWorld, opts ...func(*mockSol)) *mockSol {
	progs := mockPrograms()
	m := &mockSol{w: w, accounts: map[solana.PublicKey]mockAccount{}, supplies: map[solana.PublicKey]int64{}, progs: progs, calls: map[string]int{}}
	t := w.start.Unix()
	seats := [sol.Keys]solana.PublicKey{mockPub("g1"), mockPub("g2"), mockPub("g3"), mockPub("g4"), mockPub("watch5"), mockPub("watch6")}
	g := sol.Guardians{Keys: seats}
	pausable := progs.PausableISM()
	m.accounts[pausable.Flags()] = mockAccount{owner: progs.Pausable, data: encodeFlags(g)}
	m.accounts[pausable.State()] = mockAccount{owner: progs.Pausable, data: encodePausableState(progs, t)}
	admin := progs.AdminProgram()
	m.accounts[admin.State()] = mockAccount{owner: progs.Admin, data: encodeAdminState(g, 3, 1)}
	m.accounts[admin.Decision(3)] = mockAccount{owner: progs.Admin, data: encodeDecision(3, 7, feeParams(1000, mockPub("treasurer")), mockSlot-1000, t-3300, mockPub("relayer"))}
	mb := progs.MailboxProgram()
	m.accounts[mb.Outbox()] = mockAccount{owner: progs.Mailbox, data: encodeOutbox(10)}
	always := func() bool { return true }
	fixed := func(at int64) func() int64 { return func() int64 { return at } }
	stage := func(flow string, n int) func() bool {
		return func() bool {
			out, in, _ := w.get()
			if flow == "out" {
				return out >= n
			}
			return in >= n
		}
	}
	reachedAt := func(key string) func() int64 { return func() int64 { return w.when(key) } }
	// the burns: the history's (nonce 8) and the one in flight (nonce 9), each with its dispatch record
	m.addBurn(mockSig("burn Q"), "uniqueQ", mockQMessage(), 500*one, t-1800)
	m.addBurn(mockSig("burn in"), "unique", mockBurnMessage(), 200*one, t-300)
	// the deliveries: the decision, the history's send, and the send in flight once minted
	m.addDelivery(mockSig("delivery decision"), mockDecisionMessage(), progs.Admin, fixed(w.blockTime(decisionHeight).Unix()+90), always)
	m.addDelivery(mockSig("delivery P"), mockPMessage(), progs.Tokens[0], fixed(w.blockTime(pHeight).Unix()+100), always)
	m.addDelivery(mockSig("delivery out"), mockOutMessage(), progs.Tokens[0], reachedAt("out3"), stage("out", 3))
	// the client: headers past each send's block and the proofs staged under them
	m.addUpdate("P header", 604, fixed(w.blockTime(pHeight).Unix()+40), always)
	m.addProof("P proof", mockPMessage(), 601, 4, 2, 2, fixed(w.blockTime(pHeight).Unix()+60), always)
	m.addUpdate("header 1288", 1288, fixed(w.blockTime(1288).Unix()+20), always)
	m.addUpdate("out header", outHeight+1, reachedAt("out1"), stage("out", 1))
	m.addProof("out proof", mockOutMessage(), -(outHeight + 1), 3, 1, 2, reachedAt("out2"), stage("out", 2))
	for _, o := range opts {
		o(m)
	}
	return m
}

func (m *mockSol) add(name string, sig solana.Signature, payer solana.PublicKey, at func() int64, reached func() bool, ixs ...solana.Instruction) {
	tx := &mockTx{sig: sig, at: at, reached: reached, ixs: ixs, payer: payer}
	seen := map[solana.PublicKey]bool{payer: true}
	tx.keys = append(tx.keys, payer)
	for _, ix := range ixs {
		for _, a := range ix.Accounts() {
			if !seen[a.PublicKey] {
				seen[a.PublicKey] = true
				tx.keys = append(tx.keys, a.PublicKey)
			}
		}
	}
	m.txs = append(m.txs, tx)
}

// addBurn is a burn toward gno.land: alice's transfer_remote and the dispatch record the mailbox wrote in its slot.
func (m *mockSol) addBurn(sig solana.Signature, unique string, msg hl.Message, amount int64, at int64) {
	alice, u := mockPub("alice"), mockPub(unique)
	rcpt, _ := hl.GnoAddressToBytes32(mockRecipient)
	token := sol.Synthetic{Program: m.progs.Tokens[1]}
	mb := m.progs.MailboxProgram()
	m.add("burn", sig, alice, func() int64 { return at }, func() bool { return true }, sol.ComputeLimitIx(400_000), token.TransferRemoteIx(mb, alice, u, mockGnoDomain, rcpt, uint64(amount)))
	data := []byte{1}
	data = append(data, dispatchTag...)
	data = binary.LittleEndian.AppendUint32(data, msg.Nonce)
	data = binary.LittleEndian.AppendUint64(data, m.w.slotAt(at))
	data = append(data, u.Bytes()...)
	data = append(data, msg.Bytes()...)
	m.accounts[mb.DispatchedMessage(u)] = mockAccount{owner: m.progs.Mailbox, data: data}
}

// addDelivery is the relayer's InboxProcess of one of gno.land's messages: the processed-message record stands
// once it is reached.
func (m *mockSol) addDelivery(sig solana.Signature, msg hl.Message, recipient solana.PublicKey, at func() int64, reached func() bool) {
	mb := m.progs.MailboxProgram()
	id := msg.ID()
	relayer := mockPub("relayer")
	m.add("delivery", sig, relayer, at, reached, sol.ComputeLimitIx(400_000), sol.ProcessIx(mb, relayer, []byte{}, msg.Bytes(), id, recipient, m.progs.Pausable, nil, nil, nil))
}

// addUpdate is the updater's header: its StageInit, then its UpdateFinish a moment later.
func (m *mockSol) addUpdate(name string, height int64, at func() int64, reached func() bool) {
	c, updater := m.progs.LightClient(), mockPub("updater")
	h := &gnolight.Header{ChainID: "dev", Height: height}
	stage := c.UpdateStage(updater, [32]byte(sum256(name)))
	m.add(name, mockSig(name+" init"), updater, func() int64 { return at() - 2 }, reached, c.StageInitIx(updater, stage, &sol.StageInitArgs{Kind: sol.StageKindUpdate, Header: h, NSlots: 6}))
	m.add(name, mockSig(name+" finish"), updater, at, reached, c.UpdateFinishIx(stage, updater))
}

// addProof is the relayer's proof of a message: its StageInit, the header it is checked under (a negative height:
// the header the client holds in its ring, named by its hash alone) and the tree.
func (m *mockSol) addProof(name string, msg hl.Message, header int64, total, index uint32, aunts int, at func() int64, reached func() bool) {
	c, relayer := m.progs.LightClient(), mockPub("relayer")
	hash := gnolight.MessageHash(msg.Bytes())
	stage := c.ProofStage(relayer, hash)
	p := &gnolight.SimpleProof{Total: total, Index: index, LeafHash: [32]byte(sum256(name)), Aunts: make([][32]byte, aunts)}
	args := &sol.StageInitArgs{Kind: sol.StageKindProof, Header: &gnolight.Header{ChainID: "dev", Height: header}, MessageHash: hash, Proof: p, NSlots: 6}
	if header < 0 {
		args.Header, args.RingHash, args.NSlots = nil, [32]byte(sum256("block "+strconv.FormatInt(-header, 10))), 0
	}
	m.add(name, mockSig(name), relayer, at, reached, c.StageInitIx(relayer, stage, args))
}

func mustSig(s string) solana.Signature { return solana.MustSignatureFromBase58(s) }

// withHold puts two guardian flags on both objects (a hold).
func withHold(m *mockSol) {
	progs := mockPrograms()
	t := m.w.start.Unix()
	g := sol.Guardians{Keys: [sol.Keys]solana.PublicKey{mockPub("g1"), mockPub("g2"), mockPub("g3"), mockPub("g4"), mockPub("watch5"), mockPub("watch6")}}
	g.Flags[0], g.Flags[1] = &sol.Flag{Slot: mockSlot - 200, Time: t - 900}, &sol.Flag{Slot: mockSlot - 190, Time: t - 880}
	g.HoldSince = &sol.Flag{Slot: mockSlot - 190, Time: t - 880}
	m.accounts[progs.PausableISM().Flags()] = mockAccount{owner: progs.Pausable, data: encodeFlags(g)}
	m.accounts[progs.AdminProgram().State()] = mockAccount{owner: progs.Admin, data: encodeAdminState(g, 3, 1)}
}

// lookup is an account by the world's stages: the client's state, the inbox's count, the processed records.
func (m *mockSol) lookup(k solana.PublicKey) (mockAccount, bool) {
	out, _, _ := m.w.get()
	mb := m.progs.MailboxProgram()
	switch {
	case k.Equals(m.progs.LightClient().State()):
		trusted, at, ring := int64(1288), m.w.blockTime(1288).Unix()+20, []int64{604, 1288}
		if out >= 1 {
			trusted, at, ring = outHeight+1, m.w.when("out1"), []int64{604, 1288, outHeight + 1}
		}
		return mockAccount{owner: m.progs.Client, data: encodeClientState(uint64(trusted), sum256("block "+strconv.FormatInt(trusted, 10)), at, m.w.blockTime(trusted).Unix(), ring)}, true
	case k.Equals(mb.Inbox()):
		n := uint64(2)
		if out >= 3 {
			n = 3
		}
		return mockAccount{owner: m.progs.Mailbox, data: encodeInbox(n, m.progs.Pausable)}, true
	}
	for _, msg := range []hl.Message{mockDecisionMessage(), mockPMessage(), mockOutMessage()} {
		if k.Equals(mb.ProcessedMessage(msg.ID())) {
			if msg.Nonce == 2 && out < 3 {
				return mockAccount{}, false
			}
			return mockAccount{owner: m.progs.Mailbox, data: []byte{1}}, true
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.accounts[k]
	return a, ok
}

// supply is a mint's supply at the world's stages: GNOT's takes the send in flight at its mint; BUBBLE's burn
// happened before the world began.
func (m *mockSol) supply(k solana.PublicKey) (int64, bool) {
	m.mu.Lock()
	n, ok := m.supplies[k]
	m.mu.Unlock()
	if ok {
		return n, true
	}
	out, _, _ := m.w.get()
	switch {
	case k.Equals(sol.Synthetic{Program: m.progs.Tokens[0]}.Mint()):
		if out >= 3 {
			return 1_000_000 * one, true
		}
		return 1_000_000*one - 10*one, true
	case k.Equals(sol.Synthetic{Program: m.progs.Tokens[1]}.Mint()):
		return 2_500_000*one - 300*one, true
	}
	return 0, false
}

func (m *mockSol) GetAccountInfoWithOpts(ctx context.Context, k solana.PublicKey, opts *rpc.GetAccountInfoOpts) (*rpc.GetAccountInfoResult, error) {
	m.count("getAccountInfo")
	if n, isMint := m.supply(k); isMint { // a mint: the base layout with its supply (sol.MintSupply reads it)
		d := make([]byte, 82)
		binary.LittleEndian.PutUint64(d[36:44], uint64(n))
		d[44], d[45] = decimals, 1
		return &rpc.GetAccountInfoResult{RPCContext: rpc.RPCContext{Context: rpc.Context{Slot: mockSlot}}, Value: &rpc.Account{Owner: sol.Token2022, Data: rpc.DataBytesOrJSONFromBytes(d)}}, nil
	}
	a, ok := m.lookup(k)
	if !ok {
		return nil, rpc.ErrNotFound
	}
	return &rpc.GetAccountInfoResult{RPCContext: rpc.RPCContext{Context: rpc.Context{Slot: mockSlot}}, Value: &rpc.Account{Owner: a.owner, Data: rpc.DataBytesOrJSONFromBytes(a.data)}}, nil
}

func (m *mockSol) GetMultipleAccountsWithOpts(ctx context.Context, keys []solana.PublicKey, opts *rpc.GetMultipleAccountsOpts) (*rpc.GetMultipleAccountsResult, error) {
	m.count("getMultipleAccounts")
	if m.delay > 0 {
		time.Sleep(m.delay)
	}
	if len(keys) > sol.MultiMax {
		return nil, fmt.Errorf("mock: %d accounts in one getMultipleAccounts (a public provider blocks more than %d)", len(keys), sol.MultiMax)
	}
	out := &rpc.GetMultipleAccountsResult{RPCContext: rpc.RPCContext{Context: rpc.Context{Slot: mockSlot}}}
	for _, k := range keys {
		if a, ok := m.lookup(k); ok {
			out.Value = append(out.Value, &rpc.Account{Owner: a.owner, Data: rpc.DataBytesOrJSONFromBytes(a.data)})
		} else {
			out.Value = append(out.Value, nil)
		}
	}
	return out, nil
}

func (m *mockSol) tx(sig solana.Signature) *mockTx {
	for _, t := range m.txs {
		if t.sig == sig && t.reached() {
			return t
		}
	}
	return nil
}

func (m *mockSol) GetTransaction(ctx context.Context, sig solana.Signature, opts *rpc.GetTransactionOpts) (*rpc.GetTransactionResult, error) {
	m.count("getTransaction")
	t := m.tx(sig)
	if t == nil {
		return nil, rpc.ErrNotFound
	}
	tx, err := solana.NewTransaction(t.ixs, mockBlockhash, solana.TransactionPayer(t.payer))
	if err != nil {
		return nil, err
	}
	raw, _ := tx.MarshalBinary()
	var env rpc.TransactionResultEnvelope
	if err := json.Unmarshal([]byte(`["`+base64.StdEncoding.EncodeToString(raw)+`","base64"]`), &env); err != nil {
		return nil, err
	}
	bt := solana.UnixTimeSeconds(t.at())
	return &rpc.GetTransactionResult{Slot: t.slot(m.w), BlockTime: &bt, Transaction: &env, Meta: &rpc.TransactionMeta{}}, nil
}

func (m *mockSol) GetSignaturesForAddressWithOpts(ctx context.Context, k solana.PublicKey, opts *rpc.GetSignaturesForAddressOpts) ([]*rpc.TransactionSignature, error) {
	m.count("getSignaturesForAddress")
	var out []*rpc.TransactionSignature
	for _, t := range m.txs {
		if !t.reached() {
			continue
		}
		for _, x := range t.keys {
			if x.Equals(k) {
				bt := solana.UnixTimeSeconds(t.at())
				out = append(out, &rpc.TransactionSignature{Signature: t.sig, Slot: t.slot(m.w), BlockTime: &bt})
				break
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Slot > out[j].Slot }) // newest first, as the RPC lists
	if opts != nil && opts.Limit != nil && len(out) > *opts.Limit {
		out = out[:*opts.Limit]
	}
	return out, nil
}

func (m *mockSol) GetLatestBlockhash(ctx context.Context, commitment rpc.CommitmentType) (*rpc.GetLatestBlockhashResult, error) {
	return &rpc.GetLatestBlockhashResult{RPCContext: rpc.RPCContext{Context: rpc.Context{Slot: mockSlot}}, Value: &rpc.LatestBlockhashResult{Blockhash: mockBlockhash, LastValidBlockHeight: mockSlot + 150}}, nil
}

// mockProber is the three signers' endpoints: every burn of the history is signed by all three; the burn in
// flight by signer 1 from stage 1, signer 2 from stage 2, signer 3 from stage 4; signer 3 answers nothing while
// it is down.
type mockProber struct{ w *mockWorld }

func (p mockProber) Probe(ctx context.Context, base string, nonce uint64) (int, error) {
	_, in, down := p.w.get()
	n, _ := strconv.Atoi(base[strings.LastIndexByte(base, '/')+1:])
	if n == 3 && down {
		return 0, errors.New("connection refused")
	}
	switch {
	case nonce < 9:
		return 200, nil
	case nonce == 9 && in >= map[int]int{1: 1, 2: 2, 3: 4}[n]:
		return 200, nil
	}
	return 404, nil
}

// ------------------------------------------------------------ encoders --

// The accounts' borsh forms, the inverse of bridge/sol's parsers (sol-util's layouts).

func encodeFlag(f *sol.Flag) []byte {
	if f == nil {
		return []byte{0}
	}
	b := []byte{1}
	b = binary.LittleEndian.AppendUint64(b, f.Slot)
	return binary.LittleEndian.AppendUint64(b, uint64(f.Time))
}

func encodeGuardians(g sol.Guardians) []byte {
	var b []byte
	for _, k := range g.Keys {
		b = append(b, k.Bytes()...)
	}
	for _, f := range g.Flags {
		b = append(b, encodeFlag(f)...)
	}
	b = append(b, encodeFlag(g.HoldSince)...)
	return append(b, encodeFlag(g.DipSince)...)
}

// encodeFlags is the pausable ISM's flags PDA: a version byte, then the guardians.
func encodeFlags(g sol.Guardians) []byte { return append([]byte{1}, encodeGuardians(g)...) }

// encodePausableState is the pausable ISM's state PDA: version, clock factor, owner, target, the target's layout
// version, then the two tokens' mint limits (25,000 a message, 100,000 a day, nothing used).
func encodePausableState(p sol.Programs, now int64) []byte {
	b := []byte{1, 1}
	b = append(b, p.AdminProgram().Authority().Bytes()...)
	b = append(b, p.Client.Bytes()...)
	b = append(b, sol.ClientStateVersion)
	for range 2 {
		b = binary.LittleEndian.AppendUint64(b, uint64(25_000*one))
		b = binary.LittleEndian.AppendUint64(b, uint64(100_000*one))
		b = binary.LittleEndian.AppendUint64(b, uint64(1_000*one))
		b = binary.LittleEndian.AppendUint64(b, uint64(now-7200))
	}
	return b
}

// encodeAdminState is the admin program's state PDA (M2j's layout) with no successor, no cancelled range and no
// hold history, lastSeq the last number and pending the pending count, padded to the account's length.
func encodeAdminState(g sol.Guardians, lastSeq uint64, pending uint32) []byte {
	b := []byte{1, 1, 1}
	b = binary.LittleEndian.AppendUint32(b, mockGnoDomain)
	b = binary.LittleEndian.AppendUint64(b, lastSeq)
	b = append(b, encodeGuardians(g)...)
	b = append(b, 0)   // no successor
	b = append(b, 254) // the authority bump
	b = binary.LittleEndian.AppendUint32(b, 0)
	b = binary.LittleEndian.AppendUint32(b, 0)
	b = binary.LittleEndian.AppendUint32(b, pending)
	b = append(b, make([]byte, 32)...) // no predecessor
	b = append(b, 0)                   // inherited
	for len(b) < sol.AdminStateAccountLen {
		b = append(b, 0)
	}
	return b
}

// encodeDecision is a pending decision's account: version, number, kind, params, delivered slot and time, the
// deliverer, no refund key (and the 32 spare bytes the program allocates for one).
func encodeDecision(number uint64, kind byte, params []byte, slot uint64, at int64, deliverer solana.PublicKey) []byte {
	b := []byte{1}
	b = binary.LittleEndian.AppendUint64(b, number)
	b = append(b, kind)
	b = binary.LittleEndian.AppendUint32(b, uint32(len(params)))
	b = append(b, params...)
	b = binary.LittleEndian.AppendUint64(b, slot)
	b = binary.LittleEndian.AppendUint64(b, uint64(at))
	b = append(b, deliverer.Bytes()...)
	b = append(b, 0)
	return append(b, make([]byte, 32)...)
}

// feeParams is a kind 7's parameters (set protocol fee): the fee and the beneficiary, as sol.EncodeParams writes them.
func feeParams(fee uint64, beneficiary solana.PublicKey) []byte {
	return sol.EncodeParams(&sol.Params{Kind: sol.KindSetFee, Lamports: fee, Beneficiary: beneficiary})
}

// encodeClientState is the light client's state: the 64-byte head (version, clock factor, the ring counters, the
// body cap, no quarantine, the last accepted time, the trusted height and hash), then the body — the admin, the
// chain, the trusted header, a set of six validators of power 60, no next set, no history, the record's counter
// and hash, the ring epoch — padded to the body cap, then the ring: each header's hash, height and results hash.
func encodeClientState(trustedHeight uint64, hash []byte, lastAccepted, trustedTime int64, ring []int64) []byte {
	const bodyCap8 = 128 // the body cap in eight-byte units
	b := []byte{sol.ClientStateVersion, 1}
	b = binary.LittleEndian.AppendUint16(b, uint16(len(ring)))
	b = binary.LittleEndian.AppendUint16(b, uint16(len(ring)))
	b = binary.LittleEndian.AppendUint16(b, bodyCap8)
	b = binary.LittleEndian.AppendUint64(b, 0)
	b = binary.LittleEndian.AppendUint64(b, uint64(lastAccepted))
	b = binary.LittleEndian.AppendUint64(b, trustedHeight)
	b = append(b, hash...)
	b = append(b, mockPub("admin").Bytes()...)
	b = binary.LittleEndian.AppendUint32(b, 3)
	b = append(b, "dev"...)
	b = binary.LittleEndian.AppendUint32(b, mockGnoDomain)
	b = binary.LittleEndian.AppendUint64(b, trustedHeight)
	b = append(b, hash...)
	b = append(b, sum256("vals")...)
	b = append(b, sum256("vals")...)
	b = binary.LittleEndian.AppendUint64(b, uint64(trustedTime))
	b = binary.LittleEndian.AppendUint32(b, 6)
	for i := range 6 {
		b = append(b, mockPub("validator "+strconv.Itoa(i)).Bytes()...)
		b = binary.LittleEndian.AppendUint64(b, 60)
	}
	b = append(b, sum256("vals")...)
	b = append(b, 0) // no next set
	b = append(b, sum256("vals")...)
	b = binary.LittleEndian.AppendUint32(b, 0) // no history
	b = binary.LittleEndian.AppendUint64(b, 7)
	b = append(b, sum256("record")...)
	b = binary.LittleEndian.AppendUint64(b, 0)
	for len(b) < sol.HeadLen+bodyCap8*8 {
		b = append(b, 0)
	}
	for _, h := range ring {
		b = append(b, sum256("block "+strconv.FormatInt(h, 10))...)
		b = binary.LittleEndian.AppendUint64(b, uint64(h))
		b = append(b, sum256("results "+strconv.FormatInt(h, 10))...)
	}
	return b
}

// encodeOutbox is the mailbox's outbox: initialized, the domain, the bump, no owner, an empty branch, the dispatch
// count, the fee maximum and fee, the beneficiary.
func encodeOutbox(count uint64) []byte {
	b := []byte{1}
	b = binary.LittleEndian.AppendUint32(b, mockSolDomain)
	b = append(b, 255, 0)
	b = append(b, make([]byte, 32*32)...)
	b = binary.LittleEndian.AppendUint64(b, count)
	b = binary.LittleEndian.AppendUint64(b, 50_000_000)
	b = binary.LittleEndian.AppendUint64(b, 0)
	return append(b, mockPub("treasurer").Bytes()...)
}

// encodeInbox is the mailbox's inbox: initialized, the domain, the bump, the default ISM, the processed count.
func encodeInbox(processed uint64, ism solana.PublicKey) []byte {
	b := []byte{1}
	b = binary.LittleEndian.AppendUint32(b, mockSolDomain)
	b = append(b, 255)
	b = append(b, ism.Bytes()...)
	return binary.LittleEndian.AppendUint64(b, processed)
}

// mockServer is the whole mock deployment behind a server: the config, the gno node, Solana, the signers. With
// progress the two transfers start in flight and move only when stepped, every reading fresh; without, the world
// is done and the caches are the deployment's.
func mockServer(progress bool, site string, now func() time.Time, opts ...func(*mockSol)) (*server, *mockGno, *mockSol) {
	cfg := mockConfig()
	w := newMockWorld(now, !progress)
	g := newMockGno(w)
	ms := newMockSol(w, opts...)
	var s *server
	others := map[string]Other{"solana": newSolana(cfg.Chains[0], ms, mockGnoDomain, func() []string { return s.realmAddresses() })}
	s = newServer(cfg, g, others, site)
	s.now, s.prober, s.syncRefresh = now, mockProber{w}, true
	if progress {
		s.world = w
		s.statusTTL, s.transferTTL, s.activityTTL, s.lampsTTL = 0, 0, 0, 0
		// the browser suite is one client making a crowd's requests (its pages poll while it steps the world):
		// the mock serves it at a hundred a second; a deployment keeps two
		s.lim = newLimiter(100, 1000, func() time.Time { return s.now() })
	}
	burnSig := mustSig(mockBurnTx)
	s.mockInfo = map[string]string{"sender": mockSender, "recipient": mockRecipient, "alice": mockPub("alice").String(), "gnoTx": mockGnoTx,
		"burnSig": mockBurnTx, "burnSigBase64": base64.StdEncoding.EncodeToString(burnSig[:]), "outId": mockOutID, "inId": mockInID, "pId": mockPID, "qId": mockQID,
		"bubbleRealm": hl.GnoPackageAddress(mockNS + "hlbubble")}
	return s, g, ms
}
