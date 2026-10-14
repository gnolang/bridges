package main

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gagliardetto/solana-go"
	"github.com/gagliardetto/solana-go/rpc"

	"github.com/gnolang/bridges/solana/bridge/agent"
	"github.com/gnolang/bridges/solana/bridge/sol"
)

// K13: watch alarms the holder on each route's identity verdict — a flag on BROKEN, an alarm on a short right
// side, the clear once it holds — and the set-bound alarm follows the client's view.
func TestWatchAlarmsOnTheIdentity(t *testing.T) {
	w := &watcher{alarm: &agent.Alarmer{Source: "test"}}
	v := func(id agent.Identity, bound string) *view {
		return &view{Routes: []agent.RouteView{{Symbol: "BUBBLE"}}, Sol: &agent.SolanaView{Identities: map[string]agent.Identity{"BUBBLE": id}, Client: agent.ClientView{SetBound: bound}}}
	}
	holds := v(agent.Identity{Symbol: "BUBBLE", Lock: 100, Supply: 100}, "")
	broken := v(agent.Identity{Symbol: "BUBBLE", Lock: 100, Supply: 150}, "")
	short := v(agent.Identity{Symbol: "BUBBLE", Lock: 100, Supply: 10}, "an announced validator set has 45")
	w.diff(holds, broken)
	if a, ok := w.alarm.Standing()["identity:BUBBLE"]; !ok || !strings.Contains(a, "BROKEN") {
		t.Fatalf("no flag on a break: %v", w.alarm.Standing())
	}
	w.diff(broken, short)
	if a := w.alarm.Standing()["identity:BUBBLE"]; !strings.Contains(a, "short") {
		t.Fatalf("no alarm on a short right side: %q", a)
	}
	if a, ok := w.alarm.Standing()["client:set-bound"]; !ok || !strings.Contains(a, "45") {
		t.Fatalf("no set-bound alarm: %v", w.alarm.Standing())
	}
	w.diff(short, holds)
	if _, ok := w.alarm.Standing()["identity:BUBBLE"]; ok {
		t.Fatal("not cleared once the identity holds")
	}
	if _, ok := w.alarm.Standing()["client:set-bound"]; ok {
		t.Fatal("the set-bound alarm not cleared")
	}
	// AP1: a forged credit inside what is in flight is BROKEN to hlwatch too (the exact rule), and a waiting claim
	// with no dispatch record on either provider is a flag of its own, cleared once it is indexed or gone
	hidden := v(agent.Identity{Symbol: "BUBBLE", Lock: 60_000, Supply: 0, Owed: 1, InflightOut: 60_000}, "")
	w.diff(holds, hidden)
	if a, ok := w.alarm.Standing()["identity:BUBBLE"]; !ok || !strings.Contains(a, "BROKEN") {
		t.Fatalf("a forged credit hidden by what is in flight did not flag: %v", w.alarm.Standing())
	}
	forged := v(agent.Identity{Symbol: "BUBBLE", Lock: 100, Supply: 100}, "")
	forged.Sol.Forged = []string{"forged credit ee on BUBBLE: the claim names g1eve for 500 (credited at height 90, paid 0 so far) and neither provider's dispatch index holds a burn under its id"}
	w.diff(hidden, forged)
	if a, ok := w.alarm.Standing()["forged:ee"]; !ok || !strings.HasPrefix(a, "FORGED CREDIT: forged credit ee") {
		t.Fatalf("no flag for the forged credit: %v", w.alarm.Standing())
	}
	w.diff(forged, holds)
	if _, ok := w.alarm.Standing()["forged:ee"]; ok {
		t.Fatal("the forged-credit flag not cleared once the claim was indexed or gone")
	}
	if !strings.Contains(forged.text(), "- FORGED CREDIT: forged credit ee on BUBBLE") {
		t.Fatal("the status does not print the forged credit")
	}
	// AP6: the day cap above a fifth of the lock alarms and prints CAP RATIO; within it the alarm clears
	ratio := v(agent.Identity{Symbol: "BUBBLE", Lock: 100, Supply: 100}, "")
	ratio.Sol.Limits[1] = agent.LimitView{Symbol: "BUBBLE", DayCap: 100_000_000_000, Lock: 2_900_000_000, Ratio: "BUBBLE's day cap on the pausable ISM, 100000000000, is above a fifth of its lock on gno.land, 2900000000"}
	w.diff(holds, ratio)
	if a, ok := w.alarm.Standing()["mint:cap-ratio:BUBBLE"]; !ok || !strings.Contains(a, "above a fifth") {
		t.Fatalf("no cap-ratio alarm: %v", w.alarm.Standing())
	}
	if s := ratio.text(); !strings.Contains(s, "  - CAP RATIO: BUBBLE's day cap") || !strings.Contains(s, "the lock on gno.land 2900000000") {
		t.Fatalf("the status does not print the cap ratio: %s", s)
	}
	within := v(agent.Identity{Symbol: "BUBBLE", Lock: 100, Supply: 100}, "")
	within.Sol.Limits[1] = agent.LimitView{Symbol: "BUBBLE", DayCap: 100, Lock: 500}
	w.diff(ratio, within)
	if _, ok := w.alarm.Standing()["mint:cap-ratio:BUBBLE"]; ok {
		t.Fatal("the cap-ratio alarm not cleared")
	}
}

