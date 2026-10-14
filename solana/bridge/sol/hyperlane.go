package sol

import (
	"encoding/binary"
	"errors"
	"fmt"
	"sort"

	"github.com/gagliardetto/solana-go"
	"golang.org/x/crypto/sha3"
)

// Hyperlane's account layouts and the settings instructions the setup step and the admin program send (the
// sealevel sources at the mainnet builds' commit; sol-util's hl.rs is the programs' reading of the same bytes),
// and the Token-2022 mint with its metadata extensions, which the audit prints.

// ---------------------------------------------------------------- mailbox --

// Inbox is the mailbox's inbox account (an AccountData: an initialized byte, then borsh).
type Inbox struct {
	LocalDomain    uint32
	DefaultISM     solana.PublicKey
	ProcessedCount uint64
}

func ParseInbox(data []byte) (*Inbox, error) {
	if len(data) < 1 || data[0] != 1 {
		return nil, errors.New("inbox: not initialized")
	}
	d := newDec(data[1:])
	in := &Inbox{LocalDomain: d.u32()}
	d.u8() // the bump
	in.DefaultISM = d.key()
	in.ProcessedCount = d.u64()
	if err := d.done(); err != nil {
		return nil, fmt.Errorf("inbox: %w", err)
	}
	return in, nil
}

// Outbox is the mailbox's outbox account: the owner, the tree, the fee maximum and the fee config.
type Outbox struct {
	LocalDomain    uint32
	Owner          *solana.PublicKey
	Count          uint64
	Root           [32]byte
	MaxProtocolFee uint64
	Fee            uint64
	Beneficiary    solana.PublicKey
}

func ParseOutbox(data []byte) (*Outbox, error) {
	if len(data) < 1 || data[0] != 1 {
		return nil, errors.New("outbox: not initialized")
	}
	d := newDec(data[1:])
	ob := &Outbox{LocalDomain: d.u32()}
	d.u8() // the bump
	ob.Owner = d.optKey()
	var branch [32][32]byte
	for i := range branch {
		branch[i] = d.arr32()
	}
	ob.Count = d.u64()
	ob.MaxProtocolFee = d.u64()
	ob.Fee = d.u64()
	ob.Beneficiary = d.key()
	if err := d.done(); err != nil {
		return nil, fmt.Errorf("outbox: %w", err)
	}
	ob.Root = treeRoot(branch, uint32(ob.Count))
	return ob, nil
}

// treeRoot is Hyperlane's incremental tree root from its branch and count (bridge/hl's Tree, without the import).
func treeRoot(branch [32][32]byte, count uint32) [32]byte {
	var zeroes [32][32]byte
	for i := 1; i < 32; i++ {
		zeroes[i] = keccak(zeroes[i-1][:], zeroes[i-1][:])
	}
	var cur [32]byte
	for i := 0; i < 32; i++ {
		if (count>>uint(i))&1 == 1 {
			cur = keccak(branch[i][:], cur[:])
		} else {
			cur = keccak(cur[:], zeroes[i][:])
		}
	}
	return cur
}

// SetDefaultISMIx sets the inbox's default ISM (InboxSetDefaultIsm, variant 2); the owner signs.
func (m Mailbox) SetDefaultISMIx(owner, ism solana.PublicKey) solana.Instruction {
	var e enc
	e.u8(2)
	e.key(ism)
	return ix(m.Program, e.Bytes(), w(m.Inbox()), r(m.Outbox()), ws(owner))
}

// TransferOwnershipIx hands the mailbox to a new owner (variant 9; nil burns it).
func (m Mailbox) TransferOwnershipIx(owner solana.PublicKey, to *solana.PublicKey) solana.Instruction {
	var e enc
	e.u8(9)
	e.optKey(to)
	return ix(m.Program, e.Bytes(), w(m.Outbox()), rs(owner))
}

// SetProtocolFeeIx sets the fee and its beneficiary together (variant 11), the fee at most the maximum.
func (m Mailbox) SetProtocolFeeIx(owner solana.PublicKey, fee uint64, beneficiary solana.PublicKey) solana.Instruction {
	var e enc
	e.u8(11)
	e.u64(fee)
	e.key(beneficiary)
	return ix(m.Program, e.Bytes(), w(m.Outbox()), rs(owner))
}

// ------------------------------------------------------------------ token --

// HyperlaneToken is a warp route's own account (HyperlaneToken<SyntheticPlugin>).
type HyperlaneToken struct {
	Mailbox          solana.PublicKey
	ProcessAuthority solana.PublicKey
	Decimals         byte
	RemoteDecimals   byte
	Owner            *solana.PublicKey
	ISM              *solana.PublicKey
	IGP              *solana.PublicKey // the paymaster program, if any
	IGPAccount       *solana.PublicKey
	DestinationGas   map[uint32]uint64
	RemoteRouters    map[uint32][32]byte
	Mint             solana.PublicKey
	FeeConfig        *[2]solana.PublicKey // the fee program and account, if any
}

// RouterFor is the enrolled router for a domain, if any.
func (t *HyperlaneToken) RouterFor(domain uint32) ([32]byte, bool) {
	r, ok := t.RemoteRouters[domain]
	return r, ok
}

