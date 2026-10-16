package agent

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"reflect"
	"sort"
	"strings"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"

	"github.com/gnolang/bridges/ethereum/bridge/ethx"
	"github.com/gnolang/bridges/ethereum/bridge/repro"
)

// What a holder reads before a kind 8 or 9 (eth/bridge/PLAN.md §6.5, §8; the plan red team's E2-RT1, E2-RT3, E2-RT10):
// on Ethereum the bindings that make a contract ours live in its immutables and its constructor, and a code hash says
// nothing of either — every honest upgrade has immutables, so a build's bare hash matches none of them, and a reviewed
// build's code over a stranger's mailbox, client or verifier has the build's code exactly. So the target's code is
// held to a reproducible build with every immutable masked, each immutable's value printed beside the value it must
// have (the live deployment's: HlAdmin's bindings, the client's hlmailbox path and verifier, the domains); its creation
// transaction (-creation-tx) is held to the build's creation code and each constructor argument to its expected value
// (an initcode could write any storage and return the build's runtime; absent, the target is NOT SHOWN IMMUTABLE); a
// kind 9's successor answers HlAdmin's bindings beside this HlAdmin's, and its HlAdminExec is held to its build; a kind
// 8's call is decoded against the implementation's ABI, function and arguments ("UNDECODED" when it cannot be).

// Build is a reproducible build a target is held to (hlwatch -build): forge's artifact — its runtime with the places of
// its immutables, its creation code, its ABI — or a file holding the runtime's keccak256 alone, which matches only code
// with no immutable (and says nothing of a constructor).
type Build struct {
	Art  *repro.Artifact // nil for a hash file
	Hash common.Hash     // the runtime's keccak256 (an artifact's with its immutables zero)
}

// BuildOf is an artifact as a build.
func BuildOf(a *repro.Artifact) Build { return Build{Art: a, Hash: crypto.Keccak256Hash(a.Runtime)} }

// matched is the build a code is (the first label, sorted, whose build it is: masked for an artifact, by hash for a
// hash file), with its immutables read.
type matched struct {
	Label    string
	Build    Build
	Readings []repro.Reading
}

// matchBuild finds the build a code is, and says why none is.
func (w *Watch) matchBuild(code []byte) (*matched, string) {
	labels := make([]string, 0, len(w.Builds))
	for l := range w.Builds {
		labels = append(labels, l)
	}
	sort.Strings(labels)
	if len(labels) == 0 {
		return nil, "no reproducible build given (-build): the code hash pins bytes, not behaviour"
	}
	h := crypto.Keccak256Hash(code)
	var whys []string
	for _, l := range labels {
		b := w.Builds[l]
		if b.Art == nil {
			if b.Hash == h {
				return &matched{Label: l, Build: b}, ""
			}
			whys = append(whys, l+": another hash (a hash file matches only code with no immutable)")
			continue
		}
		readings, diff := repro.CompareCode(code, b.Art)
		if diff == "" {
			return &matched{Label: l, Build: b, Readings: readings}, ""
		}
		whys = append(whys, l+": "+diff)
	}
	return nil, "MATCHES NO REPRODUCIBLE BUILD given (" + strings.Join(whys, "; ") + "): immutable only if its source is reviewed and reaches no mutable target by DELEGATECALL"
}

// liveFacts are the live deployment's values a target's immutables and arguments are held to: this HlAdmin's
// bindings, the client's hlmailbox path hash and verifier (through its proxy).
type liveFacts struct {
	Admin    common.Address
	Bind     *ethx.Bindings
	PkgPath  [32]byte
	Verifier common.Address
}

func (w *Watch) live(ctx context.Context, at ethx.At, admin common.Address) (*liveFacts, error) {
	b, err := ethx.ReadBindings(ctx, w.m.pair, admin, at)
	if err != nil {
		return nil, fmt.Errorf("HlAdmin %s's bindings could not be read: %s", admin.Hex(), ethReadText(err))
	}
	f := &liveFacts{Admin: admin, Bind: b}
	data, err := ethx.LightClient.Pack("mailboxPkgPathHash")
	if err != nil {
		return nil, err
	}
	out, err := w.m.pair.CallAt(ctx, b.Client, data, at)
	if err != nil || len(out) < 32 {
		return nil, fmt.Errorf("the client %s's mailboxPkgPathHash() could not be read: %v", b.Client.Hex(), err)
	}
	copy(f.PkgPath[:], out[:32])
	if data, err = ethx.LightClient.Pack("ED25519"); err != nil {
		return nil, err
	}
	if out, err = w.m.pair.CallAt(ctx, b.Client, data, at); err != nil || len(out) < 32 {
		return nil, fmt.Errorf("the client %s's ED25519() could not be read: %v", b.Client.Hex(), err)
	}
	f.Verifier = common.BytesToAddress(out[12:32])
	return f, nil
}

