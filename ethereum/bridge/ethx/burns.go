package ethx

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"math/big"
	"sort"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"

	"github.com/gnolang/bridges/solana/bridge/hl"
)

// The burns toward gno.land, as every Ethereum log scanner of the bridge reads them (the signers, the monitors,
// hlwatch; the red team's ER5). Hyperlane's Mailbox.dispatch is open to anyone: a stranger can dispatch a message
// to gno.land naming our route realm as recipient for the price of a transaction. So a burn is counted, indexed and
// treated as one only when it is the token's own: a Dispatch log of our mailbox whose sender topic is one of our two
// tokens, whose destination topic is gno.land's domain and whose recipient topic is a realm of that token's line —
// its route realm, or, once a migration opened, any realm of the token's line of migrations (a kind 6 names the
// successor as the token's router, and every later burn is addressed to it; hlmailbox hands a message addressed to
// any realm of the line to the line's live successor, as the Solana relayer and signers follow the line) — its
// message checking out byte for byte (version 3, origin Ethereum's domain, the same sender, destination and
// recipient, a token message), numbered at or above the launch line (HlAdmin.launchNonce(), LaunchLine: a message
// below it was dispatched before the launch, the deployer's or a stranger's, and is none of the bridge's burns) — in a
// transaction that also carries that token's SentTransferRemote to gno.land of the same recipient and amount, emitted
// before it and matched to it once. Anything else is ignored with the reason, never counted: one cheap stranger's
// dispatch must never park a fake burn in flight and stop every signer. Both lists are read through a pair (both
// providers alike, each log's block hash canonical).

// BurnRoute is one token as the scan matches it: its HypERC20, its route realm's 32-byte address on gno.land, and
// the other realms of the token's line of migrations (its predecessors and successors, read from gno.land), each a
// recipient a burn of the token may name.
type BurnRoute struct {
	Symbol string
	Token  common.Address
	Realm  [32]byte
	Line   [][32]byte
}

// Takes says whether a recipient is a realm of the token's line: its route realm or another realm of its line.
func (r BurnRoute) Takes(k [32]byte) bool {
	if k == r.Realm {
		return true
	}
	for _, x := range r.Line {
		if x == k {
			return true
		}
	}
	return false
}

// Burn is one transfer toward gno.land as the mailbox and the token both record it.
type Burn struct {
	Symbol    string
	Token     common.Address
	Realm     [32]byte // the realm of the token's line it is addressed to (the route's, or a successor's)
	Message   []byte
	ID        [32]byte
	Nonce     uint32
	Recipient [32]byte // the gno.land recipient, as the token message carries it
	Amount    *big.Int
	Block     uint64
	BlockHash common.Hash
	Tx        common.Hash
	LogIndex  uint
}

// Ignored is a dispatch toward gno.land the scan saw and did not count, with why.
type Ignored struct {
	Tx       common.Hash
	LogIndex uint
	Why      string
}

// ScanBurns reads the burns of [from, to] (to at or below an agreed finalized block) through the pair, none numbered
// below the launch line.
func ScanBurns(ctx context.Context, p Pair, mailbox common.Address, origin, gnoDomain, line uint32, routes []BurnRoute, from, to uint64) ([]Burn, []Ignored, error) {
	if len(routes) == 0 || to < from {
		return nil, nil, nil
	}
	var tokens []common.Hash
	var tokenAddrs []common.Address
	byToken := map[common.Address]BurnRoute{}
	for _, r := range routes {
		tokens = append(tokens, AddressTopic(r.Token))
		tokenAddrs = append(tokenAddrs, r.Token)
		byToken[r.Token] = r
	}
	dispatches, err := p.Logs(ctx, LogQuery{Addresses: []common.Address{mailbox}, Topics: [][]common.Hash{{Mailbox.Event("Dispatch")}, tokens, {Uint32Topic(gnoDomain)}}}, from, to)
	if err != nil {
		return nil, nil, err
	}
	sents, err := p.Logs(ctx, LogQuery{Addresses: tokenAddrs, Topics: [][]common.Hash{{HypERC20.Event("SentTransferRemote")}, {Uint32Topic(gnoDomain)}}}, from, to)
	if err != nil {
		return nil, nil, err
	}
	return MatchBurns(mailbox, origin, gnoDomain, line, byToken, dispatches, sents)
}

