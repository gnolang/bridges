// Command resultsgen writes gnolight's differential fixture (gnolight/testdata/results_differential.json): tm2's own
// amino, at the gno version gnoland-1 runs, encodes a block's DeliverTx results both ways — the amino JSON
// block_results serves, and the results leaves LastResultsHash commits to (types.ABCIResult.Bytes, the chain's own
// function) with their merkle root — so gnolight's re-encoding of the JSON is checked byte for byte without the
// bridge module importing gno. The closed set of error types is found, not listed: every type amino's global codec
// registered in a program linking gnoland's app and node that implements abci.Error. Each is encoded bare (a failed
// transaction's result) and with data and one event of each kind gnolight knows; abci.StringError, the one with a
// value, with a range of strings around the varint and JSON-escaping boundaries. A second block holds strings that
// are not UTF-8, which the JSON cannot carry: the fixture records how they come out, and gnolight's test checks they
// disagree only there.
//
//	cd bridge/tools/resultsgen
//	go run . ../../gnolight/testdata/results_differential.json          # write
//	go run . -check ../../gnolight/testdata/results_differential.json   # regenerate and compare
//
// The gno version is go.mod's (v1.5.1, what gnoland-1 runs since 2026-10-13; v1.5.0 before it): `go get
// github.com/gnolang/gno@v1.2.0` then -check shows an earlier version encodes the same (the fixture's "gno" line
// aside). Build without cgo (CGO_ENABLED=0): gnoland's cgo databases register nothing amino needs here.
package main

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"reflect"
	"runtime/debug"
	"sort"
	"strings"
	_ "unsafe" // go:linkname to amino's global codec

	_ "github.com/gnolang/gno/gno.land/pkg/gnoland" // the app: every amino package it registers
	"github.com/gnolang/gno/gnovm/stdlibs/chain"
	"github.com/gnolang/gno/tm2/pkg/amino"
	abci "github.com/gnolang/gno/tm2/pkg/bft/abci/types"
	_ "github.com/gnolang/gno/tm2/pkg/bft/node" // the node: its amino packages too
	ctypes "github.com/gnolang/gno/tm2/pkg/bft/rpc/core/types"
	sm "github.com/gnolang/gno/tm2/pkg/bft/state"
	"github.com/gnolang/gno/tm2/pkg/bft/types"
	"github.com/gnolang/gno/tm2/pkg/sdk/bank"
	"github.com/gnolang/gno/tm2/pkg/std"
)

// gcdc is amino's global codec, which every package's `var Package = amino.RegisterPackage(…)` registers into.
//
//go:linkname gcdc github.com/gnolang/gno/tm2/pkg/amino.gcdc
var gcdc *amino.Codec

type errorType struct {
	URL    string `json:"url"`
	Go     string `json:"go"`
	Kind   string `json:"kind"`   // the Go kind: "struct", or "string" for abci.StringError
	Fields int    `json:"fields"` // the exported fields amino would encode
}

type block struct {
	Names        []string        `json:"names"`         // what each transaction's result holds, in order
	Leaves       []string        `json:"leaves"`        // hex of types.ABCIResult.Bytes() for each
	ResultsHash  string          `json:"results_hash"`  // hex of sm.ABCIResponses.ResultsHash(): LastResultsHash
	BlockResults json.RawMessage `json:"block_results"` // amino JSON of ctypes.ResultBlockResults, as the RPC serves it
}

type fixture struct {
	Generator  string      `json:"generator"`
	Gno        string      `json:"gno"`
	ErrorTypes []errorType `json:"error_types"`
	Block      block       `json:"block"`
	Lossy      block       `json:"lossy"`
}

func main() {
	check := flag.Bool("check", false, "regenerate and compare with the file instead of writing it")
	flag.Parse()
	if flag.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: resultsgen [-check] OUT.json")
		os.Exit(2)
	}
	out, summary := generate()
	path := flag.Arg(0)
	if *check {
		old, err := os.ReadFile(path)
		if err != nil {
			fail(err)
		}
		if !bytes.Equal(withoutGnoLine(old), withoutGnoLine(out)) {
			fail(fmt.Errorf("%s differs from what this gno (%s) encodes", path, gnoVersion()))
		}
		fmt.Printf("%s: the same bytes (%s)\n", path, summary)
		return
	}
	if err := os.WriteFile(path, out, 0o644); err != nil {
		fail(err)
	}
	fmt.Printf("%s: %s\n", path, summary)
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "resultsgen:", err)
	os.Exit(1)
}

