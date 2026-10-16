package repro

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"math/big"
	"strings"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
)

// The code check (PLAN.md §5.8, §5.9, §6.5): a deployed contract's runtime code against its build, byte for byte,
// except where the build leaves an immutable's 32 bytes to its constructor — those are masked out of the comparison
// and read instead, each beside the value it must have, because a value held in an immutable is outside any masked
// code hash: a token built over a stand-in "mailbox" has the release's code exactly, and only its immutable `mailbox`
// says whose it is (the red team's ER1, and round 2's E2-RT3 for every kind 8 and 9 target).

// Reading is one immutable as a deployed code holds it.
type Reading struct {
	Name  string
	Value [32]byte
	// Split says its places in the code hold different values (the compiler writes one value in every place).
	Split bool
}

// CompareCode compares deployed code with the build, the immutables masked; it answers each immutable's value and
// how the rest differs ("" when it is the build's byte for byte).
func CompareCode(onchain []byte, a *Artifact) ([]Reading, string) {
	if len(onchain) == 0 {
		return nil, "no code at the address"
	}
	if len(onchain) != len(a.Runtime) {
		return nil, fmt.Sprintf("the code is %d bytes, the build's %d", len(onchain), len(a.Runtime))
	}
	mask := make([]bool, len(a.Runtime))
	var out []Reading
	for _, im := range a.Immutables {
		r := Reading{Name: im.Name}
		for i, ref := range im.Refs {
			var w [32]byte
			copy(w[:], onchain[ref.Start:ref.Start+ref.Length])
			if i == 0 {
				r.Value = w
			} else if w != r.Value {
				r.Split = true
			}
			for k := ref.Start; k < ref.Start+ref.Length; k++ {
				mask[k] = true
			}
		}
		out = append(out, r)
	}
	n, first := 0, -1
	for i := range onchain {
		if !mask[i] && onchain[i] != a.Runtime[i] {
			if first < 0 {
				first = i
			}
			n++
		}
	}
	if n > 0 {
		return out, fmt.Sprintf("%d bytes differ from the build outside the immutables, the first at offset %d", n, first)
	}
	return out, ""
}

// WordKind says how a word is read and written: an address, an unsigned number, or 32 raw bytes.
type WordKind int

const (
	KindAddr WordKind = iota
	KindUint
	KindBytes32
)

// Expect is the value an immutable (or a constructor's argument) must have.
type Expect struct {
	Kind WordKind
	Want [32]byte
	What string // where the value comes from ("our mailbox", "gno.land's domain", …)
	// Any says any value is right (a field the contract never reads in this use: a successor's launch limits); the
	// value is printed all the same.
	Any bool
}

func ExpectAddr(a common.Address, what string) Expect {
	return Expect{Kind: KindAddr, Want: common.BytesToHash(a.Bytes()), What: what}
}

func ExpectUint(n *big.Int, what string) Expect {
	return Expect{Kind: KindUint, Want: common.BigToHash(n), What: what}
}

func ExpectU64(n uint64, what string) Expect {
	return ExpectUint(new(big.Int).SetUint64(n), what)
}

func ExpectBytes32(b [32]byte, what string) Expect {
	return Expect{Kind: KindBytes32, Want: b, What: what}
}

// ExpectAny is a value the reader prints and holds to nothing.
func ExpectAny(k WordKind, what string) Expect { return Expect{Kind: k, What: what, Any: true} }

// Render writes a word as its kind reads.
func (k WordKind) Render(w [32]byte) string {
	switch k {
	case KindAddr:
		if !IsZero(w[:12]) {
			return "0x" + hex.EncodeToString(w[:]) + " (not an address: its top 12 bytes are not zero)"
		}
		return common.BytesToAddress(w[12:]).Hex()
	case KindUint:
		return new(big.Int).SetBytes(w[:]).String()
	}
	return "0x" + hex.EncodeToString(w[:])
}

// IsZero says whether every byte is zero.
func IsZero(b []byte) bool {
	for _, x := range b {
		if x != 0 {
			return false
		}
	}
	return true
}

// Judgement is one line of a check: ok, or a failure, with its words.
type Judgement struct {
	OK   bool
	Text string
}

