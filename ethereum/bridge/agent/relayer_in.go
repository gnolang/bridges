package agent

import (
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"math/big"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"

	"github.com/gnolang/bridges/solana/bridge/gno"
	"github.com/gnolang/bridges/solana/bridge/hl"
	"github.com/gnolang/bridges/ethereum/bridge/ethx"
	"github.com/gnolang/bridges/ethereum/bridge/gnoside"
)

// The relayer's Ethereum → gno.land half (eth/bridge/PLAN.md §5.7: the Solana plan's `Process` and `Claim(20)` loops
// unchanged): a job in the relayer's tick (relayer_in_job.go registers it) that scans our mailbox's burns toward
// gno.land at or below the `finalized` block through the relayer's one provider (it decides nothing a lying provider
// could cash: hlmailbox checks the signatures, the signers checked the burn through two providers each), gathers
// the signers' signatures from their public endpoints (each checked against the key hlmailbox holds for that signer,
// cached on disk), and calls the route realm's `Process` with exactly the signatures hlmailbox's rule needs — the
// lowest-indexed valid ones up to the threshold, or one of each group — under this bridge's checkpoint (our
// MerkleTreeHook, root 0, index the nonce, hl.GnoMetadata); and it calls `Claim(20)` on every route continuously
// while a claim waits and the meter holds the fee, backing off while a call pays nothing. Parking, the backoff and
// the hourly retries are the relayer's attempt bookkeeping (TryCall); the primary and the backup are its Act. The
// realms' queued decisions (`Execute(id)`), the lapsed proposals and the re-emits of gno.land → Ethereum dispatches are
// the relayer's own loops (E-M3: executor.go, relayer_out.go), in the same binary.

const (
	// signerBodyLimit is the most a signer's answer may be (§5.7: a body limit of 1 MB).
	signerBodyLimit = 1 << 20
	// inboundScanMax bounds the blocks one tick scans for burns.
	inboundScanMax = 5_000
)

// inboundState is the job's state, kept in the relayer's state file under the job's name.
type inboundState struct {
	From    uint64                       `json:"from"`    // the first block scanned (the deployment's); 0 until known
	Cursor  uint64                       `json:"cursor"`  // the last finalized block scanned
	Pending []inboundBurn                `json:"pending"` // burns toward gno.land not yet delivered, in nonce order
	Sigs    map[string]map[string]string `json:"sigs"`    // message id → signer key → signature (cached on disk)
	Claims  map[string]*inClaim          `json:"claims"`  // by route symbol
	Ignored int                          `json:"ignored"` // dispatches toward gno.land not counted as burns, ever
	Line    uint32                       `json:"line"`    // the launch line the last scan read (HlAdmin.launchNonce())
	// Dust are the burns below their route's fee this relayer left unprocessed, by symbol (E5-GS5): the route would sweep
	// one with no fee, and a credit locks ≈0.9–1.7 GNOT of the gno key's storage deposit — anyone may Process one at
	// their own cost (the signers sign it as they sign any burn)
	Dust map[string]*dustRecord `json:"dust,omitempty"`
	// Left are the pending burns left as dust, by message id: the fee they were left under, alarmed once. Each stays
	// pending and is judged against its realm's fee again every tick, before any read of its own, so a fee a decision
	// lowers past it makes it a delivery again (the integration lane I3; the Solana relayer's 1aea555f): forgotten, it
	// was a message the monitors count waiting once the fee fell (their age alarm for good) that no relayer would bring
	Left map[string]int64 `json:"left,omitempty"`
	// LeftAt is hlmailbox's delivered count when the left burns' marks were last read (-1: read them next tick): one
	// someone else Processes at his own cost is dropped once a credit moves the count (a mark is set only with its rise;
	// the review of I3's follow-up 1, finding 4)
	LeftAt int `json:"leftAt,omitempty"`
}

// maxLeft is how many dust burns the relayer keeps pending to judge again (E5-GS5): past it the oldest is forgotten, so
// a stranger's pile does not grow the state file without end.
const maxLeft = 1000

// dustRecord is a route's burns below its fee left unprocessed: how many, and the newest.
type dustRecord struct {
	Count  int       `json:"count"`
	Nonce  uint32    `json:"nonce"`
	ID     string    `json:"id"`
	Amount string    `json:"amount"`
	Fee    int64     `json:"fee"`
	At     time.Time `json:"at"`
}

// dustAlarmFor is how long inbound:dust:<symbol> stands after the last burn below the fee was left.
const dustAlarmFor = 24 * time.Hour

// inboundBurn is one burn waiting for its delivery on gno.land.
type inboundBurn struct {
	ID      string `json:"id"` // hex
	Nonce   uint32 `json:"nonce"`
	Symbol  string `json:"symbol"`
	Block   uint64 `json:"block"`
	Tx      string `json:"tx"`
	Message string `json:"message"` // hex
	Amount  string `json:"amount"`  // base units, decimal
}

// inClaim is one route's Claim loop: it calls while something is paid and backs off while nothing is.
type inClaim struct {
	Backoff   time.Duration `json:"backoff"`
	Next      time.Time     `json:"next"`
	LastPaid  time.Time     `json:"lastPaid"`
	PaidTotal int64         `json:"paidTotal"` // the route's paidTotal at the last look
	Calls     int           `json:"calls"`
	Paid      int           `json:"paid"` // claims paid something on, ever
	// Batch is how many claims the next Claim asks to pay (0: claimBatchMax), Gas the gas the client simulates it under
	// (0: the relayer's -gas), Over the least batch known to need more than the room (0: none) — all sized from the
	// last Claims (claimSize, sized, outOfGas)
	Batch int   `json:"batch,omitempty"`
	Gas   int64 `json:"gas,omitempty"`
	Over  int   `json:"over,omitempty"`
}

