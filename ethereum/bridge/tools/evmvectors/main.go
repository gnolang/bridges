// Command evmvectors writes the JSON vectors the Solidity contracts' tests read (eth/bridge/contracts/test/vectors),
// from the Solana module's Go twins of the light client (`gnolight`) and of Hyperlane (`hl`):
//
//   - fixtures.json: gnoland-1's own seventeen blocks (bridge/programs/fixtures) as the client on Ethereum takes them:
//     each header as its sixteen amino leaves and its hash, its commit (the BlockID's part set, the round, each
//     precommit's slot, time, r and s, its sign bytes and Go's crypto/ed25519 verdict), its validator set and the
//     set's hash, and, where the next block is a fixture too, its results leaves with a SimpleProof of each against
//     the next header's LastResultsHash;
//   - synthetic.json: a test chain ("gno-test-1") signed by deterministic ed25519 keys through four validator sets
//     (4, 6, 20 and 60 validators, three announced changes, one commit at round 2, one block of two parts), whose
//     results carry Dispatch events of a given mailbox realm with real Hyperlane messages to the Ethereum
//     contracts the Solidity suite's setup deploys (their addresses are the deployer's CREATE addresses, computed
//     here and checked there), plus forged and mutated signatures with Go's verdict on each.
//
// Usage, from eth/bridge: go run ./tools/evmvectors -fixtures ../../bridge/programs/fixtures -out contracts/test/vectors
package main

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strconv"

	"github.com/gnolang/bridges/solana/bridge/gnolight"
	"github.com/gnolang/bridges/solana/bridge/hl"
)

// hexb is a byte string written as 0x-prefixed lowercase hex (forge's JSON reader takes it as bytes).
type hexb []byte

func (h hexb) MarshalJSON() ([]byte, error) { return json.Marshal("0x" + hex.EncodeToString(h)) }

// The Solidity suite's setup deploys from this address, nonce 0 first (test/helpers/BridgeFixture.sol): the
// contracts' addresses below are its CREATE addresses in the order of script/Setup.s.sol's BridgeSetup.deploy —
// 0 the ProxyAdmin, 1–2 the Mailbox and its proxy, 3 the MerkleTreeHook, 4 the ProtocolFee, 5 the Ed25519Verifier,
// 6–7 the client and its proxy, 8–9 token 0, 10–11 token 1, 12–13 the gate, 14 HlAdmin (which creates HlAdminExec).
const deployerHex = "de9107e5de9107e5de9107e5de9107e5de9107e5"

const (
	nonceToken0 = 9
	nonceToken1 = 11
	nonceAdmin  = 14
)

// The test chain and its realms (the Ethereum deployment's paths under the operator namespace of PLAN.md §4).
const (
	syntheticChain = "gno-test-1"
	namespace      = "g1leu8d2vsplhehcfkjg50mwgdpxdkt8tztu95wr"
	localDomain    = uint32(31337)
)

var (
	mailboxPath = "gno.land/r/" + namespace + "/eth/hlmailbox"
	gnotPath    = "gno.land/r/" + namespace + "/eth/hlgnot"
	bubblePath  = "gno.land/r/" + namespace + "/eth/hlbubble"
	hlgovPath   = "gno.land/r/" + namespace + "/eth/hlgov"
)

// ---------------------------------------------------------------------------------------------------- shapes --

type Precommits struct {
	Index     []int   `json:"index"`
	Secs      []int64 `json:"secs"`
	Nanos     []int32 `json:"nanos"`
	R         []hexb  `json:"r"`
	S         []hexb  `json:"s"`
	SignBytes []hexb  `json:"signBytes"`
	Valid     []bool  `json:"valid"`
}

type Proof struct {
	Index int    `json:"index"`
	Total int    `json:"total"`
	Aunts []hexb `json:"aunts"`
	Leaf  hexb   `json:"leaf"`
}

// Header is one header as the client takes it, with its commit and the set that signs it.
type Header struct {
	Height             int64      `json:"height"`
	Hash               hexb       `json:"hash"`
	Leaves             []hexb     `json:"leaves"`
	Time               int64      `json:"time"`
	ValidatorsHash     hexb       `json:"validatorsHash"`
	NextValidatorsHash hexb       `json:"nextValidatorsHash"`
	LastResultsHash    hexb       `json:"lastResultsHash"`
	Announcing         bool       `json:"announcing"`
	PartsTotal         uint32     `json:"partsTotal"`
	PartsHash          hexb       `json:"partsHash"`
	Round              int64      `json:"round"`
	Precommits         Precommits `json:"precommits"`
	Set                string     `json:"set"`     // the signing set's name (synthetic) or "" (fixtures carry it inline)
	NextSet            string     `json:"nextSet"` // the announced set's name when announcing (synthetic)
	Pubkeys            []hexb     `json:"pubkeys"` // fixtures: the set inline
	Powers             []int64    `json:"powers"`
	SetHash            hexb       `json:"setHash"`
	// The results of this header's block, proven against the next header's LastResultsHash (when it is here).
	ResultsRoot   hexb    `json:"resultsRoot"`
	ResultsProofs []Proof `json:"resultsProofs"`
}

