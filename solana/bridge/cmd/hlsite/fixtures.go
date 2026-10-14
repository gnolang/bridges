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
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gagliardetto/solana-go"
	"github.com/gagliardetto/solana-go/rpc"

	"github.com/gnolang/bridges/solana/bridge/gno"
	"github.com/gnolang/bridges/solana/bridge/hl"
	"github.com/gnolang/bridges/solana/bridge/sol"
)

// The mock deployment (-mock, and the tests): a gno node and a Solana provider from canned data in the realms'
// and the programs' exact shapes — the mailbox's and the routes' json as bridge/local/e2e.sh reads them
// (M3-NOTES.md, M3l), the pausable ISM's, the admin program's and the client's accounts in the layouts bridge/sol
// parses — so the readers run the same code they run against a chain. One send out and one burn in are known;
// with progress on, the send delivers and the burn is paid over the first few polls, so a tracker shows every
// step.

const (
	mockNS        = "gno.land/r/g1leu8d2vsplhehcfkjg50mwgdpxdkt8tztu95wr/"
	mockGovernor  = "g1dpkz6em0wejhymn0wf047h6lta047h6lvxn4gx"
	mockSender    = "g1jg8mtutu9khhfwc4nxmuhcpftf0pajdhfvsqf5" // gnodev's test1
	mockRecipient = "g1jg8mtutu9khhfwc4nxmuhcpftf0pajdhfvsqf5"
	mockSolDomain = uint32(1399811150)
	mockGnoDomain = uint32(1198419796)
	one           = int64(1_000_000)
)

// mockKey is a deterministic keypair for a name (the local run's hlsol keygen, the same idea: anyone can derive it).
func mockKey(name string) solana.PrivateKey {
	s := sha256.Sum256([]byte("hlsite mock: " + name))
	return solana.PrivateKey(ed25519.NewKeyFromSeed(s[:]))
}

func mockPub(name string) solana.PublicKey { return mockKey(name).PublicKey() }

// mockIDs are the two known transfers and the wallet's transaction hash.
var (
	mockOutID  = hex.EncodeToString(sum256("hlsite mock send out")[:])
	mockInID   = func() string { id := mockBurnMessage().ID(); return hex.EncodeToString(id[:]) }()
	mockGnoTx  = base64.StdEncoding.EncodeToString(sum256("hlsite mock gno tx")[:])
	mockBurnTx = func() string {
		h := sha512.Sum512([]byte("hlsite mock burn signature"))
		return solana.SignatureFromBytes(h[:]).String()
	}()
	mockBlockhash = solana.HashFromBytes(sum256("hlsite mock blockhash")[:])
)

func sum256(s string) []byte { h := sha256.Sum256([]byte(s)); return h[:] }

// mockConfig is the mock deployment's config.
func mockConfig() *Config {
	var c Config
	c.Gno.RPC, c.Gno.ChainID = "http://127.0.0.1:26657", "dev"
	c.Gno.Mailbox, c.Gno.Fees, c.Gno.Gov, c.Gno.Bubble = mockNS+"hlmailbox", mockNS+"hlfees", mockNS+"hlgov", mockNS+"bubble"
	c.Gno.RealmLink, c.Gno.TxLink, c.Gno.GasWanted = "https://gno.land/%s", "https://gnoscan.io/transactions/details?txhash=%s", defaultGasWanted
	ch := ChainConfig{ID: "solana", Kind: "solana", Name: "Solana", RPC: "http://127.0.0.1:8899", Domain: mockSolDomain, Explorer: "https://explorer.solana.com", Cluster: "devnet"}
	ch.Programs.Mailbox, ch.Programs.Pausable, ch.Programs.Admin, ch.Programs.Client = mockPub("mailbox").String(), mockPub("pausable").String(), mockPub("admin").String(), mockPub("client").String()
	ch.Tokens = []TokenConfig{{Symbol: "GNOT", Program: mockPub("gnot").String(), Realm: mockNS + "hlgnot"}, {Symbol: "BUBBLE", Program: mockPub("bubble").String(), Realm: mockNS + "hlbubble"}}
	c.Chains = []ChainConfig{ch}
	if err := c.check(); err != nil {
		panic(err)
	}
	return &c
}

// ------------------------------------------------------------------ gno --

