package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"

	"github.com/gnolang/bridges/ethereum/bridge/ethx"
	"github.com/gnolang/bridges/ethereum/bridge/repro"
)

// Fold F8 — kind 9's bindings: the twelve that grant power (eth/bridge/M2-NOTES.md, "Fold F8").

// f8Differs is what hlwatch says of a difference in a binding kind 9 does not compare.
const f8Differs = "differs (finishSetup's only; not checked)"

const f8HlAdminABI = `[{"type":"constructor","inputs":[{"name":"c","type":"tuple","components":[{"name":"mailbox","type":"address"},{"name":"ed25519","type":"address"},{"name":"ed25519CodeHash","type":"bytes32"},{"name":"hlgov","type":"bytes32"},{"name":"route0","type":"bytes32"},{"name":"seats","type":"address[4]"},{"name":"startSequence","type":"uint64"},{"name":"predecessor","type":"address"}]}]}]`

// f8With is a copy of m with k set to v.
func f8With[V any](m map[string]V, k string, v V) map[string]V {
	out := make(map[string]V, len(m))
	for x, y := range m {
		out[x] = y
	}
	out[k] = v
	return out
}

// HlAdmin's kind 9 compares the eleven views that grant power — with the HlAdminExec its runtime pins, the twelve —
// and no longer the verifier, its code hash, the routes or the hlmailbox path hash: only a successor's own finishSetup
// reads those, and a kind 8 (a verifier swap, a path change) or 6 (a route moved) leaves this HlAdmin's own stale. So
// before the vote a successor bound to the live verifier after a swap — its views, its immutables, its constructor's
// arguments, its HlAdminExec's immutables — is printed beside this HlAdmin's, each difference "differs
// (finishSetup's only; not checked)", and is immutable, never "would be spent"; and its own finishSetup is read
// against the live deployment, which says the one bound to HlAdmin's stale verifier cannot pass checks 27 and 28 while
// the one bound to the live verifier passes all four. With all five elsewhere it is still immutable; a binding that
// grants power, elsewhere beside them, is spent for itself alone; and in its code or its creation such a binding is
// still held whole (NOT SHOWN IMMUTABLE), while a route is only said to differ.
func TestSuccessorAfterAVerifierSwap(t *testing.T) {
	ctx := context.Background()
	r, w, codes := newTargetRig(t)
	// the swap (a kind 8 executed): the client calls v2, whose code is on chain; this HlAdmin's ed25519() is still
	// f7Verifier; the tokens' routers for gno.land are the routes
	v2 := common.HexToAddress("0x000000000000000000000000000000000000e256")
	codes[v2] = []byte("the fixed verifier's code")
	v2Hash := crypto.Keccak256Hash(codes[v2])
	r.chain.view(m3bClient, "ED25519", func(uint64, []any) []any { return []any{v2} })
	for i, tok := range f7Bindings.Tokens {
		route := f7Bindings.Routes[i]
		r.chain.view(tok, "routers", func(uint64, []any) []any { return []any{route} })
	}
	succ := common.HexToAddress("0x000000000000000000000000000000000000a9a9")
	exec := crypto.CreateAddress(succ, 1)
	hla := synthArt(t, "HlAdmin", f8HlAdminABI, 700, map[string][]int{"HlAdmin.mailbox": {20}, "HlAdmin.ed25519": {70}, "HlAdmin.ed25519CodeHash": {120},
		"HlAdmin.predecessor": {220}, "HlAdmin.exec": {320}, "HlAdmin._execCodeHash": {450}, "HlAdmin._startSequence": {500}, "HlAdmin.hlgov": {560}, "HlAdmin._route0": {620}})
	exa := synthArt(t, "HlAdminExec", `[]`, 300, map[string][]int{"HlAdminExec.admin": {10}, "HlAdminExec.ed25519": {60}, "HlAdminExec.launch": {110}})
	codes[exec] = fillArt(exa, map[string][32]byte{"HlAdminExec.admin": w32(succ), "HlAdminExec.ed25519": w32(v2), "HlAdminExec.launch": u32(0)})
	ims := map[string][32]byte{"HlAdmin.mailbox": w32(m3bMailbox), "HlAdmin.ed25519": w32(v2), "HlAdmin.ed25519CodeHash": v2Hash,
		"HlAdmin.predecessor": w32(m3bAdmin), "HlAdmin.exec": w32(exec), "HlAdmin._execCodeHash": crypto.Keccak256Hash(codes[exec]), "HlAdmin._startSequence": u32(0),
		"HlAdmin.hlgov": f7Bindings.Hlgov, "HlAdmin._route0": f7Bindings.Routes[0]}
	args := map[string]any{"mailbox": m3bMailbox, "ed25519": v2, "ed25519codehash": [32]byte(v2Hash), "hlgov": f7Bindings.Hlgov,
		"route0": f7Bindings.Routes[0], "seats": [4]common.Address{}, "startsequence": uint64(0), "predecessor": m3bAdmin}
	tx := common.HexToHash("0x5ecc")
	w.Builds = map[string]Build{"hladmin-v2": BuildOf(hla), "hladminexec-v2": BuildOf(exa)}
	w.CreationTxs[succ] = tx
	r.chain.view(succ, "predecessor", func(uint64, []any) []any { return []any{m3bAdmin} })
	r.chain.view(succ, "inherited", func(uint64, []any) []any { return []any{false} })
	r.chain.view(succ, "successor", func(uint64, []any) []any { return []any{common.Address{}} })
	r.chain.view(succ, "clockFactor", func(uint64, []any) []any { return []any{uint64(1)} })
	at := ethx.AtNumber(100)
	// the successor deployed with these immutables in its code and these arguments in its creation, answering these
	// views; its reading before the vote, the decision's code hash its own
	check := func(ims map[string][32]byte, args map[string]any, b ethx.Bindings) string {
		codes[succ] = fillArt(hla, ims)
		input, err := hla.CreationCode(repro.Values{"c": repro.Values(args)})
		if err != nil {
			t.Fatal(err)
		}
		r.chain.creation(tx, common.HexToAddress("0x0000000000000000000000000000000000000de9"), input, succ, 80)
		liveBindings(r.chain, succ, b)
		return w.SuccessorCheck(ctx, at, m3bAdmin, 1, succ, crypto.Keccak256Hash(codes[succ]))
	}
	hx := func(b [32]byte) string { return common.Hash(b).Hex() }
	const setup = "its own finishSetup on the live deployment, for the five kind 9 does not compare (said, not a verdict): "
	contains := func(what, got string, want ...string) {
		t.Helper()
		for _, s := range want {
			if !strings.Contains(got, s) {
				t.Fatalf("%s lacks %q:\n%s", what, s, got)
			}
		}
	}
	verdict := func(what, got, v string) {
		t.Helper()
		if !strings.HasSuffix(got, " — "+v) && !strings.Contains(got, " — "+v+" (") {
			t.Fatalf("%s is not %s:\n%s", what, v, got)
		}
	}
	swapped := f7Bindings
	swapped.Ed25519, swapped.Ed25519CodeHash = v2, v2Hash
	got := check(ims, args, swapped)
	contains("a successor bound to the live verifier", got,
		"its bindings beside this HlAdmin's: mailbox() "+m3bMailbox.Hex()+" ok", "client() "+m3bClient.Hex()+" ok", "hlgov() "+hx(f7Bindings.Hlgov)+" ok",
		"ed25519() "+v2.Hex()+" (this HlAdmin's "+f7Verifier.Hex()+") "+f8Differs,
		"ed25519CodeHash() "+v2Hash.Hex()+" (this HlAdmin's "+hx(f7Bindings.Ed25519CodeHash)+") "+f8Differs,
		"route(0) "+hx(f7Bindings.Routes[0])+" ok", "mailboxPkgPathHash() "+f7PkgPath.Hex()+" ok",
		setup+"passes 15 (route(0) is token 0's router for gno.land), 15 (route(1) is token 1's router for gno.land), 26 (mailboxPkgPathHash() is the client's), 27 (ed25519() is the client's ED25519()), 28 (ed25519CodeHash() is its verifier's code)",
		"HlAdmin.ed25519 = "+v2.Hex()+" (this HlAdmin's ed25519(): "+f7Verifier.Hex()+") "+f8Differs,
		"HlAdmin.ed25519CodeHash = "+v2Hash.Hex()+" (this HlAdmin's ed25519CodeHash(): "+hx(f7Bindings.Ed25519CodeHash)+") "+f8Differs,
		"HlAdmin._route0 = "+hx(f7Bindings.Routes[0])+" ok", "HlAdmin.hlgov = "+hx(f7Bindings.Hlgov)+" ok",
		"ed25519 = "+v2.Hex()+" (this HlAdmin's "+f7Verifier.Hex()+") "+f8Differs,
		"ed25519CodeHash = "+v2Hash.Hex()+" (this HlAdmin's "+hx(f7Bindings.Ed25519CodeHash)+") "+f8Differs,
		"route0 = "+hx(f7Bindings.Routes[0])+" ok", "hlgov = "+hx(f7Bindings.Hlgov)+" ok",
		"HlAdminExec.ed25519 = "+v2.Hex()+" (this HlAdmin's ed25519(): "+f7Verifier.Hex()+") "+f8Differs)
	if strings.Contains(got, "BOUND ELSEWHERE") || strings.Contains(got, "FAILS") {
		t.Fatalf("a successor bound to the live verifier after a swap is spent or fails its setup:\n%s", got)
	}
	verdict("a successor bound to the live verifier after a swap", got, VerdictImmutable)
	// bound to this HlAdmin's stale verifier: every binding "ok" beside it, and its setup cannot pass 27 and 28
	stale := f8With(f8With(ims, "HlAdmin.ed25519", w32(f7Verifier)), "HlAdmin.ed25519CodeHash", f7Bindings.Ed25519CodeHash)
	staleArgs := f8With(f8With(args, "ed25519", any(f7Verifier)), "ed25519codehash", any(f7Bindings.Ed25519CodeHash))
	got = check(stale, staleArgs, f7Bindings)
	contains("a successor bound to the stale verifier", got,
		"ed25519() "+f7Verifier.Hex()+" ok", "HlAdmin.ed25519 = "+f7Verifier.Hex()+" ok",
		setup+"passes 15 (route(0) is token 0's router for gno.land), 15 (route(1) is token 1's router for gno.land), 26 (mailboxPkgPathHash() is the client's); FAILS 27 (ed25519() "+f7Verifier.Hex()+" is not the client's ED25519() "+v2.Hex()+"), 28 (ed25519() "+f7Verifier.Hex()+" holds no contract code)")
	verdict("a successor bound to the stale verifier", got, VerdictImmutable)
	// the live verifier under another code hash: its setup cannot pass 28
	misHashed := swapped
	misHashed.Ed25519CodeHash = f7Bindings.Ed25519CodeHash
	got = check(ims, args, misHashed)
	contains("a successor bound to the live verifier under another code hash", got,
		"27 (ed25519() is the client's ED25519()); FAILS 28 (the code at ed25519() hashes to "+v2Hash.Hex()+", not ed25519CodeHash() "+hx(f7Bindings.Ed25519CodeHash)+")")
	// all five elsewhere (the verifier, its hash, both routes, the path): still immutable, each said
	all := swapped
	all.Routes = [2][32]byte{{0xa0}, {0xa1}}
	all.MailboxPkgPathHash = [32]byte{0xb0}
	got = check(ims, args, all)
	contains("a successor whose five setup-only bindings differ", got,
		"ed25519() "+v2.Hex()+" (this HlAdmin's "+f7Verifier.Hex()+") "+f8Differs,
		"ed25519CodeHash() "+v2Hash.Hex()+" (this HlAdmin's "+hx(f7Bindings.Ed25519CodeHash)+") "+f8Differs,
		"route(0) "+hx(all.Routes[0])+" (this HlAdmin's "+hx(f7Bindings.Routes[0])+") "+f8Differs,
		"route(1) "+hx(all.Routes[1])+" (this HlAdmin's "+hx(f7Bindings.Routes[1])+") "+f8Differs,
		"mailboxPkgPathHash() "+hx(all.MailboxPkgPathHash)+" (this HlAdmin's "+f7PkgPath.Hex()+") "+f8Differs,
		"FAILS 15 (route(0) "+hx(all.Routes[0])+" is not token 0's router for gno.land, "+hx(f7Bindings.Routes[0])+")",
		"26 (mailboxPkgPathHash() "+hx(all.MailboxPkgPathHash)+" is not the client's "+f7PkgPath.Hex()+")")
	if strings.Contains(got, "BOUND ELSEWHERE") {
		t.Fatalf("a successor whose five setup-only bindings differ is spent:\n%s", got)
	}
	verdict("a successor whose five setup-only bindings differ", got, VerdictImmutable)
	// a binding that grants power, elsewhere beside them: spent, for it alone
	power := all
	power.Hlgov = [32]byte{0xc0}
	got = check(ims, args, power)
	contains("a successor bound to another r/hlgov", got, "THE SUCCESSOR IS BOUND ELSEWHERE (hlgov() "+hx(power.Hlgov)+", not this HlAdmin's "+hx(f7Bindings.Hlgov)+"): HlAdmin spends it \"bindings\"")
	verdict("a successor bound to another r/hlgov", got, VerdictSpent)
	// in its code or its creation, a binding that grants power is held whole; a route is only said to differ
	got = check(f8With(ims, "HlAdmin.hlgov", [32]byte{0xc0}), args, swapped)
	contains("a successor whose code holds another r/hlgov", got, "HlAdmin.hlgov = "+hx([32]byte{0xc0})+", but it must be "+hx(f7Bindings.Hlgov))
	verdict("a successor whose code holds another r/hlgov", got, VerdictUnshown)
	got = check(ims, f8With(args, "hlgov", any([32]byte{0xc0})), swapped)
	contains("a successor created with another r/hlgov", got, "its argument hlgov = "+hx([32]byte{0xc0})+", but it must be "+hx(f7Bindings.Hlgov))
	verdict("a successor created with another r/hlgov", got, VerdictUnshown)
	got = check(f8With(ims, "HlAdmin._route0", [32]byte{0xa0}), f8With(args, "route0", any([32]byte{0xa0})), swapped)
	contains("a successor whose code holds another route", got,
		"HlAdmin._route0 = "+hx([32]byte{0xa0})+" (this HlAdmin's route(0): "+hx(f7Bindings.Routes[0])+") "+f8Differs,
		"route0 = "+hx([32]byte{0xa0})+" (this HlAdmin's "+hx(f7Bindings.Routes[0])+") "+f8Differs)
	verdict("a successor whose code holds another route", got, VerdictImmutable)
	// after a path change and a route moved too: the client reads path2, token 0 routes gno.land to route2; a successor
	// bound to them passes 15, 26, 27 and 28, beside this HlAdmin's stale path and route
	path2, route2 := [32]byte{0xb2}, [32]byte{0xa2}
	r.chain.view(m3bClient, "mailboxPkgPathHash", func(uint64, []any) []any { return []any{path2} })
	r.chain.view(m3bGNOT, "routers", func(uint64, []any) []any { return []any{route2} })
	moved := swapped
	moved.MailboxPkgPathHash, moved.Routes[0] = path2, route2
	got = check(f8With(ims, "HlAdmin._route0", route2), f8With(args, "route0", any(route2)), moved)
	contains("a successor bound to the live path and route", got,
		"route(0) "+hx(route2)+" (this HlAdmin's "+hx(f7Bindings.Routes[0])+") "+f8Differs,
		"mailboxPkgPathHash() "+hx(path2)+" (this HlAdmin's "+f7PkgPath.Hex()+") "+f8Differs,
		setup+"passes 15 (route(0) is token 0's router for gno.land), 15 (route(1) is token 1's router for gno.land), 26 (mailboxPkgPathHash() is the client's), 27 (ed25519() is the client's ED25519()), 28 (ed25519CodeHash() is its verifier's code)")
	if strings.Contains(got, "FAILS") {
		t.Fatalf("a successor bound to the live path and route fails its setup:\n%s", got)
	}
	verdict("a successor bound to the live path and route", got, VerdictImmutable)
}