// Check is one (key, sign bytes, r, s) triple and Go's crypto/ed25519 verdict on it: the suite's signature oracle
// accepts exactly the valid ones, and must refuse every one Go refuses.
type Check struct {
	Pubkey    hexb   `json:"pubkey"`
	SignBytes hexb   `json:"signBytes"`
	R         hexb   `json:"r"`
	S         hexb   `json:"s"`
	Valid     bool   `json:"valid"`
	Why       string `json:"why"`
}

type Set struct {
	Name    string  `json:"name"`
	Pubkeys []hexb  `json:"pubkeys"`
	Powers  []int64 `json:"powers"`
	Hash    hexb    `json:"hash"`
}

// Message is a Hyperlane message dispatched on the test chain, and where its Dispatch event is proven.
type Message struct {
	Name      string `json:"name"`
	Bytes     hexb   `json:"bytes"`
	ID        hexb   `json:"id"`
	Recipient hexb   `json:"recipient"`
	Body      hexb   `json:"body"`
	// The header whose LastResultsHash commits to the leaf, the leaf's place and its SimpleProof.
	Header int64  `json:"header"`
	Index  int    `json:"index"`
	Total  int    `json:"total"`
	Aunts  []hexb `json:"aunts"`
	Leaf   hexb   `json:"leaf"`
	// abi.encode(bytes32 headerHash, uint32 total, uint32 index, bytes32[] aunts, bytes leaf): the metadata the
	// gate and the client read.
	Metadata hexb `json:"metadata"`
}

type Fixtures struct {
	ChainID string   `json:"chainId"`
	Origin  uint32   `json:"origin"`
	Headers []Header `json:"headers"`
	Checks  []Check  `json:"checks"`
	Example hexb     `json:"example110"` // the Solana plan §13's 110-byte sign bytes, rebuilt here
}

type Synthetic struct {
	ChainID        string    `json:"chainId"`
	GnoDomain      uint32    `json:"gnoDomain"`
	LocalDomain    uint32    `json:"localDomain"`
	MailboxPkgPath string    `json:"mailboxPkgPath"`
	Deployer       hexb      `json:"deployer"`
	Token0         hexb      `json:"token0"`
	Token1         hexb      `json:"token1"`
	Admin          hexb      `json:"admin"`
	Route0         hexb      `json:"route0"`
	Route1         hexb      `json:"route1"`
	Hlgov          hexb      `json:"hlgov"`
	Alice          hexb      `json:"alice"`
	Bob            hexb      `json:"bob"`
	Carol          hexb      `json:"carol"`
	Sets           []Set     `json:"sets"`
	Headers        []Header  `json:"headers"`
	Messages       []Message `json:"messages"`
	Forged         Header    `json:"forged"` // header 101 with a forger set's signatures under the real set's keys
	// Header 101's slot 0 signing a nil precommit (no BlockID) and a precommit for another block: real signatures
	// over other bytes, which the client — building the sign bytes of the header's BlockID — must refuse.
	NotForTheBlock Precommits `json:"notForTheBlock"`
	Checks         []Check    `json:"checks"`
}

// ------------------------------------------------------------------------------------------------ the main --

func main() {
	fixturesDir := flag.String("fixtures", "../../bridge/programs/fixtures", "gnoland-1's fixtures")
	out := flag.String("out", "contracts/test/vectors", "where the JSON goes")
	flag.Parse()
	if err := os.MkdirAll(*out, 0o755); err != nil {
		log.Fatal(err)
	}
	fx := fixtures(*fixturesDir)
	write(filepath.Join(*out, "fixtures.json"), fx)
	write(filepath.Join(*out, "synthetic.json"), synthetic())
	fmt.Printf("evmvectors: %d fixture headers, %d checks\n", len(fx.Headers), len(fx.Checks))
}

func write(path string, v any) {
	b, err := json.MarshalIndent(v, "", " ")
	if err != nil {
		log.Fatal(err)
	}
	if err := os.WriteFile(path, append(b, '\n'), 0o644); err != nil {
		log.Fatal(err)
	}
}

