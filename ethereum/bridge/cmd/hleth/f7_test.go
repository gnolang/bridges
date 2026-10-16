package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"

	"github.com/gnolang/bridges/solana/bridge/gnolight"
	"github.com/gnolang/bridges/ethereum/bridge/ethx"
	"github.com/gnolang/bridges/ethereum/bridge/repro"
)

// Fold F7 — the plan red team's E2-RT1 and E2-RT6 in hleth.

// E2-RT1: a creation whose runtime is the build's exactly but whose initcode is not — here the ProxyAdmin's, created
// with one byte past its creation code (a contract with no constructor arguments ignores it: the same runtime, the
// same owner, the same logs) — fails the audit on its input alone, and nothing else fails: no reading of the state or
// the code sees an initcode, which could as well have planted storage. The setup's other fourteen creations, its
// settings and finishSetup are hleth's own.
func TestAnvilAuditHoldsEveryCreationInput(t *testing.T) {
	arts := artifactsOrSkip(t)
	url, _ := startAnvil(t)
	dir := t.TempDir()
	ctx := context.Background()
	cfg := testSetupConfig(t, url, dir)
	cn, err := dial(ctx, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	key, err := ethx.LoadKey(cfg.DeployerKey)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := newTxer(cn, key, ethx.SendConfig{Poll: 50 * time.Millisecond}, func(string, ...any) {})
	if err != nil {
		t.Fatal(err)
	}
	me := tx.from
	// the record a setup writes before its first creation (the nonce plan fixed), then the ProxyAdmin by hand
	d := &Deployment{ChainID: 31337, LocalDomain: 31337, GnoDomain: cfg.GnoDomain, Deployer: me.Hex(), FirstNonce: 0, AdminNonce: planHlAdmin, AdminAt: crypto.CreateAddress(me, planHlAdmin).Hex()}
	save := func() error { return d.Save(cfg.Out) }
	planted := append(append([]byte{}, arts["ProxyAdmin"].Creation...), 0x00)
	if _, _, err := tx.create(ctx, d, save, "proxyAdmin", "the ProxyAdmin (one byte past its creation code)", planted, 0); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if _, err := runSetup(ctx, cfg, &out); err != nil {
		t.Fatalf("the setup over the hand-made ProxyAdmin: %v\n%s", err, out.String())
	}
	var b bytes.Buffer
	fails, err := runAudit(ctx, auditConfig{RPC: url, Deployment: cfg.Out, ContractsOut: cfg.ContractsOut, HyperlaneDir: cfg.HyperlaneDir, At: "latest"}, &b)
	if err != nil {
		t.Fatal(err)
	}
	text := b.String()
	if len(fails) != 1 || !strings.Contains(fails[0], "proxyAdmin's creation input is not the build of ProxyAdmin with the setup's arguments: the build's constructor takes no arguments, and 1 bytes follow its creation code") {
		t.Fatalf("a ProxyAdmin made by another initcode: %d failures, want the one creation input\n%s", len(fails), text)
	}
	for _, want := range []string{
		"ok   proxyAdmin (" + d.Addr("proxyAdmin").Hex() + ") code is the build of ProxyAdmin byte for byte",
		"ok   the ProxyAdmin's ownership moved 2 time(s) since its creation",
		"ok   hlAdmin's creation input is the build of HlAdmin and the constructor's arguments the setup used, byte for byte",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("the audit lacks %q:\n%s", want, text)
		}
	}
}

// The creation input's difference is named: the build's code, a byte past a canonical encoding, or the argument.
func TestCreationDiffNamesTheArgument(t *testing.T) {
	a := synthetic()
	a.Creation = []byte{0x60, 0x80, 0x60, 0x40, 0x52}
	parsed, err := abi.JSON(strings.NewReader(`[{"type":"constructor","inputs":[{"name":"mailbox","type":"address"},{"name":"localDomain","type":"uint32"}]}]`))
	if err != nil {
		t.Fatal(err)
	}
	a.ABI = parsed
	mailbox := common.HexToAddress("0x9fE46736679d2D9a65F0992F2272dE9f3c7fa6e0")
	want, err := a.CreationCode(Values{"mailbox": mailbox, "localdomain": uint32(31337)})
	if err != nil {
		t.Fatal(err)
	}
	other, _ := a.CreationCode(Values{"mailbox": mailbox, "localdomain": uint32(31338)})
	if got := creationDiff(other, want, a); !strings.Contains(got, "its argument localDomain is uint32 localDomain = 31338, the setup's uint32 localDomain = 31337") {
		t.Fatalf("another domain: %s", got)
	}
	code := append([]byte{0x61}, want[1:]...)
	if got := creationDiff(code, want, a); !strings.Contains(got, "are not the build's creation code: 1 differ, the first at offset 0") {
		t.Fatalf("another code: %s", got)
	}
	if got := creationDiff(append(append([]byte{}, want...), make([]byte, 32)...), want, a); !strings.Contains(got, "not a canonical encoding") {
		t.Fatalf("a word past the arguments: %s", got)
	}
	if c := repro.SplitCreation(want, a); c.Diff != "" || len(c.Args) != 2 || c.Args[0].(common.Address) != mailbox {
		t.Fatalf("the build's own input: %+v", c)
	}
}

