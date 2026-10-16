package ethx

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

	"github.com/gnolang/bridges/solana/bridge/gnolight"
	"github.com/gnolang/bridges/solana/bridge/hl"
)

// The calldata against the real contracts: lane E-M2b's GnoLightClient calling lane E-M2a's Ed25519Verifier,
// compiled in a Foundry project whose out/ directory ETHX_CONTRACTS_OUT names (the devbox; skipped elsewhere),
// deployed on anvil (mainnet's rules, EIP-170's code size limit among them) behind OpenZeppelin's
// TransparentUpgradeableProxy. gnoland-1's own headers go in through UpdateCalls and the
// sender — an update, the announcing one with its next set, the first header of the new set (the epoch rises), a
// staged update in two calls — then a header proof, and the verify metadata of a real results leaf: the client finds
// the proof right and refuses the leaf only for carrying no Dispatch of its mailbox (a wrong aunt is ProofMismatch).

func artifact(t *testing.T, out, file, name string) (abi.ABI, []byte) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(out, file, name+".json"))
	if err != nil {
		t.Fatal(err)
	}
	var j struct {
		ABI      json.RawMessage `json:"abi"`
		Bytecode struct {
			Object string `json:"object"`
		} `json:"bytecode"`
	}
	if err := json.Unmarshal(raw, &j); err != nil {
		t.Fatal(err)
	}
	a, err := abi.JSON(strings.NewReader(string(j.ABI)))
	if err != nil {
		t.Fatal(err)
	}
	code, err := hexutil.Decode(j.Bytecode.Object)
	if err != nil {
		t.Fatal(err)
	}
	return a, code
}