func must[T any](v T, err error) T {
	if err != nil {
		log.Fatal(err)
	}
	return v
}

// ---------------------------------------------------------------------------------------------- the fixtures --

type fixture struct {
	height  int64
	block   *gnolight.Block
	commit  *gnolight.Commit
	votes   []*gnolight.Vote
	set     *gnolight.ValidatorSet
	results []*gnolight.TxResult
}

func fixtures(dir string) Fixtures {
	files := must(filepath.Glob(filepath.Join(dir, "*.json")))
	byHeight := map[int64]*fixture{}
	var heights []int64
	for _, f := range files {
		var j struct {
			Height       json.Number     `json:"height"`
			Block        json.RawMessage `json:"block"`
			Commit       json.RawMessage `json:"commit"`
			Validators   json.RawMessage `json:"validators"`
			BlockResults json.RawMessage `json:"block_results"`
		}
		if err := json.Unmarshal(must(os.ReadFile(f)), &j); err != nil {
			log.Fatal(err)
		}
		x := &fixture{height: must(strconv.ParseInt(string(j.Height), 10, 64))}
		x.block = must(gnolight.BlockFromJSON(j.Block))
		x.commit, x.votes = must2(gnolight.CommitFromJSON(j.Commit))
		x.set = must(gnolight.ValidatorsFromJSON(j.Validators))
		if string(j.BlockResults) != "null" {
			x.results = must(gnolight.BlockResultsFromJSON(j.BlockResults))
		}
		byHeight[x.height] = x
		heights = append(heights, x.height)
	}
	sort.Slice(heights, func(i, j int) bool { return heights[i] < heights[j] })
	out := Fixtures{ChainID: "gnoland-1", Origin: hl.DomainGnoMainnet}
	for _, h := range heights {
		x := byHeight[h]
		hd := x.block.Header
		if hd.Hash() != x.block.Hash {
			log.Fatalf("height %d: the header does not hash to the chain's", h)
		}
		e := headerOf(hd, x.block.Parts, x.commit.Round())
		setHash, _ := x.set.Hash()
		e.Pubkeys, e.Powers, e.SetHash = pubkeys(x.set), powers(x.set), setHash[:]
		for i, v := range x.votes {
			if v == nil || x.commit.Precommits[i] == nil {
				continue // a nil precommit or one for another block: never carried
			}
			sb := x.commit.SignBytes(hd.ChainID, h, i)
			pk := x.set.Validators[i].PubKey
			ok := ed25519.Verify(pk[:], sb, v.Signature)
			if !ok {
				log.Fatalf("height %d slot %d: a real signature does not verify", h, i)
			}
			addPrecommit(&e.Precommits, i, v.Time, v.Signature, sb, ok)
			out.Checks = append(out.Checks, mutations(pk, sb, v.Signature, fmt.Sprintf("gnoland-1 %d/%d", h, i))...)
		}
		if next, ok := byHeight[h+1]; ok && x.results != nil {
			leaves := must(gnolight.ResultLeaves(x.results))
			root, _ := gnolight.SimpleHash(leaves)
			if string(root[:]) != string(next.block.Header.LastResultsHash) {
				log.Fatalf("height %d: the results do not hash to the next header's LastResultsHash", h)
			}
			e.ResultsRoot = root[:]
			for i := range leaves {
				p := must(gnolight.Proof(leaves, i))
				e.ResultsProofs = append(e.ResultsProofs, Proof{Index: int(p.Index), Total: int(p.Total), Aunts: aunts(p), Leaf: leaves[i]})
			}
		}
		out.Headers = append(out.Headers, e)
	}
	// the Solana plan §13's example: validator 0's precommit at 701,862, round 0, one part
	bid := gnolight.BlockID{Hash: mustHex("B0D79144E283B51F02881A930A56E0D98DB717702D116DFF92271FA5A75A872D"),
		Parts: gnolight.PartSetHeader{Total: 1, Hash: mustHex("5AE9C4415816870AB1B14D7024D797C7E3DC3098CC3DDB01F11E5F309360112F")}}
	out.Example = gnolight.PrecommitSignBytes("gnoland-1", 701862, 0, bid, gnolight.Time{Secs: 1791632553, Nanos: 40788319})
	return out
}

func must2[A, B any](a A, b B, err error) (A, B) {
	if err != nil {
		log.Fatal(err)
	}
	return a, b
}

func mustHex(s string) []byte { return must(hex.DecodeString(s)) }