const f8ClientABI = `[{"type":"constructor","inputs":[{"name":"mailboxPkgPathHash_","type":"bytes32"},{"name":"ed25519_","type":"address"}]}]`

// The reliant fold F8 leaves without its chain: before it, HlAdmin's ed25519() was the launch's verifier in every
// successor (kind 9 compared it), and hlwatch held a kind 8 client implementation's ED25519 to it. A successor's is now
// its proposer's own, unchecked: a successor carrying a verifier M of the proposer's, then a client upgrade to M, would
// read "ok" against HlAdmin's. So the client's verifier — its ED25519 immutable and its constructor's ed25519_ — is held
// to the live client's ED25519(): an upgrade that keeps it is vouched (after a swap too, where HlAdmin's is stale), and
// any other — back to HlAdmin's, or to the one a successor named — is a verifier swap, NOT SHOWN IMMUTABLE. And a kind
// 8's build is held whole: an immutable named as a successor's setup-only one is no softer in it.
func TestClientImplementationHeldToTheLiveVerifier(t *testing.T) {
	ctx := context.Background()
	r, w, codes := newTargetRig(t)
	art := synthArt(t, "GnoLightClient", f8ClientABI, 400, map[string][]int{"GnoLightClient.mailboxPkgPathHash": {40}, "GnoLightClient.ED25519": {120, 260}})
	impl := common.HexToAddress("0x000000000000000000000000000000000000c11e")
	tx := common.HexToHash("0xc11e7a")
	w.Builds = map[string]Build{"client-v2": BuildOf(art)}
	w.CreationTxs[impl] = tx
	at := ethx.AtNumber(100)
	// an implementation of build a over verifier v, created with it (extra: its other immutables)
	checkOf := func(a *repro.Artifact, v common.Address, extra map[string][32]byte) string {
		vals := map[string][32]byte{"GnoLightClient.mailboxPkgPathHash": f7PkgPath, "GnoLightClient.ED25519": w32(v)}
		for k, x := range extra {
			vals[k] = x
		}
		codes[impl] = fillArt(a, vals)
		input, err := a.CreationCode(repro.Values{"mailboxpkgpathhash": [32]byte(f7PkgPath), "ed25519": v})
		if err != nil {
			t.Fatal(err)
		}
		r.chain.creation(tx, common.HexToAddress("0x00000000000000000000000000000000000de9e7"), input, impl, 77)
		return w.ImplementationCheck(ctx, at, m3bAdmin, m3bClient, impl, crypto.Keccak256Hash(codes[impl]), nil, "")
	}
	check := func(v common.Address) string { return checkOf(art, v, nil) }
	live := func(v common.Address) {
		r.chain.view(m3bClient, "ED25519", func(uint64, []any) []any { return []any{v} })
	}
	// a client build that holds an immutable named as a successor's HlAdminExec's ed25519: held whole in a kind 8
	odd := synthArt(t, "GnoLightClient", f8ClientABI, 400, map[string][]int{"GnoLightClient.mailboxPkgPathHash": {40}, "GnoLightClient.ED25519": {120, 260}, "HlAdminExec.ed25519": {330}})
	w.Builds = map[string]Build{"client-odd": BuildOf(odd)}
	m := common.HexToAddress("0x000000000000000000000000000000000000BAD0")
	got := checkOf(odd, f7Verifier, map[string][32]byte{"HlAdminExec.ed25519": w32(m)})
	if !strings.Contains(got, " — "+VerdictUnshown) || !strings.Contains(got, "HlAdminExec.ed25519 = "+m.Hex()+", but it must be "+f7Verifier.Hex()) || strings.Contains(got, f8Differs) {
		t.Fatalf("a kind 8's build with an immutable named as a successor's setup-only one:\n%s", got)
	}
	w.Builds = map[string]Build{"client-v2": BuildOf(art)}
	v2 := common.HexToAddress("0x000000000000000000000000000000000000e256")
	// after a swap: the client runs v2, HlAdmin's ed25519() is still f7Verifier; an upgrade that keeps v2 is vouched
	live(v2)
	got = check(v2)
	for _, s := range []string{"GnoLightClient.ED25519 = " + v2.Hex() + " ok (the live client's ED25519()", "ed25519_ = " + v2.Hex() + " ok", "mailboxPkgPathHash_ = " + f7PkgPath.Hex() + " ok"} {
		if !strings.Contains(got, s) {
			t.Fatalf("an upgrade that keeps the live verifier lacks %q:\n%s", s, got)
		}
	}
	if !strings.HasSuffix(got, " — "+VerdictImmutable) {
		t.Fatalf("an upgrade that keeps the live verifier is not immutable:\n%s", got)
	}
	// back to HlAdmin's (stale) verifier: a swap
	got = check(f7Verifier)
	if !strings.Contains(got, " — "+VerdictUnshown) || !strings.Contains(got, "GnoLightClient.ED25519 = "+f7Verifier.Hex()+", but it must be "+v2.Hex()) ||
		!strings.Contains(got, "its argument ed25519_ = "+f7Verifier.Hex()+", but it must be "+v2.Hex()) {
		t.Fatalf("an upgrade back to HlAdmin's verifier:\n%s", got)
	}
	// a successor named verifier m (unchecked by kind 9), the client still runs f7Verifier: an upgrade to m is a swap
	named := f7Bindings
	named.Ed25519 = m
	liveBindings(r.chain, m3bAdmin, named)
	live(f7Verifier)
	got = check(m)
	if !strings.Contains(got, " — "+VerdictUnshown) || !strings.Contains(got, "GnoLightClient.ED25519 = "+m.Hex()+", but it must be "+f7Verifier.Hex()) {
		t.Fatalf("an upgrade to the verifier a successor named:\n%s", got)
	}
}