func ParseToken(data []byte) (*HyperlaneToken, error) {
	if len(data) < 1 || data[0] != 1 {
		return nil, errors.New("token: not initialized")
	}
	d := newDec(data[1:])
	t := &HyperlaneToken{DestinationGas: map[uint32]uint64{}, RemoteRouters: map[uint32][32]byte{}}
	d.u8() // the bump
	t.Mailbox = d.key()
	t.ProcessAuthority = d.key()
	d.u8() // the dispatch authority's bump
	t.Decimals = d.u8()
	t.RemoteDecimals = d.u8()
	t.Owner = d.optKey()
	t.ISM = d.optKey()
	if d.option() {
		k := d.key()
		t.IGP = &k
		d.u8() // the paymaster's type
		a := d.key()
		t.IGPAccount = &a
	}
	n := d.u32()
	for i := uint32(0); i < n && d.err == nil && i < 1024; i++ {
		t.DestinationGas[d.u32()] = d.u64()
	}
	n = d.u32()
	for i := uint32(0); i < n && d.err == nil && i < 1024; i++ {
		dom := d.u32()
		t.RemoteRouters[dom] = d.arr32()
	}
	t.Mint = d.key()
	d.u8() // the mint's bump
	d.u8() // the ATA payer's bump
	if d.err == nil && d.pos+8 <= len(d.b) && string(d.b[d.pos:d.pos+8]) == "TOKFEEV1" {
		d.take(8)
		var fc [2]solana.PublicKey
		fc[0], fc[1] = d.key(), d.key()
		t.FeeConfig = &fc
	}
	if err := d.done(); err != nil {
		return nil, fmt.Errorf("token: %w", err)
	}
	return t, nil
}

// ------------------------------------------------------------- Token-2022 --

// Mint2022 is a Token-2022 mint with the extensions a synthetic token carries.
type Mint2022 struct {
	Decimals        byte
	Supply          uint64
	MintAuthority   *solana.PublicKey
	FreezeAuthority *solana.PublicKey
	// The metadata pointer extension (nil when absent).
	PointerAuthority *solana.PublicKey
	PointerAddress   *solana.PublicKey
	HasPointer       bool
	// The token metadata extension (nil when absent).
	UpdateAuthority *solana.PublicKey
	Name, Symbol    string
	URI             string
	HasMetadata     bool
}

const (
	extMetadataPointer = 18
	extTokenMetadata   = 19
	mintBaseLen        = 82
	accountTypeOffset  = 165
)

func cOption(b []byte) *solana.PublicKey {
	if len(b) < 36 || binary.LittleEndian.Uint32(b[:4]) == 0 {
		return nil
	}
	k := solana.PublicKeyFromBytes(b[4:36])
	return &k
}

func nonZeroKey(b []byte) *solana.PublicKey {
	if len(b) < 32 {
		return nil
	}
	k := solana.PublicKeyFromBytes(b[:32])
	if k.IsZero() {
		return nil
	}
	return &k
}

// ParseMint2022 reads the mint's base and its TLV extensions.
func ParseMint2022(data []byte) (*Mint2022, error) {
	if len(data) < mintBaseLen {
		return nil, errors.New("mint: shorter than a mint")
	}
	m := &Mint2022{}
	m.MintAuthority = cOption(data[0:36])
	m.Supply = binary.LittleEndian.Uint64(data[36:44])
	m.Decimals = data[44]
	if data[45] != 1 {
		return nil, errors.New("mint: not initialized")
	}
	m.FreezeAuthority = cOption(data[46:82])
	if len(data) <= accountTypeOffset+1 || data[accountTypeOffset] != 1 {
		return m, nil
	}
	pos := accountTypeOffset + 1
	for pos+4 <= len(data) {
		typ := binary.LittleEndian.Uint16(data[pos : pos+2])
		ln := int(binary.LittleEndian.Uint16(data[pos+2 : pos+4]))
		pos += 4
		if pos+ln > len(data) {
			break
		}
		v := data[pos : pos+ln]
		pos += ln
		switch typ {
		case 0:
			return m, nil
		case extMetadataPointer:
			if len(v) >= 64 {
				m.HasPointer = true
				m.PointerAuthority = nonZeroKey(v[0:32])
				m.PointerAddress = nonZeroKey(v[32:64])
			}
		case extTokenMetadata:
			d := newDec(v)
			m.HasMetadata = true
			m.UpdateAuthority = nonZeroKey(d.take(32))
			d.key() // the mint
			m.Name = d.str()
			m.Symbol = d.str()
			m.URI = d.str()
		}
	}
	return m, nil
}

// PointerAuthorityToNoneIx drops the metadata pointer's authority (Token-2022 SetAuthority, type 12, new
// authority none); the current authority signs.
func PointerAuthorityToNoneIx(mint, current solana.PublicKey) solana.Instruction {
	var e enc
	e.u8(6)  // SetAuthority
	e.u8(12) // MetadataPointer
	e.u8(0)  // COption none
	return ix(Token2022, e.Bytes(), w(mint), rs(current))
}

// MetadataUpdateAuthorityToNoneIx drops the token metadata's update authority (the metadata interface's
// UpdateAuthority with a zero key); the current update authority signs.
func MetadataUpdateAuthorityToNoneIx(mint, current solana.PublicKey) solana.Instruction {
	var e enc
	e.Write(disc("spl_token_metadata_interface:update_the_authority"))
	e.Write(make([]byte, 32))
	return ix(Token2022, e.Bytes(), w(mint), rs(current))
}

// keccak is keccak256 of the parts (hl.Keccak, kept here so sol does not import hl).
func keccak(parts ...[]byte) [32]byte {
	h := sha3.NewLegacyKeccak256()
	for _, p := range parts {
		h.Write(p)
	}
	var out [32]byte
	h.Sum(out[:0])
	return out
}

// SortedDomains lists a router map's domains in order, for printing.
func SortedDomains(m map[uint32][32]byte) []uint32 {
	var out []uint32
	for d := range m {
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}