// mockGno is a gno node with the realms' renders as the realms build them.
type mockGno struct {
	mu       sync.Mutex
	progress bool
	polls    map[string]int
	now      func() time.Time
	// Flags are standing flags on a route, as hlmailbox lists them ("fn:seat:addr:at" strings); Paused the
	// route's paused functions; Lock overrides per symbol; Supply unused here.
	flags  map[string][]string
	paused map[string]string
}

func newMockGno(progress bool, now func() time.Time) *mockGno {
	return &mockGno{progress: progress, polls: map[string]int{}, now: now, flags: map[string][]string{}, paused: map[string]string{}}
}

func (m *mockGno) poll(key string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.polls[key]++
	return m.polls[key]
}

func (m *mockGno) Status(ctx context.Context) (gno.Head, error) {
	return gno.Head{Height: 1300, Time: m.now(), Hash: hex.EncodeToString(sum256("block 1300")[:])}, nil
}

func (m *mockGno) Block(ctx context.Context, height int64) (gno.Head, error) {
	return gno.Head{Height: height, Time: m.now().Add(-time.Duration(1300-height) * 3 * time.Second), Hash: hex.EncodeToString(sum256("block " + strconv.FormatInt(height, 10))[:])}, nil
}

func (m *mockGno) GasPrice(ctx context.Context) (gno.GasPrice, error) {
	return gno.GasPrice{Gas: 1000, Amount: 1, Denom: "ugnot"}, nil
}

func (m *mockGno) TxEvents(ctx context.Context, hash string) (*gnoTx, error) {
	if hash != mockGnoTx {
		return nil, errors.New("gno rpc tx: tx (" + hash + ") not found")
	}
	return &gnoTx{Height: 1290, Events: []gnoEvent{{Type: "SentTransferRemote", PkgPath: mockNS + "hlgnot", Attrs: map[string]string{"id": mockOutID, "amount": "10000000", "from": mockSender, "destination": strconv.FormatUint(uint64(mockSolDomain), 10)}}}}, nil
}

func (m *mockGno) RenderAt(ctx context.Context, pkgPath, path string, height int64) (string, error) {
	realm := strings.TrimPrefix(pkgPath, mockNS)
	switch {
	case realm == "hlmailbox" && path == "json":
		return m.mailboxJSON(), nil
	case (realm == "hlgnot" || realm == "hlbubble") && path == "json":
		return m.routeJSON(realm), nil
	case path == "decisions.json":
		if realm == "hlfees" {
			t := m.now().Unix()
			return fmt.Sprintf(`[{"id":1,"kind":"set treasurer","text":"set treasurer to %s","state":"pending","made":%d,"recorded":%d,"due":%d,"applied":0,"cancelled":0,"note":"queued 7 days"}]`, mockSender, t-3600, t-3600, t-3600+7*86400), nil
		}
		return "[]", nil
	case realm == "hlgov" && path == "json":
		return fmt.Sprintf(`{"address":"%s","governor":"%s","yielded":0,"recipient":"%s","former":[],"last":3,"next":4}`, hl.GnoPackageAddress(pkgPath), mockGovernor, hex.EncodeToString(mockPub("admin").Bytes())), nil
	case strings.HasPrefix(path, "send/"):
		if realm == "hlgnot" && strings.TrimPrefix(path, "send/") == mockOutID {
			return fmt.Sprintf(`{"id":"%s","from":"%s","amount":%d,"at":%d,"imported":false,"refunded":0,"writtenOff":0}`, mockOutID, mockSender, 10*one, m.now().Unix()-60), nil
		}
		return "null", nil
	case strings.HasPrefix(path, "claim/"):
		if realm == "hlbubble" && strings.TrimPrefix(path, "claim/") == mockInID {
			paid, state := int64(0), "open"
			n := 99
			if m.progress {
				n = m.poll("claim")
			}
			switch {
			case n >= 4:
				paid, state = 200*one, "paid"
			case n == 3:
				paid = 100 * one
			}
			return fmt.Sprintf(`{"id":"%s","to":"%s","amount":%d,"paid":%d,"state":"%s","by":"signers","at":%d,"height":1203,"seq":1}`, mockInID, mockRecipient, 200*one, paid, state, m.now().Unix()-30), nil
		}
		return "null", nil
	case realm == "hlmailbox" && strings.HasPrefix(path, "delivered/"):
		if strings.TrimPrefix(path, "delivered/") == mockInID {
			return "1203", nil
		}
		return "0", nil
	case strings.HasPrefix(path, "claims/") || strings.HasPrefix(path, "sweeps/") || strings.HasPrefix(path, "refunds/") || strings.HasPrefix(path, "msgs/"):
		return "[]", nil
	}
	return "", fmt.Errorf("mock: no render for %s:%s", pkgPath, path)
}

