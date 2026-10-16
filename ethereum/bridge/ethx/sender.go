package ethx

import (
	"context"
	"crypto/ecdsa"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math/big"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
)

// The sender (eth/bridge/PLAN.md §5.7): every Ethereum transaction of the machines is EIP-1559 from one key file,
// its gas eth_estimateGas plus 20% (the estimate is the simulation: a call that would revert is refused with its
// reason and nothing is sent — no gas is ever spent on a call the chain would refuse as it stands), its fee cap
// twice the base fee plus the tip, never sent while the base fee is above the configured maximum (the caller
// parks: a spike delays, never strands), its nonce the key's own, one transaction in flight at a time; a
// transaction not mined in 3 minutes is replaced at the same nonce with 12.5% more tip (and a fee cap raised as
// much, as a replacement must be), or broadcast again as it stands when its bounds leave no rise (a pool that
// dropped it takes it back); a call whose base-fee cap is higher takes the nonce of a transaction the base fee has
// passed (an emergency call is never held behind a route transaction a spike stranded); the receipt is awaited and
// a mined revert read for its reason. The transaction in flight is kept in a state file, so a restart resumes its
// wait rather than stranding the nonce. A transaction of the key that another machine left pending at the nonce
// (the shared relayers' key, §5.7: the backup takes over a primary that went silent) is replaced with both its fees
// 12.5% over its own, read from the pool. A call may be a contract creation (no "to"), which `hleth setup` sends.

// GasCap is the most gas one transaction may carry: EIP-7825's cap, 2^24 = 16,777,216, on mainnet since Fusaka
// (2025-12-03). Every gas limit this package sends is the estimate plus 20%, and that sum must fit under it — except a
// contract creation on a chain that meters state gas (EIP-8037, Glamsterdam: on Sepolia since 2026-10-06), where
// "the transaction gas limit set in EIP-7825 only applies to execution-gas, while state-gas is capped by tx.gas", a
// creation's code deposit is state gas (CPSB = 1,530 a byte) and "eth_estimateGas MUST incorporate the new state-gas
// charges" (eips.ethereum.org/EIPS/eip-8037, Last Call, read 2026-10-14). No RPC answer separates the two parts, so
// the 2^24 rule cannot be held to the execution part there: a creation is bounded instead by TotalGasCap and the
// head block's gas limit (stateGasCreations; the assumptions audit's E3-AS2). A successful estimate already says its
// execution fits: under EIP-8037 the execution budget is 2^24 less the intrinsic gas whatever tx.gas, the rest only
// the state-gas reservoir, so a creation whose execution would not fit has no estimate at all ("gas required exceeds
// allowance", ErrGasCap here). Every other call keeps the 2^24 rule, its 20% margin then still the execution's.
const GasCap = 1 << 24

// TotalGasCap is EIP-8037's bound on a transaction's whole gas, execution and state together: 2^32 − 1.
const TotalGasCap = 1<<32 - 1

// stateGasPerByte is the least a chain metering state gas charges a byte of code deposit in an estimate (EIP-8037's
// CPSB is 1,530; Osaka's deposit is 200 a byte): the probe's line between the two.
const stateGasPerByte = 1_000

// StuckAfter is how many replacement windows a nonce waits out unmined before the sender says so (OnStuck): the
// caller's alarm, since a transaction the bounds no longer let rise holds every later one of the key.
const StuckAfter = 3

// Gwei is 10^9 wei.
var Gwei = big.NewInt(1_000_000_000)

// GweiOf is a decimal number of gwei in wei (the configs speak gwei).
func GweiOf(g float64) *big.Int {
	f := new(big.Float).Mul(big.NewFloat(g), new(big.Float).SetInt(Gwei))
	w, _ := f.Int(nil)
	return w
}

// FormatGwei writes wei as gwei.
func FormatGwei(w *big.Int) string {
	if w == nil {
		return "?"
	}
	f, _ := new(big.Float).Quo(new(big.Float).SetInt(w), new(big.Float).SetInt(Gwei)).Float64()
	return fmt.Sprintf("%.4g gwei", f)
}

// SendConfig is the sender's bounds.
type SendConfig struct {
	MaxBaseFee   *big.Int      // wei: no transaction is sent while the base fee is above it (default 5 gwei)
	MaxTip       *big.Int      // wei: the most tip paid, replacements included (default 2 gwei); a first version pays at most MaxTip/1.125^StuckAfter, an emergency call up to MaxTip·1.125^(StuckAfter+1)
	MinTip       *big.Int      // wei: the least tip paid whatever the provider suggests (default 0.01 gwei)
	ReplaceAfter time.Duration // an unmined transaction is replaced after this (default 3 minutes)
	Wait         time.Duration // how long one Send waits for its receipt before answering ErrStillPending (default 4 minutes)
	Poll         time.Duration // how often the receipt is asked for (default 4 s)
	State        string        // the in-flight record's file ("" keeps it in memory only)
}

func (c *SendConfig) defaults() {
	if c.MaxBaseFee == nil {
		c.MaxBaseFee = GweiOf(5)
	}
	if c.MaxTip == nil {
		c.MaxTip = GweiOf(2)
	}
	if c.MinTip == nil {
		c.MinTip = GweiOf(0.01)
	}
	if c.ReplaceAfter == 0 {
		c.ReplaceAfter = 3 * time.Minute
	}
	if c.Wait == 0 {
		c.Wait = 4 * time.Minute
	}
	if c.Poll == 0 {
		c.Poll = 4 * time.Second
	}
}

