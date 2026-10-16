package main

import (
	"context"
	"crypto/ecdsa"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
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
	"github.com/gnolang/bridges/ethereum/bridge/agent"
	"github.com/gnolang/bridges/ethereum/bridge/ethx"
)

// i4Chain is anvil with the gate (PausableGate behind its proxy) and HlAdmin of the builds ETHX_CONTRACTS_OUT names,
// each holding the seats given; every other address they are bound to is a placeholder (a flag reads and writes
// nothing else).
type i4Chain struct {
	t           *testing.T
	out, url    string
	c           *ethx.Client
	raw         *rpc.Client
	snd         *ethx.Sender
	gate, admin common.Address
}

func i4Anvil(t *testing.T) *i4Chain {
	t.Helper()
	out := os.Getenv("ETHX_CONTRACTS_OUT")
	if out == "" {
		t.Skip("ETHX_CONTRACTS_OUT names no compiled contracts")
	}
	bin, err := exec.LookPath("anvil")
	if err != nil {
		if bin = "/root/.foundry/bin/anvil"; func() bool { _, err := os.Stat(bin); return err != nil }() {
			t.Skip("no anvil")
		}
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	// one slot an epoch: anvil's finalized block is two behind its latest
	cmd := exec.Command(bin, "--port", fmt.Sprint(port), "--chain-id", "31337", "--hardfork", "osaka", "--enable-tx-gas-limit", "--slots-in-an-epoch", "1", "--silent")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	ch := &i4Chain{t: t, out: out, url: fmt.Sprintf("http://127.0.0.1:%d", port)}
	ctx := context.Background()
	for i := 0; ; i++ {
		if ch.c, err = ethx.Dial(ctx, ethx.Provider{Name: "anvil", URL: ch.url}); err == nil {
			if _, err = ch.c.ChainID(ctx); err == nil {
				break
			}
		}
		if i > 100 {
			t.Fatalf("anvil did not start: %v", err)
		}
		time.Sleep(100 * time.Millisecond)
	}
	if ch.raw, err = rpc.DialContext(ctx, ch.url); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(ch.raw.Close)
	deployer := ch.fund(nil)
	if ch.snd, err = ethx.NewSender(deployer, big.NewInt(31337), []*ethx.Client{ch.c}, ethx.SendConfig{Poll: 100 * time.Millisecond}); err != nil {
		t.Fatal(err)
	}
	return ch
}

// fund gives key (a fresh one when nil) 100 ETH on anvil and returns it.
func (ch *i4Chain) fund(key *ecdsa.PrivateKey) *ecdsa.PrivateKey {
	ch.t.Helper()
	if key == nil {
		var err error
		if key, err = crypto.GenerateKey(); err != nil {
			ch.t.Fatal(err)
		}
	}
	if err := ch.raw.CallContext(context.Background(), nil, "anvil_setBalance", crypto.PubkeyToAddress(key.PublicKey), "0x56BC75E2D63100000"); err != nil {
		ch.t.Fatal(err)
	}
	return key
}

func (ch *i4Chain) artifact(file, name string) (abi.ABI, []byte) {
	ch.t.Helper()
	b, err := os.ReadFile(filepath.Join(ch.out, file, name+".json"))
	if err != nil {
		ch.t.Fatal(err)
	}
	var j struct {
		ABI      json.RawMessage `json:"abi"`
		Bytecode struct {
			Object string `json:"object"`
		} `json:"bytecode"`
	}
	if err := json.Unmarshal(b, &j); err != nil {
		ch.t.Fatal(err)
	}
	a, err := abi.JSON(strings.NewReader(string(j.ABI)))
	if err != nil {
		ch.t.Fatal(err)
	}
	code, err := hexutil.Decode(j.Bytecode.Object)
	if err != nil {
		ch.t.Fatal(err)
	}
	return a, code
}

func (ch *i4Chain) deploy(file, name string, args ...any) (common.Address, abi.ABI) {
	ch.t.Helper()
	a, code := ch.artifact(file, name)
	if len(args) > 0 {
		enc, err := a.Constructor.Inputs.Pack(args...)
		if err != nil {
			ch.t.Fatalf("%s: %v", name, err)
		}
		code = append(append([]byte{}, code...), enc...)
	}
	ctx := context.Background()
	head, err := ch.c.BlockAt(ctx, ethx.Latest)
	if err != nil {
		ch.t.Fatal(err)
	}
	nonce, err := ch.c.NonceAt(ctx, ch.snd.From, ethx.Pending)
	if err != nil {
		ch.t.Fatal(err)
	}
	tx := types.NewTx(&types.DynamicFeeTx{ChainID: big.NewInt(31337), Nonce: nonce, GasTipCap: ethx.GweiOf(1), GasFeeCap: new(big.Int).Add(new(big.Int).Mul(head.BaseFee, big.NewInt(2)), ethx.GweiOf(1)), Gas: 15_000_000, Data: code, Value: big.NewInt(0)})
	signed, err := types.SignTx(tx, types.LatestSignerForChainID(big.NewInt(31337)), ch.snd.Key)
	if err != nil {
		ch.t.Fatal(err)
	}
	rawTx, _ := signed.MarshalBinary()
	h, err := ch.c.SendRaw(ctx, rawTx)
	if err != nil {
		ch.t.Fatalf("%s: %v", name, err)
	}
	for i := 0; i < 100; i++ {
		if rc, err := ch.c.Receipt(ctx, h); err == nil {
			if rc.Status != 1 {
				ch.t.Fatalf("%s: the deploy reverted", name)
			}
			return rc.ContractAddress, a
		}
		time.Sleep(50 * time.Millisecond)
	}
	ch.t.Fatalf("%s: no receipt", name)
	return common.Address{}, a
}

// contracts deploys the gate and HlAdmin with these seats and watch slots (HlAdmin.Config as the agent's real-chain
// test builds it; the launch HlAdmin holds the ProxyAdmin, handed to the address it lands at).
func (ch *i4Chain) contracts(seats [4]common.Address, watch [2]common.Address) {
	ch.t.Helper()
	ctx := context.Background()
	me := ch.snd.From
	ph := func(n int64) common.Address { return common.BigToAddress(big.NewInt(0xa0000 + n)) } // placeholders
	limits := [2]uint64{25_000_000_000, 25_000_000_000}
	gateImpl, gateABI := ch.deploy("PausableGate.sol", "PausableGate")
	init, err := gateABI.Pack("initialize", me, ph(1), ph(2), [2]common.Address{ph(3), ph(4)}, seats, watch, limits, [2]uint64{100_000_000_000, 100_000_000_000})
	if err != nil {
		ch.t.Fatal(err)
	}
	ch.gate, _ = ch.deploy("TransparentUpgradeableProxy.sol", "TransparentUpgradeableProxy", gateImpl, common.HexToAddress("0x000000000000000000000000000000000000dEaD"), init)
	proxyAdmin, proxyAdminABI := ch.deploy("ProxyAdmin.sol", "ProxyAdmin")
	next, err := ch.c.NonceAt(ctx, me, ethx.Pending)
	if err != nil {
		ch.t.Fatal(err)
	}
	data, err := proxyAdminABI.Pack("transferOwnership", crypto.CreateAddress(me, next+1))
	if err != nil {
		ch.t.Fatal(err)
	}
	if _, err := ch.snd.Send(ctx, ethx.Call{To: proxyAdmin, Data: data, Label: "transferOwnership"}); err != nil {
		ch.t.Fatal(err)
	}
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
	nonzero := crypto.Keccak256Hash([]byte("placeholder"))
	ch.admin, _ = ch.deploy("HlAdmin.sol", "HlAdmin", adminConfig{Mailbox: ph(1), Client: ph(2), Gate: ch.gate, Token0: ph(3), Token1: ph(4), ProxyAdmin: proxyAdmin,
		MerkleTreeHook: ph(5), ProtocolFee: ph(6), Ed25519: ph(7), Ed25519CodeHash: nonzero, LocalDomain: 31337, Origin: hl.DomainGnoTest,
		Hlgov: nonzero, Route0: nonzero, Route1: nonzero, MailboxPkgPathHash: nonzero, Seats: seats, Watch: watch, MaxPerMessage: limits, DayCap: [2]uint64{100_000_000_000, 100_000_000_000}})
}

// mining mines three blocks at once — everything deployed is final — then a block every 200 ms until the test ends: a
// flag's block becomes final two blocks on.
func (ch *i4Chain) mining() {
	for i := 0; i < 3; i++ {
		if err := ch.raw.CallContext(context.Background(), nil, "evm_mine"); err != nil {
			ch.t.Fatal(err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-ctx.Done():
				return
			case <-time.After(200 * time.Millisecond):
				_ = ch.raw.CallContext(ctx, nil, "evm_mine")
			}
		}
	}()
	ch.t.Cleanup(func() { cancel(); <-done })
}

// flagged says whether slot (0-based) holds its flag on the gate and on HlAdmin at anvil's latest block.
func (ch *i4Chain) flagged(slot int) (gate, admin bool) {
	ch.t.Helper()
	g, err := readFlags(context.Background(), ch.c, ch.gate, ethx.Gate, ethx.Latest)
	if err != nil {
		ch.t.Fatal(err)
	}
	a, err := readFlags(context.Background(), ch.c, ch.admin, ethx.Admin, ethx.Latest)
	if err != nil {
		ch.t.Fatal(err)
	}
	return g[slot].Set(), a[slot].Set()
}

// i4GnoNode is the holder's gno node as stroke reads it: hlmailbox's json, its seats the addresses given.
func i4GnoNode(t *testing.T, seats []string) string {
	t.Helper()
	mb, _ := json.Marshal(map[string]any{"seats": seats})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		var body struct {
			Method string            `json:"method"`
			Params map[string]string `json:"params"`
		}
		_ = json.NewDecoder(req.Body).Decode(&body)
		data, _ := base64.StdEncoding.DecodeString(body.Params["data"])
		if body.Method != "abci_query" || body.Params["path"] != "vm/qrender" || !strings.HasSuffix(string(data), "hlmailbox:json") {
			http.Error(w, "this node answers hlmailbox's json alone", 500)
			return
		}
		fmt.Fprintf(w, `{"jsonrpc":"2.0","id":1,"result":{"response":{"ResponseBase":{"Error":null,"Data":%q}}}}`, base64.StdEncoding.EncodeToString(mb))
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// i4StrokeGnokey is gnokey for the stroke's gno.land leg: `list` names the keys given (name → address); a flag call
// made with the key "short" is refused as a sized simulation is when the key cannot pay its fee, any other is sent
// (OK!, a hash); each call is logged as "<func> <-simulate> <key>".
func i4StrokeGnokey(t *testing.T, dir string, keys map[string]string) (bin, log string) {
	t.Helper()
	bin, log = filepath.Join(dir, "gnokey"), filepath.Join(dir, "calls.log")
	var list strings.Builder
	i := 0
	for name, addr := range keys {
		fmt.Fprintf(&list, "%d. %s (local) - addr: %s pub: gpub1test, path: <nil>\n", i, name, addr)
		i++
	}
	script := `#!/bin/sh
if [ "$1" = list ]; then cat <<'KEYS'
` + list.String() + `KEYS
exit 0; fi
fn=""; sim=""; prev=""; for a in "$@"; do [ "$prev" = -func ] && fn=$a; [ "$prev" = -simulate ] && sim=$a; prev=$a; last=$a; done
echo "$fn $sim $last" >> ` + log + `
if [ "$last" = short ]; then echo '--= Error =--'; echo 'Data: insufficient funds to pay for fees; 1000ugnot < 720000ugnot'; exit 1; fi
echo 'OK!'; echo 'GAS USED:   2000000'; echo 'HEIGHT:     9'; echo 'TX HASH:    TXGNO'
`
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin, log
}

// TestI4StrokeFlagsEthereumWhateverGnoAnswers (the plan conformance audit's E6-PC4, round 6): a guardian's `hlwatch
// flag` set hlmailbox's flag first and returned on any error before the gate's and HlAdmin's — a gno key short of a
// sized flag's simulation fee, the holder's node down or gno.land halted left Ethereum unflagged by the very tool the
// guardians are told to use, and an Ethereum key holding no slot left gno.land unflagged. On anvil with the gate and
// HlAdmin of the real builds, three guardians stroke once each: the gno key short of the fee, then the holder's node
// down — each leaves its gno.land leg failed and the command's error naming it, and both Ethereum flags set and final;
// then a key the Ethereum objects do not seat — its gno.land flag is sent all the same, both Ethereum legs failed.
func TestI4StrokeFlagsEthereumWhateverGnoAnswers(t *testing.T) {
	ch := i4Anvil(t)
	short, down, unseated := ch.fund(nil), ch.fund(nil), ch.fund(nil)
	addr := func(k *ecdsa.PrivateKey) common.Address { return crypto.PubkeyToAddress(k.PublicKey) }
	seats := [4]common.Address{addr(short), addr(down), common.BigToAddress(big.NewInt(0xb0003)), common.BigToAddress(big.NewInt(0xb0004))}
	ch.contracts(seats, [2]common.Address{common.BigToAddress(big.NewInt(0xb0005)), common.BigToAddress(big.NewInt(0xb0006))})
	ch.mining()

	dir := t.TempDir()
	keyFile := func(name string, k *ecdsa.PrivateKey) string {
		p := filepath.Join(dir, name+".eth")
		if err := os.WriteFile(p, []byte(hex.EncodeToString(crypto.FromECDSA(k))+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	gnoAddrs := map[string]string{"short": "g1shortshortshortshortshortshortshortshort", "down": "g1downdowndowndowndowndowndowndowndowndown", "unseated": "g1unseatedunseatedunseatedunseatedunseated"}
	gnokey, calls := i4StrokeGnokey(t, dir, gnoAddrs)
	node := i4GnoNode(t, []string{gnoAddrs["short"], gnoAddrs["down"], gnoAddrs["unseated"], "g1seat4", "g1watch1", "g1watch2"})
	const ns = "gno.land/r/g1leu8d2vsplhehcfkjg50mwgdpxdkt8tztu95wr/eth/"
	cfg := &agent.Config{AllowHTTP: true}
	cfg.Gno.RPC, cfg.Gno.ChainID, cfg.Gno.Mailbox, cfg.Gno.Gov, cfg.Gno.Gnokey, cfg.Gno.GasFee = node, "dev", ns+"hlmailbox", ns+"hlgov", gnokey, "1000ugnot"
	cfg.Eth.Providers = []ethx.Provider{{Name: "anvil-a", URL: ch.url}, {Name: "anvil-b", URL: ch.url}}
	cfg.Eth.ChainID, cfg.Eth.Domain = 31337, 31337
	cfg.Eth.Mailbox, cfg.Eth.Client, cfg.Eth.Gate, cfg.Eth.Admin = common.BigToAddress(big.NewInt(0xa0001)).Hex(), common.BigToAddress(big.NewInt(0xa0002)).Hex(), ch.gate.Hex(), ch.admin.Hex()
	cfg.Routes = []agent.Route{{Symbol: "GNOT", Gno: ns + "hlgnot", Token: common.BigToAddress(big.NewInt(0xa0003)).Hex()}, {Symbol: "BUBBLE", Gno: ns + "hlbubble", Token: common.BigToAddress(big.NewInt(0xa0004)).Hex()}}
	ctx := context.Background()
	head, err := ch.c.BlockAt(ctx, ethx.Latest)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Eth.DeployBlock = head.Number // the scans' start (no finishSetup here: a flag reads none of them)
	w, err := agent.NewWatch(ctx, cfg, node, "", 0, nil)
	if err != nil {
		t.Fatal(err)
	}

	// the gno key short of the simulation's fee: gno.land's leg refused, both Ethereum flags set and final
	t.Run("a gno key short of the fee", func(t *testing.T) {
		err := stroke(ctx, w, cfg, node, true, dir, "short", keyFile("short", short), "GNOT", "payouts", "")
		if g, a := ch.flagged(0); !g || !a {
			t.Fatalf("the gate flagged %v, HlAdmin %v (stroke: %v)", g, a, err)
		}
		if err == nil || !strings.Contains(err.Error(), "insufficient funds") {
			t.Fatalf("the command's error does not name gno.land's leg: %v", err)
		}
	})

	// the holder's node down: the hlmailbox read fails, both Ethereum flags set and final
	t.Run("the holder's node down", func(t *testing.T) {
		l, _ := net.Listen("tcp", "127.0.0.1:0")
		deadNode := "http://" + l.Addr().String()
		l.Close()
		err := stroke(ctx, w, cfg, deadNode, true, dir, "down", keyFile("down", down), "GNOT", "payouts", "")
		if g, a := ch.flagged(1); !g || !a {
			t.Fatalf("the gate flagged %v, HlAdmin %v (stroke: %v)", g, a, err)
		}
		if err == nil {
			t.Fatal("the command reported no failure")
		}
	})

	// a key the gate and HlAdmin do not seat: gno.land's flag is sent all the same, both Ethereum legs fail
	t.Run("a key no Ethereum object seats", func(t *testing.T) {
		_ = os.Remove(calls)
		err := stroke(ctx, w, cfg, node, true, dir, "unseated", keyFile("unseated", unseated), "GNOT", "payouts", "")
		if b, _ := os.ReadFile(calls); string(b) != "SetFlag only unseated\nSetFlag test unseated\n" {
			t.Fatalf("gno.land's flag was not sent (gnokey's calls %q; stroke: %v)", b, err)
		}
		if err == nil || !strings.Contains(err.Error(), "the gate") || !strings.Contains(err.Error(), "HlAdmin") {
			t.Fatalf("the command's error does not name both Ethereum legs: %v", err)
		}
		for slot := 2; slot < 6; slot++ {
			if g, a := ch.flagged(slot); g || a {
				t.Fatalf("slot %d flagged", slot+1)
			}
		}
	})
}