func (m *mockGno) mailboxJSON() string {
	t := m.now().Unix()
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
	return `{"domain":` + strconv.FormatUint(uint64(mockGnoDomain), 10) + `,"hook":"` + hex.EncodeToString(sum256("hook")[:]) + `","address":"` + hl.GnoPackageAddress(mockNS+"hlmailbox") + `","count":14,"root":"` + hex.EncodeToString(sum256("root")[:]) + `","delivered":3,"live":true,"launchedAt":` + strconv.FormatInt(t-86400, 10) +
		`,"governor":"` + mockGovernor + `","yielded":0,"launchOrigin":` + strconv.FormatUint(uint64(mockSolDomain), 10) + `,"clocks":{"factor":1,"delay":604800,"deadline":1209600},"decisions":4,"signers":{"keys":["` + hex.EncodeToString(mockPub("s0").Bytes()) + `","` + hex.EncodeToString(mockPub("s1").Bytes()) + `","` + hex.EncodeToString(mockPub("s2").Bytes()) + `"],"threshold":2,"groupA":[],"groupB":[]},` +
		`"verifiers":{"` + strconv.FormatUint(uint64(mockSolDomain), 10) + `":{"pkgpath":"` + mockNS + `hlverifier","registered":true}},"registered":["` + mockNS + `hlverifier"],"registeredCount":1,"seats":["` + mockGovernor + `","g1aeddl3g5hr8v5y8vx3s4x9m7qeq9h4u6xmq6wh","g1manfred47kzduec920z88wfr64y5asmjd4q3pz","","` + mockSender + `",""],` +
		`"routes":{` + route("hlbubble") + "," + route("hlgnot") + `},"droppedRoutes":{},"successors":[],"successorCount":0,"successorRoutes":{}}`
}

func (m *mockGno) routeJSON(realm string) string {
	sym, lock, fee, minimum, floor := "GNOT", 1_000_000*one, 5*one, 10*one, 10_000*one
	if realm == "hlbubble" {
		sym, lock, fee, minimum = "BUBBLE", 2_500_000*one, 100*one, 200*one
	}
	// the identity: lock = supply + owed + voided − lost, with 300 units owed on BUBBLE (an open claim) and nothing else
	owed := int64(0)
	if sym == "BUBBLE" {
		owed = 300 * one
	}
	cap := lock / 10
	if cap < floor {
		cap = floor
	}
	openClaims := 0
	if owed > 0 {
		openClaims = 1
	}
	return fmt.Sprintf(`{"address":%q,"live":true,"governor":%q,"yielded":0,"balance":%d,"paused":%q,"symbol":%q,"domain":%d,"router":%q,"oldRouter":%q,"oldUntil":0,"lock":%d,"sentTotal":%d,"paidTotal":%d,"owed":%d,"voided":0,"lost":0,"sweep":0,"cap":%d,"meter":%d,"capFloor":%d,"fee":%d,"feeFloor":%d,"minimum":%d,"maximum":%d,"openClaims":%d,"claims":7,"sends":12,"refunded":0,"refundedTotal":0,"writtenOff":0,"writtenOffTotal":0,"migratedTo":"","importing":false,"imported":0,"clocks":{"factor":1,"delay":604800,"deadline":1209600},"decisions":0}`,
		hl.GnoPackageAddress(mockNS+realm), mockGovernor, lock, m.paused[realm], sym, mockSolDomain, hex.EncodeToString(mockPub(strings.ToLower(sym)).Bytes()), strings.Repeat("00", 32), lock, lock+700*one, 700*one, owed, cap, cap/3, floor, fee, fee/2, minimum, 25_000*one, openClaims)
}

// --------------------------------------------------------------- solana --