// Call is one transaction to send.
type Call struct {
	To common.Address
	// Create makes the call a contract creation: a transaction with no "to", Data the creation code with the
	// constructor's arguments (To must be zero). The contract lands at CREATE(the key, the nonce), which
	// BeforeBroadcast hears before anything is sent; the receipt names it (ContractAddress).
	Create bool
	Data   []byte
	Value  *big.Int
	Label  string // what it is, for the log and the status ("deliver gno #12", "update 1,234,567")
	// MaxBaseFee overrides the sender's for this call (nil keeps it): the emergency cap of the calls that never park
	// under maxBaseFee — a decision's delivery and the header it needs, HlAdmin.execute, a flag (the red team's ER6:
	// GovDAO's cancels must land in a spike).
	MaxBaseFee *big.Int
	// BeforeBroadcast, when set, hears each version's nonce and hash after it is signed and before it is first
	// broadcast by Send (a creation's address is CREATE(key, nonce)): a caller that must find its transaction again
	// after a stop records it first. Its error stops the call, nothing broadcast.
	BeforeBroadcast func(nonce uint64, hash common.Hash) error
	// MinGas, when above the estimate with its margin, is the transaction's gas limit: a call whose execution needs more
	// than its estimate shows — HlAdmin forwards a kind 8's call its fixed budget only with that much gas left, else
	// reverts CallStarved (audit round 3, E3-CT4), and a provider whose estimate is the gas used rather than the least
	// limit that passes falls short of it. Never above GasCap, but on a chain that meters state gas (EIP-8037), where the
	// gas past it is the state-gas reservoir (E4-CT3), up to TotalGasCap and the head block's gas limit.
	MinGas uint64
}

// FeeTooHigh is a send refused because the base fee is above the configured maximum: the caller parks the action
// and tries again when it falls (never a failure toward parking for good).
type FeeTooHigh struct {
	BaseFee, Max *big.Int
}

func (e *FeeTooHigh) Error() string {
	return fmt.Sprintf("the base fee is %s, above the configured maximum %s: not sent (parked until it falls)", FormatGwei(e.BaseFee), FormatGwei(e.Max))
}

// ErrStillPending is a transaction broadcast and not yet mined when Send stopped waiting; the in-flight record
// stays and the next Send resumes it first.
var ErrStillPending = errors.New("the transaction is broadcast and not yet mined")

// ErrNonceTaken is the key's nonce consumed by a transaction this sender did not send (the other relayer sharing
// the key): the call was not made; it is tried again.
var ErrNonceTaken = errors.New("the key's nonce was taken by another transaction")

// ErrGasCap is a call whose estimate is above the transaction gas cap: it cannot be sent as one transaction.
var ErrGasCap = errors.New("the call's gas is above the transaction gas cap")

// Transient says whether a send's failure may pass by itself (the way, the fee, the nonce, a pending
// transaction) rather than being the chain's refusal of the call (a revert, a call too big, an empty float).
func Transient(err error) bool {
	if err == nil {
		return false
	}
	if _, ok := AsRevert(err); ok {
		return false
	}
	var ft *FeeTooHigh
	if errors.As(err, &ft) || errors.Is(err, ErrStillPending) || errors.Is(err, ErrNonceTaken) {
		return true
	}
	if errors.Is(err, ErrGasCap) || lowerContains(err, "insufficient funds", "exceeds block gas limit", "intrinsic gas too low", "gas limit too high") {
		return false
	}
	return true
}

type inflight struct {
	Nonce   uint64         `json:"nonce"`
	Hashes  []common.Hash  `json:"hashes"` // every version broadcast at the nonce, the newest last
	Tip     *hexutil.Big   `json:"tip"`
	FeeCap  *hexutil.Big   `json:"feeCap"`
	Gas     uint64         `json:"gas"`
	To      common.Address `json:"to"`
	Create  bool           `json:"create,omitempty"` // a contract creation: the transaction carries no "to"
	Data    hexutil.Bytes  `json:"data"`
	Value   *hexutil.Big   `json:"value,omitempty"`
	Label   string         `json:"label"`
	FirstAt time.Time      `json:"firstAt"`
	SentAt  time.Time      `json:"sentAt"`
	// Windows is how many replacement windows (ReplaceAfter each) it has waited out unmined, re-priced or not.
	Windows int `json:"windows,omitempty"`
	// MaxBaseFee is the call's own cap when it has one (its replacements' ceiling).
	MaxBaseFee *hexutil.Big `json:"maxBaseFee,omitempty"`
}

// maxBaseFee is a transaction's cap: its own, else the sender's.
func (s *Sender) maxBaseFee(f *inflight) *big.Int {
	if f != nil && f.MaxBaseFee != nil {
		return (*big.Int)(f.MaxBaseFee)
	}
	return s.Cfg.MaxBaseFee
}

type senderState struct {
	InFlight *inflight `json:"inflight,omitempty"`
	// Own are the nonces this key sent from this sender (the last few), so a moved nonce is told apart from
	// another machine's send (KeyQuiet).
	Own []uint64 `json:"own,omitempty"`
}

// Sender sends from one key.
type Sender struct {
	Key     *ecdsa.PrivateKey
	From    common.Address
	ChainID *big.Int
	Clients []*Client // the first reads; every one is given each signed transaction
	Cfg     SendConfig
	// OnStuck hears of a nonce that survived StuckAfter replacements unmined (each later wait while it stands too):
	// the machine's alarm. Nil logs it.
	OnStuck func(label string, nonce uint64, replacements int)

	now   func() time.Time
	sleep func(ctx context.Context, d time.Duration) error

	mu        sync.Mutex
	st        senderState
	loaded    bool
	seenNonce *uint64
	foreignAt time.Time
	// stateGas is whether the chain's estimates count a creation's code deposit as EIP-8037 state gas (probed once,
	// stateGasCreations)
	stateGas *bool
}

