package main

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gnolang/bridges/solana/bridge/hl"
)

// The mock Ethereum deployment (E-M4: -mock and the tests), beside the Solana one in the same world: the Ethereum
// deployment's realms on gno.land (a second set, under …/eth/) and an Ethereum provider answering JSON-RPC from
// canned blocks, logs, receipts, transactions and contract calls in the contracts' own ABI — the calldata built
// with the encoders below, which the tests pin against Foundry's — so the readers run the code they run against a
// chain.
//
//	history: r/hlgov's decision #1 (set the protocol fee) sent (nonce 0, block 320), delivered under the header of
//	         321 and pending on HlAdmin; 100 GNOT test1 → bob (nonce 1, block 640), the header of 641, its delivery
//	         with the proof (leaf 3 of 4); alice's 3,000 BUBBLE burned back (Ethereum nonce 2), final, credited in
//	         block 1000 and paid; a stranger's dispatch toward gno.land (nonce 3), which is no burn (ER5); the
//	         heartbeat header of 1050, the trusted one
//	out:     120 GNOT test1 → bob (nonce 2, block 1293): 0 locked · 1 the header of 1294 recorded · 2 delivered with
//	         its proof under it (leaf 2 of 3)
//	in:      2,500 BUBBLE alice → test1 (Ethereum nonce 4, five minutes before the world began): 0 burned, its block
//	         not final · 1 final · 2 signer 1 vouches · 3 signer 2 · 4 credited · 5 half paid (signer 3 too) · 6 paid
//
// Ethereum's clock: a block every twelve seconds from 7,000,000 at the world's start, the finalized block 64
// behind the head — except the in-flight burn's, which is final from stage 1 and not before (the browser suite
// cannot wait the 13 to 19 minutes).

const (
	mockEthDomain   = uint32(11155111) // Sepolia's domain and chain id
	mockEthHead     = uint64(7_000_000)
	mockEthBlockS   = 12
	mockEthFinality = uint64(64)
	mockProtocolFee = uint64(100_000_000_000_000) // 0.0001 ETH
	ethDecHeight    = int64(320)
	ethPHeight      = int64(640)
	ethQCredit      = int64(1000)
	ethHeartbeat    = int64(1050)
	ethOutHeight    = int64(1293)
)

func mockEthNS(r string) string { return mockNS + "eth/" + r }

func mockEthAddr(name string) ethAddr {
	h := hl.Keccak([]byte("hlsite mock eth: " + name))
	return ethAddr(h[12:])
}

func mockEthTx(name string) string {
	h := hl.Keccak([]byte("hlsite mock eth tx: " + name))
	return hex0x(h[:])
}

func mockEthBlockHash(n uint64) [32]byte {
	return hl.Keccak([]byte("hlsite mock eth block " + strconv.FormatUint(n, 10)))
}

// mockHeaderHash is a gno.land header's hash as the mock gno node and the client agree on it.
func mockHeaderHash(h int64) [32]byte { return [32]byte(sum256("block " + strconv.FormatInt(h, 10))) }

func mockEthSendMessage(nonce uint32, amount int64) hl.Message {
	return hl.Message{Version: hl.Version, Nonce: nonce, Origin: mockGnoDomain, Sender: gnoAddr32(mockEthNS("hlgnot")), Destination: mockEthDomain,
		Recipient: mockEthAddr("GNOT").word(), Body: hl.TokenMessage(mockEthAddr("bob").word(), uint64(amount), nil)}
}

func mockEthOutMessage() hl.Message { return mockEthSendMessage(2, 120*one) }
func mockEthPMessage() hl.Message   { return mockEthSendMessage(1, 100*one) }

// mockEthDecisionParams is decision #1's: set the protocol fee to 0.0001 ETH, the treasurer the beneficiary.
func mockEthDecisionParams() []byte {
	p := binary.BigEndian.AppendUint64(nil, mockProtocolFee)
	w := mockEthAddr("treasurer").word()
	return append(p, w[:]...)
}

func mockEthDecisionMessage() hl.Message {
	body := append([]byte{7}, binary.BigEndian.AppendUint64(nil, 1)...)
	return hl.Message{Version: hl.Version, Nonce: 0, Origin: mockGnoDomain, Sender: gnoAddr32(mockEthNS("hlgov")), Destination: mockEthDomain,
		Recipient: mockEthAddr("admin").word(), Body: append(body, mockEthDecisionParams()...)}
}

func mockEthBurnMessageOf(nonce uint32, sender ethAddr, amount int64) hl.Message {
	rcpt, _ := hl.GnoAddressToBytes32(mockRecipient)
	return hl.Message{Version: hl.Version, Nonce: nonce, Origin: mockEthDomain, Sender: sender.word(), Destination: mockGnoDomain,
		Recipient: gnoAddr32(mockEthNS("hlbubble")), Body: hl.TokenMessage(rcpt, uint64(amount), nil)}
}

func mockEthInMessage() hl.Message { return mockEthBurnMessageOf(4, mockEthAddr("BUBBLE"), 2500*one) }
func mockEthQMessage() hl.Message  { return mockEthBurnMessageOf(2, mockEthAddr("BUBBLE"), 3000*one) }

// mockEthStrangerMessage is a stranger contract's dispatch toward the BUBBLE route: no burn (ER5).
func mockEthStrangerMessage() hl.Message {
	return mockEthBurnMessageOf(3, mockEthAddr("stranger"), 1_000_000*one)
}

var (
	mockEthOutID  = idOf(mockEthOutMessage())
	mockEthPID    = idOf(mockEthPMessage())
	mockEthInID   = idOf(mockEthInMessage())
	mockEthQID    = idOf(mockEthQMessage())
	mockEthDecID  = idOf(mockEthDecisionMessage())
	mockEthGnoTx  = base64.StdEncoding.EncodeToString(sum256("hlsite mock eth gno tx"))
	mockEthBurnTx = mockEthTx("burn in")
)