// AP3: a recipient switch proposed or queued while the admin program's kind 9 is unexecuted is a finding (and
// an alarm of the watch); a kind 9 executed while r/hlgov still dispatches to the retired program says RETIRED;
// a recipient that is a successor which has not inherited says NOT INHERITED; a switch matching the executed
// kind 9 to an inherited successor is clean.
func TestRecipientSwitchNotes(t *testing.T) {
	gov := "gno.land/r/test/hlgov"
	admin, succ := strings.Repeat("aa", 32), strings.Repeat("bb", 32)
	text := "set decision recipient to " + succ + " (now " + admin + ")"
	base := func() *view {
		return &view{Proposals: []agent.Proposal{{ID: 7, Realm: gov, Text: text}, {ID: 8, Realm: gov, Text: "set protocol fee 1000 lamports"}},
			Pending: []agent.PendingDecision{{Realm: gov, ID: 3, Kind: "set decision recipient", Text: text, Due: 1_800_000_000}},
			Gov:     agent.GovView{Address: "g1gov", Recipient: admin}, Sol: &agent.SolanaView{LiveHex: admin}}
	}
	v := base()
	notes := switchNotes(v, gov)
	if len(notes) != 2 || notes[0].key != "switch:proposal:7" || notes[1].key != "switch:queued:3" || !strings.Contains(notes[0].text, "UNEXECUTED") || !strings.Contains(notes[1].text, "decision #3, matures at 2027-01-15T08:00:00Z") {
		t.Fatalf("the switch before the kind 9: %+v", notes)
	}
	v.Sol.Successor = strings.Repeat("cc", 32)
	if notes = switchNotes(v, gov); len(notes) != 3 || !strings.Contains(notes[0].text, "successor is "+strings.Repeat("cc", 32)+", not this one") || notes[2].key != "switch:retired" {
		t.Fatalf("a kind 9 to another successor: %+v", notes)
	}
	v.Sol.Successor = succ
	if notes = switchNotes(v, gov); len(notes) != 1 || notes[0].key != "switch:retired" || !strings.Contains(notes[0].text, "RETIRED") {
		t.Fatalf("the kind 9 executed, the switch pending: %+v", notes)
	}
	// the switch landed: r/hlgov dispatches to the successor, which has not inherited yet
	v.Gov.Recipient = succ
	no := false
	v.Sol.RecipientInherited = &no
	if notes = switchNotes(v, gov); len(notes) != 1 || notes[0].key != "switch:not-inherited" {
		t.Fatalf("a successor not yet inherited: %+v", notes)
	}
	yes := true
	v.Sol.RecipientInherited = &yes
	if notes = switchNotes(v, gov); len(notes) != 0 {
		t.Fatalf("a clean switch: %+v", notes)
	}
	// the watch alarms a finding and clears it once it goes
	w := &watcher{alarm: &agent.Alarmer{Source: "test"}}
	before, after := base(), base()
	after.Switches = switchNotes(after, gov)
	w.diff(before, after)
	if a, ok := w.alarm.Standing()["switch:proposal:7"]; !ok || !strings.Contains(a, "deadlocks GovDAO's own channel") {
		t.Fatalf("no alarm on the switch: %v", w.alarm.Standing())
	}
	w.diff(after, before)
	if _, ok := w.alarm.Standing()["switch:proposal:7"]; ok {
		t.Fatal("the switch alarm not cleared")
	}
	if s := after.text(); !strings.Contains(s, "- RECIPIENT SWITCH: the recipient switch proposed (GovDAO #7)") {
		t.Fatalf("the status does not print the switch: %s", s)
	}
}

