package ethx

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
)

// Every provider URL is cut to its scheme and host wherever it appears, keys in paths, queries and userinfo alike.
func TestRedact(t *testing.T) {
	cases := map[string]string{
		`Post "https://eth-mainnet.g.alchemy.com/v2/KEY": dial tcp`: `Post "https://eth-mainnet.g.alchemy.com/…": dial tcp`,
		"wss://mainnet.infura.io/ws/v3/KEY failed.":                 "wss://mainnet.infura.io/… failed.",
		"https://user:pass@rpc.example/?apikey=KEY, then":           "https://rpc.example/…, then",
		"http://127.0.0.1:8545":                                     "http://127.0.0.1:8545",
		"no url here":                                               "no url here",
	}
	for in, want := range cases {
		if got := Redact(in); got != want {
			t.Errorf("%q: %q, want %q", in, got, want)
		}
	}
	var buf bytes.Buffer
	_, _ = RedactWriter{W: &buf}.Write([]byte("rpc https://x.example/v3/SECRET down\n"))
	if strings.Contains(buf.String(), "SECRET") {
		t.Fatal(buf.String())
	}
	p := Provider{Name: "drpc", URL: "https://eth.drpc.org/?dkey=SECRET"}
	if strings.Contains(p.String(), "SECRET") || !strings.Contains(p.String(), "drpc") {
		t.Fatal(p.String())
	}
	e := &ProviderError{Provider: p.String(), Err: errors.New(`Post "https://eth.drpc.org/?dkey=SECRET": EOF`)}
	if strings.Contains(e.Error(), "SECRET") {
		t.Fatal(e.Error())
	}
}

// Reverts decode: Error(string), Panic(uint256), a custom error by its selector in the embedded ABIs, and data
// carried only in the provider's message.
func TestDecodeRevert(t *testing.T) {
	d, _ := revertData("PausableGate: paused")
	r := DecodeRevert(d, "execution reverted")
	if r.Name != "Error" || r.Reason != "PausableGate: paused" || !r.Named("paused") {
		t.Fatalf("%+v", r)
	}
	ty, _ := newType("uint256")
	enc, _ := argsOf(ty).Pack(big.NewInt(0x11))
	r = DecodeRevert(append(append([]byte{}, panicSelector...), enc...), "")
	if r.Name != "Panic" || !strings.Contains(r.Reason, "overflow") {
		t.Fatalf("%+v", r)
	}
	// a custom error: an ABI with one, beside the embedded ones (an implementation's ABI adds them, abi/gen.sh)
	a, err := abi.JSON(strings.NewReader(`[{"type":"error","name":"OverDayCap","inputs":[{"name":"level","type":"uint64"},{"name":"cap","type":"uint64"}]}]`))
	if err != nil {
		t.Fatal(err)
	}
	extra := &Contract{Name: "PausableGate", ABI: a}
	extraContracts = append(extraContracts, extra)
	defer func() { extraContracts = nil }()
	e := a.Errors["OverDayCap"]
	args, _ := e.Inputs.Pack(uint64(90), uint64(100))
	r = DecodeRevert(append(append([]byte{}, e.ID[:4]...), args...), "")
	if r.Name != "OverDayCap" || r.Contract != "PausableGate" || r.Reason != "90 100" || !r.Named("OverDayCap") {
		t.Fatalf("%+v", r)
	}
	if !strings.Contains(r.Error(), "PausableGate.OverDayCap") {
		t.Fatal(r.Error())
	}
	// geth's error with data; another client's message alone
	err = (&Client{Provider: Provider{Name: "x"}}).wrap(revertWith("Mailbox: already delivered"))
	if !RevertIs(err, "already delivered") {
		t.Fatalf("%v", err)
	}
	err = (&Client{Provider: Provider{Name: "x"}}).wrap(errors.New("VM Exception while processing transaction: reverted with reason string 'nope' 0x" + hexutil.Encode(d)[2:]))
	if r, ok := AsRevert(err); !ok || r.Reason != "PausableGate: paused" {
		t.Fatalf("%v", err)
	}
	if _, ok := AsRevert((&Client{Provider: Provider{Name: "x"}}).wrap(errors.New("nonce too low"))); ok {
		t.Fatal("a nonce error read as a revert")
	}
}