// JudgeCode is the code check of one contract: the code outside the masks is the build's, and every immutable
// holds the value it must have — an immutable this check has no expectation for is a failure too (it cannot vouch
// for a value it does not know), as is one whose places in the code disagree.
func JudgeCode(contract string, at common.Address, onchain []byte, a *Artifact, want map[string]Expect) []Judgement {
	readings, diff := CompareCode(onchain, a)
	var out []Judgement
	if diff != "" {
		out = append(out, Judgement{false, fmt.Sprintf("%s (%s) code against the build of %s: %s", contract, at.Hex(), a.Name, diff)})
	} else {
		out = append(out, Judgement{true, fmt.Sprintf("%s (%s) code is the build of %s byte for byte outside its %d masked immutable(s)", contract, at.Hex(), a.Name, len(a.Immutables))})
	}
	for _, r := range readings {
		e, ok := want[r.Name]
		switch {
		case !ok:
			out = append(out, Judgement{false, fmt.Sprintf("%s's masked immutable %s = 0x%x: no expected value is known to this check, so it cannot vouch for it", contract, r.Name, r.Value)})
		case r.Split:
			out = append(out, Judgement{false, fmt.Sprintf("%s's masked immutable %s holds different values in its places in the code (the compiler writes one)", contract, r.Name)})
		case e.Any:
			out = append(out, Judgement{true, fmt.Sprintf("%s's masked immutable %s = %s (%s)", contract, r.Name, e.Kind.Render(r.Value), e.What)})
		case r.Value != e.Want:
			out = append(out, Judgement{false, fmt.Sprintf("%s's masked immutable %s = %s, but it must be %s (%s)", contract, r.Name, e.Kind.Render(r.Value), e.Kind.Render(e.Want), e.What)})
		default:
			out = append(out, Judgement{true, fmt.Sprintf("%s's masked immutable %s = %s (%s)", contract, r.Name, e.Kind.Render(r.Value), e.What)})
		}
	}
	return out
}

// Failures counts the failed judgements.
func Failures(js []Judgement) int {
	n := 0
	for _, j := range js {
		if !j.OK {
			n++
		}
	}
	return n
}

// JudgementText is the lines as the audit prints them.
func JudgementText(js []Judgement) string {
	var b strings.Builder
	for _, j := range js {
		if j.OK {
			b.WriteString("ok   " + j.Text + "\n")
		} else {
			b.WriteString("FAIL " + j.Text + "\n")
		}
	}
	return b.String()
}

// ----------------------------------------------------------------------------------- creation inputs --

// A contract's creation transaction carries its whole initcode: the build's creation code, then the ABI encoding of
// its constructor's arguments. A deployed runtime says nothing of what the constructor ran — an initcode can write a
// decision record, a header entry or a balance into storage and return the build's runtime exactly (the plan red
// team's E2-RT1) — so the input is held to the build byte for byte, and the arguments read.

// CreationInput is a creation transaction's input as a build reads it.
type CreationInput struct {
	Args    []any  // the constructor's arguments, decoded by the build's ABI (nil when they do not decode)
	ArgsRaw []byte // the bytes after the build's creation code
	Diff    string // "" when the input is the build's creation code and a canonical encoding of its arguments
}

// SplitCreation reads a creation transaction's input against a build: its creation code byte for byte, then the
// constructor's arguments, which must decode by the build's ABI and encode back to the same bytes (no byte more).
func SplitCreation(input []byte, a *Artifact) CreationInput {
	var c CreationInput
	if len(input) < len(a.Creation) || !bytes.Equal(input[:len(a.Creation)], a.Creation) {
		n, first := 0, -1
		for i := 0; i < len(a.Creation) && i < len(input); i++ {
			if input[i] != a.Creation[i] {
				if first < 0 {
					first = i
				}
				n++
			}
		}
		switch {
		case len(input) < len(a.Creation):
			c.Diff = fmt.Sprintf("the input is %d bytes, shorter than the build's %d-byte creation code", len(input), len(a.Creation))
		default:
			c.Diff = fmt.Sprintf("the input's first %d bytes are not the build's creation code: %d differ, the first at offset %d", len(a.Creation), n, first)
		}
		return c
	}
	c.ArgsRaw = append([]byte{}, input[len(a.Creation):]...)
	ins := a.ABI.Constructor.Inputs
	if len(ins) == 0 {
		if len(c.ArgsRaw) != 0 {
			c.Diff = fmt.Sprintf("the build's constructor takes no arguments, and %d bytes follow its creation code", len(c.ArgsRaw))
		}
		return c
	}
	vals, err := ins.Unpack(c.ArgsRaw)
	if err != nil {
		c.Diff = "the constructor's arguments do not decode by the build's ABI: " + err.Error()
		return c
	}
	back, err := ins.Pack(vals...)
	if err != nil || !bytes.Equal(back, c.ArgsRaw) {
		c.Diff = fmt.Sprintf("the constructor's arguments are not a canonical encoding (%d bytes; they encode back to %d): bytes no argument carries", len(c.ArgsRaw), len(back))
		return c
	}
	c.Args = vals
	return c
}

// ArgText writes decoded arguments as name = value, by the inputs' names.
func ArgText(ins abi.Arguments, vals []any) string {
	var parts []string
	for i, in := range ins {
		if i >= len(vals) {
			break
		}
		parts = append(parts, fmt.Sprintf("%s %s = %s", in.Type.String(), in.Name, valueText(vals[i])))
	}
	return strings.Join(parts, ", ")
}

// valueText writes a decoded ABI value for a reader: addresses checksummed, bytes in hex, tuples and lists in full.
func valueText(v any) string {
	switch x := v.(type) {
	case common.Address:
		return x.Hex()
	case [32]byte:
		return "0x" + hex.EncodeToString(x[:])
	case []byte:
		return "0x" + hex.EncodeToString(x)
	case *big.Int:
		return x.String()
	case string:
		return fmt.Sprintf("%q", x)
	}
	b, err := jsonText(v)
	if err != nil {
		return fmt.Sprint(v)
	}
	return b
}