// NewSender makes a sender; its state file (if any) is read at the first Send.
func NewSender(key *ecdsa.PrivateKey, chainID *big.Int, clients []*Client, cfg SendConfig) (*Sender, error) {
	if key == nil || chainID == nil || len(clients) == 0 {
		return nil, errors.New("a sender needs a key, a chain id and a provider")
	}
	cfg.defaults()
	return &Sender{Key: key, From: KeyAddress(key), ChainID: chainID, Clients: clients, Cfg: cfg}, nil
}

func (s *Sender) init() error {
	if s.now == nil {
		s.now = time.Now
	}
	if s.sleep == nil {
		s.sleep = func(ctx context.Context, d time.Duration) error {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(d):
				return nil
			}
		}
	}
	if s.loaded {
		return nil
	}
	// loaded only once the file is read whole: a failed or corrupt read is refused on every call, never forgotten
	// (an empty state would sign a new transaction over the in-flight one the file holds)
	if s.Cfg.State == "" {
		s.loaded = true
		return nil
	}
	b, err := os.ReadFile(s.Cfg.State)
	if errors.Is(err, os.ErrNotExist) {
		s.loaded = true
		return nil
	}
	if err != nil {
		return err
	}
	if len(b) == 0 {
		return fmt.Errorf("%s is corrupt: zero length (a write that never finished); restore it or delete it knowingly", s.Cfg.State)
	}
	var st senderState
	if err := json.Unmarshal(b, &st); err != nil {
		return fmt.Errorf("%s is corrupt: %v; restore it or delete it knowingly", s.Cfg.State, err)
	}
	s.st, s.loaded = st, true
	return nil
}

func (s *Sender) save() error {
	if s.Cfg.State == "" {
		return nil
	}
	b, err := json.MarshalIndent(s.st, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.Cfg.State + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(append(b, '\n')); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, s.Cfg.State); err != nil {
		return err
	}
	if d, err := os.Open(filepath.Dir(s.Cfg.State)); err == nil {
		_ = d.Sync()
		d.Close()
	}
	return nil
}

func (s *Sender) reader() *Client { return s.Clients[0] }

// InFlight describes the transaction in flight, "" when none (the status endpoints show it). The state file is read
// first: after a restart, before any send, the transaction it holds is still in flight — the updater ends a pending
// booking by "nothing of this key in flight" (E5-GS3's rule), and an unread state never says so.
func (s *Sender) InFlight() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.init(); err != nil {
		return "the sender's state could not be read: " + err.Error()
	}
	f := s.st.InFlight
	if f == nil {
		return ""
	}
	return fmt.Sprintf("%s at nonce %d, %d version(s), the newest %s sent %s, tip %s, fee cap %s", f.Label, f.Nonce, len(f.Hashes), f.Hashes[len(f.Hashes)-1].Hex(), f.SentAt.UTC().Format(time.RFC3339), FormatGwei((*big.Int)(f.Tip)), FormatGwei((*big.Int)(f.FeeCap)))
}

// Simulate is the call's gas estimate, refused with its reason when it would revert (nothing is sent).
func (s *Sender) Simulate(ctx context.Context, c Call) (uint64, error) {
	return s.estimate(ctx, c)
}

// estimate is eth_estimateGas of a call, or of a creation.
func (s *Sender) estimate(ctx context.Context, c Call) (uint64, error) {
	if c.Create {
		return s.reader().EstimateCreate(ctx, s.From, c.Data, c.Value)
	}
	return s.reader().EstimateGas(ctx, s.From, c.To, c.Data, c.Value)
}

