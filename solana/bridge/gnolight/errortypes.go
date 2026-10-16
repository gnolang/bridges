package gnolight

import (
	"encoding/json"
	"fmt"
	"sort"
)

// A failed transaction's results leaf carries its error as field 1 (tm2 ABCIResult.Error, bft/types/results.go):
// an amino Any of the error's concrete type, which tm2's ABCIErrorOrStringError (bft/abci/types/util.go) takes
// from errors.Cause of the handler's error — the abci.Error itself, or StringError(err.Error()) for any other
// error (a realm's panic, a storage deposit's failure). Amino writes the Any as {1: type URL, 2: value}, the value
// omitted when it encodes empty (amino binary_encode.go, encodeReflectBinaryInterface): a field-less struct is the
// type URL alone; a non-struct (StringError, a Go string) is wrapped in an implicit struct {1: the string}, omitted
// when the string is empty. block_results serves the error as amino JSON (json_encode.go,
// encodeReflectJSONInterface): {"@type": URL} for a struct with no fields, {"@type": URL, "value": "…"} for the
// string.
//
// The types are a closed set: every amino-registered concrete type implementing abci.Error that gnoland links, 43
// of them. Enumerated from gno's source at every version gnoland-1 has run — v1.2.0 (9c8eb132e, genesis to
// 36,300), v1.3.0 (31b6650a1, to 113,000), v1.4.0 (00417a1be, to 162,200), v1.5.0 (e75fef82c) and v1.5.1
// (07e44776c, a rolling patch since 2026-10-13), per gnolang's misc/deployments/mainnet.gno.land/upgrades.json —
// the defining files and amino itself byte-identical in all five; cross-checked by bridge/tools/resultsgen, which
// walks the amino registry of a program linking gno.land/pkg/gnoland and encodes each type with tm2's own codec
// (testdata/results_differential.json). Every one is a struct without exported fields but StringError: no type
// carries a field block_results' JSON would have to give back (vm's TypeCheckError keeps its messages out of the
// hashed result on purpose, vm/errors.go). The four errors of tm2/pkg/bft/abci/example/errors implement abci.Error
// too, but nothing registers them and only the example apps import them.

type errorShape int

const (
	errorNoFields errorShape = iota // a struct with no exported field: the Any is the type URL alone
	errorString                     // abci.StringError: the Any's value is {1: the string}
)

var errorTypes = map[string]errorShape{
	// tm2/pkg/std/errors.go, registered by tm2/pkg/std/package.go (amino package "std"): 25, each struct{ abciError }
	"/std.InternalError":               errorNoFields,
	"/std.TxDecodeError":               errorNoFields,
	"/std.InvalidSequenceError":        errorNoFields,
	"/std.UnauthorizedError":           errorNoFields,
	"/std.InsufficientFundsError":      errorNoFields,
	"/std.UnknownRequestError":         errorNoFields,
	"/std.InvalidAddressError":         errorNoFields,
	"/std.UnknownAddressError":         errorNoFields,
	"/std.InvalidPubKeyError":          errorNoFields,
	"/std.InsufficientCoinsError":      errorNoFields,
	"/std.InvalidCoinsError":           errorNoFields,
	"/std.InvalidGasWantedError":       errorNoFields,
	"/std.OutOfGasError":               errorNoFields,
	"/std.MemoTooLargeError":           errorNoFields,
	"/std.InsufficientFeeError":        errorNoFields,
	"/std.TooManySignaturesError":      errorNoFields,
	"/std.NoSignaturesError":           errorNoFields,
	"/std.GasOverflowError":            errorNoFields,
	"/std.RestrictedTransferError":     errorNoFields,
	"/std.SessionExpiredError":         errorNoFields,
	"/std.SessionNotFoundError":        errorNoFields,
	"/std.SessionLimitError":           errorNoFields,
	"/std.SessionNotAllowedError":      errorNoFields,
	"/std.InvalidVestingScheduleError": errorNoFields,
	"/std.VestingLockedCoinsError":     errorNoFields,

	// tm2/pkg/sdk/bank/errors.go, registered by tm2/pkg/sdk/bank/package.go ("bank"): 3
	"/bank.NoInputsError":            errorNoFields,
	"/bank.NoOutputsError":           errorNoFields,
	"/bank.InputOutputMismatchError": errorNoFields,

	// gno.land/pkg/sdk/vm/errors.go, registered by gno.land/pkg/sdk/vm/package.go ("vm"): 14
	"/vm.InvalidPkgPathError":      errorNoFields,
	"/vm.NoRenderDeclError":        errorNoFields,
	"/vm.PkgExistError":            errorNoFields,
	"/vm.InvalidStmtError":         errorNoFields,
	"/vm.InvalidExprError":         errorNoFields,
	"/vm.TypeCheckError":           errorNoFields,
	"/vm.UnauthorizedUserError":    errorNoFields,
	"/vm.InvalidPackageError":      errorNoFields,
	"/vm.InvalidFileError":         errorNoFields,
	"/vm.ObjectNotFoundError":      errorNoFields,
	"/vm.ExportSizeExceededError":  errorNoFields,
	"/vm.ExportDepthExceededError": errorNoFields,
	"/vm.UnobservedSendError":      errorNoFields,
	"/vm.UnspendableSendError":     errorNoFields,

	// tm2/pkg/bft/abci/types/types.go, registered by tm2/pkg/bft/abci/types/package.go ("abci"): 1, a Go string
	"/abci.StringError": errorString,
}

// errorTypeURLs is the closed set, sorted.
func errorTypeURLs() []string {
	out := make([]string, 0, len(errorTypes))
	for u := range errorTypes {
		out = append(out, u)
	}
	sort.Strings(out)
	return out
}

// SchemaError is a result this client cannot re-encode: an error or event type outside the set it knows (a chain
// upgrade may add one), or a known type with members it has no schema for. It names the type, so whoever cannot
// prove a block because of it can say which one to add.
type SchemaError struct {
	Kind    string // "error" or "event"
	TypeURL string
	Detail  string // what about a known type does not fit, "" for an unknown type
}

func (e *SchemaError) Error() string {
	if e.Detail != "" {
		return fmt.Sprintf("the %s type %s %s", e.Kind, e.TypeURL, e.Detail)
	}
	return fmt.Sprintf("the %s type %s has no schema here", e.Kind, e.TypeURL)
}

// errorAnyFromJSON is a result's error as the leaf carries it (the Any's bytes), from block_results' JSON of it.
func errorAnyFromJSON(raw json.RawMessage) ([]byte, error) {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil, fmt.Errorf("a failed transaction's error: %w", err)
	}
	var url string
	if err := json.Unmarshal(obj["@type"], &url); err != nil || url == "" {
		return nil, fmt.Errorf("a failed transaction's error carries no @type")
	}
	shape, ok := errorTypes[url]
	if !ok {
		return nil, &SchemaError{Kind: "error", TypeURL: url}
	}
	switch shape {
	case errorString:
		var s string
		v, has := obj["value"]
		if has {
			if err := json.Unmarshal(v, &s); err != nil {
				return nil, &SchemaError{Kind: "error", TypeURL: url, Detail: "carries a value that is not a string"}
			}
		}
		if len(obj) > 2 || len(obj) == 2 && !has {
			return nil, &SchemaError{Kind: "error", TypeURL: url, Detail: "carries members beside its value"}
		}
		return Any(url, putBytesField(nil, 1, []byte(s))), nil
	default:
		if len(obj) != 1 {
			return nil, &SchemaError{Kind: "error", TypeURL: url, Detail: "carries fields"}
		}
		return Any(url, nil), nil
	}
}