// The views decode into the typed states: the client's, an entry, the gate's flags and limits, the admin's holds,
// cancelled ranges and decisions — through a batch, and one call at a time when the provider refuses batches.
func TestReaders(t *testing.T) {
	ctx := context.Background()
	f := newFakeRPC()
	addr := common.HexToAddress("0x00000000000000000000000000000000000000c1")
	answers := map[string][]any{
		"trustedHeight": {int64(741_103)}, "trustedHash": {[32]byte{1}}, "trustedTime": {uint64(1_760_000_000)},
		"setHash": {[32]byte{2}}, "nextSetHash": {[32]byte{}}, "epoch": {uint64(3)}, "quarantineEnd": {uint64(0)},
		"chainId": {"gnoland-1"}, "originDomain": {uint32(1198419788)}, "mailboxPkgPathHash": {[32]byte{4}},
		"clockFactor": {uint64(1)}, "records": {uint64(9)}, "runningHash": {[32]byte{5}}, "admin": {addr},
		"historyLength": {big.NewInt(2)},
	}
	pack := func(c *Contract, data []byte, outs map[string][]any) ([]byte, error) {
		m, err := c.ABI.MethodById(data[:4])
		if err != nil {
			return nil, err
		}
		vals, ok := outs[m.Name]
		if !ok {
			return nil, fmt.Errorf("no answer for %s", m.Name)
		}
		return m.Outputs.Pack(vals...)
	}
	var contract *Contract
	var outs map[string][]any
	f.on("eth_call", func(args []any) (any, error) {
		b, err := pack(contract, args[0].(map[string]any)["data"].(hexutil.Bytes), outs)
		return hexutil.Bytes(b), err
	})
	c := NewClient(Provider{Name: "t"}, f)
	contract, outs = LightClient, answers
	cs, err := ReadClient(ctx, c, addr, Latest)
	if err != nil {
		t.Fatal(err)
	}
	if cs.TrustedHeight != 741_103 || cs.ChainID != "gnoland-1" || cs.Epoch != 3 || cs.HistoryLength != 2 || cs.Admin != addr || cs.Announced() || cs.Expect() != [32]byte{2} {
		t.Fatalf("%+v", cs)
	}
	f.noBatch = true // one call at a time
	type entryOut = struct {
		Epoch           uint64   `json:"epoch"`
		Height          int64    `json:"height"`
		LastResultsHash [32]byte `json:"lastResultsHash"`
	}
	outs = map[string][]any{"entry": {entryOut{Epoch: 3, Height: 741_104, LastResultsHash: [32]byte{6}}}}
	e, err := ReadEntry(ctx, c, addr, [32]byte{1}, Latest)
	if err != nil || e.Height != 741_104 || !e.Proves(3) || e.Proves(4) {
		t.Fatalf("%+v %v", e, err)
	}
	f.noBatch = false
	type flagOut = struct {
		BlockNumber uint64 `json:"blockNumber"`
		Time        uint64 `json:"time"`
	}
	type limitOut = struct {
		MaxPerMessage uint64 `json:"maxPerMessage"`
		DayCap        uint64 `json:"dayCap"`
		Used          uint64 `json:"used"`
		UsedAt        uint64 `json:"usedAt"`
	}
	var flags [6]flagOut
	flags[4] = flagOut{BlockNumber: 77, Time: 1_760_000_100}
	contract, outs = Gate, map[string][]any{
		"owner": {addr}, "mailbox": {addr}, "client": {addr}, "token": {addr}, "clockFactor": {uint64(8)},
		"keys": {[6]common.Address{addr}}, "flags": {flags}, "hold": {flagOut{}}, "holdSince": {flagOut{}}, "dipSince": {flagOut{}},
		"limits": {limitOut{MaxPerMessage: 25_000, DayCap: 100_000, Used: 50_000, UsedAt: 1_760_000_000}},
	}
	g, err := ReadGate(ctx, c, addr, Latest)
	if err != nil {
		t.Fatal(err)
	}
	if !g.AnyFlag() || g.Flags[4].BlockNumber != 77 || g.Limits[1].DayCap != 100_000 || g.ClockFactor != 8 {
		t.Fatalf("%+v", g)
	}
	// the bucket drains at the day cap per day (24 h ÷ the factor): half a day after a 50,000 charge at factor 1, 0
	if lvl := g.Limits[0].Level(1_760_000_000+43_200, 86_400); lvl != 0 {
		t.Fatalf("level %d", lvl)
	}
	if lvl := g.Limits[0].Level(1_760_000_000+8_640, 86_400); lvl != 40_000 {
		t.Fatalf("level %d", lvl)
	}
	type holdOut = struct {
		Start uint64 `json:"start"`
		End   uint64 `json:"end"`
	}
	type decisionOut = struct {
		Number         uint64         `json:"number"`
		Kind           uint8          `json:"kind"`
		DeliveredBlock uint64         `json:"deliveredBlock"`
		DeliveredTime  uint64         `json:"deliveredTime"`
		Deliverer      common.Address `json:"deliverer"`
		Params         []byte         `json:"params"`
	}
	contract, outs = Admin, map[string][]any{
		"lastSeq": {uint64(12)}, "pending": {uint32(1)}, "keys": {[6]common.Address{}}, "flags": {[6]flagOut{}}, "hold": {flagOut{}},
		"holds": {[]holdOut{{Start: 10, End: 20}}}, "cancelledRanges": {[][2]uint64{{3, 5}}}, "setupDone": {true},
		"successor": {common.Address{}}, "predecessor": {common.Address{}}, "inherited": {false}, "clockFactor": {uint64(1)},
		"origin": {uint32(1198419788)}, "hlgov": {[32]byte{7}}, "mailbox": {addr}, "client": {addr}, "gate": {addr}, "token": {addr},
		"proxyAdmin": {addr}, "merkleTreeHook": {addr}, "protocolFee": {addr},
		"decision": {decisionOut{Number: 12, Kind: 7, DeliveredBlock: 500, DeliveredTime: 1_760_000_000, Deliverer: addr, Params: []byte{1, 2}}},
	}
	st, err := ReadAdmin(ctx, c, addr, Latest)
	if err != nil {
		t.Fatal(err)
	}
	if st.LastSeq != 12 || len(st.Holds) != 1 || st.Holds[0].End != 20 || !st.IsCancelled(4) || st.IsCancelled(6) || st.Retired() || !st.SetupDone {
		t.Fatalf("%+v", st)
	}
	ds, err := ReadDecisions(ctx, c, addr, []uint64{12}, Latest)
	if err != nil || ds[0] == nil || ds[0].Kind != 7 || ds[0].DeliveredBlock != 500 || !bytes.Equal(ds[0].Params, []byte{1, 2}) {
		t.Fatalf("%+v %v", ds, err)
	}
	outs["decision"] = []any{decisionOut{}}
	if ds, err := ReadDecisions(ctx, c, addr, []uint64{13}, Latest); err != nil || ds[0] != nil {
		t.Fatalf("an empty record read as a decision: %+v %v", ds, err)
	}
}