// claimBatchMax is the most claims one Claim pays: §5.4's Claim(20).
const claimBatchMax = 20

// The Claim batch and its gas (the integration lane I2, from F15d's measure). gno.Client.Call simulates a call under the
// gas it is given and once more under four times it when that runs out — the relayer's 45M, so 180M at most — and
// p/hlroute's Claim(20) took ≈140M of it on a gno v1.5.1 node at 400 waiting claims, its reads growing with the state;
// an out-of-gas is a transient failure, retried each minute for good, so a Claim(20) past 180M paid nothing ever and
// alarmed nothing. So the batch is sized from the last Claims to a room of three quarters of the client's ceiling: one
// past the room shrinks it in proportion and is remembered as over (Over); a full batch under the room grows it by its
// own projection (n × room / used, which over-counts the call's fixed cost, so it is safe) and by one at least, never
// to a size known over, until a projection says that size fits now (the state shrank); an out-of-gas halves it and is
// over too. A Claim of one that runs out asks four times the gas next — p/hlroute's first Claim after Launch walks every
// queue (ResetMeters: 429M at 400 queues) — as far as the client simulates (the block's gas), alarmed
// (claim:gas:<symbol>) until a Claim goes or the route's payouts move otherwise, and tried hourly once nothing more can
// be asked; the gas goes back to the relayer's once a Claim fits its room again. The gno key pays the fee of the gas a
// simulation asks for, as it does a transaction's.

// claimSize is the next Claim's batch and the gas the client simulates it under; base is the relayer's -gas.
func (c *inClaim) claimSize(base int64) (int, int64) {
	n := c.Batch
	if n <= 0 || n > claimBatchMax {
		n = claimBatchMax
	}
	return n, max(c.Gas, base)
}

// claimRoom is the most gas a Claim batch is sized to use when simulated under gas: three quarters of the client's
// ceiling, four times it — what a Claim grown with the state between two calls still fits in.
func claimRoom(gas int64) int64 { return 3 * gas }

// claimReach is the most gas the gno client simulates a call under when given gas: four times it, when that is within
// the block's gas; else gas alone (gno.Client.Call).
func claimReach(gas int64) int64 {
	if 4*gas <= gno.MaxBlockGas {
		return 4 * gas
	}
	return gas
}

// sized records a Claim of n simulated under gas that paid paid claims using used gas (above).
func (c *inClaim) sized(n, paid int, gas, used, base int64) {
	if used <= 0 {
		return
	}
	room := claimRoom(gas)
	if c.Gas > base && used <= claimRoom(base) {
		c.Gas, c.Over, room = 0, 0, claimRoom(base) // back to the relayer's gas: its room, nothing known over it
	}
	proj := int(min(int64(n)*room/used, claimBatchMax))
	next := n
	switch {
	case used > room:
		c.Over, next = n, proj
	case paid >= n:
		if c.Over > 0 && proj >= c.Over {
			c.Over = 0 // what went over before fits by the projection now: the state shrank
		}
		next = max(n+1, proj)
		if c.Over > 0 {
			next = min(next, c.Over-1)
		}
	}
	c.Batch = min(max(next, 1), claimBatchMax)
}

// outOfGas records a Claim of n that ran out of the gas the client simulated it under: half the batch, n over; for a
// batch of one, four times the gas as far as the client simulates — the block's (raised) — or nothing left to ask
// (stuck).
func (c *inClaim) outOfGas(n int, gas int64) (raised, stuck bool) {
	if n > 1 {
		c.Batch, c.Over = n/2, n
		return false, false
	}
	if claimReach(gas) >= gno.MaxBlockGas {
		return false, true
	}
	next := min(4*gas, gno.MaxBlockGas/4)
	if next <= gas {
		next = gno.MaxBlockGas // a -gas past a quarter of the block's: simulated once under the block's
	}
	c.Gas, c.Over = next, 0
	return true, false
}

// outOfGasErr says whether a gno call failed for gas: the client's simulation ran out under its ceiling.
func outOfGasErr(err error) bool {
	var ce *gno.CallError
	if !errors.As(err, &ce) {
		return false
	}
	return strings.Contains(strings.ToLower(ce.Output), "out of gas") || (ce.Err != nil && strings.Contains(strings.ToLower(ce.Err.Error()), "out of gas"))
}

// The Claim cadence (E5-GS1). p/hlroute pays a claim in installments: a recipient's meter refills a quarter of the
// route's cap a day and is empty at its first credit, so a large transfer is paid over hours or days, the fee first,
// whole; a Claim that comes before the meters have refilled pays slivers. A sliver once reset the backoff, so the relayer
// called every tick for as long as the claim lasted — ≈0.035 GNOT of the gno key's gas a Claim (28.5M on the GNOT route)
// for ≈0.1 GNOT paid out, ≈185 GNOT of gas for one 1,000 GNOT transfer, the key's 500 GNOT gone in a day or two. So a
// Claim counts as paying only when the route's paidTotal (its json read again right after the call) moved by a unit —
// the fee, at least a five-hundredth of the cap (claimUnit) — for each claim it paid something on and left open (one it
// finished is paid its rest); one that paid less counts as nothing — the backoff doubles — and the next waits at least
// until a recipient's meter can have refilled a unit (claimRefill: unit ÷ (cap/4) of a day, ≈11.6 minutes at the launch
// values, an hour at most). A full batch of a unit each is followed at once (more may be payable now); fewer than the
// batch, the realm found nothing more to pay on this pass, and the next waits the refill too — unless no claim is left
// open. The backup counts a payout it did not make as the primary's pass and waits the
// refill after it before its own clock starts (else it would call a minute after each of the primary's). What the rule
// assumes of the realm is only p/hlroute's meters (the first installment the fee, whole): it holds whether a later
// installment pays whatever refilled (p/hlroute today) or only once min(remaining, fee) has (the realm half, lane F17c).

