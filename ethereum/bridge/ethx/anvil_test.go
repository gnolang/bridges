package ethx

import (
	"context"
	"errors"
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
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
)

// The sender against a real node: anvil (Foundry), when it is on the PATH (the devbox; skipped elsewhere). A probe
// contract (testdata/probe) is deployed; a write is sent and its receipt read; a require's text and a custom error
// are refused before sending, a call over the transaction gas cap is ErrGasCap; with mining off, an unmined transaction is replaced at its nonce and the replacement
// is what the node mines.

// anvilKey is anvil's first default account (a published test key, funded on every anvil).
const anvilKey = "ac0974bec39a17e36ba4a6b4d238ff944bacb478cbed5efcae784d7bf4f2ff80"

func startAnvil(t *testing.T) *Client {
	t.Helper()
	bin, err := exec.LookPath("anvil")
	if err != nil {
		if _, e := os.Stat("/root/.foundry/bin/anvil"); e == nil {
			bin = "/root/.foundry/bin/anvil"
		} else {
			t.Skip("no anvil on this machine")
		}
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	// mainnet's fork (Osaka, "Fusaka") with EIP-7825's transaction gas cap enforced, which anvil leaves off unless
	// asked: never Sepolia's newer fork, whose gas schedule differs (no gas figure here is one network's)
	cmd := exec.Command(bin, "--port", fmt.Sprint(port), "--chain-id", "31337", "--hardfork", "osaka", "--enable-tx-gas-limit", "--silent")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	ctx := context.Background()
	for i := 0; i < 100; i++ {
		c, err := Dial(ctx, Provider{Name: "anvil", URL: fmt.Sprintf("http://127.0.0.1:%d", port)})
		if err == nil {
			if _, err = c.ChainID(ctx); err == nil {
				return c
			}
			c.Close()
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("anvil did not answer")
	return nil
}

func TestSenderOnAnvil(t *testing.T) {
	c := startAnvil(t)
	ctx := context.Background()
	key, err := crypto.HexToECDSA(anvilKey)
	if err != nil {
		t.Fatal(err)
	}
	id := big.NewInt(31337)
	// deploy the probe with a raw creation transaction
	code, err := os.ReadFile(filepath.Join("testdata", "probe", "probe.bin"))
	if err != nil {
		t.Fatal(err)
	}
	bin, err := hexutil.Decode(strings.TrimSpace(string(code)))
	if err != nil {
		t.Fatal(err)
	}
	head, err := c.BlockAt(ctx, Latest)
	if err != nil {
		t.Fatal(err)
	}
	create := types.NewTx(&types.DynamicFeeTx{ChainID: id, Nonce: 0, GasTipCap: GweiOf(1), GasFeeCap: new(big.Int).Add(new(big.Int).Mul(head.BaseFee, big.NewInt(2)), GweiOf(1)), Gas: 1_000_000, Data: bin, Value: big.NewInt(0)})
	signed, err := types.SignTx(create, types.LatestSignerForChainID(id), key)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := signed.MarshalBinary()
	h, err := c.SendRaw(ctx, raw)
	if err != nil {
		t.Fatal(err)
	}
	var rc *types.Receipt
	for i := 0; i < 50; i++ {
		if rc, err = c.Receipt(ctx, h); !IsNotFound(err) {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if err != nil || rc.Status != 1 {
		t.Fatalf("deploy: %+v %v", rc, err)
	}
	probe := rc.ContractAddress
	pabi, err := os.ReadFile(filepath.Join("testdata", "probe", "probe.abi.json"))
	if err != nil {
		t.Fatal(err)
	}
	a, err := abi.JSON(strings.NewReader(string(pabi)))
	if err != nil {
		t.Fatal(err)
	}
	extraContracts = append(extraContracts, &Contract{Name: "SenderProbe", ABI: a})
	defer func() { extraContracts = nil }()
	call := func(m string) []byte {
		b, err := a.Pack(m)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	n := func() uint64 {
		out, err := c.CallAt(ctx, probe, call("n"), Latest)
		if err != nil {
			t.Fatal(err)
		}
		return new(big.Int).SetBytes(out).Uint64()
	}

	s, err := NewSender(key, id, []*Client{c}, SendConfig{Poll: 200 * time.Millisecond, ReplaceAfter: 2 * time.Second, Wait: time.Minute, State: filepath.Join(t.TempDir(), "sender.json")})
	if err != nil {
		t.Fatal(err)
	}
	rc, err = s.Send(ctx, Call{To: probe, Data: call("ok"), Label: "ok"})
	if err != nil || rc.Status != 1 || n() != 1 {
		t.Fatalf("ok: %+v %v", rc, err)
	}
	_, err = s.Send(ctx, Call{To: probe, Data: call("fail"), Label: "fail"})
	if r, ok := AsRevert(err); !ok || r.Name != "Error" || r.Reason != "nope" || r.Mined {
		t.Fatalf("fail: %v", err)
	}
	_, err = s.Send(ctx, Call{To: probe, Data: call("custom"), Label: "custom"})
	if r, ok := AsRevert(err); !ok || r.Name != "Bad" || r.Reason != "7" || r.Contract != "SenderProbe" {
		t.Fatalf("custom: %v (%+v)", err, r)
	}
	// a call no transaction's gas can carry: the node's estimate finds no limit under EIP-7825's cap — ErrGasCap,
	// which the caller splits, nothing sent
	burn, err := a.Pack("burn", big.NewInt(1_000_000))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Send(ctx, Call{To: probe, Data: burn, Label: "burn"}); !errors.Is(err, ErrGasCap) {
		t.Fatalf("burn: %v", err)
	}
	// mining off: the first version waits, a replacement goes at the same nonce with more tip, and is mined
	first, err := s.tip(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var ok bool
	if err := c.rpc.CallContext(ctx, &ok, "evm_setAutomine", false); err != nil {
		t.Fatal(err)
	}
	done := make(chan *types.Receipt, 1)
	errc := make(chan error, 1)
	go func() {
		rc, err := s.Send(ctx, Call{To: probe, Data: call("ok"), Label: "slow ok"})
		errc <- err
		done <- rc
	}()
	time.Sleep(3500 * time.Millisecond) // past ReplaceAfter: the replacement is broadcast
	var mined any
	if err := c.rpc.CallContext(ctx, &mined, "evm_mine"); err != nil {
		t.Fatal(err)
	}
	if err := <-errc; err != nil {
		t.Fatal(err)
	}
	rc = <-done
	tx, err := c.Transaction(ctx, rc.TxHash)
	if err != nil {
		t.Fatal(err)
	}
	if tx.Nonce != 2 {
		t.Fatalf("mined at nonce %d, want 2 (the deploy took 0, the first ok 1)", tx.Nonce)
	}
	var full struct {
		Tip hexutil.Big `json:"maxPriorityFeePerGas"`
	}
	if err := c.rpc.CallContext(ctx, &full, "eth_getTransactionByHash", rc.TxHash); err != nil {
		t.Fatal(err)
	}
	if (*big.Int)(&full.Tip).Cmp(mulDiv(first, 1125, 1000)) < 0 {
		t.Fatalf("the mined version's tip %s is not 12.5%% above the first version's %s: no replacement", (*big.Int)(&full.Tip), first)
	}
	if n() != 2 {
		t.Fatalf("n %d", n())
	}
}
