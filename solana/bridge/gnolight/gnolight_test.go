package gnolight

import (
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"testing"
)

// The core against gnoland-1's own blocks (bridge/programs/fixtures, fetched by bridge/tools/gnoheaders.py), the
// same checks the Rust core's tests make: every header hash equal to the chain's, every ValidatorsHash equal to
// the set's hash, every precommit signature verifying over our sign bytes, the commit rule, the three set changes,
// LastResultsHash from the re-encoded results with a SimpleProof of each leaf, the Dispatch event round trip.

type fixture struct {
	height  int64
	block   *Block
	commit  *Commit
	votes   []*Vote
	set     *ValidatorSet
	results []*TxResult // nil when the block had no transactions
}

func loadFixtures(t *testing.T) map[int64]*fixture {
	t.Helper()
	dir := filepath.Join("..", "programs", "fixtures")
	files, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil || len(files) == 0 {
		t.Skip("no fixtures at " + dir)
	}
	out := map[int64]*fixture{}
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		var j struct {
			Height       json.Number     `json:"height"`
			Block        json.RawMessage `json:"block"`
			Commit       json.RawMessage `json:"commit"`
			Validators   json.RawMessage `json:"validators"`
			BlockResults json.RawMessage `json:"block_results"`
		}
		if err := json.Unmarshal(raw, &j); err != nil {
			t.Fatal(err)
		}
		fx := &fixture{height: num(j.Height)}
		if fx.block, err = BlockFromJSON(j.Block); err != nil {
			t.Fatalf("%s: block: %v", f, err)
		}
		if fx.commit, fx.votes, err = CommitFromJSON(j.Commit); err != nil {
			t.Fatalf("%s: commit: %v", f, err)
		}
		if fx.set, err = ValidatorsFromJSON(j.Validators); err != nil {
			t.Fatalf("%s: validators: %v", f, err)
		}
		if string(j.BlockResults) != "null" {
			if fx.results, err = BlockResultsFromJSON(j.BlockResults); err != nil {
				t.Fatalf("%s: results: %v", f, err)
			}
		}
		out[fx.height] = fx
	}
	return out
}

func heights(fx map[int64]*fixture) []int64 {
	var hs []int64
	for h := range fx {
		hs = append(hs, h)
	}
	sort.Slice(hs, func(i, j int) bool { return hs[i] < hs[j] })
	return hs
}

func TestHeaderHashesMatchTheChain(t *testing.T) {
	fx := loadFixtures(t)
	if len(fx) < 13 {
		t.Fatalf("%d fixtures", len(fx))
	}
	for _, h := range heights(fx) {
		f := fx[h]
		if err := f.block.Header.Check(); err != nil {
			t.Fatalf("height %d: %v", h, err)
		}
		if got := f.block.Header.Hash(); got != f.block.Hash {
			t.Fatalf("height %d: hash %x, the chain says %x", h, got, f.block.Hash)
		}
		if string(f.commit.BlockID.Hash) != string(f.block.Hash[:]) {
			t.Fatalf("height %d: the commit's BlockID is not the header's hash", h)
		}
		if f.block.Header.Height != h {
			t.Fatalf("height %d: header says %d", h, f.block.Header.Height)
		}
	}
}

func TestValidatorSetsHashToValidatorsHash(t *testing.T) {
	fx := loadFixtures(t)
	for _, h := range heights(fx) {
		f := fx[h]
		if err := f.set.Check(); err != nil {
			t.Fatalf("height %d: %v", h, err)
		}
		got, ok := f.set.Hash()
		if !ok || string(got[:]) != string(f.block.Header.ValidatorsHash) {
			t.Fatalf("height %d: set hash %x, header %x", h, got, f.block.Header.ValidatorsHash)
		}
		sorted := &ValidatorSet{Validators: append([]Validator(nil), f.set.Validators...)}
		sorted.Sort()
		for i := range sorted.Validators {
			if sorted.Validators[i] != f.set.Validators[i] {
				t.Fatalf("height %d: the chain's order is not address order", h)
			}
		}
	}
}