// Send sends one call and waits for its receipt. A call that would revert is refused before sending (a *Revert);
// a base fee above the maximum is a *FeeTooHigh; a transaction still unmined after Cfg.Wait is ErrStillPending
// (resumed by the next Send); a mined revert is a *Revert with Mined set, the receipt beside it.
func (s *Sender) Send(ctx context.Context, c Call) (*types.Receipt, error) {
	if c.Create && c.To != (common.Address{}) {
		return nil, fmt.Errorf("%s: a creation carries no \"to\" (%s given)", c.Label, c.To.Hex())
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.init(); err != nil {
		return nil, err
	}
	// an earlier transaction still in flight is resolved first (its nonce is the next one): its fate is logged —
	// its own caller stopped waiting for it — and only its still being pending, or a failed read, holds this call;
	// a call of a higher fee cap takes the nonce of one the base fee left unmineable (displaces)
	var displaced *inflight
	if f := s.st.InFlight; f != nil {
		rc, err := s.waitFor(ctx, s.now().Add(s.Cfg.Poll))
		switch {
		case err == nil:
			log.Printf("ethx: %s (nonce %d) was mined in block %v: %s", f.Label, f.Nonce, rc.BlockNumber, rc.TxHash.Hex())
		case errors.Is(err, ErrStillPending) && s.displaces(ctx, f, c):
			displaced = f
		case errors.Is(err, ErrStillPending) || !(errors.Is(err, ErrNonceTaken) || isMinedRevert(err)):
			return nil, fmt.Errorf("%s is still in flight: %w", f.Label, err)
		default:
			log.Printf("ethx: %s (nonce %d): %v", f.Label, f.Nonce, err)
		}
	}
	gas, err := s.estimate(ctx, c)
	if err != nil {
		// a node finds no gas limit it allows that carries the call (geth: "gas required exceeds allowance", its
		// search capped at EIP-7825's 2^24 or the node's own lower cap): the caller splits, as for an estimate over
		// the cap
		if _, rev := AsRevert(err); !rev && lowerContains(err, "exceeds allowance", "gas required exceeds") {
			return nil, fmt.Errorf("%w: %s: %s", ErrGasCap, c.Label, errText(err))
		}
		return nil, err
	}
	// the estimate plus 20% must fit the transaction cap (EIP-7825): a call that does not is split by its caller
	// (a staged update's batches are sized by it), never sent with less margin and never over the cap; a creation on a
	// chain whose estimates count state gas (EIP-8037) is bounded by the whole transaction's cap and the block's instead
	head, err := s.reader().BlockAt(ctx, Latest)
	if err != nil {
		return nil, err
	}
	if withMargin := gas + gas/5; withMargin > GasCap || withMargin < gas {
		if !c.Create || withMargin < gas || withMargin > TotalGasCap || (head.GasLimit > 0 && withMargin > head.GasLimit) || !s.stateGasCreations(ctx) {
			return nil, fmt.Errorf("%w: %s estimates %d gas, %d with its 20%% margin, above %d", ErrGasCap, c.Label, gas, gas+gas/5, GasCap)
		}
		log.Printf("ethx: %s estimates %d gas, %d with its margin: past EIP-7825's %d, which on this chain bounds a transaction's execution gas only — its estimate counts a creation's code deposit as state gas (EIP-8037)", c.Label, gas, withMargin, GasCap)
	}
	gas += gas / 5
	if c.MinGas > gas {
		// a gas limit above EIP-7825's cap goes only on a chain that meters state gas (EIP-8037), where the cap bounds the
		// execution part and the rest is a reservoir for state growth — an execute's resend carries one there (E4-CT3) —
		// bounded by the whole transaction's cap and the head block's gas limit, as a creation's is
		if c.MinGas > GasCap && (c.MinGas > TotalGasCap || (head.GasLimit > 0 && c.MinGas > head.GasLimit) || !s.stateGasCreations(ctx)) {
			return nil, fmt.Errorf("%w: %s asks for %d gas, above %d", ErrGasCap, c.Label, c.MinGas, GasCap)
		}
		gas = c.MinGas
	}
	if head.BaseFee == nil {
		return nil, errors.New("the chain's head carries no base fee (a pre-London chain?)")
	}
	maxBase := s.Cfg.MaxBaseFee
	if c.MaxBaseFee != nil {
		maxBase = c.MaxBaseFee
	}
	if head.BaseFee.Cmp(maxBase) > 0 {
		return nil, &FeeTooHigh{BaseFee: head.BaseFee, Max: maxBase}
	}
	tip, err := s.tip(ctx)
	if err != nil {
		return nil, err
	}
	feeCap := new(big.Int).Add(new(big.Int).Mul(head.BaseFee, big.NewInt(2)), tip)
	nonce, err := s.reader().NonceAt(ctx, s.From, Latest)
	if err != nil {
		return nil, err
	}
	if displaced != nil && nonce == displaced.Nonce {
		// a replacement of the displaced transaction: both fees 12.5% over its own (geth's pool takes nothing less),
		// within this call's bounds
		if t := bumped((*big.Int)(displaced.Tip)); t.Cmp(tip) > 0 {
			tip = t
		}
		feeCap = new(big.Int).Add(new(big.Int).Mul(head.BaseFee, big.NewInt(2)), tip)
		if fc := bumped((*big.Int)(displaced.FeeCap)); fc.Cmp(feeCap) > 0 {
			feeCap = fc
		}
		tipMax := s.tipCeiling(c.MaxBaseFee != nil)
		ceiling := new(big.Int).Add(new(big.Int).Mul(maxBase, big.NewInt(2)), tipMax)
		if tip.Cmp(tipMax) > 0 || feeCap.Cmp(ceiling) > 0 {
			return nil, fmt.Errorf("%s is still in flight and unmineable, and this call's bounds leave no room to replace it: %w", displaced.Label, ErrStillPending)
		}
		log.Printf("ethx: %s takes nonce %d from %s, whose fee cap %s the base fee %s has passed: that call is made again by its machine", c.Label, nonce, displaced.Label, FormatGwei((*big.Int)(displaced.FeeCap)), FormatGwei(head.BaseFee))
	} else if p := s.pendingOther(ctx, nonce); p != nil {
		// a transaction of the key's pending at this nonce that this sender did not send — the other relayer's of
		// the shared key, gone silent with it in the pool (§5.7: the backup takes over), or this machine's own from
		// before its state file — is replaced with both fees 12.5% over its own (geth's pool takes nothing less;
		// the PriceBump rule of every replacement here), never below what this call would pay, within this call's
		// bounds: a transaction the bounds cannot outbid keeps its nonce, and this call waits
		if t := bumped(p.Tip); t.Cmp(tip) > 0 {
			tip = t
		}
		if want := new(big.Int).Add(new(big.Int).Mul(head.BaseFee, big.NewInt(2)), tip); want.Cmp(feeCap) > 0 {
			feeCap = want
		}
		if fc := bumped(p.FeeCap); fc.Cmp(feeCap) > 0 {
			feeCap = fc
		}
		tipMax := s.tipCeiling(c.MaxBaseFee != nil)
		ceiling := new(big.Int).Add(new(big.Int).Mul(maxBase, big.NewInt(2)), tipMax)
		if tip.Cmp(tipMax) > 0 || feeCap.Cmp(ceiling) > 0 {
			return nil, fmt.Errorf("%w: the key's transaction %s pending at nonce %d (not this sender's) pays tip %s and fee cap %s; replacing it takes 12.5%% more of both (tip %s, fee cap %s), past this call's bounds (tip %s, fee cap %s): %s waits", ErrStillPending, p.Hash.Hex(), nonce, FormatGwei(p.Tip), FormatGwei(p.FeeCap), FormatGwei(tip), FormatGwei(feeCap), FormatGwei(tipMax), FormatGwei(ceiling), c.Label)
		}
		log.Printf("ethx: %s replaces the key's transaction %s pending at nonce %d (not this sender's): tip %s and fee cap %s, each 12.5%% over its own %s and %s", c.Label, p.Hash.Hex(), nonce, FormatGwei(tip), FormatGwei(feeCap), FormatGwei(p.Tip), FormatGwei(p.FeeCap))
	}
	f := &inflight{Nonce: nonce, Tip: (*hexutil.Big)(tip), FeeCap: (*hexutil.Big)(feeCap), Gas: gas, To: c.To, Create: c.Create, Data: c.Data, Label: c.Label, FirstAt: s.now()}
	if c.MaxBaseFee != nil {
		f.MaxBaseFee = (*hexutil.Big)(new(big.Int).Set(c.MaxBaseFee))
	}
	if c.Value != nil && c.Value.Sign() > 0 {
		f.Value = (*hexutil.Big)(new(big.Int).Set(c.Value))
	}
	// broadcast; a transaction of the key's already pending at the nonce is replaced when ours pays 12.5% more (the
	// pool's own fees read above when it shows them; else raised until the pool takes it)
	for tries := 0; ; tries++ {
		signed, err := s.sign(f)
		if err != nil {
			return nil, err
		}
		if c.BeforeBroadcast != nil {
			if err := c.BeforeBroadcast(f.Nonce, signed.Hash()); err != nil {
				return nil, err
			}
		}
		h, err := s.send(ctx, signed)
		if err == nil {
			f.Hashes = append(f.Hashes, h)
			break
		}
		switch {
		case lowerContains(err, "nonce too low", "nonce has already been used"):
			if tries >= 2 {
				return nil, err
			}
			if nonce, err = s.reader().NonceAt(ctx, s.From, Latest); err != nil {
				return nil, err
			}
			f.Nonce = nonce
		case lowerContains(err, "underpriced", "fee too low", "less than block base fee"):
			if tries >= 4 || !s.bump(ctx, f) {
				return nil, err
			}
		default:
			return nil, err
		}
	}
	f.SentAt = s.now()
	s.st.InFlight = f
	s.noteOwn(f.Nonce)
	if err := s.save(); err != nil {
		return nil, err
	}
	return s.waitFor(ctx, s.now().Add(s.Cfg.Wait))
}

// Settle resolves the transaction in flight before the caller plans from what its calls carried (a staged update's
// resumption reads the chain): nil when none is pending — mined, its nonce taken, or none at all — and
// ErrStillPending while it is.
func (s *Sender) Settle(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.init(); err != nil {
		return err
	}
	f := s.st.InFlight
	if f == nil {
		return nil
	}
	_, err := s.waitFor(ctx, s.now().Add(s.Cfg.Poll))
	switch {
	case err == nil || errors.Is(err, ErrNonceTaken) || isMinedRevert(err):
		return nil
	}
	return err
}

// Resume finishes the transaction a stopped run left in flight (the state file's record), waiting up to Cfg.Wait: its
// receipt once mined (a mined revert is the *Revert beside it), nil when none was in flight or another transaction
// took its nonce, ErrStillPending while it is still pending. A version the pool no longer holds is given to every
// provider again at once, as it stands (a pool that dropped it, a restarted node).
func (s *Sender) Resume(ctx context.Context) (*types.Receipt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.init(); err != nil {
		return nil, err
	}
	f := s.st.InFlight
	if f == nil {
		return nil, nil
	}
	if pend, err := s.reader().NonceAt(ctx, s.From, Pending); err == nil && pend <= f.Nonce {
		if _, err := s.broadcast(ctx, f); err != nil {
			log.Printf("ethx: %s (nonce %d): the pool no longer holds it and it could not be broadcast again: %v", f.Label, f.Nonce, errText(err))
		}
	}
	rc, err := s.waitFor(ctx, s.now().Add(s.Cfg.Wait))
	if errors.Is(err, ErrNonceTaken) {
		return nil, nil
	}
	return rc, err
}

// StateGas says whether the chain meters state gas (EIP-8037): its estimates count a creation's code deposit at the
// state-gas price (stateGasCreations' probe, made once a sender; a failed probe is no, asked again next time).
func (s *Sender) StateGas(ctx context.Context) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stateGasCreations(ctx)
}