// E2-RT6 by the launch line (M2-NOTES.md Fold F5, F5.1 item 12): every Dispatch and Process of the mailbox before
// SetupDone is listed. Only what no stranger can do fails — a token's burn of a positive amount below the line
// (something was minted before the launch), a delivery to the deployment before it, a dispatch before SetupDone that the
// line does not draw — so no stranger can fail the audit with a dispatch of its own, a transfer of 0 or a delivery to
// a recipient of its own through its own ISM (the veto E2-RT6 found in finishSetup's checks 32–33).
func TestJudgeEarlyLogs(t *testing.T) {
	tok0, tok1 := common.HexToAddress("0x0000000000000000000000000000000000000a01"), common.HexToAddress("0x0000000000000000000000000000000000000a02")
	admin := common.HexToAddress("0x0000000000000000000000000000000000000ad1")
	stranger := common.HexToAddress("0x00000000000000000000000000000000000000ee")
	ls := []earlyLog{
		{Event: "Dispatch", Block: 90, Index: 3, Who: stranger, Nonce: 0, Parsed: true, Amount: big.NewInt(9)}, // a stranger's
		{Event: "Dispatch", Block: 91, Index: 0, Who: tok0, Nonce: 1, Parsed: true, Amount: new(big.Int)},      // anyone's transfer of 0
		{Event: "Process", Block: 91, Index: 1, Who: stranger},                                                 // a stranger's own recipient
		{Event: "Dispatch", Block: 92, Index: 2, Who: tok1, Nonce: 2, Parsed: true, Amount: big.NewInt(500)},   // the deployer's burn back
		{Event: "Process", Block: 92, Index: 1, Who: tok1},                                                     // its mint
		{Event: "Process", Block: 93, Index: 0, Who: admin},                                                    // a decision before the launch
		{Event: "Dispatch", Block: 93, Index: 4, Who: stranger, Nonce: 3, Parsed: true},                        // the line does not draw it
		{Event: "Dispatch", Block: 93, Index: 5, Who: stranger},                                                // no message
	}
	warns, fails := judgeEarly(ls, 3, [2]common.Address{tok0, tok1}, []common.Address{tok0, tok1, admin})
	if len(warns) != 3 || len(fails) != 5 {
		t.Fatalf("%d warnings, %d failures:\n%s\n%s", len(warns), len(fails), strings.Join(warns, "\n"), strings.Join(fails, "\n"))
	}
	for i, want := range []string{"a stranger's dispatch below the launch line: #0", "a token's dispatch of nothing below the launch line: #1", "a recipient of its own with an ISM of its own"} {
		if !strings.Contains(warns[i], want) {
			t.Errorf("warning %d: %s, want %q", i, warns[i], want)
		}
	}
	for i, want := range []string{"a token's burn below the launch line: #2 by " + tok1.Hex(), "delivered a message to " + tok1.Hex() + " (the deployment's)", "delivered a message to " + admin.Hex(),
		"#3 by " + stranger.Hex() + " at block 93 (tx", "carries no message that reads"} {
		if !strings.Contains(strings.Join(fails, "\n"), want) {
			t.Errorf("failure %d: want %q in\n%s", i, want, strings.Join(fails, "\n"))
		}
	}
	if w, f := judgeEarly(nil, 0, [2]common.Address{tok0, tok1}, nil); len(w)+len(f) != 0 {
		t.Fatal("nothing before the setup listed something")
	}
}

// The record keeps the trusted header's leaves: the client's proxy's creation is rebuilt from them, and a record
// whose leaves do not hash to its trusted hash is refused.
func TestClientInitFromTheRecord(t *testing.T) {
	l := Launch{GnoChainID: "dev", TrustedHash: "0x" + strings.Repeat("00", 32), Validators: []ValidatorJSON{{PubKey: "0x" + strings.Repeat("11", 32), Power: 10}}}
	if _, err := clientInitValues(l, 1, common.Address{}); err == nil || !strings.Contains(err.Error(), "0 of the trusted header's sixteen leaves") {
		t.Fatalf("no leaves: %v", err)
	}
	var leaves [][]byte
	for i := 0; i < 16; i++ {
		leaves = append(leaves, []byte{0x0a, 0x01, byte(i)})
		l.TrustedLeaves = append(l.TrustedLeaves, fmt.Sprintf("0x0a01%02x", i))
	}
	if _, err := clientInitValues(l, 1, common.Address{}); err == nil || !strings.Contains(err.Error(), "not its trusted hash") {
		t.Fatalf("leaves of another header: %v", err)
	}
	root, _ := gnolight.SimpleHash(leaves)
	l.TrustedHash = "0x" + hex.EncodeToString(root[:])
	v, err := clientInitValues(l, 1198419796, common.HexToAddress("0x01"))
	if err != nil {
		t.Fatal(err)
	}
	if v["chainid"] != "dev" || v["origindomain"] != uint32(1198419796) || len(v["headerleaves"].([][]byte)) != 16 || len(v["validators"].([]Values)) != 1 {
		t.Fatalf("the initializer's values: %v", v)
	}
}

