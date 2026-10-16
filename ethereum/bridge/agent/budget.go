package agent

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
	"math/big"
	"sort"
	"strconv"
	"time"

	"github.com/ethereum/go-ethereum/crypto"

	"github.com/gnolang/bridges/solana/bridge/gnolight"
)

// The fee-funded delivery budget (eth/bridge/PLAN.md §5.5, §5.7, §6.1; the plan red team's E2-RT2, E2-RT4, E2-RT5).
// The operator's spending on route traffic toward Ethereum is funded by the routes' fees: a header is bought for
// waiting sends only while the fees of the sends it serves — each its own, as its SentTransferRemote carried it —
// cover the WHOLE cost it commits: the header's and every delivery's, each delivery priced by its results leaf. Both
// are priced by E-M2b's measured model at the live base fee plus the tip (never by a stranger's receipt, whose gas
// a stranger chooses: E2-RT4). What the fees cannot cover waits; a waiting send six hours old buys its own header
// within the budget — the surplus earlier sends' fees left over their costs — whatever anyone else's updates did; a
// send waiting seven hours alarms. Decisions, the headers they need, the announcing header, HlAdmin.execute and the
// flags are outside the budget (the emergency cap): GovDAO never waits for fees.

const (
	// SendAgeHeader is a waiting send's age at which it buys its own header within the budget (§5.5, §6.1): the
	// heartbeat is the oldest waiting send's age, never the client's freshness, which anyone's update resets.
	SendAgeHeader = 6 * time.Hour
	// DeliveryAgeAfter is a waiting send's age that alarms (§6.1: delivery:age, whoever's updates kept the client
	// fresh).
	DeliveryAgeAfter = 7 * time.Hour
)

// The model (E-M2b's measurements, M2-NOTES.md §6: Osaka, every account and slot cold, whole transactions). An update
// in one transaction measured 966,125 gas at 4 validators (3 signatures), 1,445,334 at 6 (5), 3,646,855 at 20 (14),
// 4,868,663 at 28 (19), 6,822,321 at 40 (27), 10,236,808 at 60 (41), 16,595,220 at 100 (67): ≈220,950 a signature,
// ≈15,500 a validator of the set the call carries and hashes, and at most ≈247,700 besides — the model's intercept
// sits above every measurement, so it never prices an update under its cost. A delivery (Mailbox.process through the
// gate and the client, minting to a fresh holder) measured 311,246 gas from a leaf of one Dispatch (451 bytes); the
// client reads every Dispatch of the mailbox in the leaf whole, ≈93,800 gas each (419,534 to verify a leaf of 4,
// 1,545,394 of 16, against 138,909 of 1), and each carries ≈450 bytes of calldata (≈7,200 gas) besides. The leaf's
// other bytes are paid too, whatever they hold (E3-GS2): verifying 8,924 bytes — one Dispatch beside 8 KB of another
// realm's events — measured 284,069 gas against 138,909, ≈17.1 a byte, and each byte is calldata (16 when not zero):
// 34 a byte in all, so the model stays above the measurement.
const (
	headerGasBase       = 250_000
	headerGasValidator  = 15_600
	headerGasSignature  = 221_000
	deliveryGasOne      = 311_300
	deliveryGasDispatch = 101_000
	// deliveryGasByte prices a leaf's bytes past its Dispatch events' own (≈17.1 to verify, 16 of calldata)
	deliveryGasByte = 34
	// dispatchLeafBytes is a Dispatch event's share of a leaf (E-M2b's leaf of one: 451 bytes), already in the two
	// figures above
	dispatchLeafBytes = 451
	// txGasCap is EIP-7825's bound (§5.7): a call's estimate plus 20% fits it, or the update is staged
	txGasCap = 16_777_216
)

// headerGas is the model's gas for an update signed by k of a set of n validators: one transaction while its gas
// plus 20% fits the cap, else staged — each call paying the set's share again.
func headerGas(n, k int) uint64 {
	set := uint64(headerGasBase + headerGasValidator*n)
	perCall := (uint64(txGasCap)*10/12 - set) / headerGasSignature
	if perCall < 1 {
		perCall = 1
	}
	calls := (uint64(k) + perCall - 1) / perCall
	if calls < 1 {
		calls = 1
	}
	return calls*set + headerGasSignature*uint64(k)
}

// headerGasOfSet is the model's gas for an update signed by a set: the fewest of its validators, largest powers first,
// whose power crosses 2/3 (what the updater's choice of precommits carries).
func headerGasOfSet(set *gnolight.ValidatorSet) uint64 {
	if set == nil || len(set.Validators) == 0 {
		return headerGas(6, 5) // gnoland-1's today
	}
	powers := make([]int64, len(set.Validators))
	for i, v := range set.Validators {
		powers[i] = v.Power
	}
	sort.Slice(powers, func(i, j int) bool { return powers[i] > powers[j] })
	total := set.TotalPower()
	var tally int64
	k := 0
	for _, p := range powers {
		tally += p
		k++
		if gnolight.CommitRule(tally, total) {
			break
		}
	}
	return headerGas(len(powers), k)
}