// mockSol is a Solana provider from canned accounts.
type mockSol struct {
	mu       sync.Mutex
	progress bool
	polls    map[string]int
	now      func() time.Time
	accounts map[solana.PublicKey]mockAccount
	supplies map[solana.PublicKey]int64
	txs      map[solana.Signature]*rpc.GetTransactionResult
	sigs     map[solana.PublicKey][]*rpc.TransactionSignature
	slot     uint64
}

type mockAccount struct {
	owner solana.PublicKey
	data  []byte
}

// mockDeployment is the far side as the mock knows it: the programs, the processed record of the known send and
// the dispatch record of the known burn.
type mockDeployment struct {
	progs   sol.Programs
	tokens  map[string]solana.PublicKey
	burnSig solana.Signature
}

func newMockSol(progress bool, now func() time.Time, opts ...func(*mockSol)) *mockSol {
	m := &mockSol{progress: progress, polls: map[string]int{}, now: now, accounts: map[solana.PublicKey]mockAccount{}, supplies: map[solana.PublicKey]int64{}, txs: map[solana.Signature]*rpc.GetTransactionResult{}, sigs: map[solana.PublicKey][]*rpc.TransactionSignature{}, slot: 400_000}
	progs := sol.Programs{Mailbox: mockPub("mailbox"), Tokens: [2]solana.PublicKey{mockPub("gnot"), mockPub("bubble")}, Client: mockPub("client"), Pausable: mockPub("pausable"), Admin: mockPub("admin")}
	t := now().Unix()
	// the supplies: GNOT's equals its lock; BUBBLE's is its lock less the 300 owed (the identity holds on both)
	m.supplies[sol.Synthetic{Program: progs.Tokens[0]}.Mint()] = 1_000_000 * one
	m.supplies[sol.Synthetic{Program: progs.Tokens[1]}.Mint()] = 2_500_000*one - 300*one
	seats := [sol.Keys]solana.PublicKey{mockPub("g1"), mockPub("g2"), mockPub("g3"), mockPub("g4"), mockPub("watch5"), mockPub("watch6")}
	g := sol.Guardians{Keys: seats}
	pausable := progs.PausableISM()
	m.accounts[pausable.Flags()] = mockAccount{owner: progs.Pausable, data: encodeFlags(g)}
	m.accounts[pausable.State()] = mockAccount{owner: progs.Pausable, data: encodePausableState(progs, t)}
	admin := progs.AdminProgram()
	m.accounts[admin.State()] = mockAccount{owner: progs.Admin, data: encodeAdminState(g, 3, 1)}
	m.accounts[admin.Decision(3)] = mockAccount{owner: progs.Admin, data: encodeDecision(3, 7, feeParams(1000, mockPub("treasurer")), m.slot-1000, t-3600, mockPub("relayer"))}
	m.accounts[progs.LightClient().State()] = mockAccount{owner: progs.Client, data: encodeClientHead(1299, sum256("block 1299"), t-20)}
	// the known burn: alice's transaction on the BUBBLE program, its dispatch record at the slot
	m.txs[mustSig(mockBurnTx)], m.accounts[progs.MailboxProgram().DispatchedMessage(mockPub("unique"))] = mockBurn(progs, m.slot-500, t-300)
	for _, o := range opts {
		o(m)
	}
	return m
}

func mustSig(s string) solana.Signature { return solana.MustSignatureFromBase58(s) }

// withHold puts two guardian flags on both objects (a hold) and withFlag one watch-key flag on the pausable ISM.
func withHold(m *mockSol) {
	progs := mockPrograms()
	t := m.now().Unix()
	g := sol.Guardians{Keys: [sol.Keys]solana.PublicKey{mockPub("g1"), mockPub("g2"), mockPub("g3"), mockPub("g4"), mockPub("watch5"), mockPub("watch6")}}
	g.Flags[0], g.Flags[1] = &sol.Flag{Slot: m.slot - 200, Time: t - 900}, &sol.Flag{Slot: m.slot - 190, Time: t - 880}
	g.HoldSince = &sol.Flag{Slot: m.slot - 190, Time: t - 880}
	m.accounts[progs.PausableISM().Flags()] = mockAccount{owner: progs.Pausable, data: encodeFlags(g)}
	m.accounts[progs.AdminProgram().State()] = mockAccount{owner: progs.Admin, data: encodeAdminState(g, 3, 1)}
}