// mockEthChain is the Ethereum deployment's config: its own realms on gno.land, its contracts, its signers.
func mockEthChain() ChainConfig {
	ch := ChainConfig{ID: "ethereum", Kind: "ethereum", Name: "Ethereum", RPC: "http://127.0.0.1:8545", Domain: mockEthDomain, Explorer: "https://sepolia.etherscan.io",
		FromBlock: mockEthHead - 2000, Signers: []string{"https://signers.mock/eth/1", "https://signers.mock/eth/2", "https://signers.mock/eth/3"},
		Gno: &ChainRealms{Mailbox: mockEthNS("hlmailbox"), Fees: mockEthNS("hlfees"), Gov: mockEthNS("hlgov")}}
	p := &ch.Programs
	p.Mailbox, p.Pausable, p.Admin, p.Client = mockEthAddr("mailbox").String(), mockEthAddr("gate").String(), mockEthAddr("admin").String(), mockEthAddr("client").String()
	p.ProxyAdmin, p.MerkleTreeHook, p.ProtocolFee = mockEthAddr("proxyAdmin").String(), mockEthAddr("merkleTreeHook").String(), mockEthAddr("protocolFee").String()
	p.MailboxImpl, p.Token0Impl, p.Token1Impl = mockEthAddr("mailboxImpl").String(), mockEthAddr("token0Impl").String(), mockEthAddr("token1Impl").String()
	p.ClientImpl, p.GateImpl = mockEthAddr("clientImpl").String(), mockEthAddr("gateImpl").String()
	p.Ed25519Verifier, p.HlAdminExec = mockEthAddr("ed25519Verifier").String(), mockEthAddr("hlAdminExec").String()
	ch.Tokens = []TokenConfig{{Symbol: "GNOT", Program: mockEthAddr("GNOT").String(), Realm: mockEthNS("hlgnot")}, {Symbol: "BUBBLE", Program: mockEthAddr("BUBBLE").String(), Realm: mockEthNS("hlbubble")}}
	return ch
}

// mockBoth is the whole mock: the Solana deployment as mockServer serves it and the Ethereum one beside it.
func mockBoth(progress bool, site string, now func() time.Time) (*server, *mockGno, *mockSol, *mockEth) {
	s, g, ms := mockServer(progress, site, now)
	s.cfg.Chains = append(s.cfg.Chains, mockEthChain())
	if err := s.cfg.check(); err != nil {
		panic(err)
	}
	me := newMockEth(g.w)
	s.others["ethereum"] = newEthereum(*s.cfg.Chain("ethereum"), me, mockGnoDomain, func() []string { return s.realmAddresses() })
	for k, v := range map[string]string{"ethSender": mockEthAddr("alice").String(), "ethRecipient": mockEthAddr("bob").String(), "ethGnoTx": mockEthGnoTx, "ethBurnTx": mockEthBurnTx,
		"ethOutId": mockEthOutID, "ethInId": mockEthInID, "ethPId": mockEthPID, "ethQId": mockEthQID, "ethBubbleRealm": hl.GnoPackageAddress(mockEthNS("hlbubble")),
		"ethGnot": mockEthAddr("GNOT").String(), "ethBubble": mockEthAddr("BUBBLE").String(), "ethMailbox": mockEthAddr("mailbox").String(), "ethChainId": qty(uint64(mockEthDomain)),
		"ethFee": qty(mockProtocolFee), "ethQBurnTx": mockEthTx("burn Q"), "ethStrangerTx": mockEthTx("stranger"), "ethFailedTx": mockEthTx("failed"),
		"ethVerifierImpl": mockEthAddr("ed25519Verifier").String(), "ethClientImpl": mockEthAddr("clientImpl").String()} {
		s.mockInfo[k] = v
	}
	return s, g, ms, me
}

// ------------------------------------------------------------------ gno --

// ledgerPage is a ledger's "decisions.json/<from>" page as the Ethereum realms render it from their whole ledger: the
// decisions numbered from <from> on (0 reads as 1), twenty at most, "[]" past the last or for no number.
func ledgerPage(whole, from string) (string, error) {
	n, err := strconv.ParseUint(from, 10, 63)
	if err != nil {
		return "[]", nil
	}
	n = max(n, 1)
	var all []json.RawMessage
	if err := json.Unmarshal([]byte(whole), &all); err != nil {
		return "", err
	}
	var page []json.RawMessage
	for _, raw := range all {
		var d struct {
			ID uint64 `json:"id"`
		}
		if err := json.Unmarshal(raw, &d); err != nil {
			return "", err
		}
		if d.ID >= n && len(page) < 20 {
			page = append(page, raw)
		}
	}
	if len(page) == 0 {
		return "[]", nil
	}
	b, err := json.Marshal(page)
	return string(b), err
}

// mockEthMsgs are the Ethereum deployment's hlmailbox dispatches, as "msgs/" renders them.
func mockEthMsgs() []msgJSON {
	var out []msgJSON
	for _, x := range []struct {
		msg    hl.Message
		height int64
	}{{mockEthDecisionMessage(), ethDecHeight}, {mockEthPMessage(), ethPHeight}, {mockEthOutMessage(), ethOutHeight}} {
		out = append(out, msgJSON{Nonce: x.msg.Nonce, ID: idOf(x.msg), Height: x.height, Message: hex.EncodeToString(x.msg.Bytes())})
	}
	return out
}

