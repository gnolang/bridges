package ethx

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/common"
)

// viewsReader answers HlAdmin's views by their calldata (selector and arguments): each view in answers packs its
// value, one in reverts reverts as the contract would, one in empties answers no bytes, one in broken is the
// provider's own failure; anything else is no contract's answer.
type viewsReader struct {
	answers map[string][]byte
	reverts map[string]bool
	empties map[string]bool
	broken  map[string]bool
	calls   int
}

func (v *viewsReader) CallAt(_ context.Context, _ common.Address, data []byte, _ At) ([]byte, error) {
	v.calls++
	k := string(data)
	switch {
	case v.reverts[k]:
		return nil, &Revert{Message: "execution reverted"}
	case v.empties[k]:
		return []byte{}, nil
	case v.broken[k]:
		return nil, errors.New("the provider's connection reset")
	}
	if out, ok := v.answers[k]; ok {
		return out, nil
	}
	return nil, errors.New("no such view in the fake")
}

func (v *viewsReader) CallBatch(ctx context.Context, reqs []CallReq, at At) ([][]byte, []error, error) {
	res, errs := make([][]byte, len(reqs)), make([]error, len(reqs))
	for i, q := range reqs {
		res[i], errs[i] = v.CallAt(ctx, q.To, q.Data, at)
	}
	return res, errs, nil
}

// adminViews are an HlAdmin's sixteen binding views as a successor of this build answers them.
func adminViews(t *testing.T) (*viewsReader, map[string]string) {
	t.Helper()
	a := func(n byte) common.Address { return common.BytesToAddress([]byte{0xa0, n}) }
	h := func(n byte) [32]byte { return [32]byte{31: n} }
	r := &viewsReader{answers: map[string][]byte{}, reverts: map[string]bool{}, empties: map[string]bool{}, broken: map[string]bool{}}
	names := map[string]string{}
	set := func(name, method string, args []any, out ...any) {
		data, err := Admin.Pack(method, args...)
		if err != nil {
			t.Fatal(err)
		}
		packed, err := Admin.ABI.Methods[method].Outputs.Pack(out...)
		if err != nil {
			t.Fatal(err)
		}
		r.answers[string(data)] = packed
		names[name] = string(data)
	}
	set("mailbox()", "mailbox", nil, a(1))
	set("client()", "client", nil, a(2))
	set("gate()", "gate", nil, a(3))
	set("token(0)", "token", []any{uint8(0)}, a(4))
	set("token(1)", "token", []any{uint8(1)}, a(5))
	set("proxyAdmin()", "proxyAdmin", nil, a(6))
	set("merkleTreeHook()", "merkleTreeHook", nil, a(7))
	set("protocolFee()", "protocolFee", nil, a(8))
	set("localDomain()", "localDomain", nil, uint32(31337))
	set("origin()", "origin", nil, uint32(1198419796))
	set("hlgov()", "hlgov", nil, h(9))
	set("ed25519()", "ed25519", nil, a(10))
	set("ed25519CodeHash()", "ed25519CodeHash", nil, h(11))
	set("route(0)", "route", []any{uint8(0)}, h(12))
	set("route(1)", "route", []any{uint8(1)}, h(13))
	set("mailboxPkgPathHash()", "mailboxPkgPathHash", nil, h(14))
	return r, names
}

// E3-CF10's Go half: ReadBindings reads the eleven views kind 9 compares and the five it never reads apart — a successor
// whose optional views revert (a later build that dropped them) still reads, its eleven whole and the five named; a
// required view that reverts, or a provider's failure on any view, is still the answer.
func TestReadBindingsRequiredAndOptional(t *testing.T) {
	ctx := context.Background()
	at := Latest
	succ := common.HexToAddress("0x00000000000000000000000000000000000005cc")
	if len(RequiredBindings) != 11 || len(OptionalBindings) != 5 {
		t.Fatalf("%d required and %d optional bindings", len(RequiredBindings), len(OptionalBindings))
	}

	// every view answers: all sixteen read, nothing unread
	r, names := adminViews(t)
	b, err := ReadBindings(ctx, r, succ, at)
	if err != nil || len(b.Unread) != 0 || b.Mailbox != common.BytesToAddress([]byte{0xa0, 1}) || b.MailboxPkgPathHash != [32]byte{31: 14} || b.Routes[1] != [32]byte{31: 13} {
		t.Fatalf("a full successor: %+v (%v)", b, err)
	}

	// the five optional views revert (two) or answer nothing (three): the eleven read, the five named and zero
	r, names = adminViews(t)
	for _, n := range []string{"ed25519()", "route(0)"} {
		r.reverts[names[n]] = true
	}
	for _, n := range []string{"ed25519CodeHash()", "route(1)", "mailboxPkgPathHash()"} {
		r.empties[names[n]] = true
	}
	b, err = ReadBindings(ctx, r, succ, at)
	if err != nil {
		t.Fatalf("a successor whose five optional views do not answer: %v (the eleven kind 9 compares all answer)", err)
	}
	if b.Mailbox != common.BytesToAddress([]byte{0xa0, 1}) || b.Hlgov != [32]byte{31: 9} || b.Origin != 1198419796 || b.Tokens[1] != common.BytesToAddress([]byte{0xa0, 5}) {
		t.Fatalf("the eleven: %+v", b)
	}
	for _, n := range OptionalBindings {
		if b.Answered(n) {
			t.Fatalf("%s is not named unread: %v", n, b.Unread)
		}
	}
	if b.Ed25519 != (common.Address{}) || b.Routes[0] != ([32]byte{}) || b.MailboxPkgPathHash != ([32]byte{}) {
		t.Fatalf("an unread view's field is not zero: %+v", b)
	}
	if !strings.Contains(b.Unread["ed25519()"], "reverts") || !strings.Contains(b.Unread["route(1)"], "an empty answer") {
		t.Fatalf("why: %v", b.Unread)
	}

	// one optional view does not answer: the other four read
	r, names = adminViews(t)
	r.reverts[names["route(1)"]] = true
	if b, err = ReadBindings(ctx, r, succ, at); err != nil || len(b.Unread) != 1 || b.Answered("route(1)") || b.Routes[0] != [32]byte{31: 12} || b.Ed25519CodeHash != [32]byte{31: 11} {
		t.Fatalf("one optional view reverts: %+v (%v)", b, err)
	}

	// a required view that reverts is the answer (HlAdmin's execute reverts on it: the decision pends)
	r, names = adminViews(t)
	r.reverts[names["hlgov()"]] = true
	if _, err = ReadBindings(ctx, r, succ, at); err == nil {
		t.Fatal("a successor whose hlgov() reverts read")
	}

	// a provider's failure on an optional view is no answer of the contract's: the read fails
	r, names = adminViews(t)
	r.broken[names["mailboxPkgPathHash()"]] = true
	if _, err = ReadBindings(ctx, r, succ, at); err == nil || !strings.Contains(err.Error(), "connection reset") {
		t.Fatalf("a provider's failure on an optional view: %v", err)
	}
}
