package sol

import (
	"bufio"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gagliardetto/solana-go"

	"github.com/gnolang/bridges/solana/bridge/gnolight"
)

// The byte-for-byte test: testdata/vectors.txt holds what the programs' own code encodes for known instructions,
// bodies and PDAs (printed by tools/vectors from the same fixtures); each is
// rebuilt here and compared.

func vectors(t *testing.T) map[string][]byte {
	t.Helper()
	f, err := os.Open(filepath.Join("testdata", "vectors.txt"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	out := map[string][]byte{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name, hx, ok := strings.Cut(line, "=")
		if !ok {
			t.Fatalf("vectors: %q", line)
		}
		b, err := hex.DecodeString(hx)
		if err != nil {
			t.Fatalf("vectors: %s: %v", name, err)
		}
		out[name] = b
	}
	return out
}

type fx struct {
	block   *gnolight.Block
	commit  *gnolight.Commit
	set     *gnolight.ValidatorSet
	results []*gnolight.TxResult
}

func loadFixture(t *testing.T, height string) *fx {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "programs", "fixtures", height+".json"))
	if err != nil {
		t.Skip(err)
	}
	var j struct {
		Block        json.RawMessage `json:"block"`
		Commit       json.RawMessage `json:"commit"`
		Validators   json.RawMessage `json:"validators"`
		BlockResults json.RawMessage `json:"block_results"`
	}
	if err := json.Unmarshal(raw, &j); err != nil {
		t.Fatal(err)
	}
	f := &fx{}
	if f.block, err = gnolight.BlockFromJSON(j.Block); err != nil {
		t.Fatal(err)
	}
	if f.commit, _, err = gnolight.CommitFromJSON(j.Commit); err != nil {
		t.Fatal(err)
	}
	if f.set, err = gnolight.ValidatorsFromJSON(j.Validators); err != nil {
		t.Fatal(err)
	}
	if string(j.BlockResults) != "null" {
		if f.results, err = gnolight.BlockResultsFromJSON(j.BlockResults); err != nil {
			t.Fatal(err)
		}
	}
	return f
}

func rep(b byte) (out [32]byte) {
	for i := range out {
		out[i] = b
	}
	return out
}

func pk(b byte) solana.PublicKey {
	a := rep(b)
	return solana.PublicKeyFromBytes(a[:])
}