// G25: the status text a guardian pastes carries no provider URL's key: SolErr, the not-read lines and the
// unconfirmed lines print solana-go's errors, URL included, and the whole text is redacted on its way out.
func TestStatusTextRedactsURLs(t *testing.T) {
	v := &view{SolErr: `the client from provider 1: Post "https://mainnet.helius-rpc.com/?api-key=SECRET1": EOF`,
		Sol: &agent.SolanaView{Err: []string{"the client's record: rpc call getAccountInfo() on https://solana-mainnet.g.alchemy.com/v2/SECRET2: timeout"},
			Unconfirmed: []string{"the pending decisions: the providers disagree (https://user:SECRET3@h.example/x)"}}}
	text := v.text()
	for _, secret := range []string{"SECRET1", "SECRET2", "SECRET3", "api-key"} {
		if strings.Contains(text, secret) {
			t.Fatalf("the status keeps %s: %s", secret, text)
		}
	}
	for _, host := range []string{"https://mainnet.helius-rpc.com/…", "https://solana-mainnet.g.alchemy.com/…", "https://h.example/…"} {
		if !strings.Contains(text, host) {
			t.Fatalf("the status lost the host %s: %s", host, text)
		}
	}
	// the Solana section absent: the same door
	v2 := &view{SolErr: "https://h.example/?api-key=SECRET: refused"}
	if s := v2.text(); strings.Contains(s, "SECRET") {
		t.Fatalf("the status without a Solana view keeps the key: %s", s)
	}
}

// fakeAccount is one account a fake provider serves: its owner and data.
type fakeAccount struct {
	owner string
	data  []byte
}

