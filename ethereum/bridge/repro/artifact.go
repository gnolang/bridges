// Package repro holds the reproducible builds the bridge's tools hold deployed code to (eth/bridge/PLAN.md §5.8,
// §5.9, §6.5, §8): a contract's artifact — its ABI, its creation code, its runtime code and the places its immutables
// sit in the runtime code, by name — read from a forge out directory (built with `forge build --force --ast`, so every
// immutable can be named) or from Hyperlane's release compile (local/hyperlane-artifacts.sh: forge's shape with the
// immutables' names beside their references); constructor and call arguments packed by their names in the artifact's
// own ABI; a deployed code compared with its build, the immutables masked and each read; and a creation transaction's
// input split into the build's creation code and the constructor arguments it carried. `hleth setup` deploys from
// these, `hleth audit` holds the deployment to them, and `hlwatch` holds a kind 8's implementation or a kind 9's
// successor to them before the vote (the plan red team's E2-RT1 and E2-RT3: on Ethereum the immutables and the
// constructor carry every binding, and a runtime code hash says nothing of either).
package repro

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
)

// Ref is one place an immutable's 32 bytes sit in the runtime code.
type Ref struct {
	Start  int `json:"start"`
	Length int `json:"length"`
}

// Immutable is one immutable variable of a contract: its declaration's AST id, its name ("Contract.variable", the
// declaring contract's) and every place it sits in the runtime code.
type Immutable struct {
	ID   string
	Name string
	Refs []Ref
}

// Artifact is one contract as built.
type Artifact struct {
	Name       string
	ABI        abi.ABI
	Creation   []byte
	Runtime    []byte
	Immutables []Immutable // by their first place in the code
}

type artifactJSON struct {
	ABI      json.RawMessage `json:"abi"`
	Bytecode struct {
		Object         string          `json:"object"`
		LinkReferences json.RawMessage `json:"linkReferences"`
	} `json:"bytecode"`
	DeployedBytecode struct {
		Object              string           `json:"object"`
		LinkReferences      json.RawMessage  `json:"linkReferences"`
		ImmutableReferences map[string][]Ref `json:"immutableReferences"`
	} `json:"deployedBytecode"`
	ImmutableNames map[string]string `json:"immutableNames"`
	AST            json.RawMessage   `json:"ast"`
}

// ReadArtifact reads one artifact; names maps the immutables' AST ids to their names when the file carries none
// (ForgeImmutableNames of its out directory). An immutable no name is known for is refused: the tools hold every
// immutable to the value it must have, by name.
func ReadArtifact(path, name string, names map[string]string) (*Artifact, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var j artifactJSON
	if err := json.Unmarshal(b, &j); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	for _, lr := range []json.RawMessage{j.Bytecode.LinkReferences, j.DeployedBytecode.LinkReferences} {
		if s := strings.TrimSpace(string(lr)); s != "" && s != "{}" && s != "null" {
			return nil, fmt.Errorf("%s needs linking (%s): the bridge's contracts link no library", path, s)
		}
	}
	a := &Artifact{Name: name}
	if a.ABI, err = abi.JSON(bytes.NewReader(j.ABI)); err != nil {
		return nil, fmt.Errorf("%s: the ABI: %w", path, err)
	}
	if a.Creation, err = hexutil.Decode(ensure0x(j.Bytecode.Object)); err != nil || len(a.Creation) == 0 {
		return nil, fmt.Errorf("%s: no creation code (%v)", path, err)
	}
	if a.Runtime, err = hexutil.Decode(ensure0x(j.DeployedBytecode.Object)); err != nil || len(a.Runtime) == 0 {
		return nil, fmt.Errorf("%s: no runtime code (%v)", path, err)
	}
	if names == nil {
		names = j.ImmutableNames
	}
	for id, refs := range j.DeployedBytecode.ImmutableReferences {
		n, ok := names[id]
		if !ok {
			return nil, fmt.Errorf("%s: the immutable of AST id %s has no name (build with forge build --force --ast, so every source's AST is in the out directory)", path, id)
		}
		for _, r := range refs {
			if r.Length != 32 || r.Start < 0 || r.Start+r.Length > len(a.Runtime) {
				return nil, fmt.Errorf("%s: %s's reference at %d of %d bytes is not a word inside the code", path, n, r.Start, r.Length)
			}
		}
		sort.Slice(refs, func(i, k int) bool { return refs[i].Start < refs[k].Start })
		a.Immutables = append(a.Immutables, Immutable{ID: id, Name: n, Refs: refs})
	}
	sort.Slice(a.Immutables, func(i, k int) bool { return a.Immutables[i].Refs[0].Start < a.Immutables[k].Refs[0].Start })
	return a, nil
}