// deliveryGas is the model's gas for one delivery proven from a leaf of the given bytes carrying n Dispatch events:
// every Dispatch, and every byte past theirs (a stranger's events beside a Reemit: E3-GS2); bytes 0 is a leaf of the
// Dispatch events alone (one the scan has not measured).
func deliveryGas(n, bytes int) uint64 {
	if n < 1 {
		n = 1
	}
	g := deliveryGasOne + deliveryGasDispatch*uint64(n-1)
	if extra := bytes - dispatchLeafBytes*n; extra > 0 {
		g += deliveryGasByte * uint64(extra)
	}
	return g
}

// weiOf is gas at a price per gas.
func weiOf(gas uint64, price *big.Int) *big.Int {
	return new(big.Int).Mul(new(big.Int).SetUint64(gas), price)
}

// ------------------------------------------------------------------------------------------------ the ledger --

// budget is the updater's ledger of the route traffic it commits the operator's ETH to, kept in its state file: every
// header it buys for route sends books them — each send's own fee credited, each delivery's model cost debited at the
// price when it was bought (the relayer then delivers them), each send once whatever headers name it later — and the
// header's own cost from its receipts. The sends a header books are named on the chain with it: the trailer of the
// transaction that records it (bookingTrailer), the (nonce, header) pairs both machines read back, so the relayer
// delivers at no fee check exactly what the ledger booked (E4-GS2, E4-GS4, E4-CF2). A header bought for a decision or
// a validator change books no send (a route send rides it only by its own fee) and its cost is outside the budget.
// The balance is the surplus the fees left over the costs; a waiting send six hours old draws on it. A DEFICIT — an
// update that cost more than the model said, deliveries made at a base fee that rose after the header was bought —
// carries as a negative balance: sends whose own fees cover their own whole cost still go (their surplus repays it
// first), and no waiting send draws on the budget until the balance covers what its fees lack. A lost state file
// starts the ledger at zero: nothing to draw on, never a debt forgotten in the float's favour.
type budget struct {
	Balance  string          `json:"balance"`  // wei, signed: credited less debited
	Credited string          `json:"credited"` // every fee booked, wei
	Debited  string          `json:"debited"`  // every cost booked, wei
	Booked   map[string]bool `json:"booked"`   // nonces whose fee and delivery are booked (each once)
	Entries  []budgetEntry   `json:"entries"`  // the last bookings
	// Pending are the bookings of headers sent and not yet seen recorded (E3-GS5): made, and the state file saved,
	// before an update is sent, so an update that outlives the sender's wait, or a stop before the step's save, is
	// booked once the chain shows it — never delivered from unbooked. While one stands its sends count as booked and
	// its net draw (the header's model cost and the deliveries, less the fees) is held back from every other draw.
	Pending []*pendingBooking `json:"pending,omitempty"`
}

// pendingBooking is a header's booking awaiting the chain's word.
type pendingBooking struct {
	Header int64  `json:"header"`
	Hash   string `json:"hash"` // the header's hash, hex
	Prove  bool   `json:"prove,omitempty"`
	Epoch  uint64 `json:"epoch"`  // the client's epoch when it was sent
	Why    string `json:"why"`    // the booking's kind: bought by its sends' fees, a six-hour draw
	Reason string `json:"reason"` // the due rule's words
	// Nonces are the route sends the header's transactions name in their trailer (every route send it serves)
	Nonces []uint32 `json:"nonces"`
	// Sends are the amounts of each send not booked yet when the header was planned (a send booked once is never
	// credited or debited again), kept from every plan of this header (a plan made again while an earlier one's
	// transaction was in flight: the chain books whichever trailer recorded it); Fees and Deliveries are their sums
	Sends      map[string]sendAmounts `json:"sends,omitempty"`
	Fees       string                 `json:"fees"`
	Deliveries string                 `json:"deliveries"`
	HeaderCost string                 `json:"headerCost"` // the model's cost, wei ("" outside the budget)
	At         time.Time              `json:"at"`
	// Tx is this key's transaction whose receipt carries the header's HeaderRecorded log, Block its block (E5-GS2: the
	// booking settles from them, never only from a log read); IdleAt the first moment this key was seen with nothing in
	// flight since (E5-GS3: a header not recorded at a finalized block whose time is past it never will be by this key)
	Tx     string    `json:"tx,omitempty"`
	Block  uint64    `json:"block,omitempty"`
	IdleAt time.Time `json:"idleAt,omitempty"`
}