// fakeRPC is a JSON-RPC server answering getAccountInfo from a table; an account not in the table is null
// (solana-go's ErrNotFound).
func fakeRPC(t *testing.T, accounts map[string]fakeAccount) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID     any    `json:"id"`
			Method string `json:"method"`
			Params []any  `json:"params"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		var value any
		if req.Method == "getAccountInfo" && len(req.Params) > 0 {
			if a, ok := accounts[fmt.Sprint(req.Params[0])]; ok {
				value = map[string]any{"data": []string{base64.StdEncoding.EncodeToString(a.data), "base64"}, "executable": false, "lamports": 1000, "owner": a.owner, "rentEpoch": 0, "space": len(a.data)}
			}
		}
		if req.Method == "getMinimumBalanceForRentExemption" { // the test validator's rate: 6,960 a byte over 128 bytes of overhead
			size, _ := req.Params[0].(float64)
			_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": 890_880 + uint64(size)*6_960})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{"context": map[string]any{"slot": 7}, "value": value}})
	}))
	t.Cleanup(srv.Close)
	return srv
}

// G26: a kind 9's successor, a kind 8's program and buffer and the holder's seat are read through both
// providers: the two agreeing give the verdict, two that differ give UNCONFIRMED — no vote rests on one
// provider's word.
func TestProgramChecksNeedBothProviders(t *testing.T) {
	cfg := &agent.Config{}
	cfg.Solana.Mailbox = "E588QtVUvresuXq2KoNEwAmoifCzYGpRBdHByN9KQMbi"
	cfg.Solana.Client = "4RGdtbf8oxCqysLCmVDRKLLZTrTmjPWiLY1xUTiq9HXU"
	cfg.Solana.Pausable = "9EJC4KsqrxZzfp2iXp3gm8pKXTqzQo8AWUFQ22vK7Lvr"
	cfg.Solana.Admin = "ATokenGPvbdGVxr1b2hvZbsiqW5xWH25efTNsLJA8knL"
	succ := solana.MustPublicKeyFromBase58("TokenzQdBNbLqP5VEhdkAS6EPFLC1PHnBqCXEpPxuEb")
	buffer := solana.MustPublicKeyFromBase58("noopb9bkMVfRPU8AsbpTUg8AQkHtKwMYZiFUjNRtMmV")
	pda := cfg.Programs().AdminProgram().Authority()
	seat := solana.MustPublicKeyFromBase58("ComputeBudget111111111111111111111111111111")
	programData := func(auth *solana.PublicKey) []byte { // the loader's 45-byte header, then an ELF
		b := []byte{3, 0, 0, 0, 9, 0, 0, 0, 0, 0, 0, 0}
		if auth == nil {
			b = append(b, make([]byte, 33)...)
		} else {
			b = append(append(b, 1), auth[:]...)
		}
		return append(b, 0x7f, 'E', 'L', 'F')
	}
	bufferData := func(auth solana.PublicKey) []byte { // 20,004 bytes of ELF: 20,000 past the program data's 4 (AP5's growth)
		return append(append(append([]byte{1, 0, 0, 0, 1}, auth[:]...), 0x7f, 'E', 'L', 'F'), make([]byte, 20_000)...)
	}
	pausable := cfg.Programs().PausableISM()
	clientID := cfg.Programs().Client
	stateData := func() []byte {
		b := []byte{1, 8}
		b = append(b, pda[:]...)
		b = append(b, clientID[:]...)
		b = append(b, 1)
		return append(b, make([]byte, 64)...)
	}
	flagsData := func(first solana.PublicKey) []byte {
		b := []byte{1}
		b = append(b, first[:]...)
		b = append(b, make([]byte, 5*32)...)
		return append(b, make([]byte, 8)...) // six flags, holdSince, dipSince: none
	}
	// a successor's admin state (M2j's layout): its predecessor and whether it inherited (S-15)
	adminID := cfg.Programs().Admin
	successorState := func(predecessor solana.PublicKey, inherited byte) []byte {
		b := []byte{1, 8, 0}
		b = append(b, 0, 0, 0, 0)         // origin
		b = append(b, make([]byte, 8)...) // last seq
		b = append(b, make([]byte, 6*32)...)
		b = append(b, make([]byte, 8)...) // six flags, holdSince, dipSince: none
		b = append(b, 0, 254)             // no successor; the bump
		b = append(b, 0, 0, 0, 0)         // no cancelled range
		b = append(b, 0, 0, 0, 0)         // no holds
		b = append(b, 0, 0, 0, 0)         // pending
		b = append(b, predecessor[:]...)
		return append(b, inherited)
	}
	succState := sol.Admin{Program: succ}.State().String()
	loader := sol.LoaderID.String()
	honest := map[string]fakeAccount{
		sol.ProgramData(succ).String(): {loader, programData(nil)},
		buffer.String():                {loader, bufferData(pda)},
		pausable.State().String():      {pausable.Program.String(), stateData()},
		pausable.Flags().String():      {pausable.Program.String(), flagsData(seat)},
		succState:                      {succ.String(), successorState(adminID, 0)},
	}
	lying := map[string]fakeAccount{
		sol.ProgramData(succ).String(): {loader, programData(&seat)},
		buffer.String():                {loader, bufferData(seat)},
		pausable.State().String():      {pausable.Program.String(), stateData()},
		pausable.Flags().String():      {pausable.Program.String(), flagsData(succ)},
		succState:                      {succ.String(), successorState(adminID, 0)},
	}
	s1, s2 := fakeRPC(t, honest), fakeRPC(t, lying)
	ctx := context.Background()
	agreeing := &watcher{cfg: cfg, providers: [2]*rpc.Client{sol.NewRPC(s1.URL), sol.NewRPC(s1.URL)}}
	disagreeing := &watcher{cfg: cfg, providers: [2]*rpc.Client{sol.NewRPC(s1.URL), sol.NewRPC(s2.URL)}}
	succHex := strings.Repeat("00", 0) + fmt.Sprintf("%x", succ[:])
	if got, _, _ := agreeing.successorCheck(ctx, succHex, 8, 8); !strings.Contains(got, "immutable (its upgrade authority is none; its state is its own and names this admin program as predecessor, not yet inherited, no successor of its own, the version-1 layout, clock factor 8 as the deployment's, nothing pending: kind 9 can execute)") {
		t.Fatalf("two agreeing providers: %s", got)
	}
	if got, _, _ := disagreeing.successorCheck(ctx, succHex, 8, 8); !strings.Contains(got, unconfirmed) || strings.Contains(got, "immutable") {
		t.Fatalf("a lying second provider gave a verdict: %s", got)
	}
	// S-15: the admin program itself, a successor with no state, one naming another predecessor, one inherited
	if got, _, _ := agreeing.successorCheck(ctx, fmt.Sprintf("%x", adminID[:]), 8, 8); !strings.Contains(got, "IS THE ADMIN PROGRAM ITSELF") {
		t.Fatalf("a self-transfer was not refused: %s", got)
	}
	noState := fakeRPC(t, map[string]fakeAccount{sol.ProgramData(succ).String(): {loader, programData(nil)}})
	w3 := &watcher{cfg: cfg, providers: [2]*rpc.Client{sol.NewRPC(noState.URL), sol.NewRPC(noState.URL)}}
	if got, _, _ := w3.successorCheck(ctx, succHex, 8, 8); !strings.Contains(got, "HAS NO STATE") {
		t.Fatalf("a successor with no state was not named: %s", got)
	}
	wrongPred := fakeRPC(t, map[string]fakeAccount{sol.ProgramData(succ).String(): {loader, programData(nil)}, succState: {succ.String(), successorState(seat, 0)}})
	w4 := &watcher{cfg: cfg, providers: [2]*rpc.Client{sol.NewRPC(wrongPred.URL), sol.NewRPC(wrongPred.URL)}}
	if got, _, _ := w4.successorCheck(ctx, succHex, 8, 8); !strings.Contains(got, "NAMES PREDECESSOR "+seat.String()+", NOT THIS ADMIN PROGRAM") {
		t.Fatalf("a successor of another program was not named: %s", got)
	}
	inherited := fakeRPC(t, map[string]fakeAccount{sol.ProgramData(succ).String(): {loader, programData(nil)}, succState: {succ.String(), successorState(adminID, 1)}})
	w5 := &watcher{cfg: cfg, providers: [2]*rpc.Client{sol.NewRPC(inherited.URL), sol.NewRPC(inherited.URL)}}
	if got, _, _ := w5.successorCheck(ctx, succHex, 8, 8); !strings.Contains(got, "HAS INHERITED ALREADY") {
		t.Fatalf("an inherited successor was not named: %s", got)
	}
	bufHex := fmt.Sprintf("%x", buffer[:])
	// the sizes and the growth's rent beside the authorities (AP5): 45 + 4 + 20,000 bytes at 6,960 over 1,000 held
	if got := agreeing.upgradeCheck(ctx, succHex, bufHex); !strings.Contains(got, "IMMUTABLE") || !strings.Contains(got, "buffer staged") || !strings.Contains(got, "the program data holds 4 bytes, the buffer 20004: GROWTH of one ExtendProgram of 20000 bytes ≈ 0.1404 SOL of rent at the chain's rate") {
		t.Fatalf("two agreeing providers on a kind 8: %s", got)
	}
	if got := disagreeing.upgradeCheck(ctx, succHex, bufHex); strings.Count(got, unconfirmed) != 2 || strings.Contains(got, "staged (") {
		t.Fatalf("a lying second provider gave a kind 8 verdict: %s", got)
	}
	// S-22 (M2i): a kind 8 for the client says the layout contract is the build's to keep and prints the buffer's
	// ELF hash beside what the proposal names (the 20,004-byte ELF of the fixture)
	bufELF := append([]byte{0x7f, 'E', 'L', 'F'}, make([]byte, 20_000)...)
	bufSum := sha256.Sum256(bufELF)
	if got := agreeing.upgradeCheck(ctx, fmt.Sprintf("%x", clientID[:]), bufHex); !strings.Contains(got, "THE CLIENT: its state layout is this build's to keep") || !strings.Contains(got, "the buffer's ELF sha256 is "+hex.EncodeToString(bufSum[:])) {
		t.Fatalf("a kind 8 for the client: %s", got)
	}
	if got := agreeing.upgradeCheck(ctx, succHex, bufHex); strings.Contains(got, "THE CLIENT") {
		t.Fatalf("a kind 8 for another program carries the client's text: %s", got)
	}
	if slot, err := agent.SeatOnSolana(ctx, agent.TwoProviders(agreeing.providers), cfg.Programs(), seat); err != nil || slot != 1 {
		t.Fatalf("the seat through two agreeing providers: %d %v", slot, err)
	}
	if _, err := agent.SeatOnSolana(ctx, agent.TwoProviders(disagreeing.providers), cfg.Programs(), seat); !errors.Is(err, agent.ErrProviders) {
		t.Fatalf("the seat through a lying provider: %v", err)
	}
}

// K16: a kind 8's program and buffer and a kind 9's successor are read from r/hlgov's proposal text.
func TestProposalProgramIDs(t *testing.T) {
	prog := strings.Repeat("ab", 32)
	buf := strings.Repeat("cd", 32)
	m := upgradeTextRE.FindStringSubmatch("upgrade " + prog + " from buffer " + buf + " with ELF sha256 " + strings.Repeat("ef", 32))
	if m == nil || m[1] != prog || m[2] != buf {
		t.Fatalf("the upgrade text: %v", m)
	}
	m = transferTextRE.FindStringSubmatch("transfer every authority to " + prog)
	if m == nil || m[1] != prog {
		t.Fatalf("the transfer text: %v", m)
	}
	if upgradeTextRE.MatchString("set protocol fee 1000 lamports") || transferTextRE.MatchString("upgrade "+prog) {
		t.Fatal("another kind's text matched")
	}
	if k, err := keyFromHex(prog); err != nil || k.String() == "" {
		t.Fatal(err)
	}
}