// expectations are the values a target's immutables must hold, by name: the live deployment's, for each contract a
// kind 8 or 9 can name. succ is a kind 9's successor (its predecessor this HlAdmin, its HlAdminExec its own CREATE at
// nonce 1); created the block of the target's creation (Indexed.deployedBlock), 0 when not known.
func expectations(f *liveFacts, target common.Address, created uint64, execHash common.Hash) map[string]repro.Expect {
	b := f.Bind
	ex := map[string]repro.Expect{
		// Hyperlane's, behind the five proxies
		"Mailbox.localDomain":          repro.ExpectU64(uint64(b.LocalDomain), "Ethereum's domain, HlAdmin's localDomain()"),
		"MailboxClient.mailbox":        repro.ExpectAddr(b.Mailbox, "our mailbox, HlAdmin's mailbox()"),
		"MailboxClient.localDomain":    repro.ExpectU64(uint64(b.LocalDomain), "Ethereum's domain"),
		"HypERC20._decimals":           repro.ExpectU64(6, "6, gno.land's"),
		"TokenRouter.scaleNumerator":   repro.ExpectU64(1, "scale 1/1"),
		"TokenRouter.scaleDenominator": repro.ExpectU64(1, "scale 1/1"),
		// ours
		"GnoLightClient.mailboxPkgPathHash": repro.ExpectBytes32(f.PkgPath, "the live client's hlmailbox path hash"),
		"GnoLightClient.ED25519":            repro.ExpectAddr(f.Verifier, "the live client's ED25519(), the verifier it runs: another is a verifier swap, read on its own"),
		// a successor HlAdmin (kind 9): bound as this HlAdmin is (finishSetupOnly's five printed beside it, not held)
		"HlAdmin.mailbox":            repro.ExpectAddr(b.Mailbox, "this HlAdmin's mailbox()"),
		"HlAdmin.client":             repro.ExpectAddr(b.Client, "this HlAdmin's client()"),
		"HlAdmin.gate":               repro.ExpectAddr(b.Gate, "this HlAdmin's gate()"),
		"HlAdmin._token0":            repro.ExpectAddr(b.Tokens[0], "this HlAdmin's token(0)"),
		"HlAdmin._token1":            repro.ExpectAddr(b.Tokens[1], "this HlAdmin's token(1)"),
		"HlAdmin.proxyAdmin":         repro.ExpectAddr(b.ProxyAdmin, "this HlAdmin's proxyAdmin()"),
		"HlAdmin.merkleTreeHook":     repro.ExpectAddr(b.MerkleTreeHook, "this HlAdmin's merkleTreeHook()"),
		"HlAdmin.protocolFee":        repro.ExpectAddr(b.ProtocolFee, "this HlAdmin's protocolFee()"),
		"HlAdmin.ed25519":            repro.ExpectAddr(b.Ed25519, "this HlAdmin's ed25519()"),
		"HlAdmin.ed25519CodeHash":    repro.ExpectBytes32(b.Ed25519CodeHash, "this HlAdmin's ed25519CodeHash()"),
		"HlAdmin.localDomain":        repro.ExpectU64(uint64(b.LocalDomain), "this HlAdmin's localDomain()"),
		"HlAdmin.origin":             repro.ExpectU64(uint64(b.Origin), "this HlAdmin's origin()"),
		"HlAdmin.hlgov":              repro.ExpectBytes32(b.Hlgov, "this HlAdmin's hlgov(): r/hlgov"),
		"HlAdmin._route0":            repro.ExpectBytes32(b.Routes[0], "this HlAdmin's route(0)"),
		"HlAdmin._route1":            repro.ExpectBytes32(b.Routes[1], "this HlAdmin's route(1)"),
		"HlAdmin.mailboxPkgPathHash": repro.ExpectBytes32(b.MailboxPkgPathHash, "this HlAdmin's mailboxPkgPathHash()"),
		"HlAdmin.predecessor":        repro.ExpectAddr(f.Admin, "this HlAdmin, the predecessor it succeeds"),
		"HlAdmin.exec":               repro.ExpectAddr(crypto.CreateAddress(target, 1), "its own HlAdminExec, its CREATE at nonce 1"),
		"HlAdmin._execCodeHash":      repro.ExpectBytes32(execHash, "its HlAdminExec's EXTCODEHASH (held to the HlAdminExec build below; another code there and HlAdmin refuses ForeignExec)"),
		"HlAdmin._startSequence":     repro.ExpectU64(0, "0: a successor's number is the one its predecessor hands it (successorInit)"),
		// a successor's HlAdminExec: the successor's, bound as this HlAdmin is (finishSetupOnly's five beside it); a
		// successor's launch limits are never read
		"HlAdminExec.admin":              repro.ExpectAddr(target, "the successor that created it"),
		"HlAdminExec.mailbox":            repro.ExpectAddr(b.Mailbox, "this HlAdmin's mailbox()"),
		"HlAdminExec.client":             repro.ExpectAddr(b.Client, "this HlAdmin's client()"),
		"HlAdminExec.gate":               repro.ExpectAddr(b.Gate, "this HlAdmin's gate()"),
		"HlAdminExec.token0":             repro.ExpectAddr(b.Tokens[0], "this HlAdmin's token(0)"),
		"HlAdminExec.token1":             repro.ExpectAddr(b.Tokens[1], "this HlAdmin's token(1)"),
		"HlAdminExec.proxyAdmin":         repro.ExpectAddr(b.ProxyAdmin, "this HlAdmin's proxyAdmin()"),
		"HlAdminExec.merkleTreeHook":     repro.ExpectAddr(b.MerkleTreeHook, "this HlAdmin's merkleTreeHook()"),
		"HlAdminExec.protocolFee":        repro.ExpectAddr(b.ProtocolFee, "this HlAdmin's protocolFee()"),
		"HlAdminExec.ed25519":            repro.ExpectAddr(b.Ed25519, "this HlAdmin's ed25519()"),
		"HlAdminExec.ed25519CodeHash":    repro.ExpectBytes32(b.Ed25519CodeHash, "this HlAdmin's ed25519CodeHash()"),
		"HlAdminExec.localDomain":        repro.ExpectU64(uint64(b.LocalDomain), "this HlAdmin's localDomain()"),
		"HlAdminExec.origin":             repro.ExpectU64(uint64(b.Origin), "this HlAdmin's origin()"),
		"HlAdminExec.route0":             repro.ExpectBytes32(b.Routes[0], "this HlAdmin's route(0)"),
		"HlAdminExec.route1":             repro.ExpectBytes32(b.Routes[1], "this HlAdmin's route(1)"),
		"HlAdminExec.mailboxPkgPathHash": repro.ExpectBytes32(b.MailboxPkgPathHash, "this HlAdmin's mailboxPkgPathHash()"),
		"HlAdminExec.maxPerMessage0":     repro.ExpectAny(repro.KindUint, "a launch's limits: never read by a successor"),
		"HlAdminExec.maxPerMessage1":     repro.ExpectAny(repro.KindUint, "a launch's limits: never read by a successor"),
		"HlAdminExec.dayCap0":            repro.ExpectAny(repro.KindUint, "a launch's limits: never read by a successor"),
		"HlAdminExec.dayCap1":            repro.ExpectAny(repro.KindUint, "a launch's limits: never read by a successor"),
		"HlAdminExec.launch":             repro.ExpectU64(0, "false: a successor, not a launch"),
	}
	if created != 0 {
		ex["Indexed.deployedBlock"] = repro.ExpectU64(created, "the block of its creation transaction")
	}
	return ex
}

