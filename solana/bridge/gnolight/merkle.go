package gnolight

import (
	"crypto/sha256"
	"errors"
)

// tm2's simple merkle tree (tm2/pkg/crypto/merkle): leaf sha256(0x00‖x), inner sha256(0x01‖l‖r), the split at the
// largest power of two below n; and its SimpleProof.

var ErrBadProof = errors.New("gnolight: the proof does not fit")

func sha(parts ...[]byte) [32]byte {
	h := sha256.New()
	for _, p := range parts {
		h.Write(p)
	}
	var out [32]byte
	h.Sum(out[:0])
	return out
}

func LeafHash(leaf []byte) [32]byte { return sha([]byte{0}, leaf) }

func InnerHash(l, r [32]byte) [32]byte { return sha([]byte{1}, l[:], r[:]) }

// SplitPoint is the largest power of two strictly below n (n ≥ 2).
func SplitPoint(n int) int {
	k := 1
	for k*2 < n {
		k *= 2
	}
	return k
}

// SimpleHash is SimpleHashFromByteSlices; ok is false for no items.
func SimpleHash(items [][]byte) (root [32]byte, ok bool) {
	switch len(items) {
	case 0:
		return root, false
	case 1:
		return LeafHash(items[0]), true
	}
	k := SplitPoint(len(items))
	l, _ := SimpleHash(items[:k])
	r, _ := SimpleHash(items[k:])
	return InnerHash(l, r), true
}

// SimpleProof is tm2's: the leaf's position, its hash, and the sibling hashes from the leaf's sibling up to a
// child of the root.
type SimpleProof struct {
	Total    uint32
	Index    uint32
	LeafHash [32]byte
	Aunts    [][32]byte
}

// path is the directions from the root down to index: true where the path goes left.
func path(index, total uint32) ([]bool, error) {
	if total == 0 || index >= total {
		return nil, ErrBadProof
	}
	i, t := int(index), int(total)
	var dirs []bool
	for t > 1 {
		k := SplitPoint(t)
		if i < k {
			dirs = append(dirs, true)
			t = k
		} else {
			dirs = append(dirs, false)
			i -= k
			t -= k
		}
	}
	return dirs, nil
}

// Root is the root the proof implies (computeHashFromAunts): the aunts run from the leaf's sibling upward.
func (p *SimpleProof) Root() ([32]byte, error) {
	dirs, err := path(p.Index, p.Total)
	if err != nil {
		return [32]byte{}, err
	}
	if len(dirs) != len(p.Aunts) {
		return [32]byte{}, ErrBadProof
	}
	h := p.LeafHash
	for i := range dirs {
		left := dirs[len(dirs)-1-i]
		if left {
			h = InnerHash(h, p.Aunts[i])
		} else {
			h = InnerHash(p.Aunts[i], h)
		}
	}
	return h, nil
}

// Verify is SimpleProof.Verify: the leaf hashes to the proof's leaf hash and the proof's root is root.
func (p *SimpleProof) Verify(root [32]byte, leaf []byte) error {
	if LeafHash(leaf) != p.LeafHash {
		return ErrBadProof
	}
	r, err := p.Root()
	if err != nil {
		return err
	}
	if r != root {
		return ErrBadProof
	}
	return nil
}

// Proof is the proof of one item of a list, as SimpleProofsFromByteSlices makes it.
func Proof(items [][]byte, index int) (*SimpleProof, error) {
	if index < 0 || index >= len(items) {
		return nil, ErrBadProof
	}
	dirs, err := path(uint32(index), uint32(len(items)))
	if err != nil {
		return nil, err
	}
	var topDown [][32]byte
	lo, hi := 0, len(items)
	for _, left := range dirs {
		k := SplitPoint(hi - lo)
		if left {
			h, _ := SimpleHash(items[lo+k : hi])
			topDown = append(topDown, h)
			hi = lo + k
		} else {
			h, _ := SimpleHash(items[lo : lo+k])
			topDown = append(topDown, h)
			lo += k
		}
	}
	aunts := make([][32]byte, len(topDown))
	for i := range topDown {
		aunts[i] = topDown[len(topDown)-1-i]
	}
	return &SimpleProof{Total: uint32(len(items)), Index: uint32(index), LeafHash: LeafHash(items[index]), Aunts: aunts}, nil
}
