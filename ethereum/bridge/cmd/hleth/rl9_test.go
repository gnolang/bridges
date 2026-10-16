package main

import (
	"bytes"
	"context"
	"fmt"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/rpc"

	"github.com/gnolang/bridges/ethereum/bridge/ethx"
)

// E2-RL9 (audit round 2, the realms' audit; eth/bridge/M3b-NOTES.md, "Fold F7"): r/hlgov's decision bodies for every
// kind, fed through HlAdmin on anvil as the mailbox delivers them (HlAdmin.handle from the mailbox, gno.land's domain,
// r/hlgov's 32-byte address), each checked to record, mature, execute or be spent exactly as PLAN.md §13 says. The
// bodies are rebuilt byte for byte as the realm builds them — hleth's builders, which TestBodiesAreRHlgovs holds to
// the realm's own vectors (eth/realm/hlgov/hlgov_test.gno: bodyHex(kind, seq, params)) — with this deployment's
// addresses in their parameters, so each runs against the real contracts: kind 1 executes at its recording (a
// malformed one is spent "params"); kind 2 matures in a day; kinds 3, 5, 6, 7, 8 and 12 in seven, then execute and
// move what they name; kind 4 is spent "reserved" at execution; kinds 10, 11 and any unknown kind, and a body over 512
// bytes, are spent at recording; kind 8 to anything but the five proxies is spent "params", to an implementation with
// no code it pends (NoCodeYet); kind 9 to a successor bound elsewhere is spent "bindings", to one with no code it pends,
// to an honest successor it hands every authority over (Retired) — after which a pending decision is spent "retired"
// and nothing more is recorded.
func TestRHlgovBodiesThroughHlAdmin(t *testing.T) {
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

	// two successors: one bound as HlAdmin is, one bound to another client (each HlAdmin's own build and Config)
	succOf := func(client common.Address) common.Address {
		art, v, err := creationOf("hlAdmin", d, l, arts)
		rig.must(err)
		c := v["c"].(Values)
		c["predecessor"], c["seats"], c["watch"], c["startsequence"] = admin, [4]common.Address{}, [2]common.Address{}, uint64(0)
		if client != (common.Address{}) {
			c["client"] = client
		}
		code, err := art.CreationCode(v)
		rig.must(err)
		rc := rig.send(deployer, nil, code)
		if rc.Status != types.ReceiptStatusSuccessful {
			t.Fatalf("a successor's creation failed")
		}
		return rc.ContractAddress
	}
	honest, elsewhere := succOf(common.Address{}), succOf(common.HexToAddress("0x000000000000000000000000000000000000ba1d"))
	codeHash := func(a common.Address) [32]byte {
		code, err := cn.code(ctx, ethx.Latest, a)
		rig.must(err)
		return crypto.Keccak256Hash(code)
	}
	nothing := common.HexToAddress("0x000000000000000000000000000000000000d00d") // no code
	fresh := common.HexToAddress("0x000000000000000000000000000000000000fe11")
	beneficiary := common.HexToAddress("0x000000000000000000000000000000000000beef")

	seq := l.StartSequence
	deliver := func(kind uint8, params []byte) (uint64, []adminEv) {
		seq++
		data, err := ethx.Admin.Pack("handle", d.GnoDomain, realm32(l.Hlgov), decisionBody(kind, seq, params))
		rig.must(err)
		return seq, rig.events(rig.send(mailbox, &admin, data))
	}
	must := func(b []byte, err error) []byte { rig.must(err); return b }

	// recording (§13): kind 1 at once, the rest pending or spent at recording
	nLim0, ev := deliver(kindMintLimits, mintLimitsParams(0, 1_000, 5_000))
	rig.expect(ev, "DecisionRecorded", nLim0, kindMintLimits, "")
	nCancel, ev := deliver(kindCancel, must(cancelListParams(7, []uint64{nLim0})))
	rig.expect(ev, "DecisionRecorded", nCancel, kindCancel, "")
	rig.expect(ev, "DecisionCancelled", nLim0, 0, "")
	rig.expect(ev, "DecisionExecuted", nCancel, kindCancel, "")
	nBadCancel, ev := deliver(kindCancel, []byte{0, 0, 0, 0, 0, 0, 0, 1, 2}) // form 2: no such form
	rig.expect(ev, "DecisionSpent", nBadCancel, kindCancel, "params")
	nUnpause, _ := deliver(kindUnpause, []byte{1})
	nISM, _ := deliver(kindTokenISM, tokenISMParams(0, A("gate")))
	nReserved, _ := deliver(kindReserved, nil)
	nGuard, _ := deliver(kindGuardians, must(guardiansParams([]seatKey{{6, fresh}})))
	nRouter, _ := deliver(kindRouter, routerParams(1, d.GnoDomain, realm32(l.BubbleRoute)))
	nFee, _ := deliver(kindFee, feeParams(5, beneficiary))
	nUpgrade, _ := deliver(kindUpgrade, must(upgradeParams(A("gate"), A("gateImpl"), codeHash(A("gateImpl")), nil)))
	nNotProxy, _ := deliver(kindUpgrade, must(upgradeParams(admin, A("gateImpl"), codeHash(A("gateImpl")), nil)))
	nNoImpl, _ := deliver(kindUpgrade, must(upgradeParams(A("gate"), nothing, [32]byte{7}, nil)))
	for _, k := range []uint8{10, 11, 13} {
		n, ev := deliver(k, nil)
		rig.expect(ev, "DecisionRecorded", n, k, "")
		rig.expect(ev, "DecisionSpent", n, k, "unknown kind")
	}
	nLong, ev := deliver(kindFee, append(feeParams(5, beneficiary), make([]byte, 600)...))
	rig.expect(ev, "DecisionSpent", nLong, kindFee, "too long")
	nLim1, _ := deliver(kindMintLimits, mintLimitsParams(1, 2_000, 9_000))
	nBound, _ := deliver(kindSuccessor, successorParams(elsewhere, codeHash(elsewhere)))
	nNoSucc, _ := deliver(kindSuccessor, successorParams(nothing, [32]byte{9}))
	nSucc, _ := deliver(kindSuccessor, successorParams(honest, codeHash(honest)))

	// maturity: nothing runs before its clock; kind 2 a day after its delivery, the 7-day group seven
	rig.refused(rig.executeErr(nUnpause), "NotMature")
	rig.refused(rig.executeErr(nISM), "NotMature")
	rig.warp(86_400 + 60)
	rig.expect(rig.mustExecute(nUnpause), "DecisionExecuted", nUnpause, kindUnpause, "")
	rig.refused(rig.executeErr(nISM), "NotMature")
	rig.warp(6*86_400 + 60)
	rig.expect(rig.mustExecute(nISM), "DecisionExecuted", nISM, kindTokenISM, "")
	rig.expect(rig.mustExecute(nReserved), "DecisionSpent", nReserved, kindReserved, "reserved")
	rig.expect(rig.mustExecute(nGuard), "DecisionExecuted", nGuard, kindGuardians, "")
	rig.expect(rig.mustExecute(nRouter), "DecisionExecuted", nRouter, kindRouter, "")
	rig.expect(rig.mustExecute(nFee), "DecisionExecuted", nFee, kindFee, "")
	rig.expect(rig.mustExecute(nUpgrade), "DecisionExecuted", nUpgrade, kindUpgrade, "")
	rig.expect(rig.mustExecute(nNotProxy), "DecisionSpent", nNotProxy, kindUpgrade, "params")
	rig.refused(rig.executeErr(nNoImpl), "NoCodeYet")
	rig.refused(rig.executeErr(nLim0), "WrongDecision") // cancelled at its recording: no decision stands there
	rig.expect(rig.mustExecute(nLim1), "DecisionExecuted", nLim1, kindMintLimits, "")
	rig.expect(rig.mustExecute(nBound), "DecisionSpent", nBound, kindSuccessor, "bindings")
	rig.refused(rig.executeErr(nNoSucc), "NoCodeYet")

	// what the executed kinds moved
	ha, gt, pf, hy := arts["HlAdmin"], arts["PausableGate"], arts["ProtocolFee"], arts["HypERC20"]
	if ks, err := read[[6]common.Address](ctx, cn, ethx.Latest, admin, ha, "keys"); err != nil || ks[5] != fresh {
		t.Fatalf("kind 5 did not seat %s in slot 6: %v %v", fresh.Hex(), ks, err)
	}
	if ks, err := read[[6]common.Address](ctx, cn, ethx.Latest, A("gate"), gt, "keys"); err != nil || ks[5] != fresh {
		t.Fatalf("kind 5 did not seat %s in the gate's slot 6: %v %v", fresh.Hex(), ks, err)
	}
	if v, err := read[*big.Int](ctx, cn, ethx.Latest, A("protocolFee"), pf, "protocolFee"); err != nil || v.Int64() != 5 {
		t.Fatalf("kind 7's fee: %v %v", v, err)
	}
	if v, err := read[common.Address](ctx, cn, ethx.Latest, A("protocolFee"), pf, "beneficiary"); err != nil || v != beneficiary {
		t.Fatalf("kind 7's beneficiary: %v %v", v, err)
	}
	if lim, err := read[ethx.MintLimit](ctx, cn, ethx.Latest, A("gate"), gt, "limits", uint8(1)); err != nil || lim.MaxPerMessage != 2_000 || lim.DayCap != 9_000 {
		t.Fatalf("kind 12's limits for token 1: %+v %v", lim, err)
	}
	if lim, err := read[ethx.MintLimit](ctx, cn, ethx.Latest, A("gate"), gt, "limits", uint8(0)); err != nil || lim.MaxPerMessage != l.MaxPerMessage[0] {
		t.Fatalf("the cancelled kind 12 moved token 0's limits: %+v %v", lim, err)
	}
	if r, err := read[[32]byte](ctx, cn, ethx.Latest, A("token1"), hy, "routers", d.GnoDomain); err != nil || r != realm32(l.BubbleRoute) {
		t.Fatalf("kind 6's router: %x %v", r, err)
	}

	// the hand-over: every authority to the honest successor, then nothing pending here runs, nothing is recorded
	ev = rig.mustExecute(nSucc)
	rig.expect(ev, "DecisionExecuted", nSucc, kindSuccessor, "")
	rig.expect(ev, "Retired", 0, 0, "")
	if o, err := read[common.Address](ctx, cn, ethx.Latest, A("proxyAdmin"), arts["ProxyAdmin"], "owner"); err != nil || o != honest {
		t.Fatalf("the ProxyAdmin is %s, not the successor's: %v", o.Hex(), err)
	}
	rig.expect(rig.mustExecute(nNoImpl), "DecisionSpent", nNoImpl, kindUpgrade, "retired")
	rig.expect(rig.mustExecute(nNoSucc), "DecisionSpent", nNoSucc, kindSuccessor, "retired")
	data, _ := ethx.Admin.Pack("handle", d.GnoDomain, realm32(l.Hlgov), decisionBody(kindUnpause, seq+1, []byte{1}))
	_, err = rig.call(mailbox, &admin, data)
	rig.refused(err, "RetiredAdmin")
}

