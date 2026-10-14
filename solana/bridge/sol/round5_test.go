package sol

import (
	"strings"
	"testing"

	"github.com/gagliardetto/solana-go"
)

// adminStateV1 builds an admin state in the version-1 layout (M2j's form): n cancelled ranges, h holds, an
// optional successor, the predecessor and whether it inherited.
func adminStateV1(n, h int, successor *solana.PublicKey, predecessor solana.PublicKey, inherited byte) []byte {
	b := []byte{1, 8, 1}
	b = append(b, 0, 0, 0, 0)            // origin
	b = append(b, make([]byte, 8)...)    // last seq
	b = append(b, make([]byte, 6*32)...) // the seats
	b = append(b, make([]byte, 8)...)    // six flags, holdSince, dipSince: none
	if successor == nil {
		b = append(b, 0)
	} else {
		b = append(append(b, 1), successor[:]...)
	}
	b = append(b, 254)                   // the bump
	b = append(b, byte(n), 0, 0, 0)      // the cancelled ranges
	b = append(b, make([]byte, 16*n)...) //
	b = append(b, byte(h), 0, 0, 0)      // the holds
	b = append(b, make([]byte, 16*h)...) //
	b = append(b, 0, 0, 0, 0)            // pending
	b = append(b, predecessor[:]...)     //
	return append(b, inherited)          //
}

// S-21, G52: the version-1 layout the hand-over reads is judged on a successor's bytes as the program's
// `can_inherit` judges it — a tolerant borsh load of the prefix (262 for a fresh successor, plus the vectors'
// entries, predecessor and inherited the last 33 bytes), the bytes past it left alone — and a non-zero reserve
// is named as a field this build does not read (a note for the member, never a layout finding: M2h's contract
// lets a later build append a field after `inherited`); the fresh length is pinned as a unit assertion.
func TestAdminStateLayout(t *testing.T) {
	pred := solana.MustPublicKeyFromBase58("ATokenGPvbdGVxr1b2hvZbsiqW5xWH25efTNsLJA8knL")
	succ := solana.MustPublicKeyFromBase58("TokenzQdBNbLqP5VEhdkAS6EPFLC1PHnBqCXEpPxuEb")
	fresh := adminStateV1(0, 0, nil, pred, 0)
	if len(fresh) != AdminStateFreshLen {
		t.Fatalf("a fresh state is %d bytes, the layout says %d", len(fresh), AdminStateFreshLen)
	}
	st, err := ParseAdminState(fresh)
	if err != nil || st.Len != AdminStateFreshLen || !st.Predecessor.Equals(pred) || st.Inherited != 0 || !st.AwaitsInheritance() {
		t.Fatalf("the fresh state: %+v %v", st, err)
	}
	account := append(append([]byte{}, fresh...), make([]byte, AdminStateAccountLen-len(fresh))...) // the init size, the reserve zero
	st, _ = ParseAdminState(account)
	if why := AdminStateReserve(st, account); why != "" {
		t.Fatalf("a fresh successor at its init size: %s", why)
	}
	// the vectors filled: the form grows by their entries (the pin of the arithmetic: Len is where the parse
	// ended), the bytes past it the reserve
	full := adminStateV1(2, 1, &succ, pred, 1)
	st, err = ParseAdminState(full)
	if err != nil || st.Len != AdminStateFreshLen+32+2*16+16 || len(st.Cancelled) != 2 || len(st.Holds) != 1 || st.Successor == nil {
		t.Fatalf("the full state: %+v %v", st, err)
	}
	if why := AdminStateReserve(st, append(full, make([]byte, 100)...)); why != "" {
		t.Fatalf("a full state of the layout: %s", why)
	}
	// a field of another build past inherited (what the contract allows): named by its offset as a note, the
	// parse untouched — the hand-over reads none of it
	other := append(append([]byte{}, account...), 0)
	other[AdminStateFreshLen+5] = 9
	other[AdminStateFreshLen+6] = 1
	st, err = ParseAdminState(other)
	if err != nil || st.Len != AdminStateFreshLen || !st.AwaitsInheritance() {
		t.Fatalf("a state with a field appended does not parse as the version-1 prefix: %+v %v", st, err)
	}
	if why := AdminStateReserve(st, other); !strings.Contains(why, "byte 267, past the version-1 form's 262 bytes, is 0x9 (2 non-zero bytes in the reserve): a field this build does not read") || !strings.Contains(why, "the hand-over reads none of it") {
		t.Fatalf("another build's appended field: %q", why)
	}
	// a form ending early does not parse at all
	if _, err := ParseAdminState(fresh[:len(fresh)-1]); err == nil {
		t.Fatal("a form ending early parsed")
	}
	// a field inserted before the vectors (after the authority bump): the vector lengths read as another build's
	// bytes and the prefix does not parse — the layout finding, as the program's peek would spend the hand-over
	inserted := append(append(append([]byte{}, fresh[:217]...), 0xff, 0xff, 0xff, 0xff, 0, 0, 0, 0), fresh[217:]...)
	if _, err := ParseAdminState(inserted); err == nil {
		t.Fatal("a form with a field inserted before the vectors parsed")
	}
}