// A key file: 0600, hex, never printed; a wider mode, an empty file, garbage refused; NewKeyFile never overwrites.
func TestKeyFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "updater.key")
	addr, err := NewKeyFile(path)
	if err != nil {
		t.Fatal(err)
	}
	k, err := LoadKey(path)
	if err != nil || KeyAddress(k) != addr {
		t.Fatalf("%v", err)
	}
	if _, err := NewKeyFile(path); err == nil {
		t.Fatal("a key overwritten")
	}
	secret, _ := os.ReadFile(path)
	_ = os.Chmod(path, 0o644)
	if _, err := LoadKey(path); err == nil || strings.Contains(err.Error(), strings.TrimSpace(string(secret))) {
		t.Fatalf("a world-readable key loaded, or its secret printed: %v", err)
	}
	bad := filepath.Join(dir, "bad.key")
	_ = os.WriteFile(bad, []byte("zz"+strings.Repeat("0", 62)), 0o600)
	if _, err := LoadKey(bad); err == nil || strings.Contains(err.Error(), "zz") {
		t.Fatalf("%v", err)
	}
	empty := filepath.Join(dir, "empty.key")
	_ = os.WriteFile(empty, nil, 0o600)
	if _, err := LoadKey(empty); err == nil {
		t.Fatal("an empty key file loaded")
	}
}