// finishSetupOnly are a successor's immutables, and its HlAdminExec's, that only its own finishSetup reads (checks 15
// and 26–28, against the live client and tokens) and that HlAdmin's kind 9 does not compare (fold F8): the verifier
// and its code hash, the routes, the hlmailbox path hash. They grant nothing, and after a verifier swap, a path change
// or a route moved this HlAdmin's own are stale: each is printed beside this HlAdmin's, a difference said
// (setupOnlyDiffers), never a reason the code is not shown immutable.
var finishSetupOnly = map[string]bool{
	"HlAdmin.ed25519": true, "HlAdmin.ed25519CodeHash": true, "HlAdmin._route0": true, "HlAdmin._route1": true,
	"HlAdmin.mailboxPkgPathHash": true, "HlAdminExec.ed25519": true, "HlAdminExec.ed25519CodeHash": true,
	"HlAdminExec.route0": true, "HlAdminExec.route1": true, "HlAdminExec.mailboxPkgPathHash": true,
}

// argExpectations are the values a target's constructor arguments must hold, by the normalized parameter name (a
// tuple's components by theirs): the immutables' sources, and for a successor HlAdmin the seats, watch slots and number
// a successor must leave to successorInit.
func argExpectations(f *liveFacts, succ bool) map[string]any {
	b := f.Bind
	ex := map[string]any{
		"localdomain":        b.LocalDomain,
		"mailbox":            b.Mailbox,
		"decimals":           uint8(6),
		"scalenumerator":     big.NewInt(1),
		"scaledenominator":   big.NewInt(1),
		"mailboxpkgpathhash": f.PkgPath,
		// the client's verifier (its constructor's ed25519_, verifier_ in E-M2b's first builds): the live client's,
		// never HlAdmin's ed25519() — a successor's is its proposer's own, which kind 9 does not compare (fold F8)
		"ed25519":  f.Verifier,
		"verifier": f.Verifier,
	}
	if succ {
		for k, v := range map[string]any{"client": b.Client, "gate": b.Gate, "token0": b.Tokens[0], "token1": b.Tokens[1], "proxyadmin": b.ProxyAdmin,
			"merkletreehook": b.MerkleTreeHook, "protocolfee": b.ProtocolFee, "origin": b.Origin, "hlgov": b.Hlgov, "seats": [4]common.Address{},
			"watch": [2]common.Address{}, "startsequence": uint64(0), "predecessor": f.Admin,
			// finishSetup's only (fold F8): beside this HlAdmin's, a difference never against the code
			"ed25519": setupOnlyValue{b.Ed25519}, "ed25519codehash": setupOnlyValue{b.Ed25519CodeHash}, "route0": setupOnlyValue{b.Routes[0]},
			"route1": setupOnlyValue{b.Routes[1]}, "mailboxpkgpathhash": setupOnlyValue{b.MailboxPkgPathHash}} {
			ex[k] = v
		}
		ex["maxpermessage"], ex["daycap"] = anyValue{}, anyValue{} // a launch's limits: never read by a successor
	}
	return ex
}

// anyValue is an argument any value of is right (printed all the same).
type anyValue struct{}

// setupOnlyValue is a successor's argument that only its own finishSetup reads and kind 9 does not compare (fold F8:
// finishSetupOnly's): printed beside this HlAdmin's value, a difference said, never a reason against the code.
type setupOnlyValue struct{ want any }

// argLines holds each decoded constructor argument to its expected value, components of a tuple one by one: the lines,
// and the reasons it cannot vouch (an argument that differs, or one with no expected value).
func argLines(ins abi.Arguments, vals []any, ex map[string]any) (lines, bad []string) {
	var walk func(name string, v any)
	walk = func(name string, v any) {
		rv := reflect.ValueOf(v)
		if rv.Kind() == reflect.Struct && rv.Type() != reflect.TypeOf(common.Address{}) {
			t := rv.Type()
			for i := 0; i < rv.NumField(); i++ {
				n := t.Field(i).Tag.Get("json")
				if n == "" {
					n = t.Field(i).Name
				}
				walk(n, rv.Field(i).Interface())
			}
			return
		}
		key := repro.Norm(name)
		text := fmt.Sprintf("%s = %s", name, valueString(v))
		want, ok := ex[key]
		so, setupOnly := want.(setupOnlyValue)
		switch {
		case !ok:
			bad = append(bad, "its argument "+text+" has no expected value here")
			lines = append(lines, text+" (no expected value)")
		case want == (anyValue{}):
			lines = append(lines, text+" (any: never read)")
		case setupOnly && sameValue(v, so.want):
			lines = append(lines, text+" ok")
		case setupOnly:
			lines = append(lines, text+" (this HlAdmin's "+valueString(so.want)+") "+setupOnlyDiffers)
		case !sameValue(v, want):
			bad = append(bad, fmt.Sprintf("its argument %s, but it must be %s", text, valueString(want)))
			lines = append(lines, text+" DIFFERS (must be "+valueString(want)+")")
		default:
			lines = append(lines, text+" ok")
		}
	}
	for i, in := range ins {
		if i < len(vals) {
			walk(in.Name, vals[i])
		}
	}
	return lines, bad
}

