package ethx

import (
	"context"
	"errors"
	"math/big"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"

	"github.com/gnolang/bridges/solana/bridge/hl"
)

var (
	burnMailbox = common.HexToAddress("0x0000000000000000000000000000000000000b0b")
	tokGNOT     = common.HexToAddress("0x00000000000000000000000000000000000000a1")
	tokBUBBLE   = common.HexToAddress("0x00000000000000000000000000000000000000a2")
	realmGNOT   = [32]byte{31: 0x11}
	realmBUBBLE = [32]byte{31: 0x22}
)

const (
	ethDomain = uint32(31337)
	gnoDomain = hl.DomainGnoTest
)

func burnRoutes() map[common.Address]BurnRoute {
	return map[common.Address]BurnRoute{tokGNOT: {Symbol: "GNOT", Token: tokGNOT, Realm: realmGNOT}, tokBUBBLE: {Symbol: "BUBBLE", Token: tokBUBBLE, Realm: realmBUBBLE}}
}

// dispatchLog is the mailbox's Dispatch log of a message from a sender to a recipient on gno.land.
func dispatchLog(t *testing.T, sender common.Address, recipient [32]byte, nonce uint32, to [32]byte, amount uint64, tx byte, idx uint) types.Log {
	t.Helper()
	m := hl.Message{Version: 3, Nonce: nonce, Origin: ethDomain, Sender: AddressTo32(sender), Destination: gnoDomain, Recipient: recipient, Body: hl.TokenMessage(to, amount, nil)}
	data, err := Mailbox.ABI.Events["Dispatch"].Inputs.NonIndexed().Pack(m.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	return types.Log{Address: burnMailbox, Topics: []common.Hash{Mailbox.Event("Dispatch"), AddressTopic(sender), Uint32Topic(gnoDomain), common.Hash(recipient)}, Data: data,
		BlockNumber: 90, BlockHash: hashOf(1, 90), TxHash: common.Hash{tx}, Index: idx}
}

func sentLog(token common.Address, to [32]byte, amount uint64, tx byte, idx uint) types.Log {
	return types.Log{Address: token, Topics: []common.Hash{HypERC20.Event("SentTransferRemote"), Uint32Topic(gnoDomain), common.Hash(to)}, Data: common.BigToHash(new(big.Int).SetUint64(amount)).Bytes(),
		BlockNumber: 90, BlockHash: hashOf(1, 90), TxHash: common.Hash{tx}, Index: idx}
}

// A burn is counted only as the token's own: its Dispatch from one of our tokens to its route realm, with the
// token's SentTransferRemote of the same recipient and amount before it in the same transaction (ER5). A stranger's
// dispatch — any sender, a wrong recipient, no SentTransferRemote, another amount — is ignored with why.
func TestMatchBurns(t *testing.T) {
	alice := [32]byte{12: 0xaa, 31: 0x01}
	stranger := common.HexToAddress("0x00000000000000000000000000000000000000ff")
	dispatches := []types.Log{
		dispatchLog(t, tokGNOT, realmGNOT, 0, alice, 500, 1, 3),        // a real burn: sent at index 2 in tx 1
		dispatchLog(t, stranger, realmGNOT, 1, alice, 9_000_000, 2, 1), // a stranger's dispatch to our realm
		dispatchLog(t, tokBUBBLE, realmGNOT, 2, alice, 5, 3, 1),        // BUBBLE's token to GNOT's realm
		dispatchLog(t, tokGNOT, realmGNOT, 3, alice, 700, 4, 1),        // no SentTransferRemote in its transaction
		dispatchLog(t, tokBUBBLE, realmBUBBLE, 4, alice, 800, 5, 1),    // a SentTransferRemote of another amount
		dispatchLog(t, tokBUBBLE, realmBUBBLE, 5, alice, 900, 6, 1),    // its SentTransferRemote after it
		dispatchLog(t, tokBUBBLE, realmBUBBLE, 6, alice, 1_000, 7, 5),  // two burns in one transaction,
		dispatchLog(t, tokBUBBLE, realmBUBBLE, 7, alice, 1_000, 7, 9),  // each matched once
		dispatchLog(t, tokBUBBLE, realmBUBBLE, 8, alice, 1_000, 7, 12), // and a third with no sent left
		dispatchLog(t, tokGNOT, realmGNOT, 9, [32]byte{1}, 50, 8, 2),   // the same token sent to another recipient
		dispatchLog(t, stranger, realmBUBBLE, 10, alice, 1_000, 9, 2),  // a stranger in a transaction where a token sent
	}
	sents := []types.Log{
		sentLog(tokGNOT, alice, 500, 1, 2),
		sentLog(tokBUBBLE, alice, 801, 5, 0),
		sentLog(tokBUBBLE, alice, 900, 6, 2),
		sentLog(tokBUBBLE, alice, 1_000, 7, 4),
		sentLog(tokBUBBLE, alice, 1_000, 7, 8),
		sentLog(tokGNOT, alice, 50, 8, 1),
		sentLog(tokBUBBLE, alice, 1_000, 9, 1),
	}
	burns, ignored, err := MatchBurns(burnMailbox, ethDomain, gnoDomain, 0, burnRoutes(), dispatches, sents)
	if err != nil {
		t.Fatal(err)
	}
	var nonces []uint32
	for _, b := range burns {
		nonces = append(nonces, b.Nonce)
	}
	if len(burns) != 3 || nonces[0] != 0 || nonces[1] != 6 || nonces[2] != 7 {
		t.Fatalf("burns %v", nonces)
	}
	if burns[0].Symbol != "GNOT" || burns[0].Amount.Uint64() != 500 || burns[0].Recipient != alice || burns[0].ID != hl.ID(burns[0].Message) {
		t.Fatalf("%+v", burns[0])
	}
	if len(ignored) != 8 {
		t.Fatalf("%d ignored: %+v", len(ignored), ignored)
	}
	why := map[byte]string{}
	for _, ig := range ignored {
		why[ig.Tx[0]] += ig.Why + "|"
	}
	for tx, want := range map[byte]string{2: "none of our tokens", 3: "route realm", 4: "no SentTransferRemote", 5: "no SentTransferRemote", 6: "no SentTransferRemote", 7: "no SentTransferRemote", 8: "no SentTransferRemote", 9: "none of our tokens"} {
		if !strings.Contains(why[tx], want) {
			t.Errorf("tx %d: %q, want %q", tx, why[tx], want)
		}
	}
}

// The scan reads both lists through the pair: a provider that omits a SentTransferRemote is a disagreement, never
// a burn counted on one provider's word.
func TestScanBurnsThroughThePair(t *testing.T) {
	alice := [32]byte{12: 0xaa, 31: 0x01}
	d := dispatchLog(t, tokGNOT, realmGNOT, 0, alice, 500, 1, 3)
	s := sentLog(tokGNOT, alice, 500, 1, 2)
	both := func(logs ...types.Log) *provider {
		return &provider{finalized: 100, seed: 1, logs: logs}
	}
	// the fake answers every eth_getLogs with its list whatever the filter: the scan's own match sorts them
	burns, _, err := ScanBurns(context.Background(), pairOf(t, both(d, s), both(d, s)), burnMailbox, ethDomain, gnoDomain, 0,
		[]BurnRoute{{Symbol: "GNOT", Token: tokGNOT, Realm: realmGNOT}}, 80, 100)
	if err != nil || len(burns) != 1 {
		t.Fatalf("%v %v", burns, err)
	}
	if _, _, err := ScanBurns(context.Background(), pairOf(t, both(d, s), both(d)), burnMailbox, ethDomain, gnoDomain, 0,
		[]BurnRoute{{Symbol: "GNOT", Token: tokGNOT, Realm: realmGNOT}}, 80, 100); !errors.Is(err, ErrDisagree) {
		t.Fatalf("one provider without the SentTransferRemote: %v", err)
	}
}

// The client's and HlAdmin's events decode, the indexed int64 height included.
func TestDecodeEvents(t *testing.T) {
	ev := LightClient.ABI.Events["HeaderRecorded"]
	data, err := ev.Inputs.NonIndexed().Pack(uint64(3), uint64(41), [32]byte{9}, true)
	if err != nil {
		t.Fatal(err)
	}
	l := types.Log{Topics: []common.Hash{ev.ID, {1}, common.BigToHash(big.NewInt(741_104))}, Data: data, BlockNumber: 77, TxHash: common.Hash{5}}
	h, err := DecodeHeaderRecorded(l)
	if err != nil || h.Height != 741_104 || h.Epoch != 3 || h.Record != 41 || !h.Proven || h.Hash != [32]byte{1} || h.Block != 77 {
		t.Fatalf("%+v %v", h, err)
	}
	sp := LightClient.ABI.Events["StageProgress"]
	data, _ = sp.Inputs.NonIndexed().Pack([32]byte{7}, int64(741_105), int64(120), int64(360))
	who := common.HexToAddress("0x00000000000000000000000000000000000000c3")
	s, err := DecodeStageProgress(types.Log{Topics: []common.Hash{sp.ID, {8}, AddressTopic(who)}, Data: data})
	if err != nil || s.Submitter != who || s.HeaderHash != [32]byte{7} || s.Height != 741_105 || s.Tallied != 120 || s.Total != 360 || s.Key != [32]byte{8} {
		t.Fatalf("%+v %v", s, err)
	}
	dr := Admin.ABI.Events["DecisionRecorded"]
	data, _ = dr.Inputs.NonIndexed().Pack(uint8(2), uint64(1_760_086_400))
	d, err := DecodeDecisionRecorded(types.Log{Topics: []common.Hash{dr.ID, common.BigToHash(big.NewInt(12))}, Data: data})
	if err != nil || d.Number != 12 || d.Kind != 2 || d.MatureAt != 1_760_086_400 {
		t.Fatalf("%+v %v", d, err)
	}
	if _, err := DecodeHeaderRecorded(types.Log{Topics: []common.Hash{sp.ID, {}, {}}}); err == nil {
		t.Fatal("another event decoded as HeaderRecorded")
	}
}

// A call with its own cap (the emergency cap of a decision's delivery, ER6) goes where the sender's cap parks.
func TestSenderEmergencyCap(t *testing.T) {
	s, c, _ := testSender(t, SendConfig{})
	c.autoMine = true
	c.baseFee = GweiOf(30)
	var ft *FeeTooHigh
	if _, err := s.Send(context.Background(), Call{To: target, Data: []byte{1}, Label: "a route delivery"}); !errors.As(err, &ft) {
		t.Fatalf("%v", err)
	}
	if _, err := s.Send(context.Background(), Call{To: target, Data: []byte{1}, Label: "a cancel", MaxBaseFee: GweiOf(200)}); err != nil {
		t.Fatal(err)
	}
	if got := c.sent[0].GasFeeCap(); got.Cmp(GweiOf(60)) <= 0 {
		t.Fatalf("fee cap %s", got)
	}
}