func mockPrograms() sol.Programs {
	return sol.Programs{Mailbox: mockPub("mailbox"), Tokens: [2]solana.PublicKey{mockPub("gnot"), mockPub("bubble")}, Client: mockPub("client"), Pausable: mockPub("pausable"), Admin: mockPub("admin")}
}

// mockBurn is alice's burn of 200 BUBBLE toward test1: the transaction as the RPC serves it and the dispatch
// record the mailbox wrote in that slot.
func mockBurn(progs sol.Programs, slot uint64, at int64) (*rpc.GetTransactionResult, mockAccount) {
	alice, unique := mockPub("alice"), mockPub("unique")
	rcpt, _ := hl.GnoAddressToBytes32(mockRecipient)
	token := sol.Synthetic{Program: progs.Tokens[1]}
	mailbox := progs.MailboxProgram()
	tx, err := solana.NewTransaction([]solana.Instruction{sol.ComputeLimitIx(400_000), token.TransferRemoteIx(mailbox, alice, unique, mockGnoDomain, rcpt, uint64(200*one))}, mockBlockhash, solana.TransactionPayer(alice))
	if err != nil {
		panic(err)
	}
	raw, _ := tx.MarshalBinary()
	var env rpc.TransactionResultEnvelope
	if err := json.Unmarshal([]byte(`["`+base64.StdEncoding.EncodeToString(raw)+`","base64"]`), &env); err != nil {
		panic(err)
	}
	bt := solana.UnixTimeSeconds(at)
	res := &rpc.GetTransactionResult{Slot: slot, BlockTime: &bt, Transaction: &env, Meta: &rpc.TransactionMeta{}}
	msg := mockBurnMessage()
	data := []byte{1}
	data = append(data, dispatchTag...)
	data = binary.LittleEndian.AppendUint32(data, msg.Nonce)
	data = binary.LittleEndian.AppendUint64(data, slot)
	data = append(data, unique.Bytes()...)
	data = append(data, msg.Bytes()...)
	return res, mockAccount{owner: progs.Mailbox, data: data}
}

// mockBurnMessage is the known burn's Hyperlane message: from the BUBBLE token program to the BUBBLE route,
// 200 BUBBLE for test1. Its id is mockInID.
func mockBurnMessage() hl.Message {
	rcpt, _ := hl.GnoAddressToBytes32(mockRecipient)
	msg := hl.Message{Version: hl.Version, Nonce: 9, Origin: mockSolDomain, Destination: mockGnoDomain, Body: hl.TokenMessage(rcpt, uint64(200*one), nil)}
	copy(msg.Sender[:], mockPub("bubble").Bytes())
	msg.Recipient, _ = hl.GnoAddressToBytes32(hl.GnoPackageAddress(mockNS + "hlbubble"))
	return msg
}

func (m *mockSol) poll(key string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.polls[key]++
	return m.polls[key]
}

// delivered says whether the known send's processed record stands: with progress, from the third look on.
func (m *mockSol) delivered() bool {
	if !m.progress {
		return true
	}
	return m.poll("processed") >= 3
}