// claimUnit is the least a Claim must pay a claim to count as paying (E5-GS1): the route's fee, and never less than a
// five-hundredth of its cap — 20 GNOT at the launch cap, the launch fee — since the fee is GovDAO's to lower, to 0 even,
// when every sliver would count again (the review of the lane, R5).
func claimUnit(rj *gnoside.Route) int64 {
	if rj == nil {
		return 0
	}
	return max(rj.Fee, rj.Cap/500)
}

// claimRefill is how long a recipient's meter, emptied by a pass, takes to hold a claim's unit again — unit ÷ (cap/4) of
// a day, rounded up, and a block more for the chain's clock: ≈11.6 minutes at the launch values, never under ≈11.5 —
// at most backoffMax; 0 when the route renders no cap.
func claimRefill(rj *gnoside.Route) time.Duration {
	unit := claimUnit(rj)
	if rj == nil || rj.Cap <= 0 || unit <= 0 {
		return 0
	}
	const day = 86_400 // p/hlroute's meters run on the realm's Day, whatever the deployment's clock factor
	if unit > math.MaxInt64/(4*day) {
		return backoffMax
	}
	secs := (unit*4*day + rj.Cap - 1) / rj.Cap
	if secs >= int64(backoffMax/time.Second) {
		return backoffMax
	}
	return min(time.Duration(secs)*time.Second+6*time.Second, backoffMax)
}

// after records a Claim call's result: paid > 0 resets the backoff; nothing paid doubles it (a minute to an hour),
// since the meters refill on their own clock.
func (c *inClaim) after(now time.Time, paid int) {
	c.Calls++
	if paid > 0 {
		c.Paid += paid
		c.LastPaid = now
		c.Backoff = 0
		c.Next = now
		return
	}
	if c.Backoff == 0 {
		c.Backoff = backoffMin
	} else {
		c.Backoff *= 2
		if c.Backoff > backoffMax {
			c.Backoff = backoffMax
		}
	}
	c.Next = now.Add(c.Backoff)
}

// inWantClaim says whether a Claim call could pay anything now: the route is live, payouts are not paused, it is not
// migrated, a claim waits and the bridge meter holds at least the fee (a claim's first installment is the fee,
// whole; one mid-way needs less, and waits the few seconds the refill takes).
func inWantClaim(rj *gnoside.Route) (bool, string) {
	switch {
	case !rj.Live:
		return false, "not launched"
	case rj.MigratedTo != "":
		return false, "migrated to " + rj.MigratedTo
	case rj.PausedFor("payouts"):
		return false, "payouts paused"
	case rj.OpenClaims == 0:
		return false, "no open claim"
	case rj.Meter <= 0 || rj.Meter < rj.Fee:
		return false, "the meter holds less than the fee"
	}
	return true, ""
}

// inboundHost is what the job asks of the relayer (E-M3's *Relayer has these, exported for the registered jobs).
type inboundHost interface {
	Note(key, what string)
	Act(key string) bool
	Forget(key string)
	GnoClient() gnoside.Caller
	TryCall(ctx context.Context, key, pkg, fn string, args ...string) (*gno.Result, error)
	// TryCallGas is TryCall simulated under the gas given (the Claim batch's, sized from the last Claim), and
	// CallGas the relayer's own (-gas)
	TryCallGas(ctx context.Context, key, pkg, fn string, gas int64, args ...string) (*gno.Result, error)
	CallGas() int64
	// Backup says the relayer is the backup (its Claims wait the refill after a payout it did not make: E5-GS1)
	Backup() bool
}

// inboundEth is Ethereum as the job reads it: the finalized block and logs, through the relayer's provider.
type inboundEth interface {
	BlockAt(ctx context.Context, at ethx.At) (*ethx.Block, error)
	Logs(ctx context.Context, q ethx.LogQuery) ([]types.Log, error)
	ethx.Reader // the launch line (HlAdmin.launchNonce())
}

// logRanger is a provider that reads a range in its own chunks (ethx.Client.LogsRange).
type logRanger interface {
	LogsRange(ctx context.Context, q ethx.LogQuery, from, to uint64) ([]types.Log, error)
}

// inboundJob is the relayer's Ethereum → gno.land half.
type inboundJob struct {
	st    inboundState
	cfg   *Config
	eth   inboundEth
	alarm *Alarmer
	http  *http.Client
	now   func() time.Time
	// FromBlock is where a fresh scan starts when the state names none (0: the config's eth.deployBlock, else
	// finishSetup's block found on the chain).
	FromBlock uint64
}

func (j *inboundJob) init() {
	if j.st.Sigs == nil {
		j.st.Sigs = map[string]map[string]string{}
	}
	if j.st.Claims == nil {
		j.st.Claims = map[string]*inClaim{}
	}
	if j.http == nil {
		j.http = &http.Client{Timeout: 10 * time.Second}
	}
	if j.now == nil {
		j.now = time.Now
	}
}

