package gnolight

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"
)

// The results leaf: block H's per-transaction ABCIResult{Error, Data, Events} (tm2 results.go), amino-encoded
// bare — [0A <Any error>] [12 <data>] (1A <Any event>)* — and hashed as a leaf of LastResultsHash in header H+1.
// An event is an Any; gno's chain.Event (/tm.Event) is {1: type, 2: attrs {1: key, 2: value}…, 3: pkg_path}.
//
// The Dispatch event (§13): type Dispatch, pkg_path the mailbox realm's, attributes in this order: id (32 bytes,
// lowercase hex), nonce (decimal), origin, destination (decimal), sender, recipient (32 bytes hex), hash (sha256
// of the message bytes, hex). Reemit emits the same.

const (
	EventTypeURL = "/tm.Event"
	DispatchType = "Dispatch"
)

var (
	ErrBadLeaf     = errors.New("gnolight: the results leaf does not parse")
	ErrNoDispatch  = errors.New("gnolight: no Dispatch event of the mailbox realm, or a failed transaction's leaf")
	ErrBadDispatch = errors.New("gnolight: a Dispatch event's attributes are not the seven of §13")
)

type Attribute struct {
	Key, Value string
}

type Event struct {
	Type       string
	Attributes []Attribute
	PkgPath    string
}

// ParsedResult is a parsed ABCIResult: whether an error is present (and its type URL, when its Any reads), the
// data, and every event as (type URL, value bytes).
type ParsedResult struct {
	Error     bool
	ErrorType string
	Data      []byte
	Events    []AnyValue
}

type AnyValue struct {
	TypeURL string
	Value   []byte
}

// Dispatch is the seven attributes of a Dispatch event, decoded.
type Dispatch struct {
	ID          [32]byte
	Nonce       uint32
	Origin      uint32
	Destination uint32
	Sender      [32]byte
	Recipient   [32]byte
	Hash        [32]byte
}

// ParseAny is an Any's type URL and value.
func ParseAny(buf []byte) (AnyValue, error) {
	fs, err := fields(buf)
	if err != nil {
		return AnyValue{}, err
	}
	var out AnyValue
	seenURL := false
	for _, f := range fs {
		switch {
		case f.num == 1 && f.typ3 == typ3ByteLen:
			out.TypeURL = string(f.value)
			seenURL = true
		case f.num == 2 && f.typ3 == typ3ByteLen:
			out.Value = f.value
		default:
			return AnyValue{}, ErrEncoding
		}
	}
	if !seenURL {
		return AnyValue{}, ErrEncoding
	}
	return out, nil
}

func ParseResult(leaf []byte) (*ParsedResult, error) {
	fs, err := fields(leaf)
	if err != nil {
		return nil, err
	}
	r := &ParsedResult{}
	for _, f := range fs {
		switch {
		case f.num == 1 && f.typ3 == typ3ByteLen:
			r.Error = true
			if a, err := ParseAny(f.value); err == nil {
				r.ErrorType = a.TypeURL
			}
		case f.num == 2 && f.typ3 == typ3ByteLen:
			r.Data = f.value
		case f.num == 3 && f.typ3 == typ3ByteLen:
			a, err := ParseAny(f.value)
			if err != nil {
				return nil, err
			}
			r.Events = append(r.Events, a)
		default:
			return nil, ErrBadLeaf
		}
	}
	return r, nil
}

// ParseEvent reads a chain.Event's value bytes.
func ParseEvent(value []byte) (*Event, error) {
	fs, err := fields(value)
	if err != nil {
		return nil, err
	}
	e := &Event{}
	for _, f := range fs {
		switch {
		case f.num == 1 && f.typ3 == typ3ByteLen:
			e.Type = string(f.value)
		case f.num == 2 && f.typ3 == typ3ByteLen:
			afs, err := fields(f.value)
			if err != nil {
				return nil, err
			}
			var a Attribute
			for _, af := range afs {
				switch {
				case af.num == 1 && af.typ3 == typ3ByteLen:
					a.Key = string(af.value)
				case af.num == 2 && af.typ3 == typ3ByteLen:
					a.Value = string(af.value)
				default:
					return nil, ErrEncoding
				}
			}
			e.Attributes = append(e.Attributes, a)
		case f.num == 3 && f.typ3 == typ3ByteLen:
			e.PkgPath = string(f.value)
		default:
			return nil, ErrEncoding
		}
	}
	return e, nil
}

func hex32(s string) ([32]byte, error) {
	var out [32]byte
	if len(s) != 64 {
		return out, ErrBadDispatch
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return out, ErrBadDispatch
		}
	}
	b, err := hex.DecodeString(s)
	if err != nil {
		return out, ErrBadDispatch
	}
	copy(out[:], b)
	return out, nil
}

func decimalU32(s string) (uint32, error) {
	if s == "" || len(s) > 10 || (len(s) > 1 && s[0] == '0') {
		return 0, ErrBadDispatch
	}
	v, err := strconv.ParseUint(s, 10, 32)
	if err != nil {
		return 0, ErrBadDispatch
	}
	return uint32(v), nil
}