func TestEveryPrecommitSignatureVerifiesOverOurSignBytes(t *testing.T) {
	fx := loadFixtures(t)
	checked := 0
	for _, h := range heights(fx) {
		f := fx[h]
		if len(f.votes) != len(f.set.Validators) {
			t.Fatalf("height %d: %d precommits for %d validators", h, len(f.votes), len(f.set.Validators))
		}
		for i, v := range f.votes {
			if v == nil {
				continue
			}
			if v.ValidatorIndex != i || v.Type != PrecommitType || v.Height != h {
				t.Fatalf("height %d slot %d: %+v", h, i, v)
			}
			sb := CanonicalVote(v.Type, f.block.Header.ChainID, v.Height, v.Round, v.BlockID, v.Time)
			pk := ed25519.PublicKey(f.set.Validators[i].PubKey[:])
			if !ed25519.Verify(pk, sb, v.Signature) {
				t.Fatalf("height %d slot %d: the signature does not verify over our sign bytes", h, i)
			}
			// the staged commit's sign bytes are the same
			if got := f.commit.SignBytes(f.block.Header.ChainID, h, i); string(got) != string(sb) {
				t.Fatalf("height %d slot %d: the commit's sign bytes differ", h, i)
			}
			if ed25519.Verify(pk, CanonicalVote(PrevoteType, f.block.Header.ChainID, v.Height, v.Round, v.BlockID, v.Time), v.Signature) {
				t.Fatalf("height %d slot %d: verifies as a prevote", h, i)
			}
			checked++
		}
	}
	if checked < 60 {
		t.Fatalf("%d signatures checked", checked)
	}
}

// §13's full example at height 701,862: the sign bytes of validator 0's precommit, 110 bytes.
func TestAppendixSignBytes(t *testing.T) {
	want := "6D080211A6B50A000000000022480A20B0D79144E283B51F02881A930A56E0D98DB717702D116DFF92271FA5A75A872D1224080112205AE9C4415816870AB1B14D7024D797C7E3DC3098CC3DDB01F11E5F309360112F2A0B08A9C9A8D60610DFC2B9133209676E6F6C616E642D31"
	hash, _ := hex.DecodeString("B0D79144E283B51F02881A930A56E0D98DB717702D116DFF92271FA5A75A872D")
	parts, _ := hex.DecodeString("5AE9C4415816870AB1B14D7024D797C7E3DC3098CC3DDB01F11E5F309360112F")
	bid := BlockID{Hash: hash, Parts: PartSetHeader{Total: 1, Hash: parts}}
	got := PrecommitSignBytes("gnoland-1", 701862, 0, bid, Time{Secs: 1791632553, Nanos: 40788319})
	if hex.EncodeToString(got) != hex.EncodeToString(mustHex(want)) || len(got) != 110 {
		t.Fatalf("sign bytes %X", got)
	}
	// the header leaves of §13: Version 0A0B"v1.0.0-rc.0", ChainID 0A09"gnoland-1", Height 08+zigzag
	h := &Header{Version: "v1.0.0-rc.0", ChainID: "gnoland-1", Height: 701862}
	l := h.Leaves()
	if hex.EncodeToString(l[0]) != "0a0b"+hex.EncodeToString([]byte("v1.0.0-rc.0")) || hex.EncodeToString(l[2]) != "08ccd655" || len(l[4]) != 0 || len(l[7]) != 0 {
		t.Fatalf("leaves %x %x %x %x", l[0], l[2], l[4], l[7])
	}
	// a validator leaf: 0A37 { 0A11"/tm.PubKeyEd25519" 1222 { 0A20<pk> } } 10 <zigzag 60>
	var pk [32]byte
	for i := range pk {
		pk[i] = 0xaa
	}
	vl := Validator{PubKey: pk, Power: 60}.Leaf()
	if len(vl) != 59 || vl[0] != 0x0a || vl[1] != 0x37 || vl[57] != 0x10 || vl[58] != 0x78 {
		t.Fatalf("validator leaf %x", vl)
	}
}

func mustHex(s string) []byte {
	b, err := hex.DecodeString(s)
	if err != nil {
		panic(err)
	}
	return b
}

