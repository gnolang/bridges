package gnolight

import (
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The fixture bridge/tools/resultsgen writes with tm2's own amino (gno v1.5.1, the same bytes at v1.2.0, v1.3.0,
// v1.4.0 and v1.5.0): the closed set of error types found by walking amino's registry, and blocks of results as
// block_results serves them beside the leaves and root tm2 hashes.
type differential struct {
	Gno        string `json:"gno"`
	ErrorTypes []struct {
		URL    string `json:"url"`
		Kind   string `json:"kind"`
		Fields int    `json:"fields"`
	} `json:"error_types"`
	Block, Lossy struct {
		Names        []string        `json:"names"`
		Leaves       []string        `json:"leaves"`
		ResultsHash  string          `json:"results_hash"`
		BlockResults json.RawMessage `json:"block_results"`
	}
}

func loadDifferential(t *testing.T) *differential {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "results_differential.json"))
	if err != nil {
		t.Fatal(err)
	}
	var d differential
	if err := json.Unmarshal(raw, &d); err != nil {
		t.Fatal(err)
	}
	if len(d.ErrorTypes) == 0 || len(d.Block.Names) != len(d.Block.Leaves) || len(d.Lossy.Names) != len(d.Lossy.Leaves) {
		t.Fatal("a malformed differential fixture")
	}
	return &d
}

// errorTypeOf is a leaf's error type URL, "" for a leaf without one.
func errorTypeOf(t *testing.T, leaf []byte) string {
	t.Helper()
	fs, err := fields(leaf)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range fs {
		if f.num == 1 {
			a, err := ParseAny(f.value)
			if err != nil {
				t.Fatal(err)
			}
			return a.TypeURL
		}
	}
	return ""
}

// Every result of the generator's block — each of the 43 error types bare and with data and events, the string
// error around its length and escaping boundaries, two without error — rebuilds from block_results' JSON to the
// leaf tm2 hashes, and the block's root is tm2's ResultsHash: no disagreement.
func TestResultsDifferentialAgainstAmino(t *testing.T) {
	d := loadDifferential(t)
	rs, err := BlockResultsFromJSON(d.Block.BlockResults)
	if err != nil {
		t.Fatal(err)
	}
	if len(rs) != len(d.Block.Leaves) {
		t.Fatalf("%d results for %d leaves", len(rs), len(d.Block.Leaves))
	}
	disagree := 0
	seen := map[string]bool{}
	for i, r := range rs {
		name := d.Block.Names[i]
		if r.Err != nil {
			t.Errorf("%s: not rebuilt: %v", name, r.Err)
			disagree++
			continue
		}
		if got := hex.EncodeToString(r.Leaf); got != d.Block.Leaves[i] {
			t.Errorf("%s: leaf %s, amino's %s", name, got, d.Block.Leaves[i])
			disagree++
			continue
		}
		if u := errorTypeOf(t, r.Leaf); u != "" {
			seen[u] = true
		}
	}
	if disagree > 0 {
		t.Fatalf("%d of %d results disagree with amino", disagree, len(rs))
	}
	for _, et := range d.ErrorTypes {
		if !seen[et.URL] {
			t.Fatalf("%s: no result of the block carries it", et.URL)
		}
	}
	leaves, err := ResultLeaves(rs)
	if err != nil {
		t.Fatal(err)
	}
	root, _ := SimpleHash(leaves)
	if hex.EncodeToString(root[:]) != d.Block.ResultsHash {
		t.Fatalf("root %x, tm2's ResultsHash %s", root, d.Block.ResultsHash)
	}
	t.Logf("%s: %d error types, %d results, 0 disagreements", d.Gno, len(seen), len(rs))
}

// What the JSON cannot carry: a string that is not UTF-8 — a panic's message cut inside a rune, a realm's own
// bytes, an event's attribute — reads back as U+FFFD, so its leaf rebuilds (it is no schema error) to other bytes
// than the chain hashed, and differs from tm2's exactly where the bytes were replaced. Only the root's check
// against the header shows it; the relayer then treats the block as unbuildable (agent resultsMatch).
func TestStringsJSONCannotCarry(t *testing.T) {
	d := loadDifferential(t)
	rs, err := BlockResultsFromJSON(d.Lossy.BlockResults)
	if err != nil {
		t.Fatal(err)
	}
	if len(rs) != 3 {
		t.Fatalf("%d lossy results", len(rs))
	}
	for i, r := range rs {
		name := d.Lossy.Names[i]
		if r.Err != nil {
			t.Fatalf("%s: %v (a leaf is built; the root's check catches it)", name, r.Err)
		}
		got := hex.EncodeToString(r.Leaf)
		if got == d.Lossy.Leaves[i] {
			t.Fatalf("%s: rebuilt to amino's bytes, yet JSON cannot carry them", name)
		}
		if !strings.Contains(got, "efbfbd") {
			t.Fatalf("%s: no U+FFFD in the rebuilt leaf %s", name, got)
		}
	}
	leaves, _ := ResultLeaves(rs)
	if root, _ := SimpleHash(leaves); hex.EncodeToString(root[:]) == d.Lossy.ResultsHash {
		t.Fatal("the lossy block's root matched")
	}
}