// ethRender is the Ethereum deployment's realms' renders.
func (m *mockGno) ethRender(realm, path string) (string, error) {
	_, in, _ := m.w.getEth()
	switch {
	case realm == "hlmailbox" && path == "json":
		return m.ethMailboxJSON(), nil
	case realm == "hlmailbox" && strings.HasPrefix(path, "msgs/"):
		from, err := strconv.Atoi(strings.TrimPrefix(path, "msgs/"))
		if err != nil {
			return "[]", nil
		}
		var page []msgJSON
		for _, x := range mockEthMsgs() {
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
		return m.ethRouteJSON(realm), nil
	case path == "decisions.json":
		if realm == "hlfees" {
			t := m.w.start.Unix()
			return fmt.Sprintf(`[{"id":1,"kind":"set fee","text":"set the GNOT route's fee to 40 GNOT","state":"pending","made":%d,"recorded":%d,"due":%d,"applied":0,"cancelled":0,"note":"queued 7 days"}]`, t-7200, t-7200, t-7200+7*86400), nil
		}
		return "[]", nil
	case strings.HasPrefix(path, "decisions.json/"):
		// the Ethereum realms' ledgers by page, as the realms render them (hlmailbox.DecisionsJSONPage)
		whole, err := m.ethRender(realm, "decisions.json")
		if err != nil {
			return "", err
		}
		return ledgerPage(whole, strings.TrimPrefix(path, "decisions.json/"))
	case realm == "hlgov" && path == "json":
		w := mockEthAddr("admin").word()
		return fmt.Sprintf(`{"address":"%s","governor":"%s","yielded":0,"recipient":"%s","former":[],"last":1,"next":2}`, hl.GnoPackageAddress(mockEthNS("hlgov")), mockGovernor, hex.EncodeToString(w[:])), nil
	case strings.HasPrefix(path, "send/"):
		id := strings.TrimPrefix(path, "send/")
		switch {
		case realm == "hlgnot" && id == mockEthOutID:
			return fmt.Sprintf(`{"id":"%s","from":"%s","amount":%d,"at":%d,"imported":false,"refunded":0,"writtenOff":0}`, mockEthOutID, mockSender, 120*one, m.w.blockTime(ethOutHeight).Unix()), nil
		case realm == "hlgnot" && id == mockEthPID:
			return fmt.Sprintf(`{"id":"%s","from":"%s","amount":%d,"at":%d,"imported":false,"refunded":0,"writtenOff":0}`, mockEthPID, mockSender, 100*one, m.w.blockTime(ethPHeight).Unix()), nil
		}
		return "null", nil
	case strings.HasPrefix(path, "claim/"):
		id := strings.TrimPrefix(path, "claim/")
		switch {
		case realm == "hlbubble" && id == mockEthQID:
			return fmt.Sprintf(`{"id":"%s","to":"%s","amount":%d,"paid":%d,"state":"done","by":"signers","at":%d,"height":%d,"seq":1}`, mockEthQID, mockRecipient, 3000*one, 3000*one, m.w.blockTime(ethQCredit).Unix(), ethQCredit), nil
		case realm == "hlbubble" && id == mockEthInID && in >= 4:
			paid, state := int64(0), "open"
			switch {
			case in >= 6:
				paid, state = 2500*one, "done"
			case in == 5:
				paid = 1250 * one
			}
			at := m.w.when("ein4")
			return fmt.Sprintf(`{"id":"%s","to":"%s","amount":%d,"paid":%d,"state":"%s","by":"signers","at":%d,"height":%d,"seq":2}`, mockEthInID, mockRecipient, 2500*one, paid, state, at, m.w.heightAt(at)), nil
		}
		return "null", nil
	case realm == "hlmailbox" && strings.HasPrefix(path, "delivered/"):
		switch strings.TrimPrefix(path, "delivered/") {
		case mockEthQID:
			return strconv.FormatInt(ethQCredit, 10), nil
		case mockEthInID:
			if in >= 4 {
				return strconv.FormatInt(m.w.heightAt(m.w.when("ein4")), 10), nil
			}
		}
		return "0", nil
	case strings.HasPrefix(path, "claims/") || strings.HasPrefix(path, "sweeps/") || strings.HasPrefix(path, "refunds/"):
		return "[]", nil
	}
	return "", fmt.Errorf("mock: no render for %s:%s", mockEthNS(realm), path)
}

func (m *mockGno) ethMailboxJSON() string {
	t := m.w.start.Unix()
	_, in, _ := m.w.getEth()
	delivered := 1
	if in >= 4 {
		delivered = 2
	}
	route := func(realm string) string {
		return fmt.Sprintf(`%q:{"epoch":%d,"epochAt":%d,"sends":false,"credits":false,"payouts":false,"flags":[],"predecessor":"","successor":"","enrolled":0,"enrolledAt":0,"pulled":false,"dropped":[]}`, mockEthNS(realm), t-86400, t-86400)
	}
	key := func(n string) string { return hex.EncodeToString(mockPub(n).Bytes()) }
	return fmt.Sprintf(`{"domain":%d,"hook":"%s","address":"%s","count":3,"root":"%s","delivered":%d,"live":true,"launchedAt":%d,"governor":"%s","yielded":0,"launchOrigin":%d,`+
		`"clocks":{"factor":1,"delay":604800,"deadline":1209600},"decisions":2,"signers":{"keys":["%s","%s","%s"],"threshold":2,"groupA":[],"groupB":[]},`+
		`"verifiers":{"%d":{"pkgpath":"%s","registered":true}},"registered":["%s"],"registeredCount":1,"seats":["%s","g1aeddl3g5hr8v5y8vx3s4x9m7qeq9h4u6xmq6wh","g1manfred47kzduec920z88wfr64y5asmjd4q3pz","","%s",""],`+
		`"routes":{%s,%s},"droppedRoutes":{},"successors":[],"successorCount":0,"successorRoutes":{}}`,
		mockGnoDomain, hex.EncodeToString(sum256("eth hook")), hl.GnoPackageAddress(mockEthNS("hlmailbox")), hex.EncodeToString(sum256("eth root")), delivered, t-86400, mockGovernor, mockEthDomain,
		key("eth s0"), key("eth s1"), key("eth s2"), mockEthDomain, mockEthNS("hlverifier"), mockEthNS("hlverifier"), mockGovernor, mockSender, route("hlbubble"), route("hlgnot"))
}

// ethRouteJSON is a route's figures at the world's stages: GNOT's lock holds the send in flight from the start
// (Ethereum's supply takes it once the mint's block is final); BUBBLE's holds the burn in flight until it is
// credited, then owes it, then pays it out.
func (m *mockGno) ethRouteJSON(realm string) string {
	_, in, _ := m.w.getEth()
	sym, lock, fee, minimum, floor := "GNOT", 500_000*one, 50*one, 100*one, 10_000*one
	owed, openClaims := int64(0), 0
	if realm == "hlbubble" {
		sym, lock, fee, minimum = "BUBBLE", 1_202_500*one, 1000*one, 2000*one
		switch {
		case in >= 6:
			lock = 1_200_000 * one
		case in == 5:
			lock, owed, openClaims = 1_201_250*one, 1250*one, 1
		case in == 4:
			owed, openClaims = 2500*one, 1
		}
	}
	cap := max(lock/10, floor)
	router := mockEthAddr(sym).word()
	return fmt.Sprintf(`{"address":%q,"live":true,"governor":%q,"yielded":0,"balance":%d,"paused":"","symbol":%q,"domain":%d,"router":%q,"oldRouter":%q,"oldUntil":0,"lock":%d,"sentTotal":%d,"paidTotal":%d,"owed":%d,"voided":0,"lost":0,"sweep":0,"cap":%d,"meter":%d,"capFloor":%d,"fee":%d,"feeFloor":%d,"minimum":%d,"maximum":%d,"openClaims":%d,"claims":3,"sends":5,"refunded":0,"refundedTotal":0,"writtenOff":0,"writtenOffTotal":0,"migratedTo":"","importing":false,"imported":0,"clocks":{"factor":1,"delay":604800,"deadline":1209600},"decisions":0}`,
		hl.GnoPackageAddress(mockEthNS(realm)), mockGovernor, lock, sym, mockEthDomain, hex.EncodeToString(router[:]), strings.Repeat("00", 32), lock, lock+3000*one, 3000*one, owed, cap, cap/3, floor, fee, fee/2, minimum, 25_000*one, openClaims)
}

// probeEth is the Ethereum deployment's three signers: the history's burn signed by all three, the stranger's
// dispatch by none, the burn in flight by signer 1 from stage 2, signer 2 from 3, signer 3 from 5 — once its block
// is final, and not before; signer 3 answers nothing while it is down.
func (p mockProber) probeEth(base string, nonce uint64) (int, error) {
	_, in, down := p.w.getEth()
	n, _ := strconv.Atoi(base[strings.LastIndexByte(base, '/')+1:])
	if n == 3 && down {
		return 0, errors.New("connection refused")
	}
	switch {
	case nonce == 2:
		return 200, nil
	case nonce == 4 && in >= map[int]int{1: 2, 2: 3, 3: 5}[n]:
		return 200, nil
	}
	return 404, nil
}

// --------------------------------------------------------------- Ethereum --

// mockEth is an Ethereum provider from canned data, in process (ethCaller) and over HTTP (ServeHTTP).
type mockEth struct {
	mu     sync.Mutex
	w      *mockWorld
	calls  map[string]int
	hold   bool // two guardians' flags on the gate and on HlAdmin: a hold
	hashes map[[32]byte]uint64
	txs    []*mockEthTxn
	// hook answers a call before the canned data does (a test's error, a reorganization, a range refused)
	hook func(method string, params []json.RawMessage) (json.RawMessage, *rpcError, bool)
}

type mockEthTxn struct {
	hash     string
	from, to ethAddr
	input    []byte
	at       func() int64
	reached  func() bool
	status   string
	logs     []mockEthLog
}

type mockEthLog struct {
	addr   ethAddr
	topics [][32]byte
	data   []byte
}

func newMockEth(w *mockWorld) *mockEth {
	m := &mockEth{w: w, calls: map[string]int{}, hashes: map[[32]byte]uint64{}}
	t := w.start.Unix()
	gt := func(h int64) int64 { return w.blockTime(h).Unix() }
	fixed := func(at int64) func() int64 { return func() int64 { return at } }
	always := func() bool { return true }
	stage := func(key string, n int) func() bool {
		return func() bool {
			out, in, _ := w.getEth()
			if key == "out" {
				return out >= n
			}
			return in >= n
		}
	}
	when := func(key string) func() int64 { return func() int64 { return w.when(key) } }
	m.addHeader("header 321", 321, fixed(gt(ethDecHeight)+40), always)
	m.addDelivery("decision delivery", mockEthDecisionMessage(), mockEthAddr("admin"), 321, 1, 0, 0, fixed(gt(ethDecHeight)+90), always)
	m.addHeader("header 641", 641, fixed(gt(ethPHeight)+40), always)
	m.addDelivery("P delivery", mockEthPMessage(), mockEthAddr("GNOT"), 641, 4, 2, 2, fixed(gt(ethPHeight)+100), always)
	m.addHeader("header 1050", ethHeartbeat, fixed(gt(ethHeartbeat)+20), always)
	m.addBurn("burn Q", mockEthQMessage(), mockEthAddr("alice"), fixed(t-3000))
	m.addBurn("stranger", mockEthStrangerMessage(), mockEthAddr("mallory"), fixed(t-2400))
	m.addBurn("burn in", mockEthInMessage(), mockEthAddr("alice"), fixed(t-300))
	m.txs = append(m.txs, &mockEthTxn{hash: mockEthTx("failed"), from: mockEthAddr("alice"), to: mockEthAddr("BUBBLE"), at: fixed(t - 600), reached: always, status: "0x0"})
	m.addHeader("out header", ethOutHeight+1, when("eout1"), stage("out", 1))
	m.addDelivery("out delivery", mockEthOutMessage(), mockEthAddr("GNOT"), ethOutHeight+1, 3, 1, 2, when("eout2"), stage("out", 2))
	return m
}

// addHeader is the updater's update of a header: the set of six validators of power 60, a HeaderRecorded log.
func (m *mockEth) addHeader(name string, height int64, at func() int64, reached func() bool) {
	var set []mockVal
	for i := range 6 {
		set = append(set, mockVal{pubkey: [32]byte(sum256("validator " + strconv.Itoa(i))), power: 60})
	}
	input := encUpdate([][]byte{[]byte(name)}, 1, [32]byte(sum256("parts "+name)), 0, []mockPrecommit{{index: 0, secs: 1, r: [32]byte(sum256("r")), s: [32]byte(sum256("s"))}}, set, nil)
	hash := mockHeaderHash(height)
	data := encWords(u64Word(1), u64Word(uint64(height)), [32]byte(sum256("running "+name)), u64Word(0))
	m.txs = append(m.txs, &mockEthTxn{hash: mockEthTx(name), from: mockEthAddr("updater"), to: mockEthAddr("client"), input: input, at: at, reached: reached, status: "0x1",
		logs: []mockEthLog{{addr: mockEthAddr("client"), topics: [][32]byte{topicHeaderRecorded, hash, i64Word(height)}, data: data}}})
}

// addDelivery is the relayer's process(metadata, message): the proof under a header the client holds, the
// mailbox's Process and ProcessId, a token's ReceivedTransferRemote for a mint.
func (m *mockEth) addDelivery(name string, msg hl.Message, recipient ethAddr, header int64, total, index uint32, aunts int, at func() int64, reached func() bool) {
	raw := msg.Bytes()
	id := hl.ID(raw)
	meta := encProofMeta(mockHeaderHash(header), total, index, make([][32]byte, aunts), []byte("leaf "+name))
	logs := []mockEthLog{
		{addr: mockEthAddr("mailbox"), topics: [][32]byte{topicProcess, u64Word(uint64(msg.Origin)), msg.Sender, recipient.word()}},
		{addr: mockEthAddr("mailbox"), topics: [][32]byte{topicProcessID, id}},
	}
	if to, amt, _, err := hl.ParseTokenMessage(msg.Body); err == nil && recipient != mockEthAddr("admin") {
		logs = append(logs, mockEthLog{addr: recipient, topics: [][32]byte{eventTopic("Transfer(address,address,uint256)"), {}, to}, data: encWords(u64Word(amt))},
			mockEthLog{addr: recipient, topics: [][32]byte{topicReceived, u64Word(uint64(msg.Origin)), to}, data: encWords(u64Word(amt))})
	}
	m.txs = append(m.txs, &mockEthTxn{hash: mockEthTx(name), from: mockEthAddr("relayer"), to: mockEthAddr("mailbox"), input: encProcess(meta, raw), at: at, reached: reached, status: "0x1", logs: logs})
}

// addBurn is a transferRemote toward gno.land (or, from a stranger, a dispatch of its own): the token's burn and
// SentTransferRemote, the mailbox's Dispatch and DispatchId.
func (m *mockEth) addBurn(name string, msg hl.Message, from ethAddr, at func() int64) {
	raw := msg.Bytes()
	id := hl.ID(raw)
	sender, _ := addrOf32(msg.Sender)
	_, amt, _, _ := hl.ParseTokenMessage(msg.Body)
	rcpt, _, _, _ := hl.ParseTokenMessage(msg.Body)
	logs := []mockEthLog{
		{addr: sender, topics: [][32]byte{eventTopic("Transfer(address,address,uint256)"), from.word(), {}}, data: encWords(u64Word(amt))},
		{addr: sender, topics: [][32]byte{eventTopic("SentTransferRemote(uint32,bytes32,uint256)"), u64Word(uint64(msg.Destination)), rcpt}, data: encWords(u64Word(amt))},
		{addr: mockEthAddr("mailbox"), topics: [][32]byte{topicDispatch, msg.Sender, u64Word(uint64(msg.Destination)), msg.Recipient}, data: append(encWords(u64Word(32)), encBytes(raw)...)},
		{addr: mockEthAddr("mailbox"), topics: [][32]byte{eventTopic("DispatchId(bytes32)"), id}},
	}
	input := calldata(selTransfer, u64Word(uint64(msg.Destination)), rcpt, u64Word(amt))
	m.txs = append(m.txs, &mockEthTxn{hash: mockEthTx(name), from: from, to: sender, input: input, at: at, reached: func() bool { return true }, status: "0x1", logs: logs})
}

func (m *mockEth) count(method string) {
	m.mu.Lock()
	m.calls[method]++
	m.mu.Unlock()
}

// the clock: a block every twelve seconds from mockEthHead at the world's start
func (m *mockEth) blockAt(t int64) uint64 {
	d := t - m.w.start.Unix()
	q := d / mockEthBlockS
	if d%mockEthBlockS != 0 && d < 0 {
		q--
	}
	return uint64(int64(mockEthHead) + q)
}

func (m *mockEth) timeOf(n uint64) int64 {
	return m.w.start.Unix() + (int64(n)-int64(mockEthHead))*mockEthBlockS
}

func (m *mockEth) head() uint64 { return m.blockAt(m.w.now().Unix()) }

// finalized trails the head by mockEthFinality, the in-flight burn's block final from stage 1 and not before.
func (m *mockEth) finalized() uint64 {
	f := m.head() - mockEthFinality
	burn := m.blockAt(m.w.start.Unix() - 300)
	if _, in, _ := m.w.getEth(); in >= 1 {
		return max(f, burn)
	}
	return min(f, burn-1)
}

func (m *mockEth) blockJSON(n uint64) map[string]string {
	h := mockEthBlockHash(n)
	m.mu.Lock()
	m.hashes[h] = n
	m.mu.Unlock()
	p := mockEthBlockHash(n - 1)
	return map[string]string{"number": qty(n), "hash": hex0x(h[:]), "parentHash": hex0x(p[:]), "timestamp": qty(uint64(m.timeOf(n)))}
}

func (m *mockEth) tx(hash string) *mockEthTxn {
	for _, t := range m.txs {
		if t.hash == strings.ToLower(hash) && t.reached() {
			return t
		}
	}
	return nil
}

func (m *mockEth) logJSON(t *mockEthTxn, i int, l mockEthLog) map[string]any {
	n := m.blockAt(t.at())
	h := mockEthBlockHash(n)
	topics := []string{}
	for _, x := range l.topics {
		topics = append(topics, hex0x(x[:]))
	}
	return map[string]any{"address": strings.ToLower(l.addr.String()), "topics": topics, "data": hex0x(l.data), "blockNumber": qty(n), "blockHash": hex0x(h[:]),
		"transactionHash": t.hash, "transactionIndex": "0x0", "logIndex": qty(uint64(i)), "removed": false}
}

// Call answers in process, through the same JSON a provider would send.
func (m *mockEth) Call(ctx context.Context, method string, params []any, out any) error {
	raw := make([]json.RawMessage, len(params))
	for i, p := range params {
		b, err := json.Marshal(p)
		if err != nil {
			return err
		}
		raw[i] = b
	}
	res, rerr := m.answer(method, raw)
	if rerr != nil {
		return fmt.Errorf("%s: %w", method, rerr)
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(res, out)
}

// ServeHTTP answers JSON-RPC over HTTP (the tests of the HTTP client).
func (m *mockEth) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	var req struct {
		ID     json.RawMessage   `json:"id"`
		Method string            `json:"method"`
		Params []json.RawMessage `json:"params"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, "bad request", 400)
		return
	}
	res, rerr := m.answer(req.Method, req.Params)
	out := map[string]any{"jsonrpc": "2.0", "id": req.ID}
	if rerr != nil {
		out["error"] = rerr
	} else {
		out["result"] = res
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

func (m *mockEth) answer(method string, params []json.RawMessage) (json.RawMessage, *rpcError) {
	m.count(method)
	m.mu.Lock()
	hook := m.hook
	m.mu.Unlock()
	if hook != nil {
		if res, rerr, ok := hook(method, params); ok {
			return res, rerr
		}
	}
	v, rerr := m.handle(method, params)
	if rerr != nil {
		return nil, rerr
	}
	b, _ := json.Marshal(v)
	return b, nil
}

func str(p []json.RawMessage, i int) string {
	var s string
	if i < len(p) {
		_ = json.Unmarshal(p[i], &s)
	}
	return s
}

func (m *mockEth) handle(method string, p []json.RawMessage) (any, *rpcError) {
	switch method {
	case "eth_chainId":
		return qty(uint64(mockEthDomain)), nil
	case "eth_blockNumber":
		return qty(m.head()), nil
	case "eth_getBlockByNumber":
		tag := str(p, 0)
		var n uint64
		switch tag {
		case "latest":
			n = m.head()
		case "finalized", "safe":
			n = m.finalized()
		default:
			x, err := parseQty(tag)
			if err != nil {
				return nil, &rpcError{Code: -32602, Message: "invalid block"}
			}
			if x > m.head() {
				return nil, nil
			}
			n = x
		}
		return m.blockJSON(n), nil
	case "eth_call":
		var c struct{ To, Data string }
		if len(p) < 2 || json.Unmarshal(p[0], &c) != nil {
			return nil, &rpcError{Code: -32602, Message: "invalid call"}
		}
		n := m.head()
		var at struct {
			BlockHash string `json:"blockHash"`
		}
		if json.Unmarshal(p[1], &at) == nil && at.BlockHash != "" {
			h, _ := parseHash(at.BlockHash)
			m.mu.Lock()
			x, ok := m.hashes[h]
			m.mu.Unlock()
			if !ok {
				return nil, &rpcError{Code: -32000, Message: "header not found"}
			}
			n = x
		}
		to, err := parseEthWire(c.To)
		data, err2 := parseData(c.Data)
		if err != nil || err2 != nil || len(data) < 4 {
			return nil, &rpcError{Code: -32602, Message: "invalid call"}
		}
		out, rerr := m.ethCall(to, [4]byte(data[:4]), data[4:], n)
		if rerr != nil {
			return nil, rerr
		}
		return hex0x(out), nil
	case "eth_getLogs":
		var f struct {
			FromBlock, ToBlock string
			Address            []string
			Topics             [][]string
		}
		if len(p) < 1 || json.Unmarshal(p[0], &f) != nil {
			return nil, &rpcError{Code: -32602, Message: "invalid filter"}
		}
		from, err1 := parseQty(f.FromBlock)
		to, err2 := parseQty(f.ToBlock)
		if err1 != nil || err2 != nil || to < from {
			return nil, &rpcError{Code: -32602, Message: "invalid block range"}
		}
		addrs := map[string]bool{}
		for _, a := range f.Address {
			addrs[strings.ToLower(a)] = true
		}
		want := func(pos int, t [32]byte) bool {
			if pos >= len(f.Topics) || len(f.Topics[pos]) == 0 {
				return true
			}
			for _, x := range f.Topics[pos] {
				if strings.EqualFold(x, hex0x(t[:])) {
					return true
				}
			}
			return false
		}
		out := []map[string]any{}
		for _, t := range m.txs {
			if !t.reached() || t.status != "0x1" {
				continue
			}
			n := m.blockAt(t.at())
			if n < from || n > to || n > m.head() {
				continue
			}
			for i, l := range t.logs {
				if len(addrs) > 0 && !addrs[strings.ToLower(l.addr.String())] {
					continue
				}
				ok := true
				for pos, x := range l.topics {
					ok = ok && want(pos, x)
				}
				if len(l.topics) < len(f.Topics) {
					for pos := len(l.topics); pos < len(f.Topics); pos++ {
						ok = ok && len(f.Topics[pos]) == 0
					}
				}
				if ok {
					out = append(out, m.logJSON(t, i, l))
				}
			}
		}
		return out, nil
	case "eth_getTransactionReceipt":
		t := m.tx(str(p, 0))
		if t == nil || m.blockAt(t.at()) > m.head() {
			return nil, nil
		}
		n := m.blockAt(t.at())
		h := mockEthBlockHash(n)
		logs := []map[string]any{}
		if t.status == "0x1" {
			for i, l := range t.logs {
				logs = append(logs, m.logJSON(t, i, l))
			}
		}
		return map[string]any{"status": t.status, "blockNumber": qty(n), "blockHash": hex0x(h[:]), "transactionHash": t.hash, "logs": logs}, nil
	case "eth_getTransactionByHash":
		t := m.tx(str(p, 0))
		if t == nil {
			return nil, nil
		}
		n := m.blockAt(t.at())
		h := mockEthBlockHash(n)
		return map[string]string{"hash": t.hash, "from": strings.ToLower(t.from.String()), "to": strings.ToLower(t.to.String()), "input": hex0x(t.input), "blockNumber": qty(n), "blockHash": hex0x(h[:])}, nil
	}
	return nil, &rpcError{Code: -32601, Message: "the method " + method + " does not exist"}
}

// supply is a token's total supply at a block.
func (m *mockEth) supply(sym string, n uint64) int64 {
	t := m.w.start.Unix()
	if sym == "GNOT" {
		s := 499_780 * one
		if n >= m.blockAt(m.w.blockTime(ethPHeight).Unix()+100) {
			s += 100 * one
		}
		if out, _, _ := m.w.getEth(); out >= 2 && n >= m.blockAt(m.w.when("eout2")) {
			s += 120 * one
		}
		return s
	}
	s := 1_205_500 * one
	if n >= m.blockAt(t-3000) {
		s -= 3000 * one
	}
	if n >= m.blockAt(t-300) {
		s -= 2500 * one
	}
	return s
}

// ethCall answers the contracts' calls at block n.
func (m *mockEth) ethCall(to ethAddr, sel [4]byte, args []byte, n uint64) ([]byte, *rpcError) {
	revert := &rpcError{Code: 3, Message: "execution reverted"}
	arg := func(i uint64) [32]byte { w, _ := wordAt(args, 32*i); return w }
	t := m.w.start.Unix()
	keys := func() []byte {
		var b []byte
		for _, k := range []string{"g1", "g2", "g3", "g4", "watch5", "watch6"} {
			w := mockEthAddr(k).word()
			b = append(b, w[:]...)
		}
		return b
	}
	flags := func() []byte {
		var ws [][32]byte
		for i := range 6 {
			if m.hold && i < 2 {
				ws = append(ws, u64Word(m.head()-200+uint64(10*i)), u64Word(uint64(t-900+int64(20*i))))
			} else {
				ws = append(ws, [32]byte{}, [32]byte{})
			}
		}
		return encWords(ws...)
	}
	hold := func() []byte {
		if m.hold {
			return encWords(u64Word(m.head()-190), u64Word(uint64(t-880)))
		}
		return encWords([32]byte{}, [32]byte{})
	}
	eout, _, _ := m.w.getEth()
	switch to {
	case mockEthAddr("GNOT"), mockEthAddr("BUBBLE"):
		sym := "GNOT"
		if to == mockEthAddr("BUBBLE") {
			sym = "BUBBLE"
		}
		switch sel {
		case selTotalSupply:
			return encWords(u64Word(uint64(m.supply(sym, n)))), nil
		case selBalanceOf:
			if arg(0) == mockEthAddr("alice").word() {
				return encWords(u64Word(uint64(map[string]int64{"GNOT": 1000 * one, "BUBBLE": 10_000 * one}[sym]))), nil
			}
			return encWords([32]byte{}), nil
		case selQuoteTransfer:
			amt := arg(2)
			return encQuotes([]quote{{Amount: new(big.Int).SetUint64(mockProtocolFee)}, {Token: to, Amount: bigWord(amt)}, {Token: to, Amount: new(big.Int)}}), nil
		}
	case mockEthAddr("mailbox"):
		switch sel {
		case selNonce:
			return encWords(u64Word(5)), nil
		case selProcessedAt:
			id := arg(0)
			for _, x := range []struct {
				name string
				id   string
			}{{"decision delivery", mockEthDecID}, {"P delivery", mockEthPID}, {"out delivery", mockEthOutID}} {
				if hex.EncodeToString(id[:]) == x.id {
					if tx := m.tx(mockEthTx(x.name)); tx != nil && m.blockAt(tx.at()) <= n {
						return encWords(u64Word(m.blockAt(tx.at()))), nil
					}
				}
			}
			return encWords([32]byte{}), nil
		}
	case mockEthAddr("gate"):
		switch sel {
		case selKeys:
			return keys(), nil
		case selFlags:
			return flags(), nil
		case selHold:
			return hold(), nil
		case selClockFactor:
			return encWords(u64Word(1)), nil
		case selToken:
			i, _ := u64Of(arg(0))
			if i > 1 {
				return nil, revert
			}
			w := mockEthAddr([]string{"GNOT", "BUBBLE"}[i]).word()
			return encWords(w), nil
		case selLimits:
			return encWords(u64Word(uint64(25_000*one)), u64Word(uint64(100_000*one)), u64Word(uint64(1_000*one)), u64Word(uint64(t-7200))), nil
		}
	case mockEthAddr("client"):
		trusted := ethHeartbeat
		if eout >= 1 {
			trusted = ethOutHeight + 1
		}
		switch sel {
		case selTrustedHeight:
			return encWords(i64Word(trusted)), nil
		case selTrustedTime:
			return encWords(u64Word(uint64(m.w.blockTime(trusted).Unix()))), nil
		case selTrustedHash:
			return encWords(mockHeaderHash(trusted)), nil
		case selQuarantineEnd:
			return encWords([32]byte{}), nil
		case selClockFactor:
			return encWords(u64Word(1)), nil
		case selEntry:
			h := arg(0)
			for _, x := range []int64{321, 641, ethHeartbeat, ethOutHeight + 1} {
				if h == mockHeaderHash(x) {
					return encWords(u64Word(1), i64Word(x), [32]byte(sum256("results "+strconv.FormatInt(x, 10)))), nil
				}
			}
			return encWords([32]byte{}, [32]byte{}, [32]byte{}), nil
		}
	case mockEthAddr("admin"):
		switch sel {
		case selKeys:
			return keys(), nil
		case selFlags:
			return flags(), nil
		case selHold:
			return hold(), nil
		case selClockFactor:
			return encWords(u64Word(1)), nil
		case selLastSeq:
			return encWords(u64Word(1)), nil
		case selPending:
			return encWords(u64Word(1)), nil
		case selSetupDone:
			return encWords(u64Word(1)), nil
		case selDecision:
			k, _ := u64Of(arg(0))
			if k != 1 {
				return encDecision(ethDecision{}), nil
			}
			tx := m.tx(mockEthTx("decision delivery"))
			b := m.blockAt(tx.at())
			return encDecision(ethDecision{Number: 1, Kind: 7, DeliveredBlock: b, DeliveredTime: m.timeOf(b), Deliverer: mockEthAddr("relayer"), Params: mockEthDecisionParams()}), nil
		}
	}
	return nil, revert
}

// ------------------------------------------------------------ encoders --

// The contracts' ABI as the mock answers it, the decoders' inverse; the tests pin each against Foundry's encoding.

func encWords(ws ...[32]byte) []byte {
	b := make([]byte, 0, 32*len(ws))
	for _, w := range ws {
		b = append(b, w[:]...)
	}
	return b
}

// i64Word is a signed value as one word, sign-extended.
func i64Word(v int64) [32]byte {
	var w [32]byte
	if v < 0 {
		for i := range w {
			w[i] = 0xff
		}
	}
	binary.BigEndian.PutUint64(w[24:], uint64(v))
	return w
}

func encQuotes(qs []quote) []byte {
	ws := [][32]byte{u64Word(32), u64Word(uint64(len(qs)))}
	for _, q := range qs {
		var amt [32]byte
		q.Amount.FillBytes(amt[:])
		ws = append(ws, q.Token.word(), amt)
	}
	return encWords(ws...)
}

func encDecision(d ethDecision) []byte {
	b := encWords(u64Word(32), u64Word(d.Number), u64Word(uint64(d.Kind)), u64Word(d.DeliveredBlock), u64Word(uint64(d.DeliveredTime)), d.Deliverer.word(), u64Word(192))
	return append(b, encBytes(d.Params)...)
}

func encProofMeta(header [32]byte, total, index uint32, aunts [][32]byte, leaf []byte) []byte {
	head := encWords(header, u64Word(uint64(total)), u64Word(uint64(index)), u64Word(160), u64Word(uint64(160+32+32*len(aunts))))
	head = append(head, encWords(append([][32]byte{u64Word(uint64(len(aunts)))}, aunts...)...)...)
	return append(head, encBytes(leaf)...)
}

func encProcess(meta, msg []byte) []byte {
	m := encBytes(meta)
	b := calldata(selProcess, u64Word(64), u64Word(uint64(64+len(m))))
	return append(append(b, m...), encBytes(msg)...)
}

type mockVal struct {
	pubkey [32]byte
	power  int64
}

type mockPrecommit struct {
	index uint16
	secs  int64
	nanos int32
	r, s  [32]byte
}

// encUpdate is update(bytes[] headerLeaves, (uint32 partsTotal, bytes32 partsHash, int64 round, Precommit[]) commit,
// Validator[] set, Validator[] nextSet).
func encUpdate(leaves [][]byte, partsTotal uint32, partsHash [32]byte, round int64, pcs []mockPrecommit, set, next []mockVal) []byte {
	lt := encWords(u64Word(uint64(len(leaves))))
	var heads, tails []byte
	off := uint64(32 * len(leaves))
	for _, l := range leaves {
		w := u64Word(off)
		heads = append(heads, w[:]...)
		t := encBytes(l)
		tails = append(tails, t...)
		off += uint64(len(t))
	}
	lt = append(append(lt, heads...), tails...)
	ct := encWords(u64Word(uint64(partsTotal)), partsHash, i64Word(round), u64Word(128), u64Word(uint64(len(pcs))))
	for _, pc := range pcs {
		ct = append(ct, encWords(u64Word(uint64(pc.index)), i64Word(pc.secs), i64Word(int64(pc.nanos)), pc.r, pc.s)...)
	}
	vals := func(vs []mockVal) []byte {
		b := encWords(u64Word(uint64(len(vs))))
		for _, v := range vs {
			b = append(b, encWords(v.pubkey, i64Word(v.power))...)
		}
		return b
	}
	st, nt := vals(set), vals(next)
	o1 := uint64(128)
	o2 := o1 + uint64(len(lt))
	o3 := o2 + uint64(len(ct))
	o4 := o3 + uint64(len(st))
	b := calldata(selUpdate, u64Word(o1), u64Word(o2), u64Word(o3), u64Word(o4))
	for _, part := range [][]byte{lt, ct, st, nt} {
		b = append(b, part...)
	}
	return b
}

// mockEthHash256 is a message's sha256 in hex, the key its proof is kept under.
func mockEthHash256(m hl.Message) string {
	s := sha256.Sum256(m.Bytes())
	return hex.EncodeToString(s[:])
}
