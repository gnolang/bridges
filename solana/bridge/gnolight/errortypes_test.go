package gnolight

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

// The closed set errortypes.go lists is the one resultsgen found by walking amino's registry: the same 43 URLs,
// every one a struct without exported fields but StringError, a string.
func TestTheClosedSetIsTheRegistrys(t *testing.T) {
	d := loadDifferential(t)
	var gen []string
	for _, et := range d.ErrorTypes {
		gen = append(gen, et.URL)
		want := errorNoFields
		if et.Kind == "string" {
			want = errorString
		} else if et.Kind != "struct" || et.Fields != 0 {
			t.Fatalf("%s: a %s with %d fields, which errortypes.go has no shape for", et.URL, et.Kind, et.Fields)
		}
		if shape, ok := errorTypes[et.URL]; !ok || shape != want {
			t.Fatalf("%s: shape %v (listed %v), the registry's %v", et.URL, shape, ok, want)
		}
	}
	if !reflect.DeepEqual(gen, errorTypeURLs()) || len(gen) != 43 {
		t.Fatalf("the registry's %d types are not the %d listed:\n%v\n%v", len(gen), len(errorTypes), gen, errorTypeURLs())
	}
}

// A type outside the set, or a known one in a shape it never has, is a SchemaError naming the type — for the
// error and for an event — and ResultLeaves' error carries it, so the relayer can name it.
func TestSchemaErrorsNameTheType(t *testing.T) {
	tx := func(errJSON, events string) string {
		return `{"ResponseBase":{"Error":` + errJSON + `,"Data":null,"Events":` + events + `,"Log":"","Info":""},"GasWanted":"1","GasUsed":"1"}`
	}
	block := func(txs ...string) []byte {
		return []byte(`{"height":"7","results":{"deliver_tx":[` + strings.Join(txs, ",") + `]}}`)
	}
	cases := []struct {
		name, tx, url, detail string
	}{
		{"an unknown error type", tx(`{"@type":"/vm.SomeNewError"}`, "null"), "/vm.SomeNewError", ""},
		{"a known type with a field", tx(`{"@type":"/vm.TypeCheckError","errors":["x"]}`, "null"), "/vm.TypeCheckError", "carries fields"},
		{"a string error with another member", tx(`{"@type":"/abci.StringError","value":"x","more":1}`, "null"), "/abci.StringError", "carries members beside its value"},
		{"a string error whose value is no string", tx(`{"@type":"/abci.StringError","value":7}`, "null"), "/abci.StringError", "carries a value that is not a string"},
		{"an unknown event type", tx("null", `[{"@type":"/vm.NewEvent","x":"y"}]`), "/vm.NewEvent", ""},
	}
	for _, c := range cases {
		rs, err := BlockResultsFromJSON(block(tx("null", "null"), c.tx))
		if err != nil {
			t.Fatal(err)
		}
		if rs[0].Err != nil || rs[1].Err == nil {
			t.Fatalf("%s: %v %v", c.name, rs[0].Err, rs[1].Err)
		}
		_, err = ResultLeaves(rs)
		var se *SchemaError
		if !errors.As(err, &se) || se.TypeURL != c.url || se.Detail != c.detail || !strings.Contains(err.Error(), c.url) {
			t.Fatalf("%s: %v (%+v)", c.name, err, se)
		}
	}
	// the shapes the chain serves: a field-less struct, a string (an empty one too, and one with no value member)
	for _, c := range []struct{ json, url, value string }{
		{`{"@type":"/std.OutOfGasError"}`, "/std.OutOfGasError", ""},
		{`{"@type":"/abci.StringError","value":"x"}`, "/abci.StringError", "\x0a\x01x"},
		{`{"@type":"/abci.StringError","value":""}`, "/abci.StringError", ""},
		{`{"@type":"/abci.StringError"}`, "/abci.StringError", ""},
	} {
		a, err := errorAnyFromJSON([]byte(c.json))
		if err != nil || string(a) != string(Any(c.url, []byte(c.value))) {
			t.Fatalf("%s: %x %v", c.json, a, err)
		}
	}
	for _, bad := range []string{`{}`, `{"@type":""}`, `[]`, `{"@type":7}`} {
		if _, err := errorAnyFromJSON([]byte(bad)); err == nil {
			t.Fatalf("%s: read", bad)
		}
	}
}

// A failed transaction's leaf is rebuilt and parses with its error's type, and DispatchEvents still refuses it.
func TestAFailedLeafParsesWithItsErrorType(t *testing.T) {
	d := Dispatch{Nonce: 3}
	d.ID[0] = 1
	path := "gno.land/r/test/hlmailbox"
	leaf := EncodeResult(Any("/std.OutOfGasError", nil), []byte("\n\n"), [][]byte{Any(EventTypeURL, EncodeEvent(d.ToEvent(path)))})
	r, err := ParseResult(leaf)
	if err != nil || !r.Error || r.ErrorType != "/std.OutOfGasError" || string(r.Data) != "\n\n" || len(r.Events) != 1 {
		t.Fatalf("%+v %v", r, err)
	}
	if _, err := DispatchEvents(leaf, path); err != ErrNoDispatch {
		t.Fatal(err)
	}
	if DispatchLeaf([][]byte{leaf}, d.ID, path) != -1 {
		t.Fatal("a failed transaction's Dispatch chosen")
	}
}

// The leaf a proof stages is the smallest carrying the id's Dispatch, the lowest index among equals, whatever
// comes first in the block: a stranger's Reemit of the message beside junk events, a failed transaction's.
func TestDispatchLeafIsTheSmallest(t *testing.T) {
	path := "gno.land/r/test/hlmailbox"
	d := Dispatch{Nonce: 9, Origin: 1, Destination: 2}
	d.ID[0], d.Hash[0] = 7, 8
	dispatch := Any(EventTypeURL, EncodeEvent(d.ToEvent(path)))
	junk := Any(EventTypeURL, EncodeEvent(&Event{Type: "Junk", Attributes: []Attribute{{"k", strings.Repeat("j", 600)}}, PkgPath: "gno.land/r/stranger"}))
	big := EncodeResult(nil, nil, [][]byte{junk, dispatch, junk})
	small := EncodeResult(nil, nil, [][]byte{dispatch})
	other := Dispatch{Nonce: 10}
	other.ID[0] = 9
	unrelated := EncodeResult(nil, nil, [][]byte{Any(EventTypeURL, EncodeEvent(other.ToEvent(path)))})
	failed := EncodeResult(Any("/abci.StringError", []byte("\x0a\x01x")), nil, [][]byte{dispatch})
	if len(big) <= len(small) {
		t.Fatal("the fixture's leaves")
	}
	for _, c := range []struct {
		leaves [][]byte
		want   int
	}{
		{[][]byte{big, small}, 1},
		{[][]byte{small, big}, 0},
		{[][]byte{unrelated, big, failed, small, small}, 3},
		{[][]byte{big}, 0},
		{[][]byte{failed, unrelated}, -1},
	} {
		if got := DispatchLeaf(c.leaves, d.ID, path); got != c.want {
			t.Fatalf("leaf %d, want %d", got, c.want)
		}
	}
	if DispatchLeaf([][]byte{small}, d.ID, "gno.land/r/other") != -1 {
		t.Fatal("another realm's Dispatch")
	}
}