func headerOf(hd *gnolight.Header, parts gnolight.PartSetHeader, round int64) Header {
	leaves := hd.Leaves()
	hash := hd.Hash()
	e := Header{Height: hd.Height, Hash: hash[:], Time: hd.Time.Secs, ValidatorsHash: hd.ValidatorsHash,
		NextValidatorsHash: hd.NextValidatorsHash, LastResultsHash: hd.LastResultsHash, Announcing: hd.Announcing(),
		PartsTotal: parts.Total, PartsHash: parts.Hash, Round: round, Pubkeys: []hexb{}, Powers: []int64{},
		SetHash: hexb{}, ResultsRoot: hexb{}, ResultsProofs: []Proof{},
		Precommits: Precommits{Index: []int{}, Secs: []int64{}, Nanos: []int32{}, R: []hexb{}, S: []hexb{}, SignBytes: []hexb{}, Valid: []bool{}}}
	for _, l := range leaves {
		e.Leaves = append(e.Leaves, hexb(l))
	}
	if e.LastResultsHash == nil {
		e.LastResultsHash = hexb{}
	}
	return e
}

func addPrecommit(p *Precommits, i int, t gnolight.Time, sig, sb []byte, valid bool) {
	p.Index = append(p.Index, i)
	p.Secs = append(p.Secs, t.Secs)
	p.Nanos = append(p.Nanos, t.Nanos)
	p.R = append(p.R, sig[:32])
	p.S = append(p.S, sig[32:64])
	p.SignBytes = append(p.SignBytes, sb)
	p.Valid = append(p.Valid, valid)
}

// mutations are a valid signature's neighbours, each with Go's verdict: one bit of s, one bit of r, the prevote's
// bytes in place of the precommit's, another key.
func mutations(pk [32]byte, sb, sig []byte, label string) []Check {
	var out []Check
	add := func(key [32]byte, msg, s []byte, why string) {
		out = append(out, Check{Pubkey: key[:], SignBytes: msg, R: s[:32], S: s[32:64], Valid: ed25519.Verify(key[:], msg, s), Why: label + ": " + why})
	}
	add(pk, sb, sig, "the signature as signed")
	flipS := append([]byte(nil), sig...)
	flipS[40] ^= 0x01
	add(pk, sb, flipS, "one bit of s")
	flipR := append([]byte(nil), sig...)
	flipR[3] ^= 0x80
	add(pk, sb, flipR, "one bit of r")
	prevote := append([]byte(nil), sb...)
	prevote[2] = 0x01 // `08 02` → `08 01`: the prevote's type byte
	add(pk, prevote, sig, "the prevote's bytes")
	var other [32]byte
	copy(other[:], pk[:])
	other[0] ^= 0x01
	add(other, sb, sig, "another key")
	return out
}

func pubkeys(s *gnolight.ValidatorSet) []hexb {
	var out []hexb
	for _, v := range s.Validators {
		out = append(out, append([]byte(nil), v.PubKey[:]...))
	}
	return out
}

func powers(s *gnolight.ValidatorSet) []int64 {
	var out []int64
	for _, v := range s.Validators {
		out = append(out, v.Power)
	}
	return out
}

func aunts(p *gnolight.SimpleProof) []hexb {
	out := []hexb{}
	for _, a := range p.Aunts {
		out = append(out, append([]byte(nil), a[:]...))
	}
	return out
}

// --------------------------------------------------------------------------------------------- the synthetic --

type signer struct {
	priv ed25519.PrivateKey
	pub  [32]byte
}

type vset struct {
	name    string
	signers []signer // in the set's order (by address)
	set     *gnolight.ValidatorSet
	hash    [32]byte
}

func newSet(name string, pw []int64) *vset {
	type kp struct {
		s signer
		p int64
	}
	var all []kp
	for i, p := range pw {
		seed := sha256.Sum256([]byte("evmvectors/" + name + "/" + strconv.Itoa(i)))
		priv := ed25519.NewKeyFromSeed(seed[:])
		var pub [32]byte
		copy(pub[:], priv.Public().(ed25519.PublicKey))
		all = append(all, kp{signer{priv, pub}, p})
	}
	sort.Slice(all, func(i, j int) bool {
		a, b := gnolight.Validator{PubKey: all[i].s.pub}.Address(), gnolight.Validator{PubKey: all[j].s.pub}.Address()
		return string(a[:]) < string(b[:])
	})
	v := &vset{name: name, set: &gnolight.ValidatorSet{}}
	for _, x := range all {
		v.signers = append(v.signers, x.s)
		v.set.Validators = append(v.set.Validators, gnolight.Validator{PubKey: x.s.pub, Power: x.p})
	}
	if err := v.set.Check(); err != nil {
		log.Fatal(err)
	}
	v.hash, _ = v.set.Hash()
	return v
}