// stateGasCreations says whether the chain's estimates count a creation's code deposit as state gas (EIP-8037): two
// creations differing only in the code they leave, 0 and 1,024 bytes, are estimated, and the difference a byte is
// EIP-8037's CPSB (1,530) rather than Osaka's 200. Asked once a sender; a failed probe is no (the 2^24 rule stands).
func (s *Sender) stateGasCreations(ctx context.Context) bool {
	if s.stateGas != nil {
		return *s.stateGas
	}
	probe := func(n int) (uint64, error) {
		// PUSH2 n, PUSH1 0, RETURN: n zero bytes of code
		return s.reader().EstimateCreate(ctx, s.From, []byte{0x61, byte(n >> 8), byte(n), 0x60, 0x00, 0xf3}, nil)
	}
	empty, err1 := probe(0)
	full, err2 := probe(1024)
	yes := err1 == nil && err2 == nil && full > empty && (full-empty)/1024 >= stateGasPerByte
	if err1 != nil || err2 != nil {
		log.Printf("ethx: the state-gas probe failed (%v, %v): creations keep EIP-7825's 2^24 rule", err1, err2)
	} else {
		s.stateGas = &yes
	}
	return yes
}

// pendingOther is the key's transaction the pool holds at a nonce when this sender did not send it (the pool's
// pending count is past the nonce and the pool shows it), nil otherwise — also when the provider shows no pool, in
// which case a refused broadcast raises this call's own fees until the pool takes it.
func (s *Sender) pendingOther(ctx context.Context, nonce uint64) *PendingTx {
	pend, err := s.reader().NonceAt(ctx, s.From, Pending)
	if err != nil || pend <= nonce {
		return nil
	}
	p, err := s.reader().PendingAt(ctx, s.From, nonce)
	if err != nil || p == nil || p.Tip == nil || p.FeeCap == nil {
		if err != nil && !errors.Is(err, ErrNoPoolView) {
			log.Printf("ethx: the key's transaction pending at nonce %d could not be read: %v", nonce, errText(err))
		}
		return nil
	}
	return p
}

