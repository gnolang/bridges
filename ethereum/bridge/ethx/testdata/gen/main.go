// gen prints, for one gnoland-1 fixture, the arguments of an update in cast's syntax (independently of ethx: its
// own choice of precommits, its own formatting), so `cast calldata` encodes the expected bytes.
package main

import (
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/gnolang/bridges/solana/bridge/gnolight"
)

func load(dir string, h int64) (*gnolight.Block, *gnolight.Commit, *gnolight.ValidatorSet) {
	raw, err := os.ReadFile(fmt.Sprintf("%s/%d.json", dir, h))
	if err != nil {
		panic(err)
	}
	var j struct {
		Block      json.RawMessage `json:"block"`
		Commit     json.RawMessage `json:"commit"`
		Validators json.RawMessage `json:"validators"`
	}
	if err := json.Unmarshal(raw, &j); err != nil {
		panic(err)
	}
	b, err := gnolight.BlockFromJSON(j.Block)
	if err != nil {
		panic(err)
	}
	c, _, err := gnolight.CommitFromJSON(j.Commit)
	if err != nil {
		panic(err)
	}
	s, err := gnolight.ValidatorsFromJSON(j.Validators)
	if err != nil {
		panic(err)
	}
	return b, c, s
}

func hx(b []byte) string { return "0x" + hex.EncodeToString(b) }

func setText(s *gnolight.ValidatorSet) string {
	var parts []string
	for _, v := range s.Validators {
		parts = append(parts, fmt.Sprintf("(%s,%d)", hx(v.PubKey[:]), v.Power))
	}
	return "[" + strings.Join(parts, ",") + "]"
}

func main() {
	if os.Args[1] == "meta" {
		var h int64
		var i int
		fmt.Sscan(os.Args[3], &h)
		fmt.Sscan(os.Args[4], &i)
		metaArgs(os.Args[2], h, i)
		return
	}
	dir, h, nextH := os.Args[1], int64(0), int64(0)
	fmt.Sscan(os.Args[2], &h)
	if len(os.Args) > 3 {
		fmt.Sscan(os.Args[3], &nextH)
	}
	b, c, s := load(dir, h)
	// every verifying precommit with its power; the largest powers first, ties by slot, until > 2/3
	type cand struct {
		slot  int
		power int64
	}
	var cs []cand
	for i, p := range c.Precommits {
		if p == nil {
			continue
		}
		sb := gnolight.PrecommitSignBytes(b.Header.ChainID, h, p.Round, c.BlockID, p.Time)
		if ed25519.Verify(s.Validators[i].PubKey[:], sb, c.Signatures[i]) {
			cs = append(cs, cand{i, s.Validators[i].Power})
		}
	}
	sort.SliceStable(cs, func(a, b int) bool { return cs[a].power > cs[b].power })
	var total, tally int64
	for _, v := range s.Validators {
		total += v.Power
	}
	var slots []int
	for _, x := range cs {
		slots = append(slots, x.slot)
		tally += x.power
		if 3*tally > 2*total {
			break
		}
	}
	sort.Ints(slots)
	var pcs []string
	round := int64(0)
	for _, i := range slots {
		p := c.Precommits[i]
		round = p.Round
		pcs = append(pcs, fmt.Sprintf("(%d,%d,%d,%s,%s)", i, p.Time.Secs, p.Time.Nanos, hx(c.Signatures[i][:32]), hx(c.Signatures[i][32:])))
	}
	var leaves []string
	for _, l := range b.Header.Leaves() {
		leaves = append(leaves, hx(l))
	}
	next := "[]"
	if nextH != 0 {
		_, _, ns := load(dir, nextH)
		next = setText(ns)
	}
	fmt.Println("[" + strings.Join(leaves, ",") + "]")
	fmt.Printf("(%d,%s,%d,[%s])\n", c.BlockID.Parts.Total, hx(c.BlockID.Parts.Hash), round, strings.Join(pcs, ","))
	fmt.Println(setText(s))
	fmt.Println(next)
	fmt.Fprintln(os.Stderr, "slots", slots)
}