// tick is one pass: the new burns, their deliveries, the claims.
func (j *inboundJob) tick(ctx context.Context, h inboundHost) error {
	j.init()
	if j.cfg.Eth.Mailbox == "" || j.cfg.Eth.MerkleTreeHook == "" {
		h.Note("inbound", "idle: eth.mailbox and eth.merkleTreeHook are needed to read and deliver burns")
		return nil
	}
	for sym, d := range j.st.Dust {
		if j.now().Sub(d.At) >= dustAlarmFor {
			j.alarm.Clear("inbound:dust:" + sym)
		}
	}
	g := h.GnoClient()
	mb, err := gnoside.MailboxAt(ctx, g, j.cfg.Gno.Mailbox, 0)
	if err != nil {
		return fmt.Errorf("hlmailbox: %w", err)
	}
	var errs []string
	if err := j.scan(ctx, g, mb); err != nil {
		errs = append(errs, "the burns: "+err.Error())
	}
	if err := j.deliver(ctx, h, mb); err != nil {
		errs = append(errs, "deliveries: "+err.Error())
	}
	if err := j.claims(ctx, h); err != nil {
		errs = append(errs, "claims: "+err.Error())
	}
	// the job's line in the relayer's status (its state is in the relayer's state file under "inbound")
	paid := []string{}
	for _, sym := range sortedSymbols(j.st.Claims) {
		paid = append(paid, fmt.Sprintf("%s %d", sym, j.st.Claims[sym].Paid))
	}
	h.Note("inbound", fmt.Sprintf("burns scanned to the finalized block %d (from %d; the launch line %d); %d waiting for delivery (%d of them below their route's fee, left: E5-GS5); claims paid: %s; %d dispatches toward gno.land not burns of ours",
		j.st.Cursor, j.st.From, j.st.Line, len(j.st.Pending), len(j.st.Left), strings.Join(paid, ", "), j.st.Ignored))
	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	return nil
}

// scan reads the burns of the finalized blocks after the cursor (inboundScanMax a tick) and queues the new ones: a
// token's own burns addressed to any realm of its line of migrations (read at the head first: a kind 6 names a
// successor as the token's router, and every later burn is addressed to it), each delivered through the line's live
// realm (target); while a line cannot be read the scan waits, its cursor where it stands.
func (j *inboundJob) scan(ctx context.Context, g gnoside.Reader, mb *gnoside.Mailbox) error {
	gnoDomain := mb.Domain
	F, err := j.eth.BlockAt(ctx, ethx.Finalized)
	if err != nil {
		return fmt.Errorf("the finalized block: %w", err)
	}
	if j.st.From == 0 {
		if from := j.cfg.ScanFrom(j.FromBlock); from > 0 {
			j.st.From = from
		} else if c, ok := j.eth.(*ethx.Client); ok {
			from, err := deployBlock(ctx, c, j.cfg, F.Number)
			if err != nil {
				return err
			}
			j.st.From = from
		} else {
			j.st.From = 1
		}
	}
	lo := j.st.Cursor + 1
	if j.st.Cursor == 0 {
		lo = j.st.From
	}
	if lo > F.Number {
		return nil
	}
	hi := min(F.Number, lo+inboundScanMax-1)
	lines, err := readLines(ctx, g, j.cfg, mb, 0)
	if err != nil {
		return fmt.Errorf("the scan waits for the tokens' lines of migrations: %w", err)
	}
	routes := burnRoutes(j.cfg, lines)
	if len(routes) == 0 {
		j.st.Cursor = hi
		return nil
	}
	byToken := map[common.Address]ethx.BurnRoute{}
	var tokens []common.Hash
	var addrs []common.Address
	for _, r := range routes {
		byToken[r.Token] = r
		tokens = append(tokens, ethx.AddressTopic(r.Token))
		addrs = append(addrs, r.Token)
	}
	mailbox := Address(j.cfg.Eth.Mailbox)
	dispatches, err := j.logs(ctx, ethx.LogQuery{Addresses: []common.Address{mailbox}, Topics: [][]common.Hash{{ethx.Mailbox.Event("Dispatch")}, tokens, {ethx.Uint32Topic(gnoDomain)}}}, lo, hi)
	if err != nil {
		return err
	}
	sents, err := j.logs(ctx, ethx.LogQuery{Addresses: addrs, Topics: [][]common.Hash{{ethx.HypERC20.Event("SentTransferRemote")}, {ethx.Uint32Topic(gnoDomain)}}}, lo, hi)
	if err != nil {
		return err
	}
	// the launch line (PLAN.md §5.6; M2-NOTES.md Fold F5, F5.1 item 12): a message below it was dispatched before the
	// launch, and no signer signs it — relayed, it would wait for signatures for ever
	if j.cfg.Eth.Admin == "" {
		return fmt.Errorf("the config names no HlAdmin (eth.admin): the launch line cannot be read, and no burn is relayed without it")
	}
	line, err := ethx.ReadLaunchLine(ctx, j.eth, Address(j.cfg.Eth.Admin), ethx.AtNumber(hi))
	if err != nil {
		return err
	}
	j.st.Line = line.Nonce
	burns, ignored, err := ethx.MatchBurns(mailbox, j.cfg.Eth.Domain, gnoDomain, line.Nonce, byToken, dispatches, sents)
	if err != nil {
		return err
	}
	j.st.Ignored += len(ignored)
	for _, ig := range ignored {
		log.Printf("relayer: inbound: tx %s log %d is not a burn of ours: %s", ig.Tx.Hex(), ig.LogIndex, ig.Why)
	}
	for _, b := range burns {
		if b.Amount == nil || b.Amount.Sign() == 0 {
			log.Printf("relayer: inbound: ethereum #%d carries amount 0: skipped (§5.5)", b.Nonce)
			continue
		}
		id := hex.EncodeToString(b.ID[:])
		if j.pending(id) {
			continue
		}
		j.st.Pending = append(j.st.Pending, inboundBurn{ID: id, Nonce: b.Nonce, Symbol: b.Symbol, Block: b.Block, Tx: b.Tx.Hex(), Message: hex.EncodeToString(b.Message), Amount: b.Amount.String()})
	}
	sort.SliceStable(j.st.Pending, func(a, b int) bool { return j.st.Pending[a].Nonce < j.st.Pending[b].Nonce })
	j.st.Cursor = hi
	return nil
}