// Tip is the tip a call sent now would pay (the provider's suggestion within the bounds).
func (s *Sender) Tip(ctx context.Context) (*big.Int, error) { return s.tip(ctx) }

// Address is the key's address.
func (s *Sender) Address() common.Address { return s.From }

// tip is the provider's suggestion within [MinTip, firstTipCeiling] (the minimum when the provider has none).
func (s *Sender) tip(ctx context.Context) (*big.Int, error) {
	t, err := s.reader().SuggestTip(ctx)
	if err != nil || t == nil {
		t = new(big.Int).Set(s.Cfg.MinTip)
	}
	if t.Cmp(s.Cfg.MinTip) < 0 {
		t = new(big.Int).Set(s.Cfg.MinTip)
	}
	if c := s.firstTipCeiling(); t.Cmp(c) > 0 {
		t = c
	}
	return t, nil
}

// firstTipCeiling is the most tip a first version pays: MaxTip less StuckAfter rises of 12.5%, so the replacements
// before the stuck alarm all fit under MaxTip (a first version at MaxTip could never be replaced: geth's pool takes
// a replacement only when its tip rises too).
func (s *Sender) firstTipCeiling() *big.Int {
	t := new(big.Int).Set(s.Cfg.MaxTip)
	for i := 0; i < StuckAfter; i++ {
		t = mulDiv(t, 1000, 1125)
		t.Sub(t, big.NewInt(1))
	}
	if t.Cmp(s.Cfg.MinTip) < 0 {
		t = new(big.Int).Set(s.Cfg.MinTip)
	}
	return t
}

// tipCeiling is the most tip a transaction pays, replacements included: MaxTip for a call under maxBaseFee; for an
// emergency call (its own base-fee cap: ER6) MaxTip raised StuckAfter+1 times by 12.5%, so it can take the nonce of
// a route transaction re-priced up to MaxTip (geth's pool wants the tip to rise too) and still be re-priced itself.
func (s *Sender) tipCeiling(emergency bool) *big.Int {
	t := new(big.Int).Set(s.Cfg.MaxTip)
	if emergency {
		for i := 0; i <= StuckAfter; i++ {
			t = bumped(t)
		}
	}
	return t
}

// bumped is a value raised 12.5% (and a wei): a replacement's least rise here.
func bumped(x *big.Int) *big.Int {
	out := mulDiv(x, 1125, 1000)
	return out.Add(out, big.NewInt(1))
}

// displaces says whether a call takes the nonce of the transaction in flight: the base fee has passed that
// transaction's fee cap (it cannot be mined as it stands) and the call's own base-fee cap is higher than that
// transaction's — an emergency call (ER6: a decision, its header, an execute) behind a route transaction a spike
// stranded under maxBaseFee. The displaced call is made again by its machine (its action is read back from the
// chains).
func (s *Sender) displaces(ctx context.Context, f *inflight, c Call) bool {
	callCap := s.Cfg.MaxBaseFee
	if c.MaxBaseFee != nil {
		callCap = c.MaxBaseFee
	}
	if callCap.Cmp(s.maxBaseFee(f)) <= 0 {
		return false
	}
	head, err := s.reader().BlockAt(ctx, Latest)
	if err != nil || head.BaseFee == nil {
		return false
	}
	return head.BaseFee.Cmp((*big.Int)(f.FeeCap)) > 0
}

// bump raises a transaction's tip and fee cap by 12.5% over its own pending values — geth's pool takes a
// replacement only when both rise by at least 10% (PriceBump) — the fee cap further to twice the base fee now
// plus the new tip when that is higher (never lower than the 12.5% rise, whatever the base fee did), within the
// bounds (the tip under its class's ceiling, tipCeiling; the fee cap under twice its base-fee cap plus that); false
// when the bounds leave no room for a rise of 10%.
func (s *Sender) bump(ctx context.Context, f *inflight) bool {
	tipMax := s.tipCeiling(f.MaxBaseFee != nil)
	tip := bumped((*big.Int)(f.Tip))
	if tip.Cmp(tipMax) > 0 {
		return false
	}
	feeCap := bumped((*big.Int)(f.FeeCap))
	if head, err := s.reader().BlockAt(ctx, Latest); err == nil && head.BaseFee != nil {
		if want := new(big.Int).Add(new(big.Int).Mul(head.BaseFee, big.NewInt(2)), tip); want.Cmp(feeCap) > 0 {
			feeCap = want
		}
	}
	ceiling := new(big.Int).Add(new(big.Int).Mul(s.maxBaseFee(f), big.NewInt(2)), tipMax)
	if feeCap.Cmp(ceiling) > 0 {
		feeCap = ceiling
	}
	if feeCap.Cmp(mulDiv((*big.Int)(f.FeeCap), 11, 10)) < 0 || feeCap.Cmp(tip) < 0 {
		return false
	}
	f.Tip, f.FeeCap = (*hexutil.Big)(tip), (*hexutil.Big)(feeCap)
	return true
}

func mulDiv(x *big.Int, num, den int64) *big.Int {
	out := new(big.Int).Mul(x, big.NewInt(num))
	return out.Quo(out, big.NewInt(den))
}

// broadcast signs the in-flight version and gives it to every provider; one acceptance is enough.
func (s *Sender) broadcast(ctx context.Context, f *inflight) (common.Hash, error) {
	signed, err := s.sign(f)
	if err != nil {
		return common.Hash{}, err
	}
	return s.send(ctx, signed)
}