// ReadBuildFile reads an artifact a holder names on its own (hlwatch -build): a forge artifact inside a forge out
// directory (<out>/<Source>.sol/<Contract>.json, whose directory's ASTs name the immutables) or a release compile's
// file carrying the names beside the references. The contract's name is the file's.
func ReadBuildFile(path string) (*Artifact, error) {
	name := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var j artifactJSON
	if err := json.Unmarshal(b, &j); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if len(j.DeployedBytecode.ImmutableReferences) == 0 || len(j.ImmutableNames) > 0 {
		return ReadArtifact(path, name, nil)
	}
	out := filepath.Dir(filepath.Dir(path))
	names, err := ForgeImmutableNames(out)
	if err != nil {
		return nil, fmt.Errorf("%s: its immutables' names (a forge out directory built with --ast, %s): %w", path, out, err)
	}
	return ReadArtifact(path, name, names)
}

func ensure0x(s string) string {
	if strings.HasPrefix(s, "0x") {
		return s
	}
	return "0x" + s
}

// ForgeImmutableNames walks every artifact's AST in a forge out directory for the immutable state variables, by AST
// id: an immutable is declared in its own source (MailboxClient's mailbox in MailboxClient.sol), and forge writes a
// source's AST into the artifacts of that source only. The ids are one compile's (forge build --force).
func ForgeImmutableNames(dir string) (map[string]string, error) {
	names := map[string]string{}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("-contracts %s: %w", dir, err)
	}
	seenAST := false
	for _, e := range entries {
		if !e.IsDir() || e.Name() == "build-info" {
			continue
		}
		files, _ := filepath.Glob(filepath.Join(dir, e.Name(), "*.json"))
		for _, f := range files {
			b, err := os.ReadFile(f)
			if err != nil {
				return nil, err
			}
			if bytes.Contains(b, []byte(`"ast":`)) {
				seenAST = true
			}
			if !bytes.Contains(b, []byte(`"mutability":"immutable"`)) {
				continue // no immutable declared in this source: nothing to name (and its AST need not be decoded)
			}
			var j struct {
				AST json.RawMessage `json:"ast"`
			}
			if err := json.Unmarshal(b, &j); err != nil || len(j.AST) == 0 || string(j.AST) == "null" {
				continue
			}
			seenAST = true
			var node any
			if err := json.Unmarshal(j.AST, &node); err != nil {
				return nil, fmt.Errorf("%s: the AST: %w", f, err)
			}
			walkImmutables(node, "", names)
			break // every artifact of one source carries the same AST
		}
	}
	if !seenAST {
		return nil, fmt.Errorf("-contracts %s: no artifact carries an AST (build with forge build --force --ast)", dir)
	}
	return names, nil
}

func walkImmutables(node any, contract string, names map[string]string) {
	switch n := node.(type) {
	case map[string]any:
		if n["nodeType"] == "ContractDefinition" {
			if s, ok := n["name"].(string); ok {
				contract = s
			}
		}
		if n["nodeType"] == "VariableDeclaration" && n["mutability"] == "immutable" {
			if id, ok := n["id"].(float64); ok {
				names[fmt.Sprintf("%d", int64(id))] = contract + "." + fmt.Sprint(n["name"])
			}
		}
		for _, v := range n {
			walkImmutables(v, contract, names)
		}
	case []any:
		for _, v := range n {
			walkImmutables(v, contract, names)
		}
	}
}

// ------------------------------------------------------------------------------------- packing by name --

// The constructors' and initializers' arguments are packed by their names in the artifact's own ABI (normalised:
// the underscores around a name dropped, case ignored), never by position: lane E-M2b's contracts may reorder or
// rename a parameter between builds, and a value given to the wrong position would deploy a contract bound to the
// wrong address. A parameter this tool has no value for is refused by name, before anything is sent.

// Norm is a parameter's name as values are looked up.
func Norm(s string) string { return strings.ToLower(strings.Trim(s, "_")) }

// Values are a call's arguments by normalised name.
type Values map[string]any

// PackArgs builds the arguments of inputs from vals.
func PackArgs(inputs abi.Arguments, vals Values, what string) ([]any, error) {
	args := make([]any, len(inputs))
	for i, in := range inputs {
		v, ok := vals[Norm(in.Name)]
		if !ok {
			return nil, fmt.Errorf("%s: no value for its parameter %q (%s): the contract's ABI changed — this tool must learn it before deploying", what, in.Name, in.Type.String())
		}
		cv, err := ABIValue(in.Type, v)
		if err != nil {
			return nil, fmt.Errorf("%s: parameter %q: %w", what, in.Name, err)
		}
		args[i] = cv
	}
	return args, nil
}

