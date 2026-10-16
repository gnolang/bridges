package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/rpc"

	"github.com/gnolang/bridges/solana/bridge/hl"
	"github.com/gnolang/bridges/ethereum/bridge/ethx"
	"github.com/gnolang/bridges/ethereum/bridge/gnoside"
)

// The machines over real contracts: Hyperlane 12.2.0's Mailbox, HypERC20, MerkleTreeHook and ProtocolFee, lane
// E-M2b's GnoLightClient (calling lane E-M2a's Ed25519Verifier), PausableGate and HlAdmin, compiled in the Foundry
// project ETHX_CONTRACTS_OUT names (the devbox; skipped elsewhere), deployed on anvil (EIP-170 enforced) and wired as
// eth/bridge/PLAN.md §5.8 wires them (the tokens' ISM and the mailbox's default ISM the gate, the default hook the
// MerkleTreeHook, the required hook the ProtocolFee, the tokens' routers gno.land's route realms). The gno side is the synthetic chain of fakegno_test.go,
// whose headers the real client verifies (real ed25519 over tm2's sign bytes). A send dispatched on gno.land: the
// updater buys the header after its block, the relayer delivers it through Mailbox.process — the gate charges the
// day cap, the client proves the results leaf, the token mints to the recipient — and a second tick ends it as
// delivered.

const testAnvilKey = "ac0974bec39a17e36ba4a6b4d238ff944bacb478cbed5efcae784d7bf4f2ff80"

type chainKit struct {
	t   *testing.T
	out string
	c   *ethx.Client
	s   *ethx.Sender
}

func (k *chainKit) artifact(file, name string) (abi.ABI, []byte) {
	k.t.Helper()
	raw, err := os.ReadFile(filepath.Join(k.out, file, name+".json"))
	if err != nil {
		k.t.Fatal(err)
	}
	var j struct {
		ABI      json.RawMessage `json:"abi"`
		Bytecode struct {
			Object string `json:"object"`
		} `json:"bytecode"`
	}
	if err := json.Unmarshal(raw, &j); err != nil {
		k.t.Fatal(err)
	}
	a, err := abi.JSON(strings.NewReader(string(j.ABI)))
	if err != nil {
		k.t.Fatal(err)
	}
	code, err := hexutil.Decode(j.Bytecode.Object)
	if err != nil {
		k.t.Fatal(err)
	}
	return a, code
}

// deploy creates a contract from an artifact with constructor arguments.
func (k *chainKit) deploy(file, name string, args ...any) (common.Address, abi.ABI) {
	k.t.Helper()
	a, code := k.artifact(file, name)
	if len(args) > 0 {
		enc, err := a.Constructor.Inputs.Pack(args...)
		if err != nil {
			k.t.Fatalf("%s: %v", name, err)
		}
		code = append(append([]byte{}, code...), enc...)
	}
	ctx := context.Background()
	head, _ := k.c.BlockAt(ctx, ethx.Latest)
	nonce, _ := k.c.NonceAt(ctx, k.s.From, ethx.Pending)
	tx := types.NewTx(&types.DynamicFeeTx{ChainID: k.s.ChainID, Nonce: nonce, GasTipCap: ethx.GweiOf(1), GasFeeCap: new(big.Int).Add(new(big.Int).Mul(head.BaseFee, big.NewInt(2)), ethx.GweiOf(1)), Gas: 15_000_000, Data: code, Value: big.NewInt(0)})
	signed, err := types.SignTx(tx, types.LatestSignerForChainID(k.s.ChainID), k.s.Key)
	if err != nil {
		k.t.Fatal(err)
	}
	raw, _ := signed.MarshalBinary()
	h, err := k.c.SendRaw(ctx, raw)
	if err != nil {
		k.t.Fatalf("%s: %v", name, err)
	}
	for i := 0; i < 100; i++ {
		if rc, err := k.c.Receipt(ctx, h); err == nil {
			if rc.Status != 1 {
				k.t.Fatalf("%s: the deploy reverted", name)
			}
			return rc.ContractAddress, a
		}
		time.Sleep(50 * time.Millisecond)
	}
	k.t.Fatalf("%s: no receipt", name)
	return common.Address{}, a
}

// proxy deploys an implementation behind OpenZeppelin's TransparentUpgradeableProxy, initialized in its construction.
func (k *chainKit) proxy(impl common.Address, implABI abi.ABI, init string, args ...any) common.Address {
	k.t.Helper()
	data, err := implABI.Pack(init, args...)
	if err != nil {
		k.t.Fatalf("%s: %v", init, err)
	}
	p, _ := k.deploy("TransparentUpgradeableProxy.sol", "TransparentUpgradeableProxy", impl, common.HexToAddress("0x000000000000000000000000000000000000dEaD"), data)
	return p
}

