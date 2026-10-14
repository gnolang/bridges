package main

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/gagliardetto/solana-go"
	"github.com/gagliardetto/solana-go/rpc"

	"github.com/gnolang/bridges/solana/bridge/agent"
	"github.com/gnolang/bridges/solana/bridge/sol"
)

// successorStateBytes is a successor's admin state in the version-1 layout: its predecessor, whether it
// inherited, how many decisions pend, and extra bytes past the borsh form (the reserve is
// zero; a non-zero byte there is a field of a later build, S-21, G52).
func successorStateBytes(predecessor solana.PublicKey, inherited byte, pendingMailbox uint32, extra []byte) []byte {
	return successorStateWith(8, nil, predecessor, inherited, pendingMailbox, extra)
}

// successorStateWith is successorStateBytes at a clock factor, with a successor of the successor's own (none
// for nil).
func successorStateWith(factor byte, successor *solana.PublicKey, predecessor solana.PublicKey, inherited byte, pendingMailbox uint32, extra []byte) []byte {
	b := []byte{1, factor, 0}
	b = append(b, 0, 0, 0, 0)            // origin
	b = append(b, make([]byte, 8)...)    // last seq
	b = append(b, make([]byte, 6*32)...) // the seats
	b = append(b, make([]byte, 8)...)    // six flags, holdSince, dipSince: none
	if successor == nil {
		b = append(b, 0)
	} else {
		b = append(append(b, 1), successor[:]...)
	}
	b = append(b, 254)        // the bump
	b = append(b, 0, 0, 0, 0) // no cancelled range
	b = append(b, 0, 0, 0, 0) // no holds
	b = append(b, byte(pendingMailbox), byte(pendingMailbox>>8), byte(pendingMailbox>>16), byte(pendingMailbox>>24))
	b = append(b, predecessor[:]...)
	b = append(b, inherited)
	return append(b, extra...)
}