func sameValue(a, b any) bool {
	if x, ok := a.(*big.Int); ok {
		y, ok := b.(*big.Int)
		return ok && x.Cmp(y) == 0
	}
	return reflect.DeepEqual(a, b)
}

func valueString(v any) string {
	switch x := v.(type) {
	case common.Address:
		return x.Hex()
	case [32]byte:
		return "0x" + hex.EncodeToString(x[:])
	case []byte:
		return "0x" + hex.EncodeToString(x)
	case *big.Int:
		return x.String()
	case [4]common.Address, [2]common.Address:
		return fmt.Sprint(x)
	}
	return fmt.Sprint(v)
}

// creationCheck holds a target's creation transaction (-creation-tx) to the build it is: read through both providers
// (agreeing), a top-level creation whose receipt created the target, final at the pinned block, and its input the
// build's creation code with a canonical encoding of the constructor's arguments, each held to its expected value. It
// answers the creation's block, the lines to print and the reasons it cannot vouch ("" none).
func (w *Watch) creationCheck(ctx context.Context, at ethx.At, target common.Address, tx common.Hash, m *matched, f *liveFacts, succ bool) (uint64, []string, []string) {
	if tx == (common.Hash{}) {
		return 0, nil, []string{"no creation transaction given (-creation-tx): its constructor could have written storage no code hash or code shows (E2-RT1)"}
	}
	if m == nil || m.Build.Art == nil {
		return 0, nil, []string{"its creation " + tx.Hex() + " cannot be held to a build: no artifact's code is the target's"}
	}
	a, err := w.m.pair.A.Transaction(ctx, tx)
	if err != nil {
		return 0, nil, []string{"its creation " + tx.Hex() + " could not be read: " + ethReadText(err)}
	}
	b, err := w.m.pair.B.Transaction(ctx, tx)
	if err != nil || !bytes.Equal(a.Input, b.Input) || (a.To == nil) != (b.To == nil) || a.From != b.From {
		return 0, nil, []string{"its creation " + tx.Hex() + ": the providers give it differently"}
	}
	ra, err1 := w.m.pair.A.Receipt(ctx, tx)
	rb, err2 := w.m.pair.B.Receipt(ctx, tx)
	if err := errors.Join(err1, err2); err != nil {
		return 0, nil, []string{"its creation's receipt " + tx.Hex() + " could not be read: " + ethReadText(err)}
	}
	var bad []string
	lines := []string{fmt.Sprintf("its creation %s by %s", tx.Hex(), a.From.Hex())}
	if ra.ContractAddress != rb.ContractAddress || ra.BlockNumber.Cmp(rb.BlockNumber) != 0 || ra.Status != rb.Status {
		return 0, nil, []string{"its creation's receipt: the providers give it differently"}
	}
	created := ra.BlockNumber.Uint64()
	switch {
	case a.To != nil:
		bad = append(bad, fmt.Sprintf("its creation %s is a call to %s, not a top-level creation: made through a factory, its initcode is not shown", tx.Hex(), a.To.Hex()))
	case ra.Status != 1 || ra.ContractAddress != target:
		bad = append(bad, fmt.Sprintf("the transaction %s did not create %s (its receipt: status %d, contract %s)", tx.Hex(), target.Hex(), ra.Status, ra.ContractAddress.Hex()))
	}
	if final, err := w.m.pair.Finalized(ctx); err == nil && created > final.Number {
		bad = append(bad, fmt.Sprintf("its creation's block %d is not final yet (finalized %d)", created, final.Number))
	}
	c := repro.SplitCreation(a.Input, m.Build.Art)
	if c.Diff != "" {
		bad = append(bad, "its creation input is not the build "+m.Label+"'s: "+c.Diff)
		return created, lines, bad
	}
	ins := m.Build.Art.ABI.Constructor.Inputs
	argText, argBad := argLines(ins, c.Args, argExpectations(f, succ))
	lines = append(lines, fmt.Sprintf("its input is the build %s's creation code (%d bytes) and its constructor's arguments: %s", m.Label, len(m.Build.Art.Creation), strings.Join(argText, ", ")))
	return created, lines, append(bad, argBad...)
}

// immutableLines judges a matched build's immutables against the live values: the lines (each value beside the value
// it must have) and the reasons it cannot vouch (a value that differs, places that disagree, a value nothing here
// knows). soft names the immutables a difference in is said and held to nothing: finishSetupOnly in a kind 9's reading
// of a successor and its HlAdminExec, nil in every other (a kind 8's build is held whole, whatever its names).
func immutableLines(m *matched, ex map[string]repro.Expect, soft map[string]bool) (lines, bad []string) {
	if m == nil || m.Build.Art == nil {
		return nil, nil
	}
	if len(m.Readings) == 0 {
		return []string{"no immutable"}, nil
	}
	for _, r := range m.Readings {
		e, ok := ex[r.Name]
		switch {
		case !ok:
			text := fmt.Sprintf("%s = 0x%x: no expected value is known here, so nothing vouches for it", r.Name, r.Value)
			lines, bad = append(lines, text), append(bad, text)
		case r.Split:
			text := r.Name + " holds different values in its places in the code (the compiler writes one)"
			lines, bad = append(lines, text), append(bad, text)
		case e.Any:
			lines = append(lines, fmt.Sprintf("%s = %s (%s)", r.Name, e.Kind.Render(r.Value), e.What))
		case r.Value != e.Want && soft[r.Name]:
			lines = append(lines, fmt.Sprintf("%s = %s (%s: %s) %s", r.Name, e.Kind.Render(r.Value), e.What, e.Kind.Render(e.Want), setupOnlyDiffers))
		case r.Value != e.Want:
			text := fmt.Sprintf("%s = %s, but it must be %s (%s)", r.Name, e.Kind.Render(r.Value), e.Kind.Render(e.Want), e.What)
			lines, bad = append(lines, text+" DIFFERS"), append(bad, text)
		default:
			lines = append(lines, fmt.Sprintf("%s = %s ok (%s)", r.Name, e.Kind.Render(r.Value), e.What))
		}
	}
	return lines, bad
}

