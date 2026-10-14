// Package gnolight is the Go twin of bridge/programs/gno-light-core: the slice of gno.land's encodings the light
// client on Solana checks (PLAN.md §13), byte for byte, so the updater and the relayer can build what the programs
// verify and the monitors can check what the programs accepted. Amino (tm2's wire form), the header hash (a
// 16-leaf RFC 6962 merkle over the amino fields), each precommit's sign bytes, the validator leaf and set hash, the
// commit rule, tm2's SimpleProof, the ABCIResult leaf and the Dispatch event. Nothing here
// touches Solana; bridge/sol wraps these types in instructions.
package gnolight

import (
	"errors"
)

// The amino rules the programs mirror (tm2/pkg/amino/binary_encode.go): a field key is uvarint(field<<3 | typ3);
// zero-valued non-struct fields are omitted; int64 and int are zigzag varints; uint32/uint64/bytes are plain
// uvarints; strings and byte slices are length-prefixed; a registered interface value is an Any (field 1 the type
// URL, field 2 the value); time.Time is {1: uvarint(seconds), 2: uvarint(nanos)}; a top-level non-struct is wrapped
// as field 1; amino.MarshalSized prefixes the uvarint length.

const (
	typ3Varint  = 0
	typ3Fixed64 = 1
	typ3ByteLen = 2
	typ3Fixed32 = 5
)

func putUvarint(out []byte, v uint64) []byte {
	for {
		b := byte(v & 0x7f)
		v >>= 7
		if v == 0 {
			return append(out, b)
		}
		out = append(out, b|0x80)
	}
}

// putZigzag is Go's binary.PutVarint: zigzag, then uvarint.
func putZigzag(out []byte, v int64) []byte {
	return putUvarint(out, uint64((v<<1)^(v>>63)))
}

func putKey(out []byte, field uint32, typ3 byte) []byte {
	return putUvarint(out, uint64(field)<<3|uint64(typ3))
}

// putBytesField is a length-prefixed field (a string, bytes, a nested struct, an Any), omitted when empty.
func putBytesField(out []byte, field uint32, b []byte) []byte {
	if len(b) == 0 {
		return out
	}
	out = putKey(out, field, typ3ByteLen)
	out = putUvarint(out, uint64(len(b)))
	return append(out, b...)
}

// putUvarintField is a plain uvarint field (uint32, uint64, a byte), omitted when zero.
func putUvarintField(out []byte, field uint32, v uint64) []byte {
	if v == 0 {
		return out
	}
	out = putKey(out, field, typ3Varint)
	return putUvarint(out, v)
}

// putZigzagField is a zigzag varint field (int64, int), omitted when zero.
func putZigzagField(out []byte, field uint32, v int64) []byte {
	if v == 0 {
		return out
	}
	out = putKey(out, field, typ3Varint)
	return putZigzag(out, v)
}

// putFixed64Field is an eight-byte little-endian field (`binary:"fixed64"`), omitted when zero.
func putFixed64Field(out []byte, field uint32, v uint64) []byte {
	if v == 0 {
		return out
	}
	out = putKey(out, field, typ3Fixed64)
	for i := 0; i < 8; i++ {
		out = append(out, byte(v>>(8*i)))
	}
	return out
}

// timeBare is time.Time's own fields: seconds then nanoseconds, each a uvarint, each omitted when zero.
func timeBare(secs int64, nanos int32) []byte {
	var out []byte
	out = putUvarintField(out, 1, uint64(secs))
	out = putUvarintField(out, 2, uint64(nanos))
	return out
}

// sized is amino.MarshalSized: the uvarint length, then the bytes.
func sized(body []byte) []byte {
	out := putUvarint(nil, uint64(len(body)))
	return append(out, body...)
}

// Any is a google.protobuf.Any: the type URL as field 1, the value as field 2 (omitted when empty).
func Any(typeURL string, value []byte) []byte {
	var out []byte
	out = putBytesField(out, 1, []byte(typeURL))
	return putBytesField(out, 2, value)
}

// ------------------------------------------------------------------ decoding --

var ErrEncoding = errors.New("gnolight: amino bytes do not decode")

func readUvarint(buf []byte, pos *int) (uint64, error) {
	var v uint64
	var shift uint
	for {
		if *pos >= len(buf) {
			return 0, ErrEncoding
		}
		b := buf[*pos]
		*pos++
		if shift >= 64 {
			return 0, ErrEncoding
		}
		v |= uint64(b&0x7f) << shift
		if b&0x80 == 0 {
			return v, nil
		}
		shift += 7
	}
}

// readKey is a field key: the field number and its typ3.
func readKey(buf []byte, pos *int) (uint32, byte, error) {
	k, err := readUvarint(buf, pos)
	if err != nil {
		return 0, 0, err
	}
	field := k >> 3
	if field == 0 || field > 0xffffffff {
		return 0, 0, ErrEncoding
	}
	return uint32(field), byte(k & 7), nil
}

// readBytes is a length-prefixed value.
func readBytes(buf []byte, pos *int) ([]byte, error) {
	n, err := readUvarint(buf, pos)
	if err != nil {
		return nil, err
	}
	end := *pos + int(n)
	if n > uint64(len(buf)) || end > len(buf) || end < *pos {
		return nil, ErrEncoding
	}
	b := buf[*pos:end]
	*pos = end
	return b, nil
}

// skipValue skips a value of the given typ3 (a field the reader does not know).
func skipValue(buf []byte, pos *int, typ3 byte) error {
	switch typ3 {
	case typ3Varint:
		_, err := readUvarint(buf, pos)
		return err
	case typ3Fixed64:
		if *pos+8 > len(buf) {
			return ErrEncoding
		}
		*pos += 8
	case typ3ByteLen:
		_, err := readBytes(buf, pos)
		return err
	case typ3Fixed32:
		if *pos+4 > len(buf) {
			return ErrEncoding
		}
		*pos += 4
	default:
		return ErrEncoding
	}
	return nil
}

// field is one field of a bare struct: its number, typ3 and bytes (the value of a length-prefixed field, the raw
// bytes of any other).
type field struct {
	num   uint32
	typ3  byte
	value []byte
}

// fields walks a bare struct's fields.
func fields(buf []byte) ([]field, error) {
	var out []field
	pos := 0
	for pos < len(buf) {
		num, typ3, err := readKey(buf, &pos)
		if err != nil {
			return nil, err
		}
		start := pos
		if typ3 == typ3ByteLen {
			v, err := readBytes(buf, &pos)
			if err != nil {
				return nil, err
			}
			out = append(out, field{num, typ3, v})
		} else {
			if err := skipValue(buf, &pos, typ3); err != nil {
				return nil, err
			}
			out = append(out, field{num, typ3, buf[start:pos]})
		}
	}
	return out, nil
}