func TestCommitRuleOnEveryFixture(t *testing.T) {
	fx := loadFixtures(t)
	for _, h := range heights(fx) {
		f := fx[h]
		total := f.set.TotalPower()
		tallied := f.set.Tallied(func(i int) bool { return f.commit.Signatures[i] != nil })
		if !CommitRule(tallied, total) {
			t.Fatalf("height %d: %d of %d", h, tallied, total)
		}
	}
	if !CommitRule(201, 300) || CommitRule(200, 300) || !CommitRule(201, 301) || CommitRule(200, 301) || CommitRule(0, 0) {
		t.Fatal("the two-thirds rule")
	}
}

func TestTheThreeSetChanges(t *testing.T) {
	fx := loadFixtures(t)
	a := fx[487882]
	if !a.block.Header.Announcing() {
		t.Fatal("487,882 announces the fifth validator")
	}
	five, _ := fx[487883].set.Hash()
	if string(a.block.Header.NextValidatorsHash) != string(five[:]) {
		t.Fatal("487,882's next set is not 487,883's")
	}
	if len(fx[487881].set.Validators) != 4 || fx[487881].set.TotalPower() != 240 || fx[487883].set.TotalPower() != 241 {
		t.Fatal("the fifth joins at power 1")
	}
	if fx[487881].block.Header.Announcing() {
		t.Fatal("487,881 announces nothing")
	}
	r := fx[495706]
	if !r.block.Header.Announcing() || fx[495707].set.TotalPower() != 300 {
		t.Fatal("495,706 announces the raise to 60")
	}
	b := fx[720332]
	if !b.block.Header.Announcing() || len(fx[720333].set.Validators) != 6 || fx[720333].set.TotalPower() != 301 {
		t.Fatal("720,332 announces the sixth")
	}
}

func TestLastResultsHashFromReEncodedResults(t *testing.T) {
	fx := loadFixtures(t)
	checked := 0
	for _, h := range heights(fx) {
		f := fx[h]
		next, ok := fx[h+1]
		if !ok {
			continue
		}
		if f.results == nil {
			if len(next.block.Header.LastResultsHash) != 0 || f.block.Header.NumTxs != 0 {
				t.Fatalf("height %d: no transactions, yet a results hash follows", h)
			}
			continue
		}
		leaves, err := ResultLeaves(f.results)
		if err != nil {
			t.Fatalf("height %d: %v", h, err)
		}
		if int64(len(leaves)) != f.block.Header.NumTxs {
			t.Fatalf("height %d: %d leaves for %d transactions", h, len(leaves), f.block.Header.NumTxs)
		}
		root, _ := SimpleHash(leaves)
		if string(root[:]) != string(next.block.Header.LastResultsHash) {
			t.Fatalf("height %d: LastResultsHash %x, want %x", h, root, next.block.Header.LastResultsHash)
		}
		for i, leaf := range leaves {
			p, err := Proof(leaves, i)
			if err != nil {
				t.Fatal(err)
			}
			if err := p.Verify(root, leaf); err != nil {
				t.Fatalf("height %d leaf %d: %v", h, i, err)
			}
			if p.Verify(root, append(append([]byte{}, leaf...), 0)) == nil {
				t.Fatalf("height %d leaf %d: a changed leaf verifies", h, i)
			}
		}
		checked++
	}
	if checked < 3 {
		t.Fatalf("%d result sets checked", checked)
	}
}

func TestTheResultsLeafParsesAndTheEventReads(t *testing.T) {
	fx := loadFixtures(t)
	f := fx[740913]
	r, err := ParseResult(f.results[0].Leaf)
	if err != nil || r.Error {
		t.Fatal(err)
	}
	if r.Events[0].TypeURL != EventTypeURL {
		t.Fatal(r.Events[0].TypeURL)
	}
	e, err := ParseEvent(r.Events[0].Value)
	if err != nil || e.Type != "Approval" || e.PkgPath != "gno.land/p/nt/grc20/v0" || len(e.Attributes) != 4 || e.Attributes[0].Key != "token" {
		t.Fatalf("%+v %v", e, err)
	}
	if _, err := DispatchEvents(f.results[0].Leaf, "gno.land/r/x/hlmailbox"); err != ErrNoDispatch {
		t.Fatal(err)
	}
	g := fx[701854]
	r, err = ParseResult(g.results[0].Leaf)
	if err != nil || len(r.Events) != 1 || r.Events[0].TypeURL != "/bank.TransferEvent" {
		t.Fatalf("%+v %v", r, err)
	}
	for _, h := range heights(fx) {
		for _, tr := range fx[h].results {
			if tr.Err == nil {
				if _, err := ParseResult(tr.Leaf); err != nil {
					t.Fatalf("height %d: %v", h, err)
				}
			}
		}
	}
}