// DecodeCall decodes a kind 8's call against the implementation's ABI: the function and its arguments, or UNDECODED
// with why (E2-RT10: members must read what the call does, never 0x…).
func DecodeCall(call []byte, a *abi.ABI, label string) string {
	if len(call) == 0 {
		return "no call (ProxyAdmin.upgrade)"
	}
	raw := "0x" + hex.EncodeToString(call)
	if a == nil {
		return "UNDECODED: no reproducible build matches the implementation, so no ABI reads the call; the call is " + raw
	}
	if len(call) < 4 {
		return "UNDECODED: " + raw + " is shorter than a selector"
	}
	m, err := a.MethodById(call[:4])
	if err != nil {
		return fmt.Sprintf("UNDECODED: the selector 0x%x is no function of the build %s; the call is %s", call[:4], label, raw)
	}
	vals, err := m.Inputs.Unpack(call[4:])
	if err != nil {
		return fmt.Sprintf("UNDECODED: %s's arguments do not decode (%v); the call is %s", m.Sig, err, raw)
	}
	back, err := m.Inputs.Pack(vals...)
	if err != nil || !bytes.Equal(back, call[4:]) {
		return fmt.Sprintf("UNDECODED: the call carries bytes %s's arguments do not (not a canonical encoding); the call is %s", m.Sig, raw)
	}
	return fmt.Sprintf("then call %s(%s) through ProxyAdmin.upgradeAndCall — read what it does before the vote", m.Name, repro.ArgText(m.Inputs, vals))
}

// proxyContract is the build a kind 8's proxy runs (by the live HlAdmin's bindings).
func proxyContract(b *ethx.Bindings, proxy common.Address) string {
	switch proxy {
	case b.Mailbox:
		return "Mailbox"
	case b.Tokens[0], b.Tokens[1]:
		return "HypERC20"
	case b.Client:
		return "GnoLightClient"
	case b.Gate:
		return "PausableGate"
	}
	return ""
}

// The verdicts of a kind 8's or 9's reading (round 2's fact check, item 2; the plan red team's E2-RT1, E2-RT3; PLAN.md
// §5.3, §6.5, §13).
const (
	VerdictImmutable = "immutable"
	VerdictSpent     = "MUTABLE: would be spent"
	VerdictNoCode    = "NO CODE YET: pends"
	VerdictUnshown   = "NOT SHOWN IMMUTABLE"
)

// targetReading is one kind 8 or 9 target read before the vote.
type targetReading struct {
	label  string
	addr   common.Address
	parts  []string // what is read, in order
	spent  []string // why HlAdmin would spend the decision
	unseen []string // why nothing here vouches for the code (it would execute)
	noCode bool
	final  string // a verdict that ends the reading early (UNCONFIRMED)
}

func (r *targetReading) text() string {
	head := r.label + " " + r.addr.Hex() + ": "
	if r.final != "" {
		return head + r.final
	}
	if r.noCode {
		return head + "NO CODE YET: the decision pends (HlAdmin refuses its execute NoCodeYet) until code is deployed there — whoever can create at this address decides when it runs; vote only for code already deployed, at an address only its proposer can deploy to — " + VerdictNoCode
	}
	v := VerdictImmutable
	why := ""
	switch {
	case len(r.spent) > 0:
		v, why = VerdictSpent, strings.Join(r.spent, "; ")
	case len(r.unseen) > 0:
		v, why = VerdictUnshown, strings.Join(r.unseen, "; ")
	}
	if why != "" {
		v += " (" + why + ")"
	}
	return head + strings.Join(r.parts, "; ") + " — " + v
}

