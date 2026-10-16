package ethx

import (
	"bufio"
	"bytes"
	"crypto/ed25519"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"

	"github.com/gnolang/bridges/solana/bridge/gnolight"
)

// gnoland-1's own blocks (bridge/programs/fixtures), as the gnolight tests read them.
type fixture struct {
	block   *gnolight.Block
	commit  *gnolight.Commit
	set     *gnolight.ValidatorSet
	results []*gnolight.TxResult
}

func loadFixture(t *testing.T, h int64) *fixture {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "bridge", "programs", "fixtures", strconv.FormatInt(h, 10)+".json"))
	if err != nil {
		t.Skip("no fixture: " + err.Error())
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
	f := &fixture{}
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

func vectors(t *testing.T, name string) [][]string {
	t.Helper()
	f, err := os.Open(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var out [][]string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<22)
	for sc.Scan() {
		if l := strings.TrimSpace(sc.Text()); l != "" {
			out = append(out, strings.Fields(l))
		}
	}
	return out
}

func unhex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(strings.TrimPrefix(s, "0x"))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// An update of gnoland-1's own headers is byte for byte what Foundry's cast encodes for the same header, commit and
// sets (testdata/update_vectors.txt, written by testdata/gen with its own choice of precommits): the four 60-power
// signatures of a 301-power set (the power-1 slot passed over), an announcing header with its next set, a commit
// with two absent precommits, and a set whose power-1 validator sorts first.
func TestUpdateCalldataMatchesCast(t *testing.T) {
	vs := vectors(t, "update_vectors.txt")
	if len(vs) != 4 {
		t.Fatalf("%d vectors", len(vs))
	}
	for _, v := range vs {
		h, _ := strconv.ParseInt(v[0], 10, 64)
		next, _ := strconv.ParseInt(v[1], 10, 64)
		f := loadFixture(t, h)
		sh := &SignedHeader{ChainID: "gnoland-1", Header: f.block.Header, Commit: f.commit, Set: f.set}
		if next != 0 {
			sh.Next = loadFixture(t, next).set
		}
		calls, err := UpdateCalls(sh, 0)
		if err != nil {
			t.Fatalf("height %d: %v", h, err)
		}
		if len(calls) != 1 || !calls[0].Final {
			t.Fatalf("height %d: %d calls", h, len(calls))
		}
		if want := unhex(t, v[2]); !bytes.Equal(calls[0].Data, want) {
			t.Fatalf("height %d: the calldata differs from cast's (%d bytes against %d)", h, len(calls[0].Data), len(want))
		}
		// the round trip: the header hash the leaves give is the block's, the slots the chosen ones
		m, hash, round, slots, err := HeaderCallOf(calls[0].Data)
		if err != nil || m != "update" || hash != f.block.Hash || round != f.commit.Round() || fmt.Sprint(slots) != fmt.Sprint(calls[0].Slots) {
			t.Fatalf("height %d: decoded %s %x %d %v (%v)", h, m, hash, round, slots, err)
		}
	}
}

// The fewest signatures that cross 2/3, the largest powers first, ties by slot, in slot order; a signature that
// does not verify is never chosen; a commit that cannot cross is refused.
func TestChoosePrecommits(t *testing.T) {
	const chain = "dev"
	mk := func(powers []int64, signed []bool, bad map[int]bool) (*gnolight.ValidatorSet, *gnolight.Commit, int64) {
		set := &gnolight.ValidatorSet{}
		var keys []ed25519.PrivateKey
		for i, p := range powers {
			seed := make([]byte, 32)
			seed[0] = byte(i + 1)
			k := ed25519.NewKeyFromSeed(seed)
			keys = append(keys, k)
			var pk [32]byte
			copy(pk[:], k.Public().(ed25519.PublicKey))
			set.Validators = append(set.Validators, gnolight.Validator{PubKey: pk, Power: p})
		}
		// keep the keys with their validators through the sort by address
		type kv struct {
			v gnolight.Validator
			k ed25519.PrivateKey
		}
		var both []kv
		for i := range keys {
			both = append(both, kv{set.Validators[i], keys[i]})
		}
		set.Sort()
		for i, v := range set.Validators {
			for _, b := range both {
				if b.v == v {
					keys[i] = b.k
				}
			}
		}
		const height = 10
		bid := gnolight.BlockID{Hash: bytes.Repeat([]byte{7}, 32), Parts: gnolight.PartSetHeader{Total: 1, Hash: bytes.Repeat([]byte{8}, 32)}}
		cm := &gnolight.Commit{BlockID: bid, Precommits: make([]*gnolight.Precommit, len(powers)), Signatures: make([][]byte, len(powers))}
		for i := range powers {
			if !signed[i] {
				continue
			}
			p := &gnolight.Precommit{Round: 0, Time: gnolight.Time{Secs: 1_700_000_000 + int64(i), Nanos: int32(i)}}
			cm.Precommits[i] = p
			sig := ed25519.Sign(keys[i], gnolight.PrecommitSignBytes(chain, height, 0, bid, p.Time))
			if bad[i] {
				sig[5] ^= 1
			}
			cm.Signatures[i] = sig
		}
		return set, cm, height
	}
	all := func(n int) []bool {
		out := make([]bool, n)
		for i := range out {
			out[i] = true
		}
		return out
	}
	powerOf := func(set *gnolight.ValidatorSet, slots []int) (sum int64) {
		for _, i := range slots {
			sum += set.Validators[i].Power
		}
		return sum
	}
	// equal powers: the lowest slots, just over 2/3
	set, cm, h := mk([]int64{60, 60, 60, 60, 60, 60}, all(6), nil)
	got, err := ChoosePrecommits(chain, h, set, cm)
	if err != nil || fmt.Sprint(got) != "[0 1 2 3 4]" {
		t.Fatalf("%v %v", got, err)
	}
	// unequal powers: the fewest — 50 and 30 of 115 cross alone, where the slots in order would take four
	set, cm, h = mk([]int64{10, 50, 20, 30, 5}, all(5), nil)
	got, err = ChoosePrecommits(chain, h, set, cm)
	if err != nil || len(got) != 2 || powerOf(set, got) != 80 || !gnolight.CommitRule(powerOf(set, got), set.TotalPower()) {
		t.Fatalf("%v %v (power %d of %d)", got, err, powerOf(set, got), set.TotalPower())
	}
	// a bad signature is passed over; an absent precommit too
	set, cm, h = mk([]int64{60, 60, 60, 60, 60, 60, 60}, []bool{true, true, false, true, true, true, true}, map[int]bool{0: true})
	got, err = ChoosePrecommits(chain, h, set, cm)
	if err != nil || fmt.Sprint(got) != "[1 3 4 5 6]" {
		t.Fatalf("%v %v", got, err)
	}
	// with six, the four left carry exactly 2/3: refused, not "more than"
	set, cm, h = mk([]int64{60, 60, 60, 60, 60, 60}, []bool{true, true, false, true, true, true}, map[int]bool{0: true})
	if got, err := ChoosePrecommits(chain, h, set, cm); err == nil {
		t.Fatalf("240 of 360 taken: %v", got)
	}
	// not enough: refused
	set, cm, h = mk([]int64{60, 60, 60}, []bool{true, true, false}, nil)
	if _, err := ChoosePrecommits(chain, h, set, cm); err == nil {
		t.Fatal("two thirds exactly was taken for more")
	}
	// a precommit of another round than the commit's is never chosen (its sign bytes would be another's)
	set, cm, h = mk([]int64{60, 60, 60, 60}, all(4), nil)
	cm.Precommits[1].Round = 1
	got, err = ChoosePrecommits(chain, h, set, cm)
	if err != nil || fmt.Sprint(got) != "[0 2 3]" {
		t.Fatalf("%v %v", got, err)
	}
}

// A staged update splits the chosen precommits in slot order into calls of at most the bound, the same header and
// sets in each, only the last one final; a header proof the same.
func TestStagedUpdate(t *testing.T) {
	f := loadFixture(t, 720333)
	sh := &SignedHeader{ChainID: "gnoland-1", Header: f.block.Header, Commit: f.commit, Set: f.set}
	one, err := UpdateCalls(sh, 0)
	if err != nil {
		t.Fatal(err)
	}
	staged, err := UpdateCalls(sh, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(staged) != 2 || fmt.Sprint(staged[0].Slots) != "[0 1 2]" || fmt.Sprint(staged[1].Slots) != "[4]" || staged[0].Final || !staged[1].Final {
		t.Fatalf("%+v", staged)
	}
	var all []int
	for _, c := range staged {
		_, hash, _, slots, err := HeaderCallOf(c.Data)
		if err != nil || hash != f.block.Hash {
			t.Fatalf("%x %v", hash, err)
		}
		all = append(all, slots...)
	}
	if fmt.Sprint(all) != fmt.Sprint(one[0].Slots) {
		t.Fatalf("the stages carry %v, the one call %v", all, one[0].Slots)
	}
	proof, err := ProveHeaderCalls(sh, 2)
	if err != nil || len(proof) != 2 {
		t.Fatalf("%d %v", len(proof), err)
	}
	if m, _, _, _, err := HeaderCallOf(proof[0].Data); err != nil || m != "proveHeader" {
		t.Fatalf("%s %v", m, err)
	}
	// an announcing header needs its next set, and a next set needs an announcing header
	f2 := loadFixture(t, 720332)
	if _, err := UpdateCalls(&SignedHeader{ChainID: "gnoland-1", Header: f2.block.Header, Commit: f2.commit, Set: f2.set}, 0); err == nil {
		t.Fatal("an announcing header went without its next set")
	}
	sh.Next = f.set
	if _, err := UpdateCalls(sh, 0); err == nil {
		t.Fatal("a next set went with a header announcing nothing")
	}
}

// The verify metadata and the process calldata are byte for byte cast's (testdata/meta_vectors.txt) for real
// results leaves of gnoland-1, and the metadata equals a hand layout of abi.encode's words.
func TestVerifyMetadataMatchesCast(t *testing.T) {
	msg := unhex(t, "0x0300000007476e6f540000000000000000000000000000000000000000000000000000000000000001deadbeef")
	for _, v := range vectors(t, "meta_vectors.txt") {
		h, _ := strconv.ParseInt(v[0], 10, 64)
		i, _ := strconv.Atoi(v[1])
		f := loadFixture(t, h)
		leaves, err := gnolight.ResultLeaves(f.results)
		if err != nil {
			t.Fatal(err)
		}
		proof, err := gnolight.Proof(leaves, i)
		if err != nil {
			t.Fatal(err)
		}
		next := loadFixture(t, h+1)
		meta, err := VerifyMetadata(next.block.Hash, proof, leaves[i])
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(meta, unhex(t, v[2])) {
			t.Fatalf("height %d leaf %d: the metadata differs from cast's", h, i)
		}
		if !bytes.Equal(meta, handMetadata(next.block.Hash, proof, leaves[i])) {
			t.Fatalf("height %d leaf %d: the metadata differs from the hand layout", h, i)
		}
		pc, err := ProcessCall(meta, msg)
		if err != nil || !bytes.Equal(pc, unhex(t, v[3])) {
			t.Fatalf("height %d leaf %d: the process calldata differs from cast's (%v)", h, i, err)
		}
		hh, total, index, aunts, leaf, err := MetadataOf(meta)
		if err != nil || hh != next.block.Hash || total != proof.Total || index != proof.Index || len(aunts) != len(proof.Aunts) || !bytes.Equal(leaf, leaves[i]) {
			t.Fatalf("round trip: %v", err)
		}
		// the proof the metadata carries proves the leaf under header h+1's LastResultsHash
		root, ok := next.block.Header.LastResultsHashArray()
		if !ok || (&gnolight.SimpleProof{Total: total, Index: index, LeafHash: gnolight.LeafHash(leaf), Aunts: aunts}).Verify(root, leaf) != nil {
			t.Fatal("the metadata's proof does not verify under the next header's LastResultsHash")
		}
	}
	if _, err := VerifyMetadata([32]byte{}, &gnolight.SimpleProof{LeafHash: [32]byte{1}}, []byte("x")); err == nil {
		t.Fatal("a leaf not the proof's was encoded")
	}
}

// handMetadata lays out abi.encode(bytes32, uint32, uint32, bytes32[], bytes) word by word.
func handMetadata(hash [32]byte, p *gnolight.SimpleProof, leaf []byte) []byte {
	word := func(n uint64) []byte {
		w := make([]byte, 32)
		binary.BigEndian.PutUint64(w[24:], n)
		return w
	}
	head := 5 * 32
	auntsAt := head
	auntsLen := 32 + 32*len(p.Aunts)
	leafAt := auntsAt + auntsLen
	var b []byte
	b = append(b, hash[:]...)
	b = append(b, word(uint64(p.Total))...)
	b = append(b, word(uint64(p.Index))...)
	b = append(b, word(uint64(auntsAt))...)
	b = append(b, word(uint64(leafAt))...)
	b = append(b, word(uint64(len(p.Aunts)))...)
	for _, a := range p.Aunts {
		b = append(b, a[:]...)
	}
	b = append(b, word(uint64(len(leaf)))...)
	padded := make([]byte, (len(leaf)+31)/32*32)
	copy(padded, leaf)
	return append(b, padded...)
}

// The selectors and topics the machines use are cast's (testdata/sigs.txt) and the keccak of their signatures.
func TestSelectorsAndTopics(t *testing.T) {
	want := map[string]string{}
	for _, v := range vectors(t, "sigs.txt") {
		want[v[0]+" "+v[2]] = v[1]
	}
	sel := func(c *Contract, m string) string { s := c.Selector(m); return "0x" + hex.EncodeToString(s[:]) }
	checks := map[string]string{
		"sig process(bytes,bytes)": sel(Mailbox, "process"),
		"sig delivered(bytes32)":   sel(Mailbox, "delivered"),
		"sig update(bytes[],(uint32,bytes32,int64,(uint16,int64,int32,bytes32,bytes32)[]),(bytes32,int64)[],(bytes32,int64)[])": sel(LightClient, "update"),
		"sig proveHeader(bytes[],(uint32,bytes32,int64,(uint16,int64,int32,bytes32,bytes32)[]),(bytes32,int64)[])":              sel(LightClient, "proveHeader"),
		"sig verify(bytes,bytes)":                    sel(LightClient, "verify"),
		"sig execute(uint64)":                        sel(Admin, "execute"),
		"sig transferRemote(uint32,bytes32,uint256)": sel(HypERC20, "transferRemote"),
		"sig entry(bytes32)":                         sel(LightClient, "entry"),
		"sig initialize(string,uint32,bytes[],(bytes32,int64)[],address)": sel(LightClient, "initialize"),
		"topic Dispatch(address,uint32,bytes32,bytes)":                    Mailbox.Event("Dispatch").Hex(),
		"topic Process(uint32,bytes32,address)":                           Mailbox.Event("Process").Hex(),
		"topic ProcessId(bytes32)":                                        Mailbox.Event("ProcessId").Hex(),
		"topic DispatchId(bytes32)":                                       Mailbox.Event("DispatchId").Hex(),
		"topic HeaderRecorded(bytes32,int64,uint64,uint64,bytes32,bool)":  LightClient.Event("HeaderRecorded").Hex(),
		"topic StageProgress(bytes32,address,bytes32,int64,int64,int64)":  LightClient.Event("StageProgress").Hex(),
		"topic DecisionRecorded(uint64,uint8,uint64)":                     Admin.Event("DecisionRecorded").Hex(),
		"topic SentTransferRemote(uint32,bytes32,uint256)":                HypERC20.Event("SentTransferRemote").Hex(),
	}
	for k, got := range checks {
		w, ok := want[k]
		if !ok {
			t.Fatalf("no vector for %s", k)
		}
		if !strings.EqualFold(got, w) {
			t.Errorf("%s: %s, cast says %s", k, got, w)
		}
		sig := strings.SplitN(k, " ", 2)[1]
		h := crypto.Keccak256([]byte(sig))
		if strings.HasPrefix(k, "sig ") && !strings.EqualFold(got, "0x"+hex.EncodeToString(h[:4])) || strings.HasPrefix(k, "topic ") && !strings.EqualFold(got, common.BytesToHash(h).Hex()) {
			t.Errorf("%s: not the keccak of its signature", k)
		}
	}
}