// ConstructorArgs is the ABI encoding of the constructor's arguments, by name.
func (a *Artifact) ConstructorArgs(vals Values) ([]byte, error) {
	args, err := PackArgs(a.ABI.Constructor.Inputs, vals, a.Name+"'s constructor")
	if err != nil {
		return nil, err
	}
	enc, err := a.ABI.Constructor.Inputs.Pack(args...)
	if err != nil {
		return nil, fmt.Errorf("%s's constructor: %w", a.Name, err)
	}
	return enc, nil
}

// CreationCode is the artifact's creation code with its constructor's arguments, by name: what a creation
// transaction's input is, byte for byte.
func (a *Artifact) CreationCode(vals Values) ([]byte, error) {
	enc, err := a.ConstructorArgs(vals)
	if err != nil {
		return nil, err
	}
	return append(append([]byte{}, a.Creation...), enc...), nil
}

// CallData is a method's calldata with its arguments by name.
func (a *Artifact) CallData(method string, vals Values) ([]byte, error) {
	m, ok := a.ABI.Methods[method]
	if !ok {
		return nil, fmt.Errorf("%s has no method %s", a.Name, method)
	}
	args, err := PackArgs(m.Inputs, vals, a.Name+"."+method)
	if err != nil {
		return nil, err
	}
	return a.ABI.Pack(method, args...)
}

var (
	bigType  = reflect.TypeOf((*big.Int)(nil))
	addrType = reflect.TypeOf(common.Address{})
)

// ABIValue converts a value to the Go type the ABI packs for t: a tuple from a Values map (by its components'
// names), an array or slice element by element, an integer of any width to *big.Int where the ABI wants one.
func ABIValue(t abi.Type, v any) (any, error) {
	rt := t.GetType()
	switch t.T {
	case abi.TupleTy:
		m, ok := v.(Values)
		if !ok {
			return nil, fmt.Errorf("a tuple %s takes a Values map, not %T", t.String(), v)
		}
		out := reflect.New(rt).Elem()
		for i, raw := range t.TupleRawNames {
			fv, ok := m[Norm(raw)]
			if !ok {
				return nil, fmt.Errorf("no value for the component %q of %s: the contract's ABI changed — this tool must learn it before deploying", raw, t.String())
			}
			ev, err := ABIValue(*t.TupleElems[i], fv)
			if err != nil {
				return nil, fmt.Errorf("component %q: %w", raw, err)
			}
			out.Field(i).Set(reflect.ValueOf(ev))
		}
		return out.Interface(), nil
	case abi.SliceTy, abi.ArrayTy:
		src := reflect.ValueOf(v)
		if src.Kind() != reflect.Slice && src.Kind() != reflect.Array {
			return nil, fmt.Errorf("%s takes a list, not %T", t.String(), v)
		}
		if t.T == abi.ArrayTy && src.Len() != t.Size {
			return nil, fmt.Errorf("%s takes %d elements, not %d", t.String(), t.Size, src.Len())
		}
		var out reflect.Value
		if t.T == abi.SliceTy {
			out = reflect.MakeSlice(rt, src.Len(), src.Len())
		} else {
			out = reflect.New(rt).Elem()
		}
		for i := 0; i < src.Len(); i++ {
			ev, err := ABIValue(*t.Elem, src.Index(i).Interface())
			if err != nil {
				return nil, fmt.Errorf("element %d: %w", i, err)
			}
			out.Index(i).Set(reflect.ValueOf(ev))
		}
		return out.Interface(), nil
	}
	rv := reflect.ValueOf(v)
	if !rv.IsValid() {
		return nil, errors.New("no value")
	}
	if rv.Type() == rt {
		return v, nil
	}
	if rt == bigType {
		switch x := v.(type) {
		case uint64:
			return new(big.Int).SetUint64(x), nil
		case uint32:
			return new(big.Int).SetUint64(uint64(x)), nil
		case uint8:
			return new(big.Int).SetUint64(uint64(x)), nil
		case int64:
			return big.NewInt(x), nil
		case int:
			return big.NewInt(int64(x)), nil
		}
		return nil, fmt.Errorf("%s takes an integer, not %T", t.String(), v)
	}
	if rt == addrType {
		if a, ok := v.(common.Address); ok {
			return a, nil
		}
		return nil, fmt.Errorf("%s takes an address, not %T", t.String(), v)
	}
	if rv.Type().ConvertibleTo(rt) && rv.Kind() == rt.Kind() {
		return rv.Convert(rt).Interface(), nil
	}
	return nil, fmt.Errorf("%s does not take a %T", t.String(), v)
}