// S-20, S-21, G52, AS8: before a kind-9 vote hlwatch judges a successor as the program's can_inherit does — its
// state its own, parsing as the version-1 prefix, naming this admin program as predecessor, not inherited, with
// no successor of its own — and names a successor with a decision pending (alarmed: it
// would execute with the successor's authority after the hand-over); a field inserted before `inherited` is
// LAYOUT DIFFERS (alarmed: the hand-over would be spent or die), a field appended after it — what M2h's
// contract allows — is a note beside the successor line (alarmed as a note, never a layout finding), and a
// fresh successor of the layout is immutable and clean.
func TestSuccessorCheckSeesPendingAndLayout(t *testing.T) {
	cfg := &agent.Config{}
	cfg.Solana.Mailbox = "E588QtVUvresuXq2KoNEwAmoifCzYGpRBdHByN9KQMbi"
	cfg.Solana.Client = "4RGdtbf8oxCqysLCmVDRKLLZTrTmjPWiLY1xUTiq9HXU"
	cfg.Solana.Pausable = "9EJC4KsqrxZzfp2iXp3gm8pKXTqzQo8AWUFQ22vK7Lvr"
	cfg.Solana.Admin = "ATokenGPvbdGVxr1b2hvZbsiqW5xWH25efTNsLJA8knL"
	adminID := cfg.Programs().Admin
	succ := solana.MustPublicKeyFromBase58("TokenzQdBNbLqP5VEhdkAS6EPFLC1PHnBqCXEpPxuEb")
	succHex := fmt.Sprintf("%x", succ[:])
	succState := sol.Admin{Program: succ}.State().String()
	loader := sol.LoaderID.String()
	programData := []byte{3, 0, 0, 0, 9, 0, 0, 0, 0, 0, 0, 0}
	programData = append(programData, make([]byte, 33)...) // no upgrade authority
	programData = append(programData, 0x7f, 'E', 'L', 'F')
	checkOwned := func(owner string, state []byte, more map[string]fakeAccount) (string, bool, string) {
		accounts := map[string]fakeAccount{
			sol.ProgramData(succ).String(): {loader, programData},
			succState:                      {owner, state},
		}
		for k, a := range more {
			accounts[k] = a
		}
		srv := fakeRPC(t, accounts)
		w := &watcher{cfg: cfg, providers: [2]*rpc.Client{sol.NewRPC(srv.URL), sol.NewRPC(srv.URL)}}
		return w.successorCheck(context.Background(), succHex, 8, 8) // the deployment at factor 8 (the e2e's)
	}
	check := func(state []byte) (string, bool, string) { return checkOwned(succ.String(), state, nil) }
	reserve := make([]byte, sol.AdminStateAccountLen-sol.AdminStateFreshLen) // the account at its init size, the reserve zero
	if got, finding, note := check(successorStateBytes(adminID, 0, 0, reserve)); finding || note != "" || !strings.Contains(got, "immutable (its upgrade authority is none; its state is its own and names this admin program as predecessor, not yet inherited, no successor of its own, the version-1 layout, clock factor 8 as the deployment's, nothing pending: kind 9 can execute); its authority PDA holds 0 lamports") {
		t.Fatalf("a fresh successor of the layout: %s (finding %v, note %q)", got, finding, note)
	}
	// S-23 (M2i): a successor compiled at another clock factor — the program spends the kind 9
	if got, finding, _ := check(successorStateWith(1, nil, adminID, 0, 0, reserve)); !finding || !strings.Contains(got, "AT CLOCK FACTOR 1, THE DEPLOYMENT'S IS 8 (the client's head) AND 8 (the admin program's)") {
		t.Fatalf("a successor at another clock factor: %s (finding %v)", got, finding)
	}
	// S-25 (M2i): the successor's authority PDA balance beside the verdict
	pda := sol.Admin{Program: succ}.Authority().String()
	if got, finding, _ := checkOwned(succ.String(), successorStateBytes(adminID, 0, 0, reserve), map[string]fakeAccount{pda: {sol.SystemProgram.String(), nil}}); finding || !strings.Contains(got, "its authority PDA holds 1000 lamports") {
		t.Fatalf("the PDA's balance: %s (finding %v)", got, finding)
	}
	if got, finding, _ := check(successorStateBytes(adminID, 0, 1, reserve)); !finding || !strings.Contains(got, "HAS A DECISION PENDING BEFORE THE HAND-OVER: 1 pending") {
		t.Fatalf("a successor with a decision pending: %s (finding %v)", got, finding)
	}
	// a field appended after inherited, non-zero (the contract's append-only room): a note, no finding, the
	// hand-over still judged executable
	other := append(append([]byte{}, reserve...), 0)
	other[0] = 7 // a field of a later build past inherited
	if got, finding, note := check(successorStateBytes(adminID, 0, 0, other)); finding || !strings.Contains(got, "kind 9 can execute") || !strings.Contains(note, "byte 262, past the version-1 form's 262 bytes, is 0x7 (1 non-zero byte in the reserve): a field this build does not read") || strings.Contains(got, "LAYOUT DIFFERS") {
		t.Fatalf("a successor with a field appended: %s (finding %v, note %q)", got, finding, note)
	}
	// a field inserted before inherited (after the authority bump): the prefix does not parse — LAYOUT DIFFERS
	fresh := successorStateBytes(adminID, 0, 0, nil)
	inserted := append(append(append([]byte{}, fresh[:217]...), 0xff, 0xff, 0xff, 0xff, 0, 0, 0, 0), fresh[217:]...)
	if got, finding, _ := check(append(inserted, reserve...)); !finding || !strings.Contains(got, "LAYOUT DIFFERS: its state does not parse with the version-1 layout") || !strings.Contains(got, "the hand-over would be spent or die (S-21)") {
		t.Fatalf("a successor with a field inserted: %s (finding %v)", got, finding)
	}
	short := successorStateBytes(adminID, 0, 0, nil)
	if got, finding, _ := check(short[:len(short)-1]); !finding || !strings.Contains(got, "LAYOUT DIFFERS: its state does not parse with the version-1 layout") {
		t.Fatalf("a successor whose state ends early: %s (finding %v)", got, finding)
	}
	// what can_inherit refuses beside the parse (G52): a state not the successor's own, a successor of its own
	if got, finding, _ := checkOwned(adminID.String(), successorStateBytes(adminID, 0, 0, reserve), nil); !finding || !strings.Contains(got, "ITS STATE IS NOT ITS OWN (owned by "+adminID.String()+")") {
		t.Fatalf("a state owned by another program: %s (finding %v)", got, finding)
	}
	own := solana.MustPublicKeyFromBase58("noopb9bkMVfRPU8AsbpTUg8AQkHtKwMYZiFUjNRtMmV")
	if got, finding, _ := check(successorStateWith(8, &own, adminID, 0, 0, reserve)); !finding || !strings.Contains(got, "HAS A SUCCESSOR OF ITS OWN") {
		t.Fatalf("a successor with a successor: %s (finding %v)", got, finding)
	}
	if got, finding, _ := check(successorStateBytes(adminID, 1, 0, reserve)); !finding || !strings.Contains(got, "HAS INHERITED ALREADY") {
		t.Fatalf("an inherited successor: %s (finding %v)", got, finding)
	}
	// the finding reaches the proposal's text and the watch's alarms; a note too, under its own key
	w := &watcher{cfg: cfg, alarm: &agent.Alarmer{Source: "test"}}
	w.cfg.Gno.Gov = "gno.land/r/test/hlgov"
	srv := fakeRPC(t, map[string]fakeAccount{sol.ProgramData(succ).String(): {loader, programData}, succState: {succ.String(), successorStateBytes(adminID, 0, 2, other)}})
	w.providers = [2]*rpc.Client{sol.NewRPC(srv.URL), sol.NewRPC(srv.URL)}
	v := &view{Proposals: []agent.Proposal{{ID: 4, Realm: w.cfg.Gno.Gov, Text: "transfer every authority to " + succHex}}}
	w.programChecks(context.Background(), v)
	if !strings.Contains(v.Proposals[0].Text, "HAS A DECISION PENDING BEFORE THE HAND-OVER: 2 pending") || !strings.Contains(v.Proposals[0].Text, "NOTE: byte 262") || len(v.Findings) != 2 || v.Findings[0].key != "successor:proposal:4" || v.Findings[1].key != "successor:note:4" {
		t.Fatalf("the proposal's findings: %s %+v", v.Proposals[0].Text, v.Findings)
	}
	w.diff(&view{}, v)
	for _, key := range []string{"successor:proposal:4", "successor:note:4"} {
		if _, ok := w.alarm.Standing()[key]; !ok {
			t.Fatalf("no alarm %s: %v", key, w.alarm.Standing())
		}
	}
	w.diff(v, &view{})
	for _, key := range []string{"successor:proposal:4", "successor:note:4"} {
		if _, ok := w.alarm.Standing()[key]; ok {
			t.Fatalf("the alarm %s stands after the proposal closed", key)
		}
	}
}