// DispatchFromEvent reads a Dispatch event's attributes, which must be exactly the seven of §13 in order.
func DispatchFromEvent(e *Event) (Dispatch, error) {
	var d Dispatch
	keys := []string{"id", "nonce", "origin", "destination", "sender", "recipient", "hash"}
	if e.Type != DispatchType || len(e.Attributes) != 7 {
		return d, ErrBadDispatch
	}
	for i, k := range keys {
		if e.Attributes[i].Key != k {
			return d, ErrBadDispatch
		}
	}
	a := e.Attributes
	var err error
	if d.ID, err = hex32(a[0].Value); err != nil {
		return d, err
	}
	if d.Nonce, err = decimalU32(a[1].Value); err != nil {
		return d, err
	}
	if d.Origin, err = decimalU32(a[2].Value); err != nil {
		return d, err
	}
	if d.Destination, err = decimalU32(a[3].Value); err != nil {
		return d, err
	}
	if d.Sender, err = hex32(a[4].Value); err != nil {
		return d, err
	}
	if d.Recipient, err = hex32(a[5].Value); err != nil {
		return d, err
	}
	if d.Hash, err = hex32(a[6].Value); err != nil {
		return d, err
	}
	return d, nil
}

// DispatchEvents is every Dispatch event of the mailbox realm in a results leaf, in order. It refuses a leaf that
// carries an error (a failed transaction's result) and a leaf with no such event.
func DispatchEvents(leaf []byte, mailboxPkgPath string) ([]Dispatch, error) {
	r, err := ParseResult(leaf)
	if err != nil {
		return nil, err
	}
	if r.Error {
		return nil, ErrNoDispatch
	}
	var out []Dispatch
	for _, a := range r.Events {
		if a.TypeURL != EventTypeURL {
			continue
		}
		e, err := ParseEvent(a.Value)
		if err != nil {
			return nil, err
		}
		if e.Type == DispatchType && e.PkgPath == mailboxPkgPath {
			d, err := DispatchFromEvent(e)
			if err != nil {
				return nil, err
			}
			out = append(out, d)
		}
	}
	if len(out) == 0 {
		return nil, ErrNoDispatch
	}
	return out, nil
}

// DispatchLeaf is the index of the leaf a proof of the id's message stages: of the leaves carrying a Dispatch event
// of the mailbox realm that names the id, the smallest (fewest bytes), the lowest index among equals; −1 if none.
// Anyone may Reemit a message (§5.1), so one block can carry its Dispatch twice — a stranger's beside many junk
// events, ahead of the relayer's own — and the first would be a proof too big for the ISM, or a dearer one.
func DispatchLeaf(leaves [][]byte, id [32]byte, mailboxPkgPath string) int {
	best := -1
	for i, leaf := range leaves {
		if best >= 0 && len(leaf) >= len(leaves[best]) {
			continue
		}
		ds, err := DispatchEvents(leaf, mailboxPkgPath)
		if err != nil {
			continue
		}
		for _, d := range ds {
			if d.ID == id {
				best = i
				break
			}
		}
	}
	return best
}

// ------------------------------------------------------------------ encoding --

// EncodeEvent is a chain.Event's value bytes (the bare struct).
func EncodeEvent(e *Event) []byte {
	var out []byte
	out = putBytesField(out, 1, []byte(e.Type))
	for _, a := range e.Attributes {
		var kv []byte
		kv = putBytesField(kv, 1, []byte(a.Key))
		kv = putBytesField(kv, 2, []byte(a.Value))
		out = putBytesField(out, 2, kv)
	}
	return putBytesField(out, 3, []byte(e.PkgPath))
}

// EncodeResult is a results leaf from its parts: an optional error Any, the data, and events as Any bytes.
func EncodeResult(errorAny []byte, data []byte, events [][]byte) []byte {
	var out []byte
	if errorAny != nil {
		out = putBytesField(out, 1, errorAny)
	}
	out = putBytesField(out, 2, data)
	for _, ev := range events {
		out = putBytesField(out, 3, ev)
	}
	return out
}

// ToEvent is the event the realm emits for this dispatch.
func (d Dispatch) ToEvent(mailboxPkgPath string) *Event {
	hx := func(b []byte) string { return hex.EncodeToString(b) }
	return &Event{
		Type: DispatchType,
		Attributes: []Attribute{
			{"id", hx(d.ID[:])},
			{"nonce", strconv.FormatUint(uint64(d.Nonce), 10)},
			{"origin", strconv.FormatUint(uint64(d.Origin), 10)},
			{"destination", strconv.FormatUint(uint64(d.Destination), 10)},
			{"sender", hx(d.Sender[:])},
			{"recipient", hx(d.Recipient[:])},
			{"hash", hx(d.Hash[:])},
		},
		PkgPath: mailboxPkgPath,
	}
}

// MessageHash is a proof's key and the Dispatch event's hash attribute: sha256 of the message bytes.
func MessageHash(message []byte) [32]byte { return sha256.Sum256(message) }

// DecisionPrefix is a decision body's 9-byte prefix: the kind, then the sequence number big-endian; ok false
// for a body shorter than nine bytes.
func DecisionPrefix(body []byte) (kind byte, seq uint64, ok bool) {
	if len(body) < 9 {
		return 0, 0, false
	}
	for i := 1; i < 9; i++ {
		seq = seq<<8 | uint64(body[i])
	}
	return body[0], seq, true
}