// withoutGnoLine drops the "gno" line, the one that names the version, for -check across versions.
func withoutGnoLine(b []byte) []byte {
	var out []string
	for _, l := range strings.Split(string(b), "\n") {
		if !strings.HasPrefix(strings.TrimSpace(l), `"gno":`) {
			out = append(out, l)
		}
	}
	return []byte(strings.Join(out, "\n"))
}

func gnoVersion() string {
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return "unknown"
	}
	for _, d := range bi.Deps {
		if d.Path == "github.com/gnolang/gno" {
			if d.Replace != nil {
				return d.Version + " => " + d.Replace.Path + " " + d.Replace.Version
			}
			return d.Version
		}
	}
	return "unknown"
}

// closedSet is every registered concrete type implementing abci.Error, by type URL.
func closedSet() ([]errorType, map[string]reflect.Type) {
	iface := reflect.TypeOf((*abci.Error)(nil)).Elem()
	var out []errorType
	byURL := map[string]reflect.Type{}
	for _, p := range gcdc.GetPackages() {
		for _, t := range p.Types {
			rt := t.Type
			if !rt.Implements(iface) && !reflect.PointerTo(rt).Implements(iface) {
				continue
			}
			url := p.TypeURLForType(rt)
			fields := 0
			if rt.Kind() == reflect.Struct {
				for i := 0; i < rt.NumField(); i++ {
					if rt.Field(i).IsExported() {
						fields++
					}
				}
			}
			if _, dup := byURL[url]; dup {
				fail(fmt.Errorf("type URL %s registered twice", url))
			}
			byURL[url] = rt
			out = append(out, errorType{URL: url, Go: rt.PkgPath() + "." + rt.Name(), Kind: rt.Kind().String(), Fields: fields})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].URL < out[j].URL })
	return out, byURL
}

// errorValue is a value of the type: a struct with its exported fields (none today) filled, or the string.
func errorValue(rt reflect.Type, s string) abci.Error {
	v := reflect.New(rt).Elem()
	switch rt.Kind() {
	case reflect.String:
		v.SetString(s)
	case reflect.Struct:
		for i := 0; i < rt.NumField(); i++ {
			f := v.Field(i)
			if !rt.Field(i).IsExported() {
				continue
			}
			switch f.Kind() {
			case reflect.String:
				f.SetString("field " + rt.Field(i).Name)
			case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
				f.SetInt(-7)
			case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
				f.SetUint(7)
			case reflect.Bool:
				f.SetBool(true)
			default:
				fail(fmt.Errorf("%v has a field of kind %v the generator does not fill", rt, f.Kind()))
			}
		}
	default:
		fail(fmt.Errorf("%v is a %v, neither a struct nor a string", rt, rt.Kind()))
	}
	if e, ok := v.Interface().(abci.Error); ok {
		return e
	}
	p := reflect.New(rt)
	p.Elem().Set(v)
	return p.Interface().(abci.Error)
}

// events is one event of each kind gnolight re-encodes.
func events(attr string) []abci.Event {
	return []abci.Event{
		chain.Event{Type: "Transfer", Attributes: []chain.EventAttribute{{Key: "from", Value: "g1juss2tuc97sr77rn6vprhh9lr7d9e07uu68x59"}, {Key: "amount", Value: attr}}, PkgPath: "gno.land/r/demo/grc20"},
		bank.TransferEvent{From: "g1juss2tuc97sr77rn6vprhh9lr7d9e07uu68x59", To: "g19ulvh44r54nc44pekne4cy6e7lptheq7a0wavt", Coins: std.NewCoins(std.NewCoin("ugnot", 30093))},
		chain.StorageDepositEvent{BytesDelta: 7077, FeeDelta: std.NewCoin("ugnot", 707700), PkgPath: "gno.land/r/demo/grc20"},
		chain.StorageUnlockEvent{BytesDelta: -77, FeeRefund: std.NewCoin("ugnot", 7700), PkgPath: "gno.land/r/demo/grc20", RefundWithheld: true},
	}
}

func response(e abci.Error, data []byte, evs []abci.Event) abci.ResponseDeliverTx {
	return abci.ResponseDeliverTx{
		ResponseBase: abci.ResponseBase{Error: e, Data: data, Events: evs, Log: "the log is not hashed", Info: "nor is the info"},
		GasWanted:    1_000_000,
		GasUsed:      624_889,
	}
}