// deploy sends a creation transaction and answers the contract's address.
func deploy(t *testing.T, c *Client, s *Sender, code []byte) common.Address {
	t.Helper()
	ctx := context.Background()
	head, err := c.BlockAt(ctx, Latest)
	if err != nil {
		t.Fatal(err)
	}
	nonce, err := c.NonceAt(ctx, s.From, Pending)
	if err != nil {
		t.Fatal(err)
	}
	tx := types.NewTx(&types.DynamicFeeTx{ChainID: s.ChainID, Nonce: nonce, GasTipCap: GweiOf(1), GasFeeCap: new(big.Int).Add(new(big.Int).Mul(head.BaseFee, big.NewInt(2)), GweiOf(1)), Gas: 15_000_000, Data: code, Value: big.NewInt(0)})
	signed, err := types.SignTx(tx, types.LatestSignerForChainID(s.ChainID), s.Key)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := signed.MarshalBinary()
	h, err := c.SendRaw(ctx, raw)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 100; i++ {
		rc, err := c.Receipt(ctx, h)
		if err == nil {
			if rc.Status != 1 {
				t.Fatalf("deploy reverted (%d gas)", rc.GasUsed)
			}
			return rc.ContractAddress
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("no receipt for the deploy")
	return common.Address{}
}

func TestCalldataOnTheRealContracts(t *testing.T) {
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
	// mainnet's fork with EIP-7825's cap and EIP-170's code size limit; the chain's clock starts a minute after the
	// trusted header's time (fold F4: initialize refuses a root more than an hour from block.timestamp)
	trusted := loadFixture(t, 487881)
	cmd := exec.Command(bin, "--port", fmt.Sprint(port), "--chain-id", "31337", "--hardfork", "osaka", "--enable-tx-gas-limit", "--silent", "--timestamp", fmt.Sprint(trusted.block.Header.Time.Secs+60))
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	ctx := context.Background()
	var c *Client
	for i := 0; i < 100; i++ {
		if c, err = Dial(ctx, Provider{Name: "anvil", URL: fmt.Sprintf("http://127.0.0.1:%d", port)}); err == nil {
			if _, err = c.ChainID(ctx); err == nil {
				break
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	if err != nil {
		t.Fatal(err)
	}
	key, _ := crypto.HexToECDSA(anvilKey)
	s, err := NewSender(key, big.NewInt(31337), []*Client{c}, SendConfig{Poll: 100 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	clientABI, clientCode := artifact(t, out, "GnoLightClient.sol", "GnoLightClient")
	proxyABI, proxyCode := artifact(t, out, "TransparentUpgradeableProxy.sol", "TransparentUpgradeableProxy")
	_, verifierCode := artifact(t, out, "Ed25519Verifier.sol", "Ed25519Verifier")
	verifier := deploy(t, c, s, verifierCode)
	mailbox := "gno.land/r/test/eth/hlmailbox"
	ctorArgs, err := clientABI.Constructor.Inputs.Pack(crypto.Keccak256Hash([]byte(mailbox)), verifier)
	if err != nil {
		t.Fatal(err)
	}
	impl := deploy(t, c, s, append(append([]byte{}, clientCode...), ctorArgs...))

	init, err := clientABI.Pack("initialize", "gnoland-1", hl.DomainGnoMainnet, HeaderLeaves(trusted.block.Header), SetArgs(trusted.set.Validators), s.From)
	if err != nil {
		t.Fatal(err)
	}
	proxyArgs, err := proxyABI.Constructor.Inputs.Pack(impl, common.HexToAddress("0x000000000000000000000000000000000000dEaD"), init)
	if err != nil {
		t.Fatal(err)
	}
	client := deploy(t, c, s, append(append([]byte{}, proxyCode...), proxyArgs...))

	state := func() *ClientState {
		cs, err := ReadClient(ctx, c, client, Latest)
		if err != nil {
			t.Fatal(err)
		}
		return cs
	}
	update := func(h, next int64, maxSigs int) {
		t.Helper()
		f := loadFixture(t, h)
		sh := &SignedHeader{ChainID: "gnoland-1", Header: f.block.Header, Commit: f.commit, Set: f.set}
		if next != 0 {
			sh.Next = loadFixture(t, next).set
		}
		calls, err := UpdateCalls(sh, maxSigs)
		if err != nil {
			t.Fatal(err)
		}
		// the stage key as Go computes it is the client's own (its pure stageKey, and the tally a staging call leaves)
		cs0 := state()
		key, err := StageKey(s.From, sh, cs0.Epoch, cs0.Expect())
		if err != nil {
			t.Fatal(err)
		}
		cm := commitArg(sh.Commit, nil)
		kdata, _ := LightClient.Pack("stageKey", s.From, sh.Header.Hash(), cm.PartsTotal, cm.PartsHash, cm.Round, cs0.Epoch, cs0.Expect())
		if out, err := c.CallAt(ctx, client, kdata, Latest); err != nil || len(out) != 32 || [32]byte(out) != key {
			t.Fatalf("stageKey: the client says %x (%v), Go %x", out, err, key)
		}
		for i, call := range calls {
			rc, err := s.Send(ctx, Call{To: client, Data: call.Data, Label: fmt.Sprintf("update %d (%d/%d)", h, i+1, len(calls))})
			if err != nil {
				t.Fatalf("update %d, call %d: %v", h, i+1, err)
			}
			t.Logf("update %d call %d/%d: %d signatures, %d gas", h, i+1, len(calls), len(call.Slots), rc.GasUsed)
			if !call.Final {
				if p, err := StagePower(ctx, c, client, key, Latest); err != nil || p == 0 {
					t.Fatalf("no tally under Go's stage key after a staging call: %d %v", p, err)
				}
			}
		}
		if cs := state(); cs.TrustedHeight != h || cs.TrustedHash != f.block.Hash {
			t.Fatalf("after the update to %d the client trusts %d", h, cs.TrustedHeight)
		}
	}
	if cs := state(); cs.TrustedHeight != 487881 || cs.ChainID != "gnoland-1" || cs.Epoch != 0 {
		t.Fatalf("%+v", cs)
	}
	// 487882 announces the fifth validator: its next set given in full
	update(487882, 487883, 0)
	if cs := state(); !cs.Announced() {
		t.Fatal("the announcing header announced nothing")
	}
	// the verify metadata of block 487881's results under header 487882 (accepted at the current epoch): the proof
	// is right, the leaf carries no Dispatch of the mailbox
	f := loadFixture(t, 487881)
	leaves, err := gnolight.ResultLeaves(f.results)
	if err != nil {
		t.Fatal(err)
	}
	proof, err := gnolight.Proof(leaves, 0)
	if err != nil {
		t.Fatal(err)
	}
	h882 := loadFixture(t, 487882).block.Hash
	if e, err := ReadEntry(ctx, c, client, h882, Latest); err != nil || !e.Proves(0) {
		t.Fatalf("entry %+v %v", e, err)
	}
	msg := hl.Message{Version: 3, Nonce: 0, Origin: hl.DomainGnoMainnet, Destination: 31337, Body: []byte{1}}
	meta, err := VerifyMetadata(h882, proof, leaves[0])
	if err != nil {
		t.Fatal(err)
	}
	vdata, _ := LightClient.Pack("verify", meta, msg.Bytes())
	_, err = c.CallAt(ctx, client, vdata, Latest)
	if r, ok := AsRevert(err); !ok || (r.Name != "NoDispatch" && r.Name != "DispatchMismatch") {
		t.Fatalf("verify of a real leaf: %v (want the proof taken and the leaf refused for its events)", err)
	}
	if len(proof.Aunts) > 0 {
		bad := *proof
		bad.Aunts = append([][32]byte{}, proof.Aunts...)
		bad.Aunts[0][0] ^= 1
		meta, _ = VerifyMetadata(h882, &bad, leaves[0])
		vdata, _ = LightClient.Pack("verify", meta, msg.Bytes())
		if _, err := c.CallAt(ctx, client, vdata, Latest); !RevertIs(err, "ProofMismatch") {
			t.Fatalf("a wrong aunt: %v", err)
		}
	}
	// the first header of the new set: the epoch rises
	update(487883, 0, 0)
	if cs := state(); cs.Epoch != 1 || cs.Announced() {
		t.Fatalf("%+v", cs)
	}
	update(487884, 0, 0)
	// a header proof below the trusted height, signed by the current set
	f883 := loadFixture(t, 487883)
	calls, err := ProveHeaderCalls(&SignedHeader{ChainID: "gnoland-1", Header: f883.block.Header, Commit: f883.commit, Set: f883.set}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Send(ctx, Call{To: client, Data: calls[0].Data, Label: "proveHeader 487883"}); err != nil {
		t.Fatal(err)
	}
	// a staged update: 495705 under the same set, three signatures in calls of two
	update(495705, 0, 2)
	// a header the client cannot take is refused before anything is sent
	f2 := loadFixture(t, 487884)
	again, _ := UpdateCalls(&SignedHeader{ChainID: "gnoland-1", Header: f2.block.Header, Commit: f2.commit, Set: f2.set}, 0)
	if _, err := s.Send(ctx, Call{To: client, Data: again[0].Data, Label: "stale"}); !RevertIs(err, "HeightNotAboveTrusted") {
		t.Fatalf("a stale update: %v", err)
	}
}
