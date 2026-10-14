package sol

import (
	"encoding/binary"
	"errors"

	"github.com/gagliardetto/solana-go"

	"github.com/gnolang/bridges/solana/bridge/gnolight"
)

// Borsh, as the three programs derive it (bridge/programs): little-endian integers, a u32 length before a vector
// or a string, a u8 tag before an option (0 none, 1 some) and before an enum variant, fixed arrays bare.

func (e *enc) u16(v uint16) { _ = binary.Write(e, binary.LittleEndian, v) }
func (e *enc) i32(v int32)  { _ = binary.Write(e, binary.LittleEndian, v) }
func (e *enc) i64(v int64)  { _ = binary.Write(e, binary.LittleEndian, v) }
func (e *enc) boolean(v bool) {
	if v {
		e.u8(1)
	} else {
		e.u8(0)
	}
}
func (e *enc) arr32(a [32]byte) { e.Write(a[:]) }

// header writes gno-light-core's Header in its borsh form (every string and byte field length-prefixed).
func (e *enc) header(h *gnolight.Header) {
	e.str(h.Version)
	e.str(h.ChainID)
	e.i64(h.Height)
	e.i64(h.Time.Secs)
	e.i32(h.Time.Nanos)
	e.i64(h.NumTxs)
	e.i64(h.TotalTxs)
	e.str(h.AppVersion)
	e.blockID(h.LastBlockID)
	e.vec(h.LastCommitHash)
	e.vec(h.DataHash)
	e.vec(h.ValidatorsHash)
	e.vec(h.NextValidatorsHash)
	e.vec(h.ConsensusHash)
	e.vec(h.AppHash)
	e.vec(h.LastResultsHash)
	e.str(h.ProposerAddress)
}

func (e *enc) blockID(b gnolight.BlockID) {
	e.vec(b.Hash)
	e.u32(b.Parts.Total)
	e.vec(b.Parts.Hash)
}

func (e *enc) validators(vs []gnolight.Validator) {
	e.u32(uint32(len(vs)))
	for _, v := range vs {
		e.arr32(v.PubKey)
		e.i64(v.Power)
	}
}

func (e *enc) proof(p *gnolight.SimpleProof) {
	e.u32(p.Total)
	e.u32(p.Index)
	e.arr32(p.LeafHash)
	e.u32(uint32(len(p.Aunts)))
	for _, a := range p.Aunts {
		e.arr32(a)
	}
}

// dec reads borsh; the first error sticks and every later read returns zero.
type dec struct {
	b   []byte
	pos int
	err error
}

var errShort = errors.New("borsh: the data ends early")

func newDec(b []byte) *dec { return &dec{b: b} }

func (d *dec) take(n int) []byte {
	if d.err != nil {
		return make([]byte, n)
	}
	if n < 0 || d.pos+n > len(d.b) {
		d.err = errShort
		return make([]byte, n)
	}
	out := d.b[d.pos : d.pos+n]
	d.pos += n
	return out
}

func (d *dec) u8() byte      { return d.take(1)[0] }
func (d *dec) u16() uint16   { return binary.LittleEndian.Uint16(d.take(2)) }
func (d *dec) u32() uint32   { return binary.LittleEndian.Uint32(d.take(4)) }
func (d *dec) u64() uint64   { return binary.LittleEndian.Uint64(d.take(8)) }
func (d *dec) i64() int64    { return int64(d.u64()) }
func (d *dec) i32() int32    { return int32(d.u32()) }
func (d *dec) boolean() bool { return d.u8() != 0 }
func (d *dec) arr32() (out [32]byte) {
	copy(out[:], d.take(32))
	return out
}
func (d *dec) key() solana.PublicKey { return solana.PublicKeyFromBytes(d.take(32)) }
func (d *dec) bytes() []byte {
	n := d.u32()
	if d.err == nil && int(n) > len(d.b)-d.pos {
		d.err = errShort
		return nil
	}
	return append([]byte(nil), d.take(int(n))...)
}
func (d *dec) str() string { return string(d.bytes()) }
func (d *dec) option() bool {
	switch d.u8() {
	case 0:
		return false
	case 1:
		return true
	}
	if d.err == nil {
		d.err = errors.New("borsh: an option tag that is neither 0 nor 1")
	}
	return false
}
func (d *dec) optKey() *solana.PublicKey {
	if !d.option() {
		return nil
	}
	k := d.key()
	return &k
}
func (d *dec) done() error {
	if d.err != nil {
		return d.err
	}
	return nil
}

func (d *dec) header() *gnolight.Header {
	h := &gnolight.Header{}
	h.Version = d.str()
	h.ChainID = d.str()
	h.Height = d.i64()
	h.Time.Secs = d.i64()
	h.Time.Nanos = d.i32()
	h.NumTxs = d.i64()
	h.TotalTxs = d.i64()
	h.AppVersion = d.str()
	h.LastBlockID = d.blockID()
	h.LastCommitHash = d.bytes()
	h.DataHash = d.bytes()
	h.ValidatorsHash = d.bytes()
	h.NextValidatorsHash = d.bytes()
	h.ConsensusHash = d.bytes()
	h.AppHash = d.bytes()
	h.LastResultsHash = d.bytes()
	h.ProposerAddress = d.str()
	return h
}

func (d *dec) blockID() gnolight.BlockID {
	var b gnolight.BlockID
	b.Hash = d.bytes()
	b.Parts.Total = d.u32()
	b.Parts.Hash = d.bytes()
	return b
}

func (d *dec) validators() []gnolight.Validator {
	n := d.u32()
	if d.err != nil || n > 1024 {
		if d.err == nil {
			d.err = errors.New("borsh: too many validators")
		}
		return nil
	}
	out := make([]gnolight.Validator, 0, n)
	for i := uint32(0); i < n && d.err == nil; i++ {
		out = append(out, gnolight.Validator{PubKey: d.arr32(), Power: d.i64()})
	}
	return out
}

func (d *dec) proof() *gnolight.SimpleProof {
	p := &gnolight.SimpleProof{}
	p.Total = d.u32()
	p.Index = d.u32()
	p.LeafHash = d.arr32()
	n := d.u32()
	if d.err != nil || n > 128 {
		if d.err == nil {
			d.err = errors.New("borsh: too many aunts")
		}
		return p
	}
	for i := uint32(0); i < n && d.err == nil; i++ {
		p.Aunts = append(p.Aunts, d.arr32())
	}
	return p
}