// MatchBurns pairs the Dispatch logs with the tokens' SentTransferRemote logs (the scan's rule, pure); a message
// numbered below the launch line (line) is none of the bridge's burns.
func MatchBurns(mailbox common.Address, origin, gnoDomain, line uint32, byToken map[common.Address]BurnRoute, dispatches, sents []types.Log) ([]Burn, []Ignored, error) {
	sortLogs(dispatches)
	sortLogs(sents)
	used := make([]bool, len(sents))
	var out []Burn
	var ignored []Ignored
	for _, l := range dispatches {
		why, b := checkDispatch(l, mailbox, origin, gnoDomain, byToken)
		if why == "" && b.Nonce < line {
			why = fmt.Sprintf("message #%d is below the launch line %d (HlAdmin.launchNonce()): dispatched before the launch, none of the bridge's burns", b.Nonce, line)
		}
		if why != "" {
			ignored = append(ignored, Ignored{Tx: l.TxHash, LogIndex: l.Index, Why: why})
			continue
		}
		// the token's own SentTransferRemote in the same transaction, before the Dispatch, of the same recipient and
		// amount — the nearest unmatched one
		match := -1
		for i := len(sents) - 1; i >= 0; i-- {
			s := sents[i]
			if used[i] || s.TxHash != l.TxHash || s.BlockHash != l.BlockHash || s.Index >= l.Index || s.Address != b.Token {
				continue
			}
			if len(s.Topics) != 3 || s.Topics[0] != HypERC20.Event("SentTransferRemote") || s.Topics[1] != Uint32Topic(gnoDomain) || s.Topics[2] != common.Hash(b.Recipient) || len(s.Data) != 32 {
				continue
			}
			if new(big.Int).SetBytes(s.Data).Cmp(b.Amount) != 0 {
				continue
			}
			match = i
			break
		}
		if match < 0 {
			ignored = append(ignored, Ignored{Tx: l.TxHash, LogIndex: l.Index, Why: fmt.Sprintf("no SentTransferRemote of %s to %x for %s in the same transaction: not the token's own burn (a stranger's dispatch)", b.Symbol, b.Recipient[:], b.Amount)})
			continue
		}
		used[match] = true
		out = append(out, *b)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Nonce < out[j].Nonce })
	return out, ignored, nil
}

// checkDispatch reads one Dispatch log as a burn of ours, or says why it is not one.
func checkDispatch(l types.Log, mailbox common.Address, origin, gnoDomain uint32, byToken map[common.Address]BurnRoute) (string, *Burn) {
	if l.Address != mailbox || len(l.Topics) != 4 || l.Topics[0] != Mailbox.Event("Dispatch") || l.Removed {
		return "not our mailbox's Dispatch", nil
	}
	if !bytes.Equal(l.Topics[1][:12], make([]byte, 12)) {
		return "a sender topic that is no address", nil
	}
	token := common.BytesToAddress(l.Topics[1][12:])
	r, ok := byToken[token]
	if !ok {
		return "sent by " + token.Hex() + ", none of our tokens (a stranger's dispatch)", nil
	}
	if l.Topics[2] != Uint32Topic(gnoDomain) {
		return "to another domain", nil
	}
	if !r.Takes(l.Topics[3]) {
		return fmt.Sprintf("to %x, not a realm of %s's line (its route realm or a realm its migrations named)", l.Topics[3][:], r.Symbol), nil
	}
	vals, err := Mailbox.ABI.Events["Dispatch"].Inputs.NonIndexed().Unpack(l.Data)
	if err != nil || len(vals) != 1 {
		return "a Dispatch whose message does not decode", nil
	}
	raw := vals[0].([]byte)
	m, err := hl.ParseMessage(raw)
	if err != nil {
		return "a message shorter than its header", nil
	}
	switch {
	case m.Version != hl.Version:
		return fmt.Sprintf("message version %d", m.Version), nil
	case m.Origin != origin:
		return fmt.Sprintf("origin %d, not Ethereum's %d", m.Origin, origin), nil
	case m.Sender != AddressTo32(token):
		return "a message whose sender is not the log's", nil
	case m.Destination != gnoDomain:
		return "a message to another domain than the log's", nil
	case m.Recipient != [32]byte(l.Topics[3]):
		return "a message to another recipient than the log's", nil
	}
	if len(m.Body) < 64 {
		return "a body that is no token message", nil
	}
	var rcpt [32]byte
	copy(rcpt[:], m.Body[:32])
	amount := new(big.Int).SetBytes(m.Body[32:64])
	return "", &Burn{Symbol: r.Symbol, Token: token, Realm: m.Recipient, Message: raw, ID: hl.ID(raw), Nonce: binary.BigEndian.Uint32(raw[1:5]), Recipient: rcpt, Amount: amount,
		Block: l.BlockNumber, BlockHash: l.BlockHash, Tx: l.TxHash, LogIndex: l.Index}
}