func (v *vset) export() Set {
	return Set{Name: v.name, Pubkeys: pubkeys(v.set), Powers: powers(v.set), Hash: v.hash[:]}
}

// quorum is the first slots in set order whose power crosses 2/3, plus `extra` more where there are.
func (v *vset) quorum(extra int) []int {
	total := v.set.TotalPower()
	var tallied int64
	var out []int
	for i, x := range v.set.Validators {
		if tallied*3 > total*2 {
			if extra == 0 {
				break
			}
			extra--
		}
		out = append(out, i)
		tallied += x.Power
	}
	return out
}

func sum32(parts ...string) []byte {
	h := sha256.New()
	for _, p := range parts {
		h.Write([]byte(p))
	}
	return h.Sum(nil)
}

func bytes32(addr []byte) [32]byte {
	var out [32]byte
	copy(out[32-len(addr):], addr)
	return out
}

func gnoAddr(path string) [32]byte { return must(hl.GnoAddressToBytes32(hl.GnoPackageAddress(path))) }

// createAddress is the address `sender` creates at `nonce`: keccak256(rlp([sender, nonce]))[12:].
func createAddress(sender []byte, nonce uint64) []byte {
	var n []byte
	switch {
	case nonce == 0:
		n = []byte{0x80}
	case nonce < 0x80:
		n = []byte{byte(nonce)}
	default:
		var b [8]byte
		binary.BigEndian.PutUint64(b[:], nonce)
		i := 0
		for i < 7 && b[i] == 0 {
			i++
		}
		n = append([]byte{0x80 + byte(8-i)}, b[i:]...)
	}
	body := append(append([]byte{0x94}, sender...), n...)
	rlp := append([]byte{0xc0 + byte(len(body))}, body...)
	h := hl.Keccak(rlp)
	return h[12:]
}

func ethAddress(name string) []byte {
	h := hl.Keccak([]byte(name))
	return h[12:]
}

// chainBuilder makes the test chain's headers in order.
type chainBuilder struct {
	headers []Header
	prev    [32]byte
	total   int64
	checks  []Check
}

type spec struct {
	height     int64
	set, next  *vset
	results    [32]byte // the LastResultsHash (zero: none)
	numTxs     int64
	round      int64
	partsTotal uint32
	extra      int // signers past the quorum
}

func (c *chainBuilder) header(s spec) (*gnolight.Header, Header) {
	c.total += s.numTxs
	hd := &gnolight.Header{
		Version:            "v1.0.0-rc.0",
		ChainID:            syntheticChain,
		Height:             s.height,
		Time:               gnolight.Time{Secs: 1_791_000_000 + 7*(s.height-100), Nanos: int32(100_000_000 + s.height)},
		NumTxs:             s.numTxs,
		TotalTxs:           c.total,
		LastBlockID:        gnolight.BlockID{Hash: c.prev[:], Parts: gnolight.PartSetHeader{Total: 1, Hash: sum32("parts", strconv.FormatInt(s.height-1, 10))}},
		LastCommitHash:     sum32("last commit", strconv.FormatInt(s.height, 10)),
		ValidatorsHash:     s.set.hash[:],
		NextValidatorsHash: s.next.hash[:],
		ConsensusHash:      sum32("consensus"),
		AppHash:            sum32("app", strconv.FormatInt(s.height, 10)),
		ProposerAddress:    hl.GnoPackageAddress("proposer/" + strconv.FormatInt(s.height, 10)),
	}
	if s.numTxs > 0 {
		hd.DataHash = sum32("data", strconv.FormatInt(s.height, 10))
	}
	if s.results != ([32]byte{}) {
		hd.LastResultsHash = append([]byte(nil), s.results[:]...)
	}
	if s.height == 100 {
		hd.LastBlockID = gnolight.BlockID{}
	}
	if err := hd.Check(); err != nil {
		log.Fatal(err)
	}
	parts := gnolight.PartSetHeader{Total: s.partsTotal, Hash: sum32("parts", strconv.FormatInt(s.height, 10))}
	e := headerOf(hd, parts, s.round)
	e.Set = s.set.name
	if hd.Announcing() {
		e.NextSet = s.next.name
	}
	c.prev = hd.Hash()
	c.sign(&e, hd, parts, s.set, s.set, s.quorum(), s.round)
	c.headers = append(c.headers, e)
	return hd, e
}