// Addresses: 32-byte form both ways, EIP-55 mixed case checked.
func TestAddresses(t *testing.T) {
	a := common.HexToAddress("0x5aAeb6053F3E94C9b9A09f33669435E7Ef1BeAed")
	b := AddressTo32(a)
	if back, ok := AddressFrom32(b); !ok || back != a {
		t.Fatal("round trip")
	}
	b[0] = 1
	if _, ok := AddressFrom32(b); ok {
		t.Fatal("dirty top bytes taken")
	}
	if _, err := ParseAddress("0x5aAeb6053F3E94C9b9A09f33669435E7Ef1BeAed"); err != nil {
		t.Fatal(err)
	}
	if _, err := ParseAddress("0x5AAeb6053F3E94C9b9A09f33669435E7Ef1BeAed"); err == nil {
		t.Fatal("a bad checksum taken")
	}
	if _, err := ParseAddress("5aaeb6053f3e94c9b9a09f33669435e7ef1beaed"); err == nil {
		t.Fatal("no 0x taken")
	}
}

// TestParseAddressTakesOnlyTheChecksum: the tools take an address only in its EIP-55 case (eth/bridge/PLAN.md §5.2:
// "the site and the tools accept only EIP-55 checksummed input"; round 3's E3-CF6) — a one-case address carries no
// checksum, so a typo in a seat, a watch key, the beneficiary or a decision's address would be taken silently, and
// its error names no checksummed form to paste back (that form would be the typo's checksum too). An address whose
// EIP-55 form is itself one case (no letter, or every letter's nibble on one side) is taken as typed. Cases from
// `cast to-check-sum-address` (Foundry), 2026-10-14.
func TestParseAddressTakesOnlyTheChecksum(t *testing.T) {
	const cs = "0x5aAeb6053F3E94C9b9A09f33669435E7Ef1BeAed"
	for _, s := range []string{cs, "0x1234567890123456789012345678901234567890", "0x123456789012345678901234567890123456789a",
		"0x00000000000000000000000000000000000000AB", "0x0000000000000000000000000000000000000A0b"} {
		a, err := ParseAddress(s)
		if err != nil || a.Hex() != s {
			t.Errorf("%s (its own EIP-55 form) refused: %v", s, err)
		}
	}
	for _, s := range []string{strings.ToLower(cs), "0x" + strings.ToUpper(cs[2:]), "0x00000000000000000000000000000000000000ab",
		"0x0000000000000000000000000000000000000a0b", "0x123456789012345678901234567890123456789A"} {
		_, err := ParseAddress(s)
		if err == nil {
			t.Errorf("%s (not its EIP-55 form) taken", s)
			continue
		}
		if want := common.HexToAddress(s).Hex(); strings.Contains(err.Error(), want) {
			t.Errorf("%s: the refusal names the checksummed form %s: %v", s, want, err)
		}
	}
}