// readTarget reads the code a kind 8 or 9 names and holds it to the builds: both providers' code, the designator and
// the decision's hash (HlAdmin's spends), the build it is with its immutables beside the live values, its creation
// transaction against that build. It answers the reading, the build matched (nil for none) and the live facts.
func (w *Watch) readTarget(ctx context.Context, at ethx.At, admin common.Address, label string, addr common.Address, want common.Hash, succ bool) (*targetReading, *matched, *liveFacts) {
	r := &targetReading{label: label, addr: addr}
	code, err := w.agreedCode(ctx, at, addr)
	if err != nil {
		r.final = "UNCONFIRMED — the providers give its code differently, or it could not be read (" + ethReadText(err) + ")"
		return r, nil, nil
	}
	if len(code) == 0 {
		r.noCode = true
		return r, nil, nil
	}
	h := crypto.Keccak256Hash(code)
	r.parts = append(r.parts, "code hash "+h.Hex())
	if bytes.HasPrefix(code, []byte{0xef, 0x01, 0x00}) {
		r.parts = append(r.parts, "an EIP-7702 account (a delegation designator: its key can re-delegate it at will)")
		r.spent = append(r.spent, "an EIP-7702 account: its key re-delegates it at will")
	}
	switch {
	case want == (common.Hash{}):
		r.unseen = append(r.unseen, "no code hash to hold it to")
	case h == want:
		r.parts = append(r.parts, "equals the decision's")
	default:
		r.parts = append(r.parts, "NOT the decision's "+want.Hex())
		r.spent = append(r.spent, "its code hash is not the decision's")
	}
	if succ && addr == admin {
		r.spent = append(r.spent, "the successor is this HlAdmin itself")
	}
	f, err := w.live(ctx, at, admin)
	if err != nil {
		r.unseen = append(r.unseen, err.Error())
	}
	m, why := w.matchBuild(code)
	if m == nil {
		r.parts = append(r.parts, why)
		r.unseen = append(r.unseen, "its code matches no reproducible build given: only a reviewed build's code, its immutables the live values, is immutable")
		return r, nil, f
	}
	if m.Build.Art == nil {
		r.parts = append(r.parts, "is the reproducible build "+m.Label+" by its hash")
		r.unseen = append(r.unseen, "the build "+m.Label+" is a hash alone: its constructor and immutables are not shown (give forge's artifact)")
		return r, m, f
	}
	r.parts = append(r.parts, fmt.Sprintf("is the reproducible build %s outside its %d masked immutable(s)", m.Label, len(m.Build.Art.Immutables)))
	if f == nil {
		return r, m, f
	}
	created, clines, cbad := w.creationCheck(ctx, at, addr, w.CreationTxs[addr], m, f, succ)
	execHash := common.Hash{}
	if succ {
		if ec, err := w.agreedCode(ctx, at, crypto.CreateAddress(addr, 1)); err == nil && len(ec) > 0 {
			execHash = crypto.Keccak256Hash(ec)
		}
	}
	var soft map[string]bool
	if succ {
		soft = finishSetupOnly
	}
	ilines, ibad := immutableLines(m, expectations(f, addr, created, execHash), soft)
	r.parts = append(r.parts, "its immutables: "+strings.Join(ilines, ", "))
	r.unseen = append(r.unseen, ibad...)
	r.parts = append(r.parts, clines...)
	r.unseen = append(r.unseen, cbad...)
	return r, m, f
}

// ImplementationCheck is a kind 8's reading before the vote (PLAN.md §6.5, §8): the implementation's code held to a
// reproducible build, its immutables beside the live values, its creation transaction against the build, the build the
// proxy runs (a HypERC20 for a token, …), the call decoded against the build's ABI, the storage layouts' difference,
// and the verdict: "immutable" only when all of it holds; "MUTABLE: would be spent" when HlAdmin would spend it;
// "NO CODE YET: pends"; "NOT SHOWN IMMUTABLE" when HlAdmin would execute it and nothing here vouches for its code. The
// layouts are the watch's (-layout-old, -layout-new); without both — or with layoutWhy saying why they are not this
// implementation's — the layout is NOT CHECKED, and an unchecked layout is never "immutable" (E3-CF3: §0's one check
// against a reordered layout, which could reset the mailbox's delivered marks or the tokens' balances).
func (w *Watch) ImplementationCheck(ctx context.Context, at ethx.At, admin, proxy, impl common.Address, want common.Hash, call []byte, layoutWhy string) string {
	r, m, f := w.readTarget(ctx, at, admin, "implementation", impl, want, false)
	if r.final != "" || r.noCode {
		return r.text()
	}
	if f != nil && proxy != (common.Address{}) {
		role := proxyContract(f.Bind, proxy)
		switch {
		case role == "":
			r.spent = append(r.spent, "the proxy "+proxy.Hex()+" is none of the five a kind 8 upgrades")
		case m != nil && m.Build.Art != nil && m.Build.Art.Name != role:
			r.unseen = append(r.unseen, fmt.Sprintf("the proxy %s runs %s, and the implementation is a build of %s", proxy.Hex(), role, m.Build.Art.Name))
		}
	}
	var a *abi.ABI
	label := ""
	if m != nil && m.Build.Art != nil {
		a, label = &m.Build.Art.ABI, m.Label
	}
	callText := DecodeCall(call, a, label)
	r.parts = append(r.parts, callText)
	if strings.HasPrefix(callText, "UNDECODED") {
		r.unseen = append(r.unseen, "its call is UNDECODED: nobody can read what it does")
	}
	switch {
	case layoutWhy != "":
		r.parts = append(r.parts, "LAYOUT NOT CHECKED: "+layoutWhy)
		r.unseen = append(r.unseen, "its storage layout is not checked against the running one's ("+layoutWhy+")")
	case len(w.LayoutOld) == 0 || len(w.LayoutNew) == 0:
		r.parts = append(r.parts, "LAYOUT NOT CHECKED: no storage layouts given (-layout-old, -layout-new): the append-only rule (§0) is not checked")
		r.unseen = append(r.unseen, "its storage layout is not checked against the running one's: give both (-layout-old, -layout-new) — a reordered layout could reset the mailbox's delivered marks or the tokens' balances")
	default:
		d := LayoutDiff(w.LayoutOld, w.LayoutNew)
		r.parts = append(r.parts, d)
		if !strings.HasPrefix(d, LayoutAppendOnly) {
			r.unseen = append(r.unseen, "its storage layout is not shown to be the running one's, appended to")
		}
	}
	return r.text()
}

