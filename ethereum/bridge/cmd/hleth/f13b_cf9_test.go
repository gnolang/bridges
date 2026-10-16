package main

import (
	"bytes"
	"context"
	"fmt"
	"math/big"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"

	"github.com/gnolang/bridges/ethereum/bridge/ethx"
)

// E3-CF9: the members' audit at the yield reads the bridge as it stands, after governed changes — a seat replaced
// (kind 5), a fee (kind 7), new limits (kind 12), a router enrolled (kind 6), a token's ISM (kind 3), an upgrade (kind
// 8), a kind 12 cancelled, an unpause (kind 2), and at last a successor (kind 9). Each decision is delivered to
// HlAdmin as the mailbox delivers it, with r/hlgov's own record of it on the gno node (its body under that number,
// its counter, its Ethereum seats), and executed after its clock. The audit held every value to the launch's and
// FAILed all of them; now each moved value is held to the executed decision that moved it and the audit PASSes — and
// a change no decision explains (HlAdmin's authority used directly, as a planted state would be) FAILs, as does a
// decision r/hlgov did not dispatch as it ran. (Written to the audit's API before the fold too: run on eb7cebb4 it
// fails at the first verdict.)
func TestAnvilAuditAfterGovernedChanges(t *testing.T) {
	arts := artifactsOrSkip(t)
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
	cn, err := dial(ctx, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	A := d.Addr
	admin, mailbox, deployer := A("hlAdmin"), A("mailbox"), common.HexToAddress(d.Deployer)
	l := d.Launch
	rig := &rl9Rig{t: t, raw: raw, cn: cn, admin: admin}
	rig.must(raw.Call(nil, "anvil_impersonateAccount", mailbox))
	rig.must(raw.Call(nil, "anvil_setBalance", mailbox, "0x56BC75E2D63100000"))
	fg := honestGno(t, cfg, d)
	acfg := withLaunchFlags(auditConfig{RPC: url, Deployment: cfg.Out, ContractsOut: cfg.ContractsOut, HyperlaneDir: cfg.HyperlaneDir, At: "latest", GnoRPC: fg.serve(t).URL,
		MailboxPkgPath: cfg.MailboxPkgPath, Hlgov: cfg.Hlgov, GnotRoute: cfg.GnotRoute, BubbleRoute: cfg.BubbleRoute}, cfg) // the launch values, as the yield's member gives them (E4-TL1, E4-TL6)
	audit := func() ([]string, string) {
		t.Helper()
		var b bytes.Buffer
		fails, err := runAudit(ctx, acfg, &b)
		if err != nil {
			t.Fatalf("audit: %v\n%s", err, b.String())
		}
		return fails, b.String()
	}
	codeHash := func(a common.Address) [32]byte {
		code, err := cn.code(ctx, ethx.Latest, a)
		rig.must(err)
		return crypto.Keccak256Hash(code)
	}
	seq := l.StartSequence
	// deliver is r/hlgov's dispatch and the mailbox's delivery: r/hlgov keeps the body under its number (and moves its
	// counter, and its seats for a kind 5), HlAdmin records it
	deliver := func(kind uint8, params []byte) uint64 {
		t.Helper()
		seq++
		body := decisionBody(kind, seq, params)
		data, err := ethx.Admin.Pack("handle", d.GnoDomain, realm32(l.Hlgov), body)
		rig.must(err)
		rig.send(mailbox, &admin, data)
		fg.mu.Lock()
		fg.decisions[seq] = fmt.Sprintf(`{"number":%d,"kind":%d,"body":"%x","id":"","count":1,"at":0,"text":""}`, seq, kind, body)
		fg.last = seq
		fg.mu.Unlock()
		return seq
	}
	must := func(b []byte, err error) []byte { rig.must(err); return b }

	if fails, text := audit(); len(fails) != 0 || !strings.Contains(text, "hleth audit: PASS") {
		t.Fatalf("the launch state: %d failures\n%s", len(fails), text)
	}

	// the governed changes
	fresh := common.HexToAddress("0x000000000000000000000000000000000000fe11")
	beneficiary := common.HexToAddress("0x000000000000000000000000000000000000beef")
	var other [32]byte
	other[31] = 0xab
	nGuard := deliver(kindGuardians, must(guardiansParams([]seatKey{{6, fresh}})))
	fg.mu.Lock()
	fg.seats[5] = fresh.Hex() // r/hlgov's mirror moves when it dispatches the kind 5
	fg.mu.Unlock()
	nFee := deliver(kindFee, feeParams(5, beneficiary))
	nLim := deliver(kindMintLimits, mintLimitsParams(1, 2_000, 9_000))
	nRouter := deliver(kindRouter, routerParams(1, 1, other))
	nISM := deliver(kindTokenISM, tokenISMParams(0, common.Address{}))
	nUp := deliver(kindUpgrade, must(upgradeParams(A("gate"), A("gateImpl"), codeHash(A("gateImpl")), nil)))
	nLim0 := deliver(kindMintLimits, mintLimitsParams(0, 1_000, 5_000))
	deliver(kindCancel, must(cancelListParams(7, []uint64{nLim0})))
	nUnpause := deliver(kindUnpause, []byte{1})
	rig.warp(7*86_400 + 60)
	for _, n := range []uint64{nGuard, nFee, nLim, nRouter, nISM, nUp, nUnpause} {
		rig.mustExecute(n)
	}

	fails, text := audit()
	if len(fails) != 0 || !strings.Contains(text, "hleth audit: PASS") {
		t.Fatalf("the audit of a governed bridge: %d failures\n%s", len(fails), text)
	}
	for _, want := range []string{
		fmt.Sprintf("ok   the protocol fee is 5 wei (must be 5: 0 at launch; decision #%d (kind 7, executed at block", nFee),
		fmt.Sprintf("ok   the ProtocolFee's beneficiary is %s (must be %s, the one decision #%d (kind 7", beneficiary.Hex(), beneficiary.Hex(), nFee),
		fmt.Sprintf("seat 6 by decision #%d (kind 5", nGuard),
		"ok   seat 6 is " + fresh.Hex() + ", r/hlgov's Ethereum seat as it last set it",
		fmt.Sprintf("ok   the gate's limits for token 1: 2000 a message, 9000 a day (must be 2000 and 9000: the launch's; decision #%d (kind 12", nLim),
		fmt.Sprintf("ok   the gate's limits for token 0: %d a message", l.MaxPerMessage[0]),
		fmt.Sprintf("ok   token 1's router for domain 1 is 0x%x (must be 0x%x; decision #%d (kind 6", other, other, nRouter),
		fmt.Sprintf("ok   token 0's ISM is 0x0000000000000000000000000000000000000000 (must be 0x0000000000000000000000000000000000000000, what decision #%d (kind 3", nISM),
		"ok   gate was upgraded 2 time(s): must be once, to " + A("gateImpl").Hex(),
		fmt.Sprintf("ok   gate's upgrade 2, to %s, is decision #%d (kind 8", A("gateImpl").Hex(), nUp),
		"the line recorded 9 decision(s): 8 executed, 0 spent, 1 cancelled, 0 pending",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("the governed audit lacks %q:\n%s", want, text)
		}
	}

	// a change no decision explains: HlAdmin's authority used directly (anvil impersonates it), as a planted state or
	// a stolen authority would — FAIL, named; put back, PASS
	rig.must(raw.Call(nil, "anvil_impersonateAccount", admin))
	rig.must(raw.Call(nil, "anvil_setBalance", admin, "0x56BC75E2D63100000"))
	setFee := func(v int64) {
		data, err := ethx.ProtocolFee.Pack("setProtocolFee", big.NewInt(v))
		rig.must(err)
		var h common.Hash
		rig.must(raw.Call(&h, "eth_sendTransaction", map[string]any{"from": admin, "to": A("protocolFee"), "data": hexutil.Bytes(data)}))
		waitReceipt(t, raw, h)
	}
	setFee(7)
	if fails, text = audit(); len(fails) != 1 || !strings.Contains(fails[0], "the protocol fee is 7 wei (must be 5") {
		t.Fatalf("an unexplained fee: %d failures\n%s", len(fails), text)
	}
	setFee(5)
	// a decision r/hlgov did not dispatch as it ran (its body under that number another): nothing it moved is explained
	fg.mu.Lock()
	good := fg.decisions[nFee]
	fg.decisions[nFee] = fmt.Sprintf(`{"number":%d,"kind":7,"body":"%x","id":"","count":1,"at":0,"text":""}`, nFee, decisionBody(kindFee, nFee, feeParams(6, beneficiary)))
	fg.mu.Unlock()
	if fails, text = audit(); len(fails) != 3 || !strings.Contains(fails[0], fmt.Sprintf("decision #%d (kind 7, executed at block", nFee)) || !strings.Contains(fails[0], "is not r/hlgov's") {
		t.Fatalf("a decision r/hlgov did not dispatch: %d failures\n%s", len(fails), text)
	}
	fg.mu.Lock()
	fg.decisions[nFee] = good
	fg.mu.Unlock()

	// the hand-over: a kind 9 to an honest successor — every authority is the successor's, explained
	art, v, err := creationOf("hlAdmin", d, l, arts)
	rig.must(err)
	c := v["c"].(Values)
	c["predecessor"], c["seats"], c["watch"], c["startsequence"] = admin, [4]common.Address{}, [2]common.Address{}, uint64(0)
	code, err := art.CreationCode(v)
	rig.must(err)
	rc := rig.send(deployer, nil, code)
	if rc.Status != types.ReceiptStatusSuccessful {
		t.Fatal("the successor's creation failed")
	}
	succ := rc.ContractAddress
	nSucc := deliver(kindSuccessor, successorParams(succ, codeHash(succ)))
	rig.warp(7*86_400 + 60)
	rig.expect(rig.mustExecute(nSucc), "Retired", 0, 0, "")
	if fails, text = audit(); len(fails) != 0 || !strings.Contains(text, "hleth audit: PASS") {
		t.Fatalf("the audit after the hand-over: %d failures\n%s", len(fails), text)
	}
	for _, want := range []string{
		"ok   the ProxyAdmin's owner is " + succ.Hex(),
		"ok   the client's admin is " + succ.Hex(),
		fmt.Sprintf("ok   HlAdmin %s's successor() is %s, by decision #%d (kind 9", admin.Hex(), succ.Hex(), nSucc),
		"ok   the ProxyAdmin's ownership moved 3 time(s) since its creation",
		fmt.Sprintf("ok   the ProxyAdmin went from %s to its successor %s in the transaction of the kind 9 that named it, by decision #%d", admin.Hex(), succ.Hex(), nSucc),
		fmt.Sprintf("ok   the line's last number %d is r/hlgov's counter %d", nSucc, nSucc),
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("the audit after the hand-over lacks %q:\n%s", want, text)
		}
	}
}