// rl9Rig drives HlAdmin on anvil through unlocked and impersonated accounts.
type rl9Rig struct {
	t     *testing.T
	raw   *rpc.Client
	cn    *conn
	admin common.Address
}

// adminEv is one of HlAdmin's logs as the test reads it.
type adminEv struct {
	Name   string
	Number uint64
	Kind   uint8
	Why    string
}

func (r *rl9Rig) must(err error) {
	r.t.Helper()
	if err != nil {
		r.t.Fatal(err)
	}
}

// call sends a transaction from an unlocked or impersonated account, simulated first (eth_call: a revert answers
// here, nothing is sent), as the machines send.
func (r *rl9Rig) call(from common.Address, to *common.Address, data []byte) (*types.Receipt, error) {
	tx := map[string]any{"from": from, "data": hexutil.Bytes(data)}
	if to != nil {
		tx["to"] = *to
		if _, err := r.cn.c.CallFrom(context.Background(), from, *to, data, nil, ethx.Latest); err != nil {
			return nil, err
		}
	}
	var h common.Hash
	if err := r.raw.Call(&h, "eth_sendTransaction", tx); err != nil {
		return nil, err
	}
	for i := 0; ; i++ {
		rc, err := r.cn.c.Receipt(context.Background(), h)
		if err == nil && rc.Status != types.ReceiptStatusSuccessful {
			return rc, fmt.Errorf("tx %s reverted", h.Hex())
		}
		if err == nil || i == 100 {
			return rc, err
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func (r *rl9Rig) send(from common.Address, to *common.Address, data []byte) *types.Receipt {
	r.t.Helper()
	rc, err := r.call(from, to, data)
	r.must(err)
	return rc
}

func (r *rl9Rig) execute(n uint64) (*types.Receipt, error) {
	data, _ := ethx.Admin.Pack("execute", n)
	return r.call(common.HexToAddress(anvilAccounts[0]), &r.admin, data)
}

func (r *rl9Rig) mustExecute(n uint64) []adminEv {
	r.t.Helper()
	rc, err := r.execute(n)
	if err != nil {
		r.t.Fatalf("execute(%d): %v", n, err)
	}
	return r.events(rc)
}

func (r *rl9Rig) executeErr(n uint64) error {
	_, err := r.execute(n)
	return err
}

// refused checks a call reverted with the named error.
func (r *rl9Rig) refused(err error, name string) {
	r.t.Helper()
	if err == nil {
		r.t.Fatalf("not refused %s", name)
	}
	text := err.Error()
	if rv, ok := ethx.AsRevert(err); ok {
		text = rv.Name + " " + rv.Error()
	} else if de, ok := err.(rpc.DataError); ok {
		if s, ok := de.ErrorData().(string); ok {
			if b, err := hexutil.Decode(s); err == nil {
				text += " " + ethx.DecodeRevert(b, err.Error()).Name
			}
		}
	}
	if !strings.Contains(text, name) {
		r.t.Fatalf("refused %q, not %s", text, name)
	}
}

func (r *rl9Rig) warp(secs int64) {
	r.t.Helper()
	r.must(r.raw.Call(nil, "evm_increaseTime", secs))
	r.must(r.raw.Call(nil, "evm_mine"))
}

// events reads HlAdmin's logs of a receipt (its own events and HlAdminExec's, which run as HlAdmin).
func (r *rl9Rig) events(rc *types.Receipt) []adminEv {
	var out []adminEv
	for _, lg := range rc.Logs {
		if lg.Address != r.admin || len(lg.Topics) == 0 {
			continue
		}
		e, err := ethx.Admin.ABI.EventByID(lg.Topics[0])
		if err != nil && ethx.AdminExec != nil {
			e, err = ethx.AdminExec.ABI.EventByID(lg.Topics[0])
		}
		if err != nil {
			continue
		}
		ev := adminEv{Name: e.Name}
		topic := 1
		for _, in := range e.Inputs {
			if in.Indexed && topic < len(lg.Topics) {
				if in.Name == "number" {
					ev.Number = new(big.Int).SetBytes(lg.Topics[topic][:]).Uint64()
				}
				topic++
			}
		}
		if vals, err := e.Inputs.NonIndexed().Unpack(lg.Data); err == nil {
			for i, in := range e.Inputs.NonIndexed() {
				switch v := vals[i].(type) {
				case uint64:
					if in.Name == "number" {
						ev.Number = v
					}
				case uint8:
					if in.Name == "kind" {
						ev.Kind = v
					}
				case string:
					ev.Why = v
				}
			}
		}
		out = append(out, ev)
	}
	return out
}

// expect checks a log among the receipt's: by name, number and kind (0: any), and the spend's reason.
func (r *rl9Rig) expect(evs []adminEv, name string, n uint64, kind uint8, why string) {
	r.t.Helper()
	for _, e := range evs {
		if e.Name == name && (n == 0 || e.Number == n) && (kind == 0 || e.Kind == kind) && e.Why == why {
			return
		}
	}
	r.t.Fatalf("no %s(%d, kind %d, %q) among %s", name, n, kind, why, fmt.Sprint(evs))
}