func makeBlock(names []string, resps []abci.ResponseDeliverTx) block {
	b := block{Names: names}
	for _, r := range resps {
		b.Leaves = append(b.Leaves, hex.EncodeToString(types.NewResultFromResponse(r).Bytes()))
	}
	ar := &sm.ABCIResponses{DeliverTxs: resps}
	b.ResultsHash = hex.EncodeToString(ar.ResultsHash())
	js, err := amino.MarshalJSON(&ctypes.ResultBlockResults{Height: 7, Results: ar})
	if err != nil {
		fail(err)
	}
	b.BlockResults = js
	return b
}

func generate() ([]byte, string) {
	set, byURL := closedSet()
	var names []string
	var resps []abci.ResponseDeliverTx
	add := func(name string, r abci.ResponseDeliverTx) {
		names = append(names, name)
		resps = append(resps, r)
	}
	add("ok, data and events", response(nil, []byte("(\"30093\" string)\n"), events("5")))
	add("ok, nothing", response(nil, nil, nil))
	for _, et := range set {
		rt := byURL[et.URL]
		add(et.URL, response(errorValue(rt, "access restricted: not moderator"), nil, nil))
		add(et.URL+" with data and events", response(errorValue(rt, "x"), []byte("\n\n\n\n"), events("5")))
	}
	// the string error around the boundaries: empty; the Any's, its value's and the string's own lengths crossing
	// 127 (url field 19 bytes, so the Any is 23 + n for a string of n < 126 bytes); the 1024
	// bytes and "..." of a truncated panic; what json.Marshal escapes (HTML, controls, U+2028/9); other planes; a
	// U+FFFD that was one in the chain's bytes too
	str := byURL["/abci.StringError"]
	if str == nil {
		fail(fmt.Errorf("no /abci.StringError in the registry"))
	}
	for _, s := range []struct{ name, s string }{
		{"empty", ""},
		{"one byte", "x"},
		{"104 bytes (the Any 127)", strings.Repeat("a", 104)},
		{"105 bytes (the Any 128)", strings.Repeat("a", 105)},
		{"125 bytes (the value 127)", strings.Repeat("b", 125)},
		{"126 bytes (the value 128)", strings.Repeat("b", 126)},
		{"127 bytes", strings.Repeat("c", 127)},
		{"128 bytes (the string's own length 128)", strings.Repeat("c", 128)},
		{"a truncated panic, 1027 bytes", strings.Repeat("p", 1024) + "..."},
		{"what json escapes", "<a href=\"x\">&amp;</a>\x00\x01\t\n\r\x1f\\/  \x7f"},
		{"other planes", "héllo — 世界 🎉 \U0010FFFF"},
		{"a real U+FFFD", "before � after"},
		{"the storage deposit's", "storage deposit processing encountered one or more errors: lockStorageDeposit failed for realm gno.land/r/gnoswap/gns: unable to transfer deposit gno.land/r/gnoswap/gns, insufficient coins error"},
	} {
		add("/abci.StringError: "+s.name, response(errorValue(str, s.s), nil, nil))
	}
	blk := makeBlock(names, resps)

	// what the JSON cannot carry: bytes that are not UTF-8, in a panic's message (a truncation inside a rune, a
	// realm's own bytes) and in an event's attribute; each reads back as U+FFFD
	var lnames []string
	var lresps []abci.ResponseDeliverTx
	ladd := func(name string, r abci.ResponseDeliverTx) {
		lnames = append(lnames, name)
		lresps = append(lresps, r)
	}
	ladd("/abci.StringError: a byte that is not UTF-8", response(errorValue(str, "panic: \xff"), nil, nil))
	ladd("/abci.StringError: 1024 bytes cut inside a rune", response(errorValue(str, strings.Repeat("p", 1023)+"\xc3"+"..."), nil, nil))
	ladd("ok, an event attribute that is not UTF-8", response(nil, nil, events("\xff\xfe")))
	lossy := makeBlock(lnames, lresps)

	fx := fixture{
		Generator:  "bridge/tools/resultsgen",
		Gno:        "github.com/gnolang/gno " + gnoVersion(),
		ErrorTypes: set,
		Block:      blk,
		Lossy:      lossy,
	}
	js, err := json.MarshalIndent(fx, "", " ")
	if err != nil {
		fail(err)
	}
	return append(js, '\n'), fmt.Sprintf("%d error types, %d results in the block, %d in the lossy block, gno %s", len(set), len(names), len(lnames), gnoVersion())
}