// SuccessorCheck is a kind 9's reading before the vote: the successor's code as readTarget reads it, then its state as
// HlAdmin's execute will read it (HlAdminExec._transfer) — predecessor() the HlAdmin that holds the decision,
// inherited() false, successor() none, clockFactor() that HlAdmin's — its sixteen bindings beside this HlAdmin's
// (E2-RT3: the mailbox, the client, the gate, the tokens, the ProxyAdmin, the hooks, r/hlgov and the domains, which
// HlAdmin compares; and, fold F8, the verifier and its hash, the routes and the hlmailbox path, which it does not),
// and its HlAdminExec held to the build named HlAdminExec; one verdict.
func (w *Watch) SuccessorCheck(ctx context.Context, at ethx.At, admin common.Address, factor uint64, succ common.Address, want common.Hash) string {
	r, _, f := w.readTarget(ctx, at, admin, "successor", succ, want, true)
	if r.final != "" || r.noCode || len(r.spent) > 0 {
		return r.text() // HlAdmin spends it before it reads the successor's answers
	}
	st, err := ethx.ReadSuccessor(ctx, w.m.pair, succ, at)
	if err != nil {
		r.unseen = append(r.unseen, "its predecessor(), inherited(), successor() and clockFactor() could not be read ("+ethReadText(err)+"): HlAdmin's execute reverts while they do not answer, and the decision pends")
		return r.text()
	}
	r.parts = append(r.parts, fmt.Sprintf("predecessor() %s, inherited() %v, successor() %s, clockFactor() %d", st.Predecessor.Hex(), st.Inherited, st.Successor.Hex(), st.ClockFactor))
	if st.Predecessor != admin {
		r.spent = append(r.spent, "its predecessor() is "+st.Predecessor.Hex()+", not this HlAdmin "+admin.Hex())
	}
	if st.Inherited {
		r.spent = append(r.spent, "it has inherited already")
	}
	if st.Successor != (common.Address{}) {
		r.spent = append(r.spent, "it names a successor of its own, "+st.Successor.Hex())
	}
	if st.ClockFactor != factor {
		r.spent = append(r.spent, fmt.Sprintf("its clock factor is %d, not this HlAdmin's %d", st.ClockFactor, factor))
	}
	if f != nil {
		if sb, err := ethx.ReadBindings(ctx, w.m.pair, succ, at); err != nil {
			r.unseen = append(r.unseen, "the eleven bindings kind 9 compares could not all be read ("+ethReadText(err)+"): HlAdmin's execute reverts while one of them does not answer, and the decision pends (GovDAO cancels it)")
		} else {
			lines, differ := bindingLines(sb, f.Bind)
			r.parts = append(r.parts, "its bindings beside this HlAdmin's: "+strings.Join(lines, ", "))
			if len(differ) > 0 {
				// HlAdmin compares the eleven that grant power and spends it "bindings" (folds F5, F8); dead on
				// arrival, said before the vote
				r.spent = append(r.spent, "THE SUCCESSOR IS BOUND ELSEWHERE ("+strings.Join(differ, "; ")+"): HlAdmin spends it \"bindings\" — it would take every authority over another mailbox, client, gate, token or r/hlgov")
			}
			r.parts = append(r.parts, w.setupReading(ctx, at, sb, f))
		}
	}
	exec := crypto.CreateAddress(succ, 1)
	var eb *matched
	for l, b := range w.Builds {
		if b.Art != nil && b.Art.Name == "HlAdminExec" {
			if code, err := w.agreedCode(ctx, at, exec); err == nil {
				if readings, diff := repro.CompareCode(code, b.Art); diff == "" {
					eb = &matched{Label: l, Build: b, Readings: readings}
				}
			}
		}
	}
	switch {
	case eb == nil:
		r.unseen = append(r.unseen, "its HlAdminExec "+exec.Hex()+" (its CREATE at nonce 1, the code it runs every kind in) is held to no build: give forge's HlAdminExec artifact (-build)")
	case f != nil:
		lines, bad := immutableLines(eb, expectations(f, succ, 0, common.Hash{}), finishSetupOnly)
		r.parts = append(r.parts, fmt.Sprintf("its HlAdminExec %s is the build %s; its immutables: %s", exec.Hex(), eb.Label, strings.Join(lines, ", ")))
		r.unseen = append(r.unseen, bad...)
	}
	return r.text()
}