func (m *mockSol) lookup(k solana.PublicKey) (mockAccount, bool) {
	progs := mockPrograms()
	if id, ok := messageID(mockOutID); ok && k.Equals(progs.MailboxProgram().ProcessedMessage(id)) {
		if m.delivered() {
			return mockAccount{owner: progs.Mailbox, data: []byte{1}}, true
		}
		return mockAccount{}, false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.accounts[k]
	return a, ok
}

func (m *mockSol) GetAccountInfoWithOpts(ctx context.Context, k solana.PublicKey, opts *rpc.GetAccountInfoOpts) (*rpc.GetAccountInfoResult, error) {
	m.mu.Lock()
	n, isMint := m.supplies[k]
	m.mu.Unlock()
	if isMint { // a mint: the base layout with its supply (sol.MintSupply reads it)
		d := make([]byte, 82)
		binary.LittleEndian.PutUint64(d[36:44], uint64(n))
		d[44], d[45] = decimals, 1
		return &rpc.GetAccountInfoResult{RPCContext: rpc.RPCContext{Context: rpc.Context{Slot: m.slot}}, Value: &rpc.Account{Owner: sol.Token2022, Data: rpc.DataBytesOrJSONFromBytes(d)}}, nil
	}
	a, ok := m.lookup(k)
	if !ok {
		return nil, rpc.ErrNotFound
	}
	return &rpc.GetAccountInfoResult{RPCContext: rpc.RPCContext{Context: rpc.Context{Slot: m.slot}}, Value: &rpc.Account{Owner: a.owner, Data: rpc.DataBytesOrJSONFromBytes(a.data)}}, nil
}

func (m *mockSol) GetMultipleAccountsWithOpts(ctx context.Context, keys []solana.PublicKey, opts *rpc.GetMultipleAccountsOpts) (*rpc.GetMultipleAccountsResult, error) {
	out := &rpc.GetMultipleAccountsResult{RPCContext: rpc.RPCContext{Context: rpc.Context{Slot: m.slot}}}
	for _, k := range keys {
		if a, ok := m.lookup(k); ok {
			out.Value = append(out.Value, &rpc.Account{Owner: a.owner, Data: rpc.DataBytesOrJSONFromBytes(a.data)})
		} else {
			out.Value = append(out.Value, nil)
		}
	}
	return out, nil
}

func (m *mockSol) GetTransaction(ctx context.Context, sig solana.Signature, opts *rpc.GetTransactionOpts) (*rpc.GetTransactionResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if tx, ok := m.txs[sig]; ok {
		return tx, nil
	}
	return nil, rpc.ErrNotFound
}

func (m *mockSol) GetSignaturesForAddressWithOpts(ctx context.Context, k solana.PublicKey, opts *rpc.GetSignaturesForAddressOpts) ([]*rpc.TransactionSignature, error) {
	progs := mockPrograms()
	if id, ok := messageID(mockOutID); ok && k.Equals(progs.MailboxProgram().ProcessedMessage(id)) {
		bt := solana.UnixTimeSeconds(m.now().Unix() - 10)
		h := sha512.Sum512([]byte("hlsite mock delivery signature"))
		return []*rpc.TransactionSignature{{Signature: solana.SignatureFromBytes(h[:]), Slot: m.slot - 5, BlockTime: &bt}}, nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.sigs[k], nil
}

func (m *mockSol) GetLatestBlockhash(ctx context.Context, commitment rpc.CommitmentType) (*rpc.GetLatestBlockhashResult, error) {
	return &rpc.GetLatestBlockhashResult{RPCContext: rpc.RPCContext{Context: rpc.Context{Slot: m.slot}}, Value: &rpc.LatestBlockhashResult{Blockhash: mockBlockhash, LastValidBlockHeight: m.slot + 150}}, nil
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

// encodeClientHead is the light client's 64-byte head: version, clock factor, the ring counters, the body cap,
// no quarantine, the last accepted time, the trusted height and hash.
func encodeClientHead(trustedHeight uint64, hash []byte, lastAccepted int64) []byte {
	b := []byte{sol.ClientStateVersion, 1}
	b = binary.LittleEndian.AppendUint16(b, 256)
	b = binary.LittleEndian.AppendUint16(b, 7)
	b = binary.LittleEndian.AppendUint16(b, 1024)
	b = binary.LittleEndian.AppendUint64(b, 0)
	b = binary.LittleEndian.AppendUint64(b, uint64(lastAccepted))
	b = binary.LittleEndian.AppendUint64(b, trustedHeight)
	return append(b, hash[:]...)
}

// mockServer is the whole mock deployment behind a server: the config, the gno node, Solana.
func mockServer(progress bool, site string, now func() time.Time, opts ...func(*mockSol)) (*server, *mockGno, *mockSol) {
	cfg := mockConfig()
	g := newMockGno(progress, now)
	ms := newMockSol(progress, now, opts...)
	var s *server
	others := map[string]Other{"solana": newSolana(cfg.Chains[0], ms, mockGnoDomain, func() []string { return s.realmAddresses() })}
	s = newServer(cfg, g, others, site)
	s.now = now
	burnSig := mustSig(mockBurnTx)
	s.mockInfo = map[string]string{"sender": mockSender, "recipient": mockRecipient, "alice": mockPub("alice").String(), "gnoTx": mockGnoTx,
		"burnSig": mockBurnTx, "burnSigBase64": base64.StdEncoding.EncodeToString(burnSig[:]), "outId": mockOutID, "inId": mockInID, "bubbleRealm": hl.GnoPackageAddress(mockNS + "hlbubble")}
	return s, g, ms
}