func (s spec) quorum() []int { return s.set.quorum(s.extra) }

// sign fills e's precommits: each slot of `slots` in the set `keysOf` signs the header's precommit at its own time;
// Go's verdict is taken under `set`'s key at that slot (a forger's signature is invalid there).
func (c *chainBuilder) sign(e *Header, hd *gnolight.Header, parts gnolight.PartSetHeader, set, keysOf *vset, slots []int, round int64) {
	bid := gnolight.BlockID{Hash: e.Hash, Parts: parts}
	for _, i := range slots {
		t := gnolight.Time{Secs: hd.Time.Secs + 1 + int64(i%3), Nanos: int32((i*7919 + int(hd.Height)) % 1_000_000_000)}
		if i%5 == 4 {
			t.Nanos = 0 // the time's nanoseconds omitted from the sign bytes
		}
		sb := gnolight.PrecommitSignBytes(syntheticChain, hd.Height, round, bid, t)
		sig := ed25519.Sign(keysOf.signers[i].priv, sb)
		pk := set.signers[i].pub
		ok := ed25519.Verify(pk[:], sb, sig)
		addPrecommit(&e.Precommits, i, t, sig, sb, ok)
		if i == slots[0] {
			c.checks = append(c.checks, mutations(pk, sb, sig, fmt.Sprintf("%s %d/%d", syntheticChain, hd.Height, i))...)
		}
	}
}

// message is one Hyperlane message from the test chain to Ethereum.
func message(nonce uint32, sender, recipient [32]byte, body []byte) []byte {
	return hl.Message{Version: hl.Version, Nonce: nonce, Origin: hl.DomainGnoTest, Sender: sender, Destination: localDomain, Recipient: recipient, Body: body}.Bytes()
}

func dispatchEvent(raw []byte) []byte {
	m := must(hl.ParseMessage(raw))
	d := gnolight.Dispatch{ID: hl.ID(raw), Nonce: m.Nonce, Origin: m.Origin, Destination: m.Destination, Sender: m.Sender, Recipient: m.Recipient, Hash: sha256.Sum256(raw)}
	return gnolight.Any(gnolight.EventTypeURL, gnolight.EncodeEvent(d.ToEvent(mailboxPath)))
}

// otherEvent is an event of another realm in the same transaction (a grc20 transfer), which the reader must walk.
func otherEvent(n int) []byte {
	e := &gnolight.Event{Type: "Transfer", PkgPath: "gno.land/r/demo/grc20", Attributes: []gnolight.Attribute{{Key: "from", Value: "g1from"}, {Key: "amount", Value: strconv.Itoa(n)}}}
	return gnolight.Any(gnolight.EventTypeURL, gnolight.EncodeEvent(e))
}

// abiMetadata is abi.encode(bytes32, uint32, uint32, bytes32[], bytes).
func abiMetadata(headerHash []byte, total, index int, aunts [][32]byte, leaf []byte) []byte {
	word := func(v uint64) []byte {
		var w [32]byte
		binary.BigEndian.PutUint64(w[24:], v)
		return w[:]
	}
	pad := func(b []byte) []byte {
		n := (len(b) + 31) / 32 * 32
		return append(append([]byte(nil), b...), make([]byte, n-len(b))...)
	}
	headLen := uint64(5 * 32)
	auntsOff := headLen
	auntsLen := uint64(32 + 32*len(aunts))
	leafOff := auntsOff + auntsLen
	var out []byte
	out = append(out, headerHash...)
	out = append(out, word(uint64(total))...)
	out = append(out, word(uint64(index))...)
	out = append(out, word(auntsOff)...)
	out = append(out, word(leafOff)...)
	out = append(out, word(uint64(len(aunts)))...)
	for _, a := range aunts {
		out = append(out, a[:]...)
	}
	out = append(out, word(uint64(len(leaf)))...)
	out = append(out, pad(leaf)...)
	return out
}