// setupReading says how the successor's own finishSetup would judge the five bindings kind 9 does not compare (fold
// F8), against the live deployment its checks read: 15 each route against its token's router for gno.land, 26 the path
// hash against the client's, 27 the verifier against the client's ED25519(), 28 that verifier's code (present, no
// EIP-7702 designator) against its code hash. Beside this HlAdmin's (bindingLines) a successor bound to the live values
// after a swap, a path change or a route moved reads "differs", and one bound to this HlAdmin's stale ones "ok": this
// says which of them can pass those checks. It is said, never a verdict — a successor whose setup cannot finish takes
// every authority all the same, nothing on chain reads its setupDone, and finishSetup's other checks are not read here.
func (w *Watch) setupReading(ctx context.Context, at ethx.At, s *ethx.Bindings, f *liveFacts) string {
	if len(s.Unread) > 0 {
		// a successor of a build without them (E3-CF10): kind 9 never reads them, and its setup is not read from here
		var names []string
		for _, n := range ethx.OptionalBindings {
			if !s.Answered(n) {
				names = append(names, n)
			}
		}
		return "its own finishSetup is not read here: " + strings.Join(names, ", ") + " do not answer (kind 9 never reads them; said, not a verdict)"
	}
	var pass, fail []string
	hx := func(b [32]byte) string { return "0x" + hex.EncodeToString(b[:]) }
	for i, t := range s.Tokens {
		switch r, err := ethx.RouterFor(ctx, w.m.pair, t, s.Origin, at); {
		case err != nil:
			fail = append(fail, fmt.Sprintf("15 not read (token %d's router: %s)", i, ethReadText(err)))
		case r == s.Routes[i]:
			pass = append(pass, fmt.Sprintf("15 (route(%d) is token %d's router for gno.land)", i, i))
		default:
			fail = append(fail, fmt.Sprintf("15 (route(%d) %s is not token %d's router for gno.land, %s)", i, hx(s.Routes[i]), i, hx(r)))
		}
	}
	if s.MailboxPkgPathHash == f.PkgPath {
		pass = append(pass, "26 (mailboxPkgPathHash() is the client's)")
	} else {
		fail = append(fail, fmt.Sprintf("26 (mailboxPkgPathHash() %s is not the client's %s)", hx(s.MailboxPkgPathHash), hx(f.PkgPath)))
	}
	if s.Ed25519 == f.Verifier {
		pass = append(pass, "27 (ed25519() is the client's ED25519())")
	} else {
		fail = append(fail, fmt.Sprintf("27 (ed25519() %s is not the client's ED25519() %s)", s.Ed25519.Hex(), f.Verifier.Hex()))
	}
	switch code, err := w.agreedCode(ctx, at, s.Ed25519); {
	case err != nil:
		fail = append(fail, "28 not read ("+ethReadText(err)+")")
	case len(code) == 0 || bytes.HasPrefix(code, []byte{0xef, 0x01, 0x00}):
		fail = append(fail, "28 (ed25519() "+s.Ed25519.Hex()+" holds no contract code)")
	case crypto.Keccak256Hash(code) != common.Hash(s.Ed25519CodeHash):
		fail = append(fail, fmt.Sprintf("28 (the code at ed25519() hashes to %s, not ed25519CodeHash() %s)", crypto.Keccak256Hash(code).Hex(), hx(s.Ed25519CodeHash)))
	default:
		pass = append(pass, "28 (ed25519CodeHash() is its verifier's code)")
	}
	text := "its own finishSetup on the live deployment, for the five kind 9 does not compare (said, not a verdict):"
	if len(pass) > 0 {
		text += " passes " + strings.Join(pass, ", ")
	}
	if len(fail) > 0 {
		if len(pass) > 0 {
			text += ";"
		}
		text += " FAILS " + strings.Join(fail, ", ") + " — such a successor takes every authority all the same; its setup cannot finish while they differ"
	}
	return text
}

// setupOnlyDiffers is what a difference in a binding kind 9 does not compare says (fold F8): only the successor's own
// finishSetup reads it, so it is printed, never spent and never a reason against the code.
const setupOnlyDiffers = "differs (finishSetup's only; not checked)"

// bindingLines compares a successor's sixteen bindings with this HlAdmin's, view by view: first the eleven HlAdmin's
// kind 9 compares — the bindings that grant power, each difference in differ (HlAdmin spends the decision
// "bindings") — then the five only a successor's own finishSetup reads (checks 15 and 26–28): the verifier and its
// code hash, the routes, the hlmailbox path hash. A kind 8 (a verifier swap, a path change) or a kind 6 (a route
// moved) leaves this HlAdmin's own stale, so a difference there is said and never spends (fold F8).
func bindingLines(s, h *ethx.Bindings) (lines, differ []string) {
	one := func(name, got, want string, compared bool) {
		switch {
		case got == want:
			lines = append(lines, name+" "+got+" ok")
		case compared:
			lines = append(lines, fmt.Sprintf("%s %s (this HlAdmin's %s) DIFFERS", name, got, want))
			differ = append(differ, fmt.Sprintf("%s %s, not this HlAdmin's %s", name, got, want))
		default:
			lines = append(lines, fmt.Sprintf("%s %s (this HlAdmin's %s) %s", name, got, want, setupOnlyDiffers))
		}
	}
	hx := func(b [32]byte) string { return "0x" + hex.EncodeToString(b[:]) }
	one("mailbox()", s.Mailbox.Hex(), h.Mailbox.Hex(), true)
	one("client()", s.Client.Hex(), h.Client.Hex(), true)
	one("gate()", s.Gate.Hex(), h.Gate.Hex(), true)
	one("token(0)", s.Tokens[0].Hex(), h.Tokens[0].Hex(), true)
	one("token(1)", s.Tokens[1].Hex(), h.Tokens[1].Hex(), true)
	one("proxyAdmin()", s.ProxyAdmin.Hex(), h.ProxyAdmin.Hex(), true)
	one("merkleTreeHook()", s.MerkleTreeHook.Hex(), h.MerkleTreeHook.Hex(), true)
	one("protocolFee()", s.ProtocolFee.Hex(), h.ProtocolFee.Hex(), true)
	one("hlgov()", hx(s.Hlgov), hx(h.Hlgov), true)
	one("origin()", fmt.Sprint(s.Origin), fmt.Sprint(h.Origin), true)
	one("localDomain()", fmt.Sprint(s.LocalDomain), fmt.Sprint(h.LocalDomain), true)
	// the five kind 9 never reads: a successor that does not answer one is said so (E3-CF10), never compared as zero
	opt := func(name, got, want string) {
		if why, unread := s.Unread[name]; unread {
			lines = append(lines, fmt.Sprintf("%s does not answer (%s; this HlAdmin's %s): kind 9 never reads it", name, why, want))
			return
		}
		one(name, got, want, false)
	}
	opt("ed25519()", s.Ed25519.Hex(), h.Ed25519.Hex())
	opt("ed25519CodeHash()", hx(s.Ed25519CodeHash), hx(h.Ed25519CodeHash))
	opt("route(0)", hx(s.Routes[0]), hx(h.Routes[0]))
	opt("route(1)", hx(s.Routes[1]), hx(h.Routes[1]))
	opt("mailboxPkgPathHash()", hx(s.MailboxPkgPathHash), hx(h.MailboxPkgPathHash))
	return lines, differ
}