func TestVectors(t *testing.T) {
	v := vectors(t)
	f3, f2, f1, fr := loadFixture(t, "487883"), loadFixture(t, "487882"), loadFixture(t, "487881"), loadFixture(t, "740913")
	client := LightClient{Program: pk(0x11)}
	pausable := Pausable{Program: pk(0x22)}
	admin := Admin{Program: pk(0x33)}
	submitter := pk(4)
	keys := keysOf(f3.set.Validators)
	items, present := Staged(f3.commit)
	message := []byte("a message")
	leaves, err := gnolight.ResultLeaves(fr.results)
	if err != nil {
		t.Fatal(err)
	}
	proof, err := gnolight.Proof(leaves, 0)
	if err != nil {
		t.Fatal(err)
	}
	var seats [Seats]solana.PublicKey
	var watch [Watch]solana.PublicKey
	for i := range seats {
		seats[i] = pk(byte(11 + i))
	}
	watch[0], watch[1] = pk(15), pk(16)
	var entries []Ed25519Entry
	for _, i := range present[:2] {
		var sig [64]byte
		copy(sig[:], f3.commit.Signatures[i])
		entries = append(entries, Ed25519Entry{PubKey: keys[i], Signature: sig, Message: f3.commit.SignBytes("gnoland-1", 487883, i)})
	}
	cbody := DecisionBody(KindCancel, 0, EncodeParams(&Params{Kind: KindCancel, Salt: 0x1122334455667788, Cancel: []CancelRange{{0, 2, 9}}}))
	hash3 := f3.block.Header.Hash()
	announcing := &StageInitArgs{Kind: StageKindUpdate, Header: f2.block.Header, BlockID: f2.commit.BlockID, NSlots: uint16(len(f2.set.Validators))}
	announcing.Announce(f3.set.Validators)

	got := map[string][]byte{
		"client_stage_init_update_487883":       data(client.StageInitIx(submitter, pk(0), &StageInitArgs{Kind: StageKindUpdate, Header: f3.block.Header, BlockID: f3.commit.BlockID, NSlots: uint16(len(keys))})),
		"client_stage_init_announcing_487882":   data(client.StageInitIx(submitter, pk(0), announcing)),
		"client_stage_next_set_487883":          data(client.StageNextSetIx(submitter, pk(0), 0, f3.set.Validators)),
		"client_stage_precommits_487883":        data(client.StagePrecommitsIx(submitter, pk(0), 0, items)),
		"client_stage_verify_012":               data(client.StageVerifyIx(pk(0), 0, []uint16{0, 1, 2})),
		"client_update_finish":                  data(client.UpdateFinishIx(pk(0), submitter)),
		"client_stage_close":                    data(client.StageCloseIx(pk(0), submitter)),
		"client_grow":                           data(client.GrowIx(submitter, 0)),
		"client_grow_8192":                      data(client.GrowIx(submitter, 8192)),
		"ed25519_487883_first_two":              data(Ed25519Ix(entries)),
		"client_stage_init_proof_ring_740913":   data(client.StageInitIx(submitter, pk(0), &StageInitArgs{Kind: StageKindProof, RingHash: rep(7), MessageHash: gnolight.MessageHash(message), Proof: proof, LeafLen: uint32(len(leaves[0]))})),
		"client_stage_init_proof_header_487883": data(client.StageInitIx(submitter, pk(0), &StageInitArgs{Kind: StageKindProof, Header: f3.block.Header, BlockID: f3.commit.BlockID, NSlots: uint16(len(keys)), MessageHash: gnolight.MessageHash(message), Proof: proof, LeafLen: uint32(len(leaves[0]))})),
		"client_stage_leaf":                     data(client.StageLeafIx(submitter, pk(0), leaves[0][:100])),
		"client_init_487881":                    data(client.InitIx(submitter, submitter, "gnoland-1", 1198419788, 487881, f1.block.Hash, f1.set.Validators, admin.Program)),
		"client_set_admin":                      data(client.SetAdminIx(submitter, pk(6))),
		"client_state_pda":                      client.State().Bytes(),
		"client_update_stage_pda":               client.UpdateStage(submitter, rep(9)).Bytes(),
		"client_proof_stage_pda":                client.ProofStage(submitter, gnolight.MessageHash(message)).Bytes(),
		"pausable_init":                         data(pausable.InitIx(submitter, submitter, pk(1), client.Program, 1, seats, watch)),
		"pausable_set_flag":                     data(pausable.SetFlagIx(submitter)),
		"pausable_clear_flag":                   data(pausable.ClearFlagIx(submitter)),
		"pausable_unpause_777":                  data(pausable.UnpauseIx(submitter, 777)),
		"pausable_set_guardians":                data(pausable.SetGuardiansIx(submitter, []SeatChange{{3, pk(7)}, {6, pk(8)}})),
		"pausable_transfer":                     data(pausable.TransferOwnershipIx(submitter, pk(6))),
		"pausable_set_mint_limits":              data(pausable.SetMintLimitsIx(submitter, 1, 7_000_000_000, 9_000_000_000)),
		"pausable_flags_pda":                    pausable.Flags().Bytes(),
		"pausable_state_pda":                    pausable.State().Bytes(),
		"admin_init":                            data(admin.InitIx(submitter, submitter, seats, watch, 1198419788, solana.PublicKey{}, 0)),
		"admin_init_fresh_37":                   data(admin.InitIx(submitter, submitter, seats, watch, 1198419788, solana.PublicKey{}, 37)), // a fresh deployment under the same r/hlgov (AT3)
		"admin_init_successor":                  data(admin.SuccessorInitIx(submitter, submitter, 1198419788, admin.Program)),
		"admin_transfer_authority_in":           data(admin.TransferAuthorityInIx(pk(1), submitter)),
		"admin_finish_setup":                    data(admin.FinishSetupIx(Programs{})),
		"admin_execute_5":                       data(admin.ExecuteIx(Programs{}, 5, submitter, submitter, nil, false)),
		"admin_stage_upgrade_2":                 data(admin.StageUpgradeIx(2, pk(1), submitter)),
		"admin_set_flag":                        data(admin.SetFlagIx(submitter)),
		"admin_clear_flag":                      data(admin.ClearFlagIx(submitter)),
		"admin_close_cancelled_9":               data(admin.CloseCancelledIx(9, submitter)),
		"admin_successor_init":                  SuccessorInitData(1198419788, 300, seats, watch, []Hold{{Start: 1_700_000_000, End: 1_700_100_000}}),
		"loader_extend_program_step":            data(ExtendProgramIx(pausable.Program, submitter, ExtendStep)),
		"admin_state_pda":                       admin.State().Bytes(),
		"admin_authority_pda":                   admin.Authority().Bytes(),
		"admin_decision_5_pda":                  admin.Decision(5).Bytes(),
		"admin_decision_300_pda":                admin.Decision(300).Bytes(),
		"body_cancel_range":                     cbody,
		"body_cancel_list":                      DecisionBody(KindCancel, 4, EncodeParams(&Params{Kind: KindCancel, Salt: 0x99aabbccddeeff00, Cancel: []CancelRange{{0, 2, 2}, {0, 5, 5}}})),
		"body_unpause_1":                        DecisionBody(KindUnpause, 5, EncodeParams(&Params{Kind: KindUnpause, Object: 1})),
		"body_token_ism":                        DecisionBody(KindSetTokenISM, 6, EncodeParams(&Params{Kind: KindSetTokenISM, Token: 1, ISM: pausable.Program})),
		"body_guardians":                        DecisionBody(KindSetGuardians, 8, EncodeParams(&Params{Kind: KindSetGuardians, Seats: []SeatChange{{3, pk(7)}, {6, pk(8)}}})),
		"body_router":                           DecisionBody(KindEnrollRouter, 9, EncodeParams(&Params{Kind: KindEnrollRouter, Token: 1, Domain: 1198419788, Router: rep(0xab)})),
		"body_fee":                              DecisionBody(KindSetFee, 3, EncodeParams(&Params{Kind: KindSetFee, Lamports: 1000, Beneficiary: pk(9)})),
		"body_upgrade":                          DecisionBody(KindUpgrade, 10, EncodeParams(&Params{Kind: KindUpgrade, Program: pausable.Program, Buffer: pk(0x88), Hash: rep(0x77)})),
		"body_transfer":                         DecisionBody(KindTransfer, 11, EncodeParams(&Params{Kind: KindTransfer, Program: pk(0x99)})),
		"body_mint_limits":                      DecisionBody(KindSetMintLimits, 12, EncodeParams(&Params{Kind: KindSetMintLimits, Token: 1, Max: 7_000_000_000, DayCap: 9_000_000_000})),
		"header_hash_487883":                    hash3[:],
		"sign_bytes_487883_slot0":               f3.commit.SignBytes("gnoland-1", 487883, present[0]),
	}
	for name, want := range v {
		g, ok := got[name]
		if !ok {
			t.Errorf("%s: not rebuilt here", name)
			continue
		}
		if hex.EncodeToString(g) != hex.EncodeToString(want) {
			t.Errorf("%s:\n  got  %x\n  want %x", name, g, want)
		}
	}
	for name := range got {
		if _, ok := v[name]; !ok {
			t.Errorf("%s: no vector", name)
		}
	}
	// every body parses back to what was encoded, and a wrong one is refused as the program spends it (kind 4,
	// reserved, has no body: TestSaltedCancelAndReserved)
	for _, body := range [][]byte{got["body_cancel_range"], got["body_cancel_list"], got["body_unpause_1"], got["body_token_ism"], got["body_guardians"], got["body_router"], got["body_fee"], got["body_upgrade"], got["body_transfer"], got["body_mint_limits"]} {
		kind, _, ok := gnolight.DecisionPrefix(body)
		if !ok {
			t.Fatal("prefix")
		}
		p, err := ParseParams(kind, body[9:])
		if err != nil {
			t.Fatalf("kind %d: %v", kind, err)
		}
		if hex.EncodeToString(EncodeParams(p)) != hex.EncodeToString(body[9:]) {
			t.Fatalf("kind %d: not a round trip", kind)
		}
		if _, err := ParseParams(kind, append(body[9:], 0)); err == nil {
			t.Fatalf("kind %d: a trailing byte parsed", kind)
		}
	}
	// the staged instruction decodes back to its header
	a, ok, err := DecodeStageInit(got["client_stage_init_update_487883"])
	if err != nil || !ok || a.Header.Hash() != hash3 || a.NSlots != 5 || a.Kind != StageKindUpdate || a.NextSetLen != 0 {
		t.Fatalf("decode: %v %v %+v", ok, err, a)
	}
	a, _, err = DecodeStageInit(got["client_stage_init_announcing_487882"])
	if err != nil || int(a.NextSetLen) != len(f3.set.Validators) || a.NextSetHash != f2.block.Header.NextValidatorsHashArray() {
		t.Fatalf("decode announcing: %v %+v", err, a)
	}
	a, _, err = DecodeStageInit(got["client_stage_init_proof_ring_740913"])
	if err != nil || a.Header != nil || a.Proof == nil || a.Proof.Index != 0 || a.RingHash != rep(7) {
		t.Fatalf("decode proof: %v %+v", err, a)
	}
	// an announcing update's transactions: the init and precommits, the next set's chunks, the verifies, the finish
	txs, err := client.UpdateTxs(submitter, "gnoland-1", f2.block.Header, f2.commit, keysOf(f2.set.Validators), f3.set.Validators, 3, 0)
	if err != nil {
		t.Fatal(err)
	}
	if d := data(txs[1][0]); d[0] != 9 || len(txs) < 4 {
		t.Fatalf("the next set's chunk is not the second transaction: %x (%d transactions)", d[:3], len(txs))
	}
	big := make([]gnolight.Validator, 60)
	if n := len(client.NextSetTxs(submitter, pk(0), big, 0)); n != 3 {
		t.Fatalf("60 validators in %d chunks, want 3 of %d", n, NextSetChunk)
	}
}