// Fold F5's F5.1 items 12–13 in the audit: HlAdmin's stored record is held to its logs, the launch line to the
// dispatches before SetupDone, and finishSetup is run again on the launch's state with setupDone false. A record planted
// after the setup (anvil writes HlAdmin's first word: three decisions pending, the last number 5 above the start, the
// launch line at 5, no log of any) fails all three — the re-run refuses it check 34 (SetupDone's block is the latest
// here), and the reads name each planted field — while setupDone() still says true.
func TestAnvilAuditRefusesAPlantedRecord(t *testing.T) {
	artifactsOrSkip(t)
	url, raw := startAnvil(t)
	dir := t.TempDir()
	ctx := context.Background()
	cfg := testSetupConfig(t, url, dir)
	var out bytes.Buffer
	if _, err := runSetup(ctx, cfg, &out); err != nil {
		t.Fatalf("setup: %v\n%s", err, out.String())
	}
	d, err := LoadDeployment(cfg.Out, false)
	if err != nil {
		t.Fatal(err)
	}
	admin := d.Addr("hlAdmin")
	var word common.Hash
	if err := raw.Call(&word, "eth_getStorageAt", admin, hlAdminSlot, "latest"); err != nil {
		t.Fatal(err)
	}
	if word[wordSetupDone] != 1 {
		t.Fatalf("the first word %x holds no setupDone at byte %d", word, wordSetupDone)
	}
	start := d.Launch.StartSequence
	planted := word
	binary.BigEndian.PutUint32(planted[wordPending:], 3)
	binary.BigEndian.PutUint64(planted[wordLastSeq:], start+5)
	binary.BigEndian.PutUint32(planted[wordLaunchNonce:], 5) // a launch line above every message dispatched
	if err := raw.Call(nil, "anvil_setStorageAt", admin, hlAdminSlot, planted); err != nil {
		t.Fatal(err)
	}
	var b bytes.Buffer
	fails, err := runAudit(ctx, auditConfig{RPC: url, Deployment: cfg.Out, ContractsOut: cfg.ContractsOut, HyperlaneDir: cfg.HyperlaneDir, At: "latest"}, &b)
	if err != nil {
		t.Fatal(err)
	}
	text := b.String()
	for _, want := range []string{
		"FAIL finishSetup refuses again on block " + fmt.Sprint(d.DeployBlock) + "'s state with setupDone false: reverts: HlAdmin.SetupIncomplete 34",
		fmt.Sprintf("FAIL HlAdmin's lastSeq() %d is the launch's start %d plus its 0 DecisionRecorded log(s)", start+5, start),
		"FAIL HlAdmin's pending() 3 is at most the 0 decision(s) its logs recorded to pend",
		"FAIL HlAdmin's launchNonce() is 5, the number of messages the mailbox dispatched before SetupDone (0)",
		"HlAdmin's setupDone() is true — storage an initcode can plant, so proof of nothing",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("the audit of a planted record lacks %q (%d failures):\n%s", want, len(fails), text)
		}
	}
}

// setupDone() is storage an initcode can plant, and HlAdmin's creation runs as HlAdmin: a SetupDone logged by the
// creation transaction is its initcode's, never finishSetup's, and fails the audit (M2-NOTES.md Fold F5, F5.1 item 13).
func TestSetupDoneByItsCreationFails(t *testing.T) {
	creation := common.HexToHash("0xc4ea7e")
	var b bytes.Buffer
	a := &audit{out: &b, d: &Deployment{Steps: []Step{{Name: "hlAdmin", Tx: creation.Hex(), Block: 30}}}}
	a.setupDoneBy(context.Background(), common.HexToAddress("0xad"), types.Log{TxHash: creation, BlockNumber: 30})
	if len(a.fails) != 1 || !strings.Contains(a.fails[0], "logged by its creation transaction "+creation.Hex()+": its initcode, not finishSetup") {
		t.Fatalf("a SetupDone of HlAdmin's creation: %v\n%s", a.fails, b.String())
	}
}