// sendAmounts is one send's fee and its delivery's model cost, wei.
type sendAmounts struct {
	Fee      string `json:"fee"`
	Delivery string `json:"delivery"`
}

// budgetEntry is one header's booking.
type budgetEntry struct {
	At         time.Time `json:"at"`
	Header     int64     `json:"header"`
	Why        string    `json:"why"`
	Nonces     []uint32  `json:"nonces"`
	Fees       string    `json:"fees"`
	Deliveries string    `json:"deliveries"`
	HeaderCost string    `json:"headerCost"` // "" when the header is outside the budget
	Balance    string    `json:"balance"`
}

const budgetEntriesKept = 32

func bigOf(s string) *big.Int {
	n, ok := new(big.Int).SetString(s, 10)
	if !ok {
		return new(big.Int)
	}
	return n
}

func (b *budget) init() {
	if b.Booked == nil {
		b.Booked = map[string]bool{}
	}
	for _, p := range []*string{&b.Balance, &b.Credited, &b.Debited} {
		if *p == "" {
			*p = "0"
		}
	}
}

// balance is the ledger's balance, wei (negative: a deficit carried).
func (b *budget) balance() *big.Int { return bigOf(b.Balance) }

// available is what a six-hour send may draw on: the balance less what the pending bookings will debit past their
// fees (their headers at the model's cost until their receipts are read), so one surplus never pays two headers.
func (b *budget) available() *big.Int {
	out := b.balance()
	for _, p := range b.Pending {
		net := new(big.Int).Add(bigOf(p.Deliveries), bigOf(p.HeaderCost))
		net.Sub(net, bigOf(p.Fees))
		if net.Sign() > 0 {
			out.Sub(out, net)
		}
	}
	return out
}

// booked says whether a send's fee and delivery are in the ledger already, or in a pending booking.
func (b *budget) booked(n uint32) bool {
	if b.settled(n) {
		return true
	}
	for _, p := range b.Pending {
		for _, x := range p.Nonces {
			if x == n {
				return true
			}
		}
	}
	return false
}

// settled says whether a send's fee and delivery are in the ledger (booked once, never again).
func (b *budget) settled(n uint32) bool { return b.Booked[strconv.FormatUint(uint64(n), 10)] }

// pend keeps a header's pending booking in place of an earlier one for the same header, the earlier one's amounts of
// sends this one does not name kept (its transaction may be the one the chain records).
func (b *budget) pend(p *pendingBooking) {
	if old := b.pendingAt(p.Header); old != nil {
		if p.Tx == "" {
			p.Tx, p.Block = old.Tx, old.Block // its recording transaction, should an earlier one have recorded it
		}
		for nk, a := range old.Sends {
			if _, ok := p.Sends[nk]; !ok {
				if p.Sends == nil {
					p.Sends = map[string]sendAmounts{}
				}
				p.Sends[nk] = a
			}
		}
	}
	if len(p.Sends) > 0 {
		fees, dels := new(big.Int), new(big.Int)
		for _, a := range p.Sends {
			fees.Add(fees, bigOf(a.Fee))
			dels.Add(dels, bigOf(a.Delivery))
		}
		p.Fees, p.Deliveries = fees.String(), dels.String()
	}
	b.drop(p.Header)
	b.Pending = append(b.Pending, p)
}

// amountsOf are the fees and the deliveries a recorded header's booking enters: those of the sends its trailer names
// that the ledger has not booked yet (each send once), at the amounts its pending booking kept (a booking made before
// the amounts were kept by send enters its sums).
func (b *budget) amountsOf(p *pendingBooking, trailer []uint32) (fees, deliveries *big.Int) {
	if len(p.Sends) == 0 {
		return bigOf(p.Fees), bigOf(p.Deliveries)
	}
	fees, deliveries = new(big.Int), new(big.Int)
	seen := map[uint32]bool{}
	for _, n := range trailer {
		if seen[n] || b.settled(n) {
			continue
		}
		seen[n] = true
		if a, ok := p.Sends[strconv.FormatUint(uint64(n), 10)]; ok {
			fees.Add(fees, bigOf(a.Fee))
			deliveries.Add(deliveries, bigOf(a.Delivery))
		}
	}
	return fees, deliveries
}

// drop forgets a header's pending booking.
func (b *budget) drop(header int64) {
	var keep []*pendingBooking
	for _, p := range b.Pending {
		if p.Header != header {
			keep = append(keep, p)
		}
	}
	b.Pending = keep
}

// pendingAt is a header's pending booking, nil for none.
func (b *budget) pendingAt(header int64) *pendingBooking {
	for _, p := range b.Pending {
		if p.Header == header {
			return p
		}
	}
	return nil
}