// The decisions' clocks (§6.5, plan v35: the mailbox's kinds alone): a kind 5 under a hold lands on the hold's
// 21st day (the floor), every other kind of the 7-day group waits until the hold ends, an unpause is never
// deferred (spent under a hold instead), a cancel executes at once.
func TestDecisionClocks(t *testing.T) {
	hold := &Flag{Time: 1000}
	k5 := &Decision{Kind: KindSetGuardians, DeliveredTime: 500}
	if k5.MaturesAt(1, hold, nil) != 1000+21*Day || !k5.HeldToTheDay() || k5.HeldUntilTheEnd() {
		t.Fatalf("kind 5 under a hold: %d", k5.MaturesAt(1, hold, nil))
	}
	if k5.MaturesAt(8, nil, nil) != 500+Week(8) {
		t.Fatal("kind 5 without a hold")
	}
	k7 := &Decision{Kind: KindSetFee, DeliveredTime: 500}
	if k7.MaturesAt(1, hold, nil) != 500+7*Day || k7.HeldToTheDay() || !k7.HeldUntilTheEnd() {
		t.Fatal("kind 7 is deferred past the hold, not timed by it")
	}
	k2 := &Decision{Kind: KindUnpause, DeliveredTime: 500}
	if k2.HeldUntilTheEnd() || k2.HeldToTheDay() || k2.MaturesAt(1, hold, nil) != 500+Day {
		t.Fatal("an unpause is never deferred (spent under a hold instead)")
	}
	k1 := &Decision{Kind: KindCancel, DeliveredTime: 500}
	if k1.HeldUntilTheEnd() || k1.HeldToTheDay() || k1.MaturesAt(1, hold, nil) != 500 {
		t.Fatal("a cancel executes at once")
	}
}