// sign signs the in-flight version (a creation's transaction carries no "to").
func (s *Sender) sign(f *inflight) (*types.Transaction, error) {
	var to *common.Address
	if !f.Create {
		to = &f.To
	}
	tx := types.NewTx(&types.DynamicFeeTx{
		ChainID:   s.ChainID,
		Nonce:     f.Nonce,
		GasTipCap: (*big.Int)(f.Tip),
		GasFeeCap: (*big.Int)(f.FeeCap),
		Gas:       f.Gas,
		To:        to,
		Value:     valueOf(f.Value),
		Data:      f.Data,
	})
	return types.SignTx(tx, types.LatestSignerForChainID(s.ChainID), s.Key)
}

// send gives a signed version to every provider; one acceptance is enough.
func (s *Sender) send(ctx context.Context, signed *types.Transaction) (common.Hash, error) {
	raw, err := signed.MarshalBinary()
	if err != nil {
		return common.Hash{}, err
	}
	var firstErr error
	accepted := false
	for _, c := range s.Clients {
		_, err := c.SendRaw(ctx, raw)
		if err == nil || lowerContains(err, "already known", "known transaction", "alreadyknown") {
			accepted = true
			continue
		}
		if firstErr == nil {
			firstErr = err
		}
	}
	if !accepted {
		return common.Hash{}, firstErr
	}
	return signed.Hash(), nil
}

func valueOf(v *hexutil.Big) *big.Int {
	if v == nil {
		return new(big.Int)
	}
	return (*big.Int)(v)
}

func isMinedRevert(err error) bool {
	r, ok := AsRevert(err)
	return ok && r.Mined
}

// waitFor waits for the in-flight transaction until the deadline: its receipt (any version), its nonce taken by
// another transaction, or a replacement when it has waited ReplaceAfter.
func (s *Sender) waitFor(ctx context.Context, deadline time.Time) (*types.Receipt, error) {
	f := s.st.InFlight
	takenSeen := 0
	for {
		mined, err := s.reader().NonceAt(ctx, s.From, Latest)
		if err != nil {
			return nil, err
		}
		rc, err := s.receiptOf(ctx, f)
		if err != nil {
			return nil, err
		}
		if rc != nil {
			s.st.InFlight = nil
			if err := s.save(); err != nil {
				return nil, err
			}
			if rc.Status != types.ReceiptStatusSuccessful {
				return rc, s.minedRevert(ctx, f, rc)
			}
			return rc, nil
		}
		if mined > f.Nonce {
			// the nonce is spent and none of our versions' receipts is served: another transaction took it — on the
			// second look, since a provider behind a balancer may count the nonce a moment before it serves the receipt
			if takenSeen++; takenSeen >= 2 {
				s.st.InFlight = nil
				if err := s.save(); err != nil {
					return nil, err
				}
				return nil, fmt.Errorf("%w (nonce %d, %s)", ErrNonceTaken, f.Nonce, f.Label)
			}
			if err := s.sleep(ctx, s.Cfg.Poll); err != nil {
				return nil, err
			}
			continue
		}
		now := s.now()
		if now.Sub(f.SentAt) >= s.Cfg.ReplaceAfter {
			// a record from before Windows was kept counts its replacements
			f.Windows = max(f.Windows, len(f.Hashes)-1)
			if f.Windows >= StuckAfter {
				s.stuck(f, f.Windows)
			}
			f.Windows++
			prev := *f
			rebroadcast := true
			if s.bump(ctx, f) {
				if h, err := s.broadcast(ctx, f); err == nil {
					f.Hashes = append(f.Hashes, h)
					rebroadcast = false
				} else {
					f.Tip, f.FeeCap = prev.Tip, prev.FeeCap // the pool refused it: the pending values stand
					log.Printf("ethx: %s (nonce %d): the replacement was refused: %v", f.Label, f.Nonce, errText(err))
					// the pool holds another version of the key at the nonce — the backup's, which took it over at 12.5%
					// above this one and stopped — priced over that one within this call's bounds, as Send prices over
					// pendingOther: never a bump over its own version that the pool refuses window after window (E3-GS8)
					if lowerContains(err, "underpriced", "fee too low") && s.outbid(ctx, f) {
						rebroadcast = false
					}
				}
			}
			if rebroadcast {
				// no rise this window: the pending version is given to every provider again, so a pool that dropped
				// it takes it back ("already known" is an acceptance) and the nonce is never left unsent
				if _, err := s.broadcast(ctx, f); err != nil {
					log.Printf("ethx: %s (nonce %d): the pending version could not be broadcast again: %v", f.Label, f.Nonce, errText(err))
				}
			}
			f.SentAt = now // one replacement per ReplaceAfter, sent or not
			if err := s.save(); err != nil {
				return nil, err
			}
		}
		if !now.Before(deadline) {
			return nil, ErrStillPending
		}
		if err := s.sleep(ctx, s.Cfg.Poll); err != nil {
			return nil, err
		}
	}
}