// book enters one header: fees credited, deliveries and (when inside the budget) the header's cost debited.
func (b *budget) book(at time.Time, header int64, why string, nonces []uint32, fees, deliveries, headerCost *big.Int) {
	b.init()
	if fees == nil {
		fees = new(big.Int)
	}
	if deliveries == nil {
		deliveries = new(big.Int)
	}
	debit := new(big.Int).Set(deliveries)
	hc := ""
	if headerCost != nil {
		debit.Add(debit, headerCost)
		hc = headerCost.String()
	}
	bal := new(big.Int).Add(b.balance(), fees)
	bal.Sub(bal, debit)
	b.Balance = bal.String()
	b.Credited = new(big.Int).Add(bigOf(b.Credited), fees).String()
	b.Debited = new(big.Int).Add(bigOf(b.Debited), debit).String()
	for _, n := range nonces {
		b.Booked[strconv.FormatUint(uint64(n), 10)] = true
	}
	b.Entries = append(b.Entries, budgetEntry{At: at.UTC(), Header: header, Why: why, Nonces: nonces, Fees: fees.String(), Deliveries: deliveries.String(), HeaderCost: hc, Balance: b.Balance})
	if len(b.Entries) > budgetEntriesKept {
		b.Entries = b.Entries[len(b.Entries)-budgetEntriesKept:]
	}
}

// prune forgets the booked marks of the nonces the walk ended (below next).
func (b *budget) prune(next uint32) {
	for k := range b.Booked {
		n, err := strconv.ParseUint(k, 10, 32)
		if err != nil || uint32(n) < next {
			delete(b.Booked, k)
		}
	}
}

// text is the ledger in one line.
func (b *budget) text() string {
	b.init()
	s := fmt.Sprintf("balance %s ETH (credited %s, debited %s; %d bookings kept)", eth(b.balance()), eth(bigOf(b.Credited)), eth(bigOf(b.Debited)), len(b.Entries))
	if len(b.Pending) > 0 {
		s += fmt.Sprintf("; %d header(s) sent and not yet seen recorded, %s ETH available to draw", len(b.Pending), eth(b.available()))
	}
	return s
}

// ------------------------------------------------------------------------------------------- the trailer --

// The (nonce, header) pairs (E4-GS2, E4-GS4, E4-CF2): the updater ends every call of a header it buys for route sends
// with a trailer naming them — the nonces as 32-byte words, their count, then bookingMark — and the relayer delivers at
// no fee check a send only from a header whose recording transaction is eth.updater's and names it there, never from
// "the header after its newest event": a header bought for a decision, or for another group, or a stranger's, books
// nothing for it, and a stranger's Reemit after the header moves nothing the trailer named. The light client never
// reads the bytes past its arguments (Solidity decodes calldata by its offsets, and the proxy forwards the calldata
// whole: ERC-2771's appended sender rests on the same), so the trailer costs its calldata alone (≈200 gas a send) and
// is read back only from the chain — the transaction both machines find by the header's HeaderRecorded log.
var bookingMark = crypto.Keccak256Hash([]byte("github.com/gnolang/bridges/ethereum/bridge: the route sends the updater booked under this header, v1"))

// maxTrailerSends bounds the nonces a trailer names (a group's route sends; a word each).
const maxTrailerSends = 4096

// bookingTrailer is the trailer naming nonces (nil for none).
func bookingTrailer(nonces []uint32) []byte {
	if len(nonces) == 0 {
		return nil
	}
	out := make([]byte, 0, 32*(len(nonces)+2))
	word := func(v uint64) {
		var w [32]byte
		binary.BigEndian.PutUint64(w[24:], v)
		out = append(out, w[:]...)
	}
	for _, n := range nonces {
		word(uint64(n))
	}
	word(uint64(len(nonces)))
	return append(out, bookingMark[:]...)
}

// trailerNonces reads a trailer off the end of a transaction's calldata: the nonces it names, and whether it carries
// one at all.
func trailerNonces(input []byte) ([]uint32, bool) {
	if len(input) < 4+64 || !bytes.Equal(input[len(input)-32:], bookingMark[:]) {
		return nil, false
	}
	small := func(w []byte) (uint64, bool) {
		for _, b := range w[:24] {
			if b != 0 {
				return 0, false
			}
		}
		return binary.BigEndian.Uint64(w[24:]), true
	}
	end := len(input) - 32
	count, ok := small(input[end-32 : end])
	if !ok || count == 0 || count > maxTrailerSends || uint64(end-32-4) < 32*count {
		return nil, false
	}
	start := end - 32 - 32*int(count)
	out := make([]uint32, 0, count)
	for i := 0; i < int(count); i++ {
		v, ok := small(input[start+32*i : start+32*i+32])
		if !ok || v > math.MaxUint32 {
			return nil, false
		}
		out = append(out, uint32(v))
	}
	return out, true
}
