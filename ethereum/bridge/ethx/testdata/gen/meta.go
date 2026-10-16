package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/gnolang/bridges/solana/bridge/gnolight"
)

// metaArgs prints, for block h's results leaf i, the verify metadata's five arguments in cast's syntax: header
// h+1's hash, the proof's total and index, the aunts, the leaf.
func metaArgs(dir string, h int64, i int) {
	raw, err := os.ReadFile(fmt.Sprintf("%s/%d.json", dir, h))
	if err != nil {
		panic(err)
	}
	var j struct {
		BlockResults json.RawMessage `json:"block_results"`
	}
	if err := json.Unmarshal(raw, &j); err != nil {
		panic(err)
	}
	rs, err := gnolight.BlockResultsFromJSON(j.BlockResults)
	if err != nil {
		panic(err)
	}
	leaves, err := gnolight.ResultLeaves(rs)
	if err != nil {
		panic(err)
	}
	p, err := gnolight.Proof(leaves, i)
	if err != nil {
		panic(err)
	}
	next, _, _ := load(dir, h+1)
	hash := next.Header.Hash()
	var aunts []string
	for _, a := range p.Aunts {
		aunts = append(aunts, hx(a[:]))
	}
	fmt.Println(hx(hash[:]))
	fmt.Println(p.Total)
	fmt.Println(p.Index)
	fmt.Println("[" + strings.Join(aunts, ",") + "]")
	fmt.Println(hx(leaves[i]))
}