// logs reads a range through the provider, in its own chunks when it has them.
func (j *inboundJob) logs(ctx context.Context, q ethx.LogQuery, from, to uint64) ([]types.Log, error) {
	if lr, ok := j.eth.(logRanger); ok {
		return lr.LogsRange(ctx, q, from, to)
	}
	var out []types.Log
	for lo := from; lo <= to; lo += ethx.DefaultLogRange {
		cq := q
		cq.From, cq.To = lo, min(to, lo+ethx.DefaultLogRange-1)
		ls, err := j.eth.Logs(ctx, cq)
		if err != nil {
			return nil, err
		}
		out = append(out, ls...)
	}
	return out, nil
}

func (j *inboundJob) pending(id string) bool {
	for _, p := range j.st.Pending {
		if p.ID == id {
			return true
		}
	}
	return false
}

// deliver delivers every pending burn that has the signatures hlmailbox's rule needs, through its route's Process.
func (j *inboundJob) deliver(ctx context.Context, h inboundHost, mb *gnoside.Mailbox) error {
	g := h.GnoClient()
	rule, err := mb.Rule()
	if err != nil {
		return err
	}
	hook := ethx.AddressTo32(Address(j.cfg.Eth.MerkleTreeHook))
	var keep []inboundBurn
	var firstErr error
	// each token's realm, read once a tick (a burn's realm is its token's: a pile of pending dust costs no read of its own)
	type realmOf struct {
		target, wait string
		rj           *gnoside.Route
		err          error
	}
	realms := map[string]*realmOf{}
	// the left dust someone else delivered: its marks read only once hlmailbox's delivered count moved since they were
	// last read (a pile of it costs nothing a tick), a pile as the done tree
	gone := map[string]bool{}
	if len(j.st.Left) > 0 && mb.Delivered != j.st.LeftAt {
		ids := make([]string, 0, len(j.st.Left))
		for id := range j.st.Left {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		if marks, err := deliveredMarks(ctx, g, j.cfg.Gno.Mailbox, ids, mb.Delivered, 0); err == nil {
			for id := range marks {
				gone[id] = true
			}
			j.st.LeftAt = mb.Delivered
		} else if firstErr == nil {
			firstErr = err
		}
	}
	for _, b := range j.st.Pending {
		key := "process:" + b.ID
		t := realms[b.Symbol]
		if t == nil {
			t = &realmOf{}
			t.target, t.wait, t.rj, t.err = j.target(ctx, g, mb, b)
			realms[b.Symbol] = t
		}
		if t.err != nil {
			keep = append(keep, b)
			h.Note(key, "the route's state could not be read: "+t.err.Error())
			continue
		}
		// a burn below the fee of the realm that would credit it — the route, or its line's successor, whose import a burn
		// to it waits for — is left for anyone to Process at their own cost (E5-GS5): kept pending, judged against the fee
		// every tick before any read of its own, so a fee lowered past it makes it a delivery again; one someone delivered
		// is dropped
		if t.target != "" {
			if gone[b.ID] {
				h.Forget(key)
				delete(j.st.Sigs, b.ID)
				delete(j.st.Left, b.ID)
				log.Printf("relayer: inbound: ethereum #%d %s, dust left under the fee, was delivered by someone else", b.Nonce, short(b.ID))
				continue
			}
			if dust, first := j.dust(b, t.rj); dust {
				keep = append(keep, b)
				if first {
					h.Forget(key)
					delete(j.st.Sigs, b.ID)
					j.st.LeftAt = -1 // its mark is read next tick: it may have been delivered before it was left
				}
				continue
			}
			delete(j.st.Left, b.ID) // a fee's worth now: a burn like any other
		}
		at, err := gnoside.DeliveredAt(ctx, g, j.cfg.Gno.Mailbox, b.ID, 0)
		if err != nil {
			keep = append(keep, b)
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if at != 0 {
			h.Forget(key)
			delete(j.st.Sigs, b.ID)
			continue
		}
		keep = append(keep, b)
		target, wait := t.target, t.wait
		if target == "" {
			continue // nobody's here
		}
		if wait != "" {
			h.Note(key, "waits: "+wait)
			continue
		}
		raw, err := hex.DecodeString(b.Message)
		if err != nil {
			h.Note(key, "the message: "+err.Error())
			continue
		}
		cp := hl.Checkpoint{Hook: hook, Index: b.Nonce}
		sigs, err := j.gather(ctx, rule, cp, b, raw)
		if err != nil {
			h.Note(key, "waiting for signatures: "+err.Error())
			continue
		}
		if !h.Act(key) {
			continue
		}
		meta := hl.GnoMetadata(cp, sigs)
		res, err := h.TryCall(ctx, key, target, "Process", hex.EncodeToString(meta), b.Message)
		if err != nil {
			if !errors.Is(err, errNotYet) {
				log.Printf("relayer: ethereum #%d: %v", b.Nonce, err)
			}
			continue
		}
		h.Note(key, "delivered: "+res.Hash)
		log.Printf("relayer: ethereum #%d %s delivered on gno (%s %s, through %s): %s", b.Nonce, short(b.ID), b.Symbol, b.Amount, target, res.Hash)
	}
	j.st.Pending = j.boundLeft(keep)
	return firstErr
}

// boundLeft keeps at most maxLeft dust burns pending (E5-GS5), forgetting the oldest past it, and forgets the marks of
// the dust no longer pending.
func (j *inboundJob) boundLeft(pending []inboundBurn) []inboundBurn {
	left := 0
	for _, b := range pending {
		if _, ok := j.st.Left[b.ID]; ok {
			left++
		}
	}
	out := pending[:0]
	ids := map[string]bool{}
	for _, b := range pending {
		if _, ok := j.st.Left[b.ID]; ok && left > maxLeft {
			left--
			delete(j.st.Left, b.ID)
			log.Printf("relayer: inbound: ethereum #%d %s, dust left under the fee, forgotten: more than %d dust burns pending (E5-GS5)", b.Nonce, short(b.ID), maxLeft)
			continue
		}
		ids[b.ID] = true
		out = append(out, b)
	}
	for id := range j.st.Left {
		if !ids[id] {
			delete(j.st.Left, id)
		}
	}
	return out
}

// target is the realm whose Process takes a burn, whether it must wait first, and that realm's json: the route itself
// while it is live, the line's last successor once a migrate applied and that successor's import has ended (hlmailbox
// lets a successor take a message addressed to any realm before it in the line); before that, the burn waits. "" for a
// burn that is nobody's here.
func (j *inboundJob) target(ctx context.Context, g gnoside.Reader, mb *gnoside.Mailbox, b inboundBurn) (string, string, *gnoside.Route, error) {
	rt := j.cfg.RouteBySymbol(b.Symbol)
	if rt == nil {
		return "", "", nil, nil
	}
	rj, err := gnoside.RouteAt(ctx, g, rt.Gno, 0)
	if err != nil {
		return "", "", nil, err
	}
	mig, err := gnoside.MigrationOf(ctx, gnoside.RouteReaderAt(g, 0), rt.Symbol, rt.Gno, rt.Predecessor, rj, mb)
	if err != nil {
		return "", "", nil, err
	}
	if mig == nil {
		return rt.Gno, "", rj, nil
	}
	if !mig.Ended() {
		return mig.Successor, mig.WaitText(), mig.Succ, nil
	}
	return mig.Successor, "", mig.Succ, nil
}

// dust says whether a burn is below the fee of the realm that would credit it (E5-GS5): p/hlroute sweeps such a credit —
// owed, paying nobody, no fee taken — while its Process locks ≈0.9–1.7 GNOT of the relayers' gno key in storage
// deposit, so ≈300–400 burns of one base unit (≈$0.06 of Ethereum gas each) emptied the key's 500 GNOT and stopped every
// honest credit until a top-up. The relayer does not Process it: it stays pending, left under the fee (j.st.Left),
// judged against the fee again every tick, counted under its route and alarmed the first time it is left
// (inbound:dust:<symbol>, a day after the last); anyone may Process it at their own cost. A burn of amount 0 is skipped
// at the scan already. first: it was left now (not before, under any fee).
func (j *inboundJob) dust(b inboundBurn, rj *gnoside.Route) (dust, first bool) {
	if rj == nil || rj.Fee <= 0 {
		return false, false
	}
	amount, ok := new(big.Int).SetString(b.Amount, 10)
	if !ok || amount.Cmp(big.NewInt(rj.Fee)) >= 0 {
		return false, false
	}
	if _, told := j.st.Left[b.ID]; told {
		return true, false // left before: alarmed then
	}
	if j.st.Left == nil {
		j.st.Left = map[string]int64{}
	}
	j.st.Left[b.ID] = rj.Fee
	if j.st.Dust == nil {
		j.st.Dust = map[string]*dustRecord{}
	}
	d := j.st.Dust[b.Symbol]
	if d == nil {
		d = &dustRecord{}
		j.st.Dust[b.Symbol] = d
	}
	d.Count++
	d.Nonce, d.ID, d.Amount, d.Fee, d.At = b.Nonce, b.ID, b.Amount, rj.Fee, j.now()
	text := fmt.Sprintf("%d burn(s) toward %s's route below its fee left unprocessed, the newest ethereum #%d (%s, %s base units, the fee %d): the route would sweep each — owed, paying nobody, no fee — while its Process locks ≈1 GNOT of the relayers' gno key in storage deposit (E5-GS5); anyone may Process one at their own cost, and a fee lowered past one makes it a delivery again", d.Count, b.Symbol, b.Nonce, short(b.ID), b.Amount, rj.Fee)
	j.alarm.AlarmEvery("inbound:dust:"+b.Symbol, text, dustAlarmFor)
	log.Printf("relayer: inbound: ethereum #%d %s %s is below the route's fee %d: not processed, kept and judged against the fee each tick (E5-GS5)", b.Nonce, b.Symbol, b.Amount, rj.Fee)
	return true, true
}

// gather fetches the signers' signatures for a burn (cached once seen), checks each against the key hlmailbox holds
// for that signer, and picks exactly the ones the rule needs.
func (j *inboundJob) gather(ctx context.Context, rule gnoside.Rule, cp hl.Checkpoint, b inboundBurn, raw []byte) ([]hl.EdSignature, error) {
	m, err := hl.ParseMessage(raw)
	if err != nil {
		return nil, err
	}
	id := hl.ID(raw)
	if hex.EncodeToString(id[:]) != b.ID {
		return nil, errors.New("the queued message does not hash to its id")
	}
	cache := j.st.Sigs[b.ID]
	if cache == nil {
		cache = map[string]string{}
		j.st.Sigs[b.ID] = cache
	}
	have := map[int][64]byte{}
	known := 0
	for _, ref := range j.cfg.Signers {
		key, err := ref.EdKey()
		if err != nil {
			continue
		}
		i := rule.Index(key)
		if i < 0 {
			continue // not in hlmailbox's set: its signature would not count
		}
		known++
		sigHex, ok := cache[ref.Key]
		if !ok {
			s, err := j.fetch(ctx, ref.URL, b.Nonce)
			if err != nil || s.ID != b.ID {
				continue
			}
			sigHex = s.Signature
		}
		sb, err := hex.DecodeString(sigHex)
		if err != nil || !verifyBurnSig(key, m.Origin, cp, id, sb) {
			log.Printf("relayer: ethereum #%d: signer %d's signature is not its own over this checkpoint", b.Nonce, i)
			delete(cache, ref.Key)
			continue
		}
		cache[ref.Key] = sigHex
		var sig [64]byte
		copy(sig[:], sb)
		have[i] = sig
	}
	if known == 0 {
		return nil, errors.New("no signer in the config holds a key of hlmailbox's set")
	}
	return pickSignatures(rule, have)
}

// fetch asks a signer for one signature, over HTTPS (or a loopback address), reading at most 1 MB.
func (j *inboundJob) fetch(ctx context.Context, base string, nonce uint32) (*SignedBurn, error) {
	if err := checkSignerURL(base, j.cfg.AllowHTTP); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, "GET", strings.TrimRight(base, "/")+"/v1/eth/"+strconv.FormatUint(uint64(nonce), 10), nil)
	if err != nil {
		return nil, err
	}
	resp, err := j.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("%s: %s", ethx.Redact(base), resp.Status)
	}
	var s SignedBurn
	if err := json.NewDecoder(io.LimitReader(resp.Body, signerBodyLimit)).Decode(&s); err != nil {
		return nil, err
	}
	return &s, nil
}