func synthetic() Synthetic {
	deployer := mustHex(deployerHex)
	token0 := createAddress(deployer, nonceToken0)
	token1 := createAddress(deployer, nonceToken1)
	admin := createAddress(deployer, nonceAdmin)
	route0, route1, hlgov := gnoAddr(gnotPath), gnoAddr(bubblePath), gnoAddr(hlgovPath)
	alice, bob, carol := ethAddress("alice"), ethAddress("bob"), ethAddress("carol")
	out := Synthetic{ChainID: syntheticChain, GnoDomain: hl.DomainGnoTest, LocalDomain: localDomain, MailboxPkgPath: mailboxPath,
		Deployer: deployer, Token0: token0, Token1: token1, Admin: admin, Route0: route0[:], Route1: route1[:], Hlgov: hlgov[:],
		Alice: alice, Bob: bob, Carol: carol}

	A := newSet("A", []int64{60, 60, 60, 60})
	B := newSet("B", []int64{60, 60, 60, 60, 60, 1})
	cp := make([]int64, 20)
	for i := range cp {
		cp[i] = 10
	}
	C := newSet("C", cp)
	dp := make([]int64, 60)
	for i := range dp {
		dp[i] = int64(1 + (i*37)%60)
	}
	D := newSet("D", dp)
	forgers := newSet("forgers", []int64{60, 60, 60, 60})
	for _, s := range []*vset{A, B, C, D} {
		out.Sets = append(out.Sets, s.export())
	}

	// the messages and the blocks that carry them
	type pending struct {
		name   string
		raw    []byte
		header int64 // the header whose LastResultsHash proves it
	}
	var msgs []pending
	tok := func(nonce uint32, route, token []byte, to []byte, amount uint64) []byte {
		return message(nonce, bytes32(route), bytes32(token), hl.TokenMessage(bytes32(to), amount, nil))
	}
	// block 101: a grc20 transaction, then one carrying four dispatches (GNOT to alice, BUBBLE to bob, a decision
	// of kind 12 to HlAdmin, GNOT to carol to be proven only after the set changed — refused then)
	m1 := tok(0, route0[:], token0, alice, 1_000_000)
	m2 := tok(1, route1[:], token1, bob, 2_500_000)
	decision := append([]byte{12}, binary.BigEndian.AppendUint64(nil, 1)...)
	decision = append(decision, 0)
	decision = binary.BigEndian.AppendUint64(decision, 50_000_000_000)
	decision = binary.BigEndian.AppendUint64(decision, 200_000_000_000)
	m3 := message(2, hlgov, bytes32(admin), decision)
	m4 := tok(3, route0[:], token0, carol, 7_000_000)
	block101 := [][]byte{
		gnolight.EncodeResult(nil, []byte("ok"), [][]byte{otherEvent(5)}),
		gnolight.EncodeResult(nil, nil, [][]byte{otherEvent(1), dispatchEvent(m1), dispatchEvent(m2), dispatchEvent(m3), dispatchEvent(m4)}),
	}
	msgs = append(msgs, pending{"gnot-alice", m1, 102}, pending{"bubble-bob", m2, 102}, pending{"decision-kind12", m3, 102}, pending{"gnot-carol-old-epoch", m4, 102})
	// block 105: GNOT to alice again, and 30,000 GNOT (past the 25,000 a message the gate allows)
	m5 := tok(4, route0[:], token0, alice, 3_000_000)
	m6 := tok(5, route0[:], token0, bob, 30_000_000_000)
	block105 := [][]byte{gnolight.EncodeResult(nil, nil, [][]byte{dispatchEvent(m5)}), gnolight.EncodeResult(nil, nil, [][]byte{dispatchEvent(m6)}), gnolight.EncodeResult(nil, []byte{1}, nil)}
	msgs = append(msgs, pending{"gnot-alice-2", m5, 106}, pending{"gnot-over-max", m6, 106})
	// block 109 (set D): BUBBLE to carol, in a block of one transaction
	m7 := tok(6, route1[:], token1, carol, 4_000_000)
	block109 := [][]byte{gnolight.EncodeResult(nil, nil, [][]byte{dispatchEvent(m7)})}
	msgs = append(msgs, pending{"bubble-carol", m7, 110})

	root := func(leaves [][]byte) [32]byte {
		r, _ := gnolight.SimpleHash(leaves)
		return r
	}
	blocks := map[int64][][]byte{101: block101, 105: block105, 109: block109}

	c := &chainBuilder{}
	c.header(spec{height: 100, set: A, next: A, partsTotal: 1})
	c.header(spec{height: 101, set: A, next: A, numTxs: 2, partsTotal: 1})
	c.header(spec{height: 102, set: A, next: A, results: root(block101), partsTotal: 2})           // a block of two parts
	c.header(spec{height: 103, set: A, next: B, partsTotal: 1, extra: 1})                          // announces B
	c.header(spec{height: 104, set: B, next: B, partsTotal: 1})                                    // B's first header
	c.header(spec{height: 105, set: B, next: B, numTxs: 3, partsTotal: 1})                         // carries block 105's txs
	c.header(spec{height: 106, set: B, next: B, results: root(block105), partsTotal: 1})           // proves them
	c.header(spec{height: 107, set: B, next: C, partsTotal: 1, round: 2})                          // announces C, round 2
	c.header(spec{height: 108, set: C, next: D, partsTotal: 1})                                    // C's first, announces D
	c.header(spec{height: 109, set: D, next: D, numTxs: 1, partsTotal: 1})                         // D's first
	c.header(spec{height: 110, set: D, next: D, results: root(block109), partsTotal: 1, extra: 3}) // proves block 109
	c.header(spec{height: 111, set: D, next: D, partsTotal: 1})
	out.Headers = c.headers

	// header 101 signed by the forgers' keys in the real set's slots: Go refuses every signature under the set's keys
	var h101 *gnolight.Header
	{
		cc := &chainBuilder{prev: [32]byte{}, total: 0}
		cc.prev = hashOf(c.headers[0])
		h101, out.Forged = cc.header(spec{height: 101, set: A, next: A, numTxs: 2, partsTotal: 1})
		out.Forged.Precommits = Precommits{Index: []int{}, Secs: []int64{}, Nanos: []int32{}, R: []hexb{}, S: []hexb{}, SignBytes: []hexb{}, Valid: []bool{}}
		cc.sign(&out.Forged, h101, gnolight.PartSetHeader{Total: 1, Hash: sum32("parts", "101")}, A, forgers, []int{0, 1, 2, 3}, 0)
		for _, v := range out.Forged.Precommits.Valid {
			if v {
				log.Fatal("a forged signature verifies")
			}
		}
	}
	out.Checks = c.checks

	// a nil precommit and a precommit for another block, by header 101's slot 0 at its own time
	{
		hd101 := c.headers[1]
		t := gnolight.Time{Secs: hd101.Time + 1, Nanos: 42}
		blockSB := gnolight.PrecommitSignBytes(syntheticChain, 101, 0, gnolight.BlockID{Hash: hd101.Hash, Parts: gnolight.PartSetHeader{Total: hd101.PartsTotal, Hash: hd101.PartsHash}}, t)
		other := gnolight.BlockID{Hash: sum32("another block"), Parts: gnolight.PartSetHeader{Total: 1, Hash: sum32("another parts")}}
		out.NotForTheBlock = Precommits{Index: []int{}, Secs: []int64{}, Nanos: []int32{}, R: []hexb{}, S: []hexb{}, SignBytes: []hexb{}, Valid: []bool{}}
		for _, bid := range []gnolight.BlockID{{}, other} {
			sb := gnolight.PrecommitSignBytes(syntheticChain, 101, 0, bid, t)
			sig := ed25519.Sign(A.signers[0].priv, sb)
			pk := A.signers[0].pub
			if !ed25519.Verify(pk[:], sb, sig) || ed25519.Verify(pk[:], blockSB, sig) {
				log.Fatal("the not-for-the-block signatures")
			}
			// the sign bytes the client builds (the header's BlockID) and Go's verdict on them: false
			addPrecommit(&out.NotForTheBlock, 0, t, sig, blockSB, false)
		}
	}

	for _, p := range msgs {
		leaves := blocks[p.header-1]
		idx := -1
		for i, l := range leaves {
			for _, d := range must(dispatchesOrNone(l)) {
				if d.ID == hl.ID(p.raw) {
					idx = i
				}
			}
		}
		if idx < 0 {
			log.Fatalf("message %s is in no leaf", p.name)
		}
		pr := must(gnolight.Proof(leaves, idx))
		var hh []byte
		for _, h := range c.headers {
			if h.Height == p.header {
				hh = h.Hash
			}
		}
		id := hl.ID(p.raw)
		m := must(hl.ParseMessage(p.raw))
		out.Messages = append(out.Messages, Message{Name: p.name, Bytes: p.raw, ID: id[:], Recipient: m.Recipient[:], Body: m.Body,
			Header: p.header, Index: idx, Total: len(leaves), Aunts: aunts(pr), Leaf: leaves[idx],
			Metadata: abiMetadata(hh, len(leaves), idx, pr.Aunts, leaves[idx])})
	}
	return out
}

func hashOf(h Header) [32]byte {
	var out [32]byte
	copy(out[:], h.Hash)
	return out
}

func dispatchesOrNone(leaf []byte) ([]gnolight.Dispatch, error) {
	ds, err := gnolight.DispatchEvents(leaf, mailboxPath)
	if err == gnolight.ErrNoDispatch {
		return nil, nil
	}
	return ds, err
}