// Every method and event the machines name exists in the embedded ABIs (a typo is a test failure, never a runtime
// one), and the interfaces' ABIs carry what the plan's §13 says.
func TestABIsCarryWhatTheMachinesUse(t *testing.T) {
	uses := map[*Contract][]string{
		LightClient: {"update", "proveHeader", "verify", "entry", "trustedHeight", "trustedHash", "trustedTime", "setHash", "nextSetHash", "epoch", "quarantineEnd", "chainId", "originDomain", "mailboxPkgPathHash", "clockFactor", "records", "runningHash", "admin", "historyLength", "history", "stagePower", "moduleType", "setAdmin"},
		Gate:        {"verify", "setFlag", "clearFlag", "owner", "mailbox", "client", "token", "clockFactor", "keys", "flags", "hold", "holdSince", "dipSince", "limits"},
		Admin:       {"handle", "execute", "setFlag", "clearFlag", "finishSetup", "lastSeq", "pending", "decision", "isCancelled", "cancelledRanges", "holds", "keys", "flags", "hold", "setupDone", "successor", "predecessor", "inherited", "clockFactor", "origin", "hlgov", "mailbox", "client", "gate", "token", "proxyAdmin", "merkleTreeHook", "protocolFee"},
		Mailbox:     {"process", "delivered", "localDomain", "nonce", "defaultIsm", "defaultHook", "requiredHook", "owner", "recipientIsm", "VERSION", "PACKAGE_VERSION", "processedAt"},
		HypERC20:    {"transferRemote", "routers", "totalSupply", "balanceOf", "decimals", "owner", "interchainSecurityModule", "hook", "mailbox", "feeRecipient", "feeHook", "quoteGasPayment", "approve"},
		ProtocolFee: {"protocolFee", "MAX_PROTOCOL_FEE", "beneficiary", "owner", "collectProtocolFees"},
		ProxyAdmin:  {"getProxyImplementation", "getProxyAdmin", "owner", "upgrade"},
	}
	for c, ms := range uses {
		for _, m := range ms {
			if _, ok := c.ABI.Methods[m]; !ok {
				t.Errorf("%s has no %s", c.Name, m)
			}
		}
	}
	events := map[*Contract][]string{
		LightClient:    {"HeaderRecorded", "SetAnnounced", "SetChanged", "StageProgress", "AdminChanged"},
		Admin:          {"DecisionRecorded", "DecisionIgnored", "DecisionSpent", "DecisionExecuted", "DecisionCancelled", "RangeCancelled", "Retired"},
		Mailbox:        {"Dispatch", "DispatchId", "Process", "ProcessId"},
		HypERC20:       {"SentTransferRemote", "ReceivedTransferRemote", "Transfer"},
		MerkleTreeHook: {"InsertedIntoTree"},
		Proxy:          {"Upgraded", "AdminChanged"},
	}
	for c, es := range events {
		for _, e := range es {
			_ = c.Event(e) // panics on a missing one
		}
	}
	// the implementations' ABIs (lane E-M2b's) name the errors the machines act on
	for c, names := range map[*Contract][]string{Gate: {"OverDayCap", "OverMax", "Paused", "Quarantine"}, Admin: {"NotMature", "Deferred", "RetiredAdmin", "OutOfOrder"}, LightClient: {"UnknownHeader", "HeightNotAboveTrusted", "ProofMismatch"}} {
		for _, n := range names {
			if _, ok := c.ABI.Errors[n]; !ok {
				t.Errorf("%s names no error %s", c.Name, n)
			}
		}
	}
	e := Gate.ABI.Errors["OverDayCap"]
	if r := DecodeRevert(e.ID[:4], ""); r.Name != "OverDayCap" || !RevertIs(r, "OverDayCap") {
		t.Fatalf("%+v", r)
	}
	// the plan's §13: verify's metadata is bytes, the update takes the four arguments in that order
	u := LightClient.ABI.Methods["update"]
	if len(u.Inputs) != 4 || u.Inputs[0].Type.String() != "bytes[]" || u.Inputs[2].Name != "set" || u.Inputs[3].Name != "nextSet" {
		t.Fatalf("update: %v", u.Inputs)
	}
	if v := Mailbox.ABI.Methods["process"]; v.StateMutability != "payable" || len(v.Inputs) != 2 {
		t.Fatalf("process: %v", v)
	}
}