// verifyBurnSig checks one signer's signature over a burn's checkpoint digest.
func verifyBurnSig(key ed25519.PublicKey, origin uint32, cp hl.Checkpoint, id [32]byte, sig []byte) bool {
	if len(sig) != ed25519.SignatureSize {
		return false
	}
	d := hl.CheckpointDigest(origin, cp.Hook, cp.Root, cp.Index, id)
	return ed25519.Verify(key, d[:], sig)
}

// pickSignatures chooses, from the signatures gathered (by signer index), the ones the rule needs: hlmailbox's
// verifier checks every signature the metadata carries, in increasing signer order, and then the rule — so the
// relayer sends exactly the signatures the rule needs and not one more: the lowest-indexed valid ones up to the
// threshold, or the lowest-indexed valid one of each group. Every signature given must already verify.
func pickSignatures(rule gnoside.Rule, have map[int][64]byte) ([]hl.EdSignature, error) {
	var idx []int
	for i := range have {
		if i >= 0 && i < len(rule.Keys) {
			idx = append(idx, i)
		}
	}
	sort.Ints(idx)
	var chosen []int
	if rule.TwoGroups() {
		a, b := -1, -1
		for _, i := range idx {
			if a < 0 && inGroup(rule.GroupA, i) {
				a = i
			} else if b < 0 && inGroup(rule.GroupB, i) {
				b = i
			}
		}
		if a < 0 || b < 0 {
			return nil, fmt.Errorf("the rule (%s) needs one of each group; have %v", rule, idx)
		}
		chosen = []int{a, b}
		sort.Ints(chosen)
	} else {
		if len(idx) < rule.Threshold {
			return nil, fmt.Errorf("the rule (%s) needs %d; have %d", rule, rule.Threshold, len(idx))
		}
		chosen = idx[:rule.Threshold]
	}
	var out []hl.EdSignature
	for _, i := range chosen {
		out = append(out, hl.EdSignature{Validator: uint8(i), Sig: have[i]})
	}
	return out, nil
}