// call sends a call as the deployer.
func (k *chainKit) call(to common.Address, a abi.ABI, method string, args ...any) {
	k.t.Helper()
	data, err := a.Pack(method, args...)
	if err != nil {
		k.t.Fatalf("%s: %v", method, err)
	}
	if _, err := k.s.Send(context.Background(), ethx.Call{To: to, Data: data, Label: method}); err != nil {
		k.t.Fatalf("%s: %v", method, err)
	}
}

func TestMachinesOverTheRealContracts(t *testing.T) {
	out := os.Getenv("ETHX_CONTRACTS_OUT")
	if out == "" {
		t.Skip("ETHX_CONTRACTS_OUT names no compiled contracts")
	}
	bin, err := exec.LookPath("anvil")
	if err != nil {
		t.Skip("no anvil")
	}
	l, _ := net.Listen("tcp", "127.0.0.1:0")
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	// the gno.land side: the synthetic chain, its header 10 the client's trusted one — its blocks ten minutes old, as a
	// live chain's would be beside anvil's clock (fold F4: initialize refuses a root more than an hour from its block)
	g := newFakeGno(testMailbox, 20)
	g.genesis = time.Now().Add(-10 * time.Minute)
	// one slot an epoch: anvil's finalized block is two behind its latest (a delivery ends once final)
	cmd := exec.Command(bin, "--port", fmt.Sprint(port), "--chain-id", "31337", "--hardfork", "osaka", "--enable-tx-gas-limit", "--slots-in-an-epoch", "1", "--silent")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	ctx := context.Background()
	var c *ethx.Client
	for i := 0; i < 100; i++ {
		if c, err = ethx.Dial(ctx, ethx.Provider{Name: "anvil", URL: fmt.Sprintf("http://127.0.0.1:%d", port)}); err == nil {
			if _, err = c.ChainID(ctx); err == nil {
				break
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	if err != nil {
		t.Fatal(err)
	}
	raw, err := rpc.DialContext(ctx, fmt.Sprintf("http://127.0.0.1:%d", port))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(raw.Close)
	finalize := func() { // two more blocks: everything mined so far is final
		for i := 0; i < 2; i++ {
			if err := raw.CallContext(ctx, nil, "evm_mine"); err != nil {
				t.Fatal(err)
			}
		}
	}
	key, _ := crypto.HexToECDSA(testAnvilKey)
	s, err := ethx.NewSender(key, big.NewInt(31337), []*ethx.Client{c}, ethx.SendConfig{Poll: 100 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	k := &chainKit{t: t, out: out, c: c, s: s}
	me := s.From

	g.renders[testGNOT+":json"] = routeJSON(50_000_000, "")
	g.renders[testBUBBLE+":json"] = routeJSON(1_000_000_000, "")
	for _, p := range []string{testMailbox, testGNOT, testBUBBLE, testGov} {
		g.renders[p+":decisions.json"] = "[]"
	}
	g.mu.Lock()
	trusted := g.header(10)
	set := g.setAt(10).set
	g.mu.Unlock()

	// the Ethereum side, wired as §5.8 wires it: the ProxyAdmin first (HlAdmin's from before HlAdmin exists, fold F4)
	proxyAdmin, proxyAdminABI := k.deploy("ProxyAdmin.sol", "ProxyAdmin")
	fee, _ := k.deploy("ProtocolFee.sol", "ProtocolFee", big.NewInt(1e15), big.NewInt(0), me, me)
	mailboxImpl, mailboxABI := k.deploy("Mailbox.sol", "Mailbox", uint32(31337))
	mailbox := k.proxy(mailboxImpl, mailboxABI, "initialize", me, fee, fee, fee)
	tree, _ := k.deploy("MerkleTreeHook.sol", "MerkleTreeHook", mailbox)
	verifier, _ := k.deploy("Ed25519Verifier.sol", "Ed25519Verifier")
	verifierCode, err := c.CodeAt(ctx, verifier, ethx.Latest)
	if err != nil {
		t.Fatal(err)
	}
	clientImpl, clientABI := k.deploy("GnoLightClient.sol", "GnoLightClient", crypto.Keccak256Hash([]byte(testMailbox)), verifier)
	client := k.proxy(clientImpl, clientABI, "initialize", "dev", hl.DomainGnoTest, ethx.HeaderLeaves(trusted.Header), ethx.SetArgs(set.Validators), me)
	tokImpl, tokABI := k.deploy("HypERC20.sol", "HypERC20", uint8(6), big.NewInt(1), big.NewInt(1), mailbox)
	tok0 := k.proxy(tokImpl, tokABI, "initialize", big.NewInt(0), "GNOT", "GNOT", common.Address{}, common.Address{}, me)
	tok1 := k.proxy(tokImpl, tokABI, "initialize", big.NewInt(0), "BUBBLE", "BUBBLE", common.Address{}, common.Address{}, me)
	gateImpl, gateABI := k.deploy("PausableGate.sol", "PausableGate")
	seats := [4]common.Address{common.HexToAddress("0x00000000000000000000000000000000000a0001"), common.HexToAddress("0x00000000000000000000000000000000000a0002"), common.HexToAddress("0x00000000000000000000000000000000000a0003"), common.HexToAddress("0x00000000000000000000000000000000000a0004")}
	watch := [2]common.Address{common.HexToAddress("0x00000000000000000000000000000000000a0005"), common.HexToAddress("0x00000000000000000000000000000000000a0006")}
	maxPerMessage, dayCap := [2]uint64{25_000_000_000, 25_000_000_000}, [2]uint64{100_000_000_000, 100_000_000_000}
	gate := k.proxy(gateImpl, gateABI, "initialize", me, mailbox, client, [2]common.Address{tok0, tok1}, seats, watch, maxPerMessage, dayCap)
	for i, tok := range []common.Address{tok0, tok1} {
		realm := []string{testGNOT, testBUBBLE}[i]
		k.call(tok, tokABI, "setInterchainSecurityModule", gate)
		k.call(tok, tokABI, "enrollRemoteRouter", hl.DomainGnoTest, gnoside.RealmKey(realm))
	}
	k.call(mailbox, mailboxABI, "setDefaultIsm", gate)
	k.call(mailbox, mailboxABI, "setDefaultHook", tree)
	k.call(mailbox, mailboxABI, "setRequiredHook", fee)
	// HlAdmin.Config as lane E-M2b's head has it (M2-NOTES §1.D)
	type adminConfig struct {
		Mailbox, Client, Gate, Token0, Token1, ProxyAdmin, MerkleTreeHook, ProtocolFee, Ed25519 common.Address
		Ed25519CodeHash                                                                         [32]byte
		LocalDomain, Origin                                                                     uint32
		Hlgov, Route0, Route1, MailboxPkgPathHash                                               [32]byte
		Seats                                                                                   [4]common.Address
		Watch                                                                                   [2]common.Address
		MaxPerMessage, DayCap                                                                   [2]uint64
		StartSequence                                                                           uint64
		Predecessor                                                                             common.Address
	}
	// a launch HlAdmin is built only holding the ProxyAdmin (ProxyAdminNotHeld otherwise): handed to the address HlAdmin
	// lands at, the deployer's next nonce after the hand-over
	next, err := c.NonceAt(ctx, me, ethx.Pending)
	if err != nil {
		t.Fatal(err)
	}
	k.call(proxyAdmin, proxyAdminABI, "transferOwnership", crypto.CreateAddress(me, next+1))
	admin, _ := k.deploy("HlAdmin.sol", "HlAdmin", adminConfig{Mailbox: mailbox, Client: client, Gate: gate, Token0: tok0, Token1: tok1, ProxyAdmin: proxyAdmin, MerkleTreeHook: tree, ProtocolFee: fee,
		Ed25519: verifier, Ed25519CodeHash: crypto.Keccak256Hash(verifierCode), LocalDomain: 31337,
		Origin: hl.DomainGnoTest, Hlgov: gnoside.RealmKey(testGov), Route0: gnoside.RealmKey(testGNOT), Route1: gnoside.RealmKey(testBUBBLE), MailboxPkgPathHash: crypto.Keccak256Hash([]byte(testMailbox)), Seats: seats, Watch: watch,
		MaxPerMessage: maxPerMessage, DayCap: dayCap})

	cfg := testConfig()
	cfg.Eth.Mailbox, cfg.Eth.Client, cfg.Eth.Gate, cfg.Eth.Admin = mailbox.Hex(), client.Hex(), gate.Hex(), admin.Hex()
	cfg.Routes[0].Token, cfg.Routes[1].Token = tok0.Hex(), tok1.Hex()
	cfg.Eth.Updater = s.Address().Hex() // the one key here sends the updates and the deliveries
	recipient := common.HexToAddress("0x00000000000000000000000000000000000b0b0b")

	// a send of 500,000 base units dispatched in block 12
	m := hl.Message{Sender: gnoside.RealmKey(testGNOT), Destination: 31337, Recipient: ethx.AddressTo32(tok0), Body: hl.TokenMessage(ethx.AddressTo32(recipient), 500_000, nil)}
	g.dispatch(12, m)
	g.head = 16

	// the updater buys header 13
	u := &Updater{Cfg: cfg, Sender: s, Eth: c, MinGap: time.Millisecond, Alarm: &Alarmer{Source: "updater"}, gno: g, blocks: g}
	if err := u.init(); err != nil {
		t.Fatal(err)
	}
	if err := u.tick(ctx); err != nil {
		t.Fatalf("updater: %v (plan %q)", err, u.status["plan"])
	}
	cs, err := ethx.ReadClient(ctx, c, client, ethx.Latest)
	if err != nil || cs.TrustedHeight != 13 {
		t.Fatalf("the client trusts %d (%v; plan %q, due %v)", cs.TrustedHeight, err, u.status["plan"], u.status["due"])
	}
	// the relayer delivers the send under it
	r := &Relayer{Cfg: cfg, Role: "primary", Alarm: &Alarmer{Source: "relayer"}, Sender: s, Eth: c, gno: g, light: g, blocks: g, runner: g}
	if err := r.init(); err != nil {
		t.Fatal(err)
	}
	r.tick(ctx)
	bal, err := c.CallAt(ctx, tok0, mustPack(t, tokABI, "balanceOf", recipient), ethx.Latest)
	if err != nil {
		t.Fatal(err)
	}
	if got := new(big.Int).SetBytes(bal); got.Int64() != 500_000 {
		t.Fatalf("the recipient holds %s (relayer: %v)", got, r.last)
	}
	if r.state.Out.Done["0"] != "" {
		t.Fatalf("ended before the finalized block shows it: %v", r.state.Out.Done)
	}
	finalize()
	r.tick(ctx)
	if r.state.Out.Done["0"] != "delivered" {
		t.Fatalf("%v", r.state.Out.Done)
	}
	ok, err := ethx.Delivered(ctx, c, mailbox, g.idOf(0), ethx.Latest)
	if err != nil || !ok {
		t.Fatalf("the mailbox's mark: %v %v", ok, err)
	}
	// a stranger's dispatch to the same token is never sent; the next tick walks past both
	g.dispatch(18, hl.Message{Sender: gnoside.RealmKey("gno.land/r/someone/else"), Destination: 31337, Recipient: ethx.AddressTo32(tok0), Body: hl.TokenMessage(ethx.AddressTo32(recipient), 9, nil)})
	g.head = 22
	r.tick(ctx)
	if r.state.Out.Next != 2 {
		t.Fatalf("the walk stands at %d (done %v)", r.state.Out.Next, r.state.Out.Done)
	}
	// decisions to HlAdmin: a kind 7 (set the protocol fee; 7 days), then a cancel of it, which executes at recording,
	// and a second copy of the cancel's number; the relayer delivers each number once, under the header after their
	// block, and the executor closes the cancelled decision
	salt := []byte{0, 0, 0, 0, 0, 0, 0, 9}
	g.dispatch(24, decisionMsg(admin, 7, 1, append(make([]byte, 7), 0x01, 0)...))
	g.dispatch(25, decisionMsg(admin, 1, 2, append(append(salt, 0, 1, 0), 0, 0, 0, 0, 0, 0, 0, 1)...), decisionMsg(admin, 1, 2, append(append(salt, 0, 1, 0), 0, 0, 0, 0, 0, 0, 0, 1)...))
	g.head = 30
	u.MinGap = time.Millisecond
	for i := 0; i < 5; i++ { // the batch re-emit, the update, the deliveries, the executes, the ends once final
		if err := u.tick(ctx); err != nil {
			t.Logf("updater: %v", err)
		}
		r.tick(ctx)
		finalize()
		time.Sleep(10 * time.Millisecond)
		r.now = func() time.Time { return time.Now().Add(time.Duration(i+1) * time.Minute) }
	}
	st, err := ethx.ReadAdmin(ctx, c, admin, ethx.Latest)
	if err != nil {
		t.Fatal(err)
	}
	ds, _ := ethx.ReadDecisions(ctx, c, admin, []uint64{1, 2}, ethx.Latest)
	if st.LastSeq != 2 || ds[0] != nil || st.Pending != 0 {
		t.Fatalf("HlAdmin: last %d, pending %d, decision 1 %+v (relayer %v, updater %q)", st.LastSeq, st.Pending, ds[0], r.last, u.status["plan"])
	}
	copies := 0
	for _, n := range []string{"2", "3", "4"} {
		if d := r.state.Out.Done[n]; d == "delivered" || d == "recorded" {
			copies++
		}
	}
	if r.state.Out.Next < 5 && copies != 3 {
		t.Fatalf("the decisions' copies: next %d, done %v", r.state.Out.Next, r.state.Out.Done)
	}
}

func mustPack(t *testing.T, a abi.ABI, method string, args ...any) []byte {
	t.Helper()
	b, err := a.Pack(method, args...)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