// outbid replaces the version of the key the pool holds at the in-flight nonce when it is not one of this sender's
// (E3-GS8): both fees 12.5% over that version's, the fee cap at least twice the base fee now plus the tip, within this
// call's bounds (the tip under its class's ceiling, the fee cap under twice its base-fee cap plus that); true when the
// pool took it. A version the bounds cannot outbid keeps the nonce, and the call waits.
func (s *Sender) outbid(ctx context.Context, f *inflight) bool {
	p, err := s.reader().PendingAt(ctx, s.From, f.Nonce)
	if err != nil || p == nil || p.Tip == nil || p.FeeCap == nil {
		return false
	}
	for _, h := range f.Hashes {
		if h == p.Hash {
			return false // this sender's own version: the bump over it was the one to make
		}
	}
	tip := bumped(p.Tip)
	if (*big.Int)(f.Tip).Cmp(tip) > 0 {
		tip = new(big.Int).Set((*big.Int)(f.Tip))
	}
	feeCap := bumped(p.FeeCap)
	if head, err := s.reader().BlockAt(ctx, Latest); err == nil && head.BaseFee != nil {
		if want := new(big.Int).Add(new(big.Int).Mul(head.BaseFee, big.NewInt(2)), tip); want.Cmp(feeCap) > 0 {
			feeCap = want
		}
	}
	if (*big.Int)(f.FeeCap).Cmp(feeCap) > 0 {
		feeCap = new(big.Int).Set((*big.Int)(f.FeeCap))
	}
	tipMax := s.tipCeiling(f.MaxBaseFee != nil)
	ceiling := new(big.Int).Add(new(big.Int).Mul(s.maxBaseFee(f), big.NewInt(2)), tipMax)
	if tip.Cmp(tipMax) > 0 || feeCap.Cmp(ceiling) > 0 {
		log.Printf("ethx: %s (nonce %d): the key's other version %s pays tip %s and fee cap %s; outbidding it takes tip %s and fee cap %s, past this call's bounds (tip %s, fee cap %s): it waits", f.Label, f.Nonce, p.Hash.Hex(), FormatGwei(p.Tip), FormatGwei(p.FeeCap), FormatGwei(tip), FormatGwei(feeCap), FormatGwei(tipMax), FormatGwei(ceiling))
		return false
	}
	prevTip, prevCap := f.Tip, f.FeeCap
	f.Tip, f.FeeCap = (*hexutil.Big)(tip), (*hexutil.Big)(feeCap)
	h, err := s.broadcast(ctx, f)
	if err != nil {
		f.Tip, f.FeeCap = prevTip, prevCap
		log.Printf("ethx: %s (nonce %d): outbidding the key's other version %s was refused: %v", f.Label, f.Nonce, p.Hash.Hex(), errText(err))
		return false
	}
	f.Hashes = append(f.Hashes, h)
	log.Printf("ethx: %s (nonce %d) outbids the key's other version %s at the nonce: tip %s and fee cap %s, each 12.5%% over its own %s and %s", f.Label, f.Nonce, p.Hash.Hex(), FormatGwei(tip), FormatGwei(feeCap), FormatGwei(p.Tip), FormatGwei(p.FeeCap))
	return true
}

// stuck reports a nonce that waited out StuckAfter replacement windows unmined (re-priced in each, or broadcast
// again where its bounds left no rise).
func (s *Sender) stuck(f *inflight, windows int) {
	if s.OnStuck != nil {
		s.OnStuck(f.Label, f.Nonce, windows)
		return
	}
	log.Printf("ethx: %s (nonce %d) waited out %d replacement windows unmined (%d versions, tip %s, fee cap %s): every later transaction of the key waits behind it", f.Label, f.Nonce, windows, len(f.Hashes), FormatGwei((*big.Int)(f.Tip)), FormatGwei((*big.Int)(f.FeeCap)))
}

// receiptOf is the receipt of whichever version of the in-flight transaction was mined, nil while none is.
func (s *Sender) receiptOf(ctx context.Context, f *inflight) (*types.Receipt, error) {
	for i := len(f.Hashes) - 1; i >= 0; i-- {
		rc, err := s.reader().Receipt(ctx, f.Hashes[i])
		if IsNotFound(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		return rc, nil
	}
	return nil, nil
}

// minedRevert reads a mined revert's reason by calling it again at the state before its block.
func (s *Sender) minedRevert(ctx context.Context, f *inflight, rc *types.Receipt) error {
	r := &Revert{Mined: true, Message: fmt.Sprintf("%s (tx %s, block %v, %d gas spent)", f.Label, rc.TxHash.Hex(), rc.BlockNumber, rc.GasUsed)}
	if rc.BlockNumber != nil && rc.BlockNumber.Sign() > 0 {
		var err error
		if f.Create {
			_, err = s.reader().CallCreate(ctx, s.From, f.Data, valueOf(f.Value), AtNumber(rc.BlockNumber.Uint64()-1))
		} else {
			_, err = s.reader().CallFrom(ctx, s.From, f.To, f.Data, valueOf(f.Value), AtNumber(rc.BlockNumber.Uint64()-1))
		}
		if rv, ok := AsRevert(err); ok {
			r.Data, r.Name, r.Reason, r.Contract = rv.Data, rv.Name, rv.Reason, rv.Contract
		}
	}
	return r
}

// noteOwn remembers a nonce this sender used (the last 64).
func (s *Sender) noteOwn(n uint64) {
	s.st.Own = append(s.st.Own, n)
	if len(s.st.Own) > 64 {
		s.st.Own = s.st.Own[len(s.st.Own)-64:]
	}
}

func (s *Sender) own(n uint64) bool {
	for _, x := range s.st.Own {
		if x == n {
			return true
		}
	}
	return false
}

// KeyQuiet says whether the key has sent nothing but this sender's own transactions for at least d, by the key's
// pending nonce as watched over time: a backup that shares the primary's key sends only then (PLAN.md §5.7: one
// machine sends with a key at a time; the backup only while the primary's key has sent nothing for its 60 s). The
// first look counts as a move, so a backup waits d after it starts.
func (s *Sender) KeyQuiet(ctx context.Context, d time.Duration) (bool, error) {
	n, err := s.reader().NonceAt(ctx, s.From, Pending)
	if err != nil {
		return false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.init(); err != nil {
		return false, err
	}
	now := s.now()
	switch {
	case s.seenNonce == nil:
		s.foreignAt = now
	case n != *s.seenNonce:
		for x := *s.seenNonce; x < n; x++ {
			if !s.own(x) {
				s.foreignAt = now
				break
			}
		}
		if n < *s.seenNonce {
			s.foreignAt = now // a dropped pending transaction: someone's, not settled
		}
	}
	s.seenNonce = &n
	return now.Sub(s.foreignAt) >= d, nil
}

// Balance is the key's wei (the floats' runway).
func (s *Sender) Balance(ctx context.Context) (*big.Int, error) {
	return s.reader().BalanceAt(ctx, s.From, Latest)
}