func inGroup(xs []int, x int) bool {
	for _, y := range xs {
		if y == x {
			return true
		}
	}
	return false
}

// claims runs Claim on every route while a claim waits and the meter holds something (§5.4: the relayers call it
// continuously, twenty claims a call at most, the batch and its gas sized from the last Claim), backing off while a
// call pays nothing; a migrated route pays nothing — its successor does, once the import has ended.
func (j *inboundJob) claims(ctx context.Context, h inboundHost) error {
	g := h.GnoClient()
	var mb *gnoside.Mailbox
	var firstErr error
	for _, rt := range j.cfg.Routes {
		key := "claim:" + rt.Symbol
		rj, err := gnoside.RouteAt(ctx, g, rt.Gno, 0)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		pkg := rt.Gno
		if mb == nil {
			if mb, err = gnoside.MailboxAt(ctx, g, j.cfg.Gno.Mailbox, 0); err != nil {
				return err
			}
		}
		mig, err := gnoside.MigrationOf(ctx, gnoside.RouteReaderAt(g, 0), rt.Symbol, rt.Gno, rt.Predecessor, rj, mb)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if mig != nil {
			if !mig.Ended() {
				h.Note(key, "idle: "+mig.WaitText())
				continue
			}
			pkg, rj = mig.Successor, mig.Succ
		}
		cs := j.st.Claims[rt.Symbol]
		if cs == nil {
			cs = &inClaim{}
			j.st.Claims[rt.Symbol] = cs
		}
		refill := claimRefill(rj)
		if rj.PaidTotal != cs.PaidTotal { // someone else paid: the backup's clock restarts
			cs.PaidTotal = rj.PaidTotal
			h.Forget(key)
			j.alarm.Clear("claim:gas:" + rt.Symbol) // the payouts move, whatever this relayer's Claim needs
			if h.Backup() && refill > 0 && cs.Next.Before(j.now().Add(refill)) {
				cs.Next = j.now().Add(refill) // the primary's pass: the backup waits the refill after it (E5-GS1)
			}
		}
		if want, why := inWantClaim(rj); !want {
			h.Forget(key)
			h.Note(key, "idle: "+why)
			j.alarm.Clear("claim:gas:" + rt.Symbol)
			continue
		}
		// its own clock first, so the backup's minute starts only once a Claim is due (E5-GS1)
		if j.now().Before(cs.Next) {
			h.Note(key, fmt.Sprintf("waits until %s: %s", cs.Next.UTC().Format(time.RFC3339), cs.waitText(refill)))
			continue
		}
		if !h.Act(key) {
			continue
		}
		base := h.CallGas()
		n, gas := cs.claimSize(base)
		res, err := h.TryCallGas(ctx, key, pkg, "Claim", gas, strconv.Itoa(n))
		if err != nil {
			if outOfGasErr(err) {
				raised, stuck := cs.outOfGas(n, gas)
				next, nextGas := cs.claimSize(base)
				switch {
				case stuck:
					cs.Next = j.now().Add(time.Hour) // nothing more to ask: tried hourly, as a parked action is
					j.alarm.Alarm("claim:gas:"+rt.Symbol, fmt.Sprintf("%s's Claim of one claim ran out of %d gas, the most the gno client simulates under (the block's): payouts stand still until the route's Claim costs less (§5.4); tried again hourly", rt.Symbol, claimReach(gas)))
				case raised:
					j.alarm.Alarm("claim:gas:"+rt.Symbol, fmt.Sprintf("%s's Claim of one claim ran out of %d gas: the next is simulated under %d (as far as %d; the gno key pays the fee of the gas a simulation asks) — p/hlroute's first Claim after Launch walks every queue, or the state has outgrown the relayer's -gas", rt.Symbol, claimReach(gas), nextGas, claimReach(nextGas)))
				}
				h.Note(key, fmt.Sprintf("Claim(%d) ran out of gas under %d: the next is Claim(%d) under %d", n, claimReach(gas), next, nextGas))
				log.Printf("relayer: %s Claim(%d) ran out of gas under %d: the next is Claim(%d) under %d", rt.Symbol, n, claimReach(gas), next, nextGas)
			}
			if !errors.Is(err, errNotYet) && firstErr == nil {
				firstErr = err
			}
			continue
		}
		paid := 0
		if v, ok := res.Attr("Claimed", "count"); ok {
			paid, _ = strconv.Atoi(v)
		}
		j.alarm.Clear("claim:gas:" + rt.Symbol)
		cs.sized(n, paid, gas, res.GasUsed, base)
		// what the pass paid, read from the route right after it (E5-GS1): a unit for each claim it paid on and left open,
		// or it counts as nothing — a claim it finished is paid its rest, which may be less (the review of the lane, R5)
		var moved, left int64
		read, finished := false, 0
		if after, err := gnoside.RouteAt(ctx, g, pkg, 0); err == nil {
			moved, left, read = after.PaidTotal-rj.PaidTotal, after.OpenClaims, true
			finished = int(min(max(rj.OpenClaims-after.OpenClaims, 0), int64(paid)))
			cs.PaidTotal = after.PaidTotal // its own payment is no one else's
		}
		worth := read && paid > 0 && moved >= claimUnit(rj)*int64(paid-finished)
		now := j.now()
		switch {
		case worth && paid >= n:
			cs.after(now, paid) // a full batch, a fee's worth each: more may be payable now
		case worth:
			cs.after(now, paid)
			if left > 0 {
				cs.Next = now.Add(refill) // the realm found nothing more on this pass: the meters it visited refill first
			}
		default:
			cs.after(now, 0) // nothing, or slivers: no more than a backoff's worth of calls, and the refill at least
			cs.Paid += paid
			if t := now.Add(refill); cs.Next.Before(t) {
				cs.Next = t
			}
		}
		next, nextGas := cs.claimSize(base)
		h.Note(key, fmt.Sprintf("Claim(%d) paid %d claims %d in all using %d gas: %s (the next is Claim(%d) under %d, at %s)", n, paid, moved, res.GasUsed, res.Hash, next, nextGas, cs.Next.UTC().Format(time.RFC3339)))
		log.Printf("relayer: %s Claim(%d) paid %d claims, %d in all, using %d gas (%s)", rt.Symbol, n, paid, moved, res.GasUsed, res.Hash)
	}
	return firstErr
}

// waitText says why the next Claim waits (claims' note).
func (c *inClaim) waitText(refill time.Duration) string {
	switch {
	case c.Backoff > 0 && refill > 0:
		return fmt.Sprintf("the last Claim paid less than a unit a claim (the backoff %s; a recipient's meter refills a unit in %s)", c.Backoff, refill)
	case c.Backoff > 0:
		return fmt.Sprintf("the last Claim paid nothing (the backoff %s)", c.Backoff)
	}
	return fmt.Sprintf("installments: a recipient's meter refills a unit in %s", refill)
}

// status is the job's part of the relayer's status.
func (j *inboundJob) status() map[string]any {
	var pending []string
	for _, b := range j.st.Pending {
		pending = append(pending, fmt.Sprintf("#%d %s %s %s (block %d)", b.Nonce, b.Symbol, b.Amount, short(b.ID), b.Block))
	}
	claims := map[string]inClaim{}
	for k, c := range j.st.Claims {
		claims[k] = *c
	}
	return map[string]any{"from": j.st.From, "cursor": j.st.Cursor, "pending": pending, "claims": claims, "ignored": j.st.Ignored}
}