// data is an instruction's data (the builders never fail to encode).
func data(i solana.Instruction) []byte {
	b, err := i.Data()
	if err != nil {
		panic(err)
	}
	return b
}

// The guardians' hold arithmetic matches the programs' (sol-util's guardians tests).
func TestGuardiansHold(t *testing.T) {
	const day = int64(86400)
	g := &Guardians{}
	for i := range g.Keys {
		g.Keys[i] = pk(byte(i + 1))
	}
	at := func(s int64) *Flag { return &Flag{Slot: uint64(s), Time: s} }
	g.Flags[2], g.Flags[3] = at(1000), at(1500)
	g.HoldSince = at(1500)
	if h := g.Hold(1500, day); h == nil || h.Time != 1500 {
		t.Fatal("no hold with two guardian flags")
	}
	g.Flags[3] = nil
	g.DipSince = at(2400)
	if h := g.Hold(2400+day-1, day); h == nil {
		t.Fatal("a dip shorter than the grace ends the hold")
	}
	if h := g.Hold(2400+day, day); h != nil {
		t.Fatal("a dip of the grace's length keeps the hold")
	}
	g.Flags[4] = at(5)
	if g.GuardianFlags() != 1 || !g.AnyFlag() {
		t.Fatal("a watch key's flag counted as a guardian's")
	}
	if g.Position(pk(5)) != 4 || g.Position(pk(9)) != -1 {
		t.Fatal("position")
	}
}

// The ELF end inside a programdata account is the section header table's end.
func TestELFEnd(t *testing.T) {
	elf := make([]byte, 200)
	copy(elf, "\x7fELF")
	// shoff 100, shentsize 10, shnum 5 → 150
	elf[0x28] = 100
	elf[0x3a] = 10
	elf[0x3c] = 5
	if ELFEnd(elf) != 150 {
		t.Fatal(ELFEnd(elf))
	}
	if ELFEnd([]byte("short")) != 5 {
		t.Fatal("not an ELF")
	}
}