func TestDispatchRoundTrip(t *testing.T) {
	d := Dispatch{Nonce: 7, Origin: 1198419788, Destination: 1399811149}
	for i := range d.ID {
		d.ID[i], d.Sender[i], d.Recipient[i], d.Hash[i] = 0xab, 2, 3, 0xcd
	}
	path := "gno.land/r/g1abc/hlmailbox"
	leaf := EncodeResult(nil, nil, [][]byte{Any(EventTypeURL, EncodeEvent(d.ToEvent(path)))})
	got, err := DispatchEvents(leaf, path)
	if err != nil || len(got) != 1 || got[0] != d {
		t.Fatalf("%+v %v", got, err)
	}
	if _, err := DispatchEvents(leaf, "gno.land/r/other"); err != ErrNoDispatch {
		t.Fatal(err)
	}
	failed := EncodeResult(Any("/std.UnauthorizedError", nil), nil, [][]byte{Any(EventTypeURL, EncodeEvent(d.ToEvent(path)))})
	if _, err := DispatchEvents(failed, path); err != ErrNoDispatch {
		t.Fatal(err)
	}
	bad := d.ToEvent(path)
	bad.Attributes[0].Value = "AB" + bad.Attributes[0].Value[2:]
	leaf = EncodeResult(nil, nil, [][]byte{Any(EventTypeURL, EncodeEvent(bad))})
	if _, err := DispatchEvents(leaf, path); err != ErrBadDispatch {
		t.Fatal(err)
	}
	kind, seq, ok := DecisionPrefix([]byte{7, 0, 0, 0, 0, 0, 0, 1, 2, 9})
	if !ok || kind != 7 || seq != 258 {
		t.Fatal(kind, seq, ok)
	}
	if _, _, ok := DecisionPrefix([]byte{7, 0, 0}); ok {
		t.Fatal("a short body parsed")
	}
}

func TestProofsRoundTrip(t *testing.T) {
	for n := 1; n <= 20; n++ {
		items := make([][]byte, n)
		for i := range items {
			items[i] = make([]byte, i+1)
			for j := range items[i] {
				items[i][j] = byte(i)
			}
		}
		root, _ := SimpleHash(items)
		for i := range items {
			p, err := Proof(items, i)
			if err != nil {
				t.Fatal(err)
			}
			if err := p.Verify(root, items[i]); err != nil {
				t.Fatalf("n %d i %d: %v", n, i, err)
			}
			if n > 1 {
				bad := *p
				bad.Index = (bad.Index + 1) % uint32(n)
				if bad.Verify(root, items[i]) == nil {
					t.Fatalf("n %d i %d: a shifted index verifies", n, i)
				}
			}
		}
	}
	if SplitPoint(2) != 1 || SplitPoint(3) != 2 || SplitPoint(5) != 4 || SplitPoint(17) != 16 {
		t.Fatal("split points")
	}
}

func TestVarints(t *testing.T) {
	if hex.EncodeToString(putUvarint(nil, 300)) != "ac02" || hex.EncodeToString(putZigzag(nil, -1)) != "01" || hex.EncodeToString(putZigzag(nil, 60)) != "78" {
		t.Fatal("varints")
	}
	if hex.EncodeToString(putFixed64Field(nil, 2, 0x0ab5a6)) != "11a6b50a0000000000" {
		t.Fatal("fixed64")
	}
	if len(timeBare(0, 0)) != 0 || len(putBytesField(nil, 1, nil)) != 0 {
		t.Fatal("zero fields are omitted")
	}
	tm, err := ParseTime("2026-10-10T11:42:33.040788319Z")
	if err != nil || tm != (Time{Secs: 1791632553, Nanos: 40788319}) {
		t.Fatalf("%+v %v", tm, err)
	}
	if s := itoa(-12); s != "-12" || itoa(0) != "0" || itoa(2147483647) != strconv.Itoa(2147483647) {
		t.Fatal(s)
	}
}
