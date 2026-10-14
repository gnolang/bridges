package main

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/gnolang/bridges/solana/bridge/agent"
)

// What /api/<chain>/transfer/<ref> serves: one transfer's state by a message id (gno.land's dispatch event or
// the realm's send record), by the wallet's gno.land transaction hash (resolved to the id through the
// SentTransferRemote event) or by the far side's transaction signature (resolved through the mailbox's dispatch
// record). The two directions:
//
//	out: dispatched on gno.land → delivered on the far side
//	in:  burned on the far side → credited on gno.land (a claim) → paid, under the meters, in installments
//
// A burn the signers have not confirmed yet is known only by its signature.

type transferJSON struct {
	Ref       string     `json:"ref"`
	Kind      string     `json:"kind"`      // what the reference was: "message", "gnotx" or "signature"
	Direction string     `json:"direction"` // "out" (gno.land → the far side) or "in"
	Token     string     `json:"token"`
	ID        string     `json:"id"`
	From      string     `json:"from"`
	To        string     `json:"to"`
	Amount    int64      `json:"amount"`
	Paid      int64      `json:"paid"`
	State     string     `json:"state"`
	Note      string     `json:"note"`
	Steps     []stepJSON `json:"steps"`
	// The walkthrough's facts (M7): the five steps with their times, and what each step's picture needs.
	Walk      []walkStep  `json:"walk"`
	At        int64       `json:"at"`        // out: locked at; in: burned at (0 when not known)
	Height    int64       `json:"height"`    // out: the block the send is in; in: the block it was credited in
	Nonce     int64       `json:"nonce"`     // out: hlmailbox's dispatch nonce; in: the far side's; -1 when not known
	Trusted   int64       `json:"trusted"`   // out: the light client's trusted height now
	Proof     *ProofEvent `json:"proof"`     // out: the proof the relayer staged, once seen
	Vouched   []int       `json:"vouched"`   // in: the signers serving the burn's signature, when asked
	Signers   int         `json:"signers"`   // in: how many signers there are
	Threshold int         `json:"threshold"` // in: how many must vouch
	Meter     *meterJSON  `json:"meter"`     // in, credited and not paid in full: the payout meters now
}

// meterJSON is a route's payout meters as the pay step's gauge draws them (§5.4): the bridge meter's level now,
// its ceiling and refill a day (the day cap), a recipient's quarter of both, and what the claim still waits for.
type meterJSON struct {
	Level     int64 `json:"level"`
	Cap       int64 `json:"cap"`
	Recipient int64 `json:"recipient"`
	Remaining int64 `json:"remaining"`
	At        int64 `json:"at"`
}

type stepJSON struct {
	Name string `json:"name"`
	Done bool   `json:"done"`
	Link string `json:"link"`
	Text string `json:"text"`
}

var errUnknownTransfer = errors.New("unknown transfer")

// resolve finds a transfer by any reference.
func (s *server) resolve(ctx context.Context, ch *ChainConfig, other Other, ref string) (*transferJSON, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" || len(ref) > 128 {
		return nil, errUnknownTransfer
	}
	if other.IsSignature(ref) {
		return s.bySignature(ctx, ch, other, ref)
	}
	if id, ok := messageID(ref); ok {
		t, err := s.byID(ctx, ch, other, hex.EncodeToString(id[:]), "message")
		if err == nil || !errors.Is(err, errUnknownTransfer) {
			return t, err
		}
	}
	if h, ok := gnoTxHash(ref); ok {
		return s.byGnoTx(ctx, ch, other, h)
	}
	return nil, errUnknownTransfer
}

// byGnoTx reads the wallet's transaction on gno.land and follows its SentTransferRemote event to the id.
func (s *server) byGnoTx(ctx context.Context, ch *ChainConfig, other Other, hash string) (*transferJSON, error) {
	tx, err := s.gno.TxEvents(ctx, hash)
	if err != nil {
		if strings.Contains(err.Error(), "not found") || errors.Is(err, errNoSuchTx) {
			return nil, fmt.Errorf("%w: no transaction %s on gno.land", errUnknownTransfer, hash)
		}
		return nil, err
	}
	if tx.Failed != "" {
		return &transferJSON{Ref: hash, Kind: "gnotx", Direction: "out", State: "failed", Note: "gno.land refused the transaction: " + tx.Failed, Steps: []stepJSON{}}, nil
	}
	id, ok := tx.Attr("SentTransferRemote", "id")
	if !ok {
		return nil, fmt.Errorf("%w: transaction %s sent no transfer (no SentTransferRemote event)", errUnknownTransfer, hash)
	}
	t, err := s.byID(ctx, ch, other, strings.ToLower(id), "gnotx")
	if err != nil {
		return nil, err
	}
	t.Ref = hash
	if s.cfg.Gno.TxLink != "" && len(t.Steps) > 0 {
		t.Steps[0].Link = fmt.Sprintf(s.cfg.Gno.TxLink, hash)
	}
	return t, nil
}

// byID looks a message id up on every route of the chain: a send record is a transfer out, a claim a transfer in.
func (s *server) byID(ctx context.Context, ch *ChainConfig, other Other, id, kind string) (*transferJSON, error) {
	idb, _ := messageID(id)
	for _, t := range ch.Tokens {
		sd, err := agent.SendOf(ctx, s.gno, t.Realm, id, 0)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", t.Realm, err)
		}
		if sd != nil {
			return s.outbound(ctx, ch, other, t, id, idb, kind, sd)
		}
	}
	for _, t := range ch.Tokens {
		c, err := agent.ClaimOf(ctx, s.gno, t.Realm, id, 0)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", t.Realm, err)
		}
		if c != nil {
			return s.inbound(ctx, ch, other, t, id, kind, c, nil)
		}
	}
	return nil, fmt.Errorf("%w: %s is not a send and not a credited burn on gno.land; a burn is known by its %s signature until the signers confirm it", errUnknownTransfer, id, ch.Name)
}

// bySignature reads the burn a far-side transaction made and follows its id to gno.land.
func (s *server) bySignature(ctx context.Context, ch *ChainConfig, other Other, sig string) (*transferJSON, error) {
	burns, err := other.Burn(ctx, sig)
	if err != nil {
		if errors.Is(err, errNoSuchTx) {
			return nil, fmt.Errorf("%w: no transaction %s on %s (finalized)", errUnknownTransfer, sig, ch.Name)
		}
		return nil, err
	}
	if len(burns) == 0 {
		return nil, fmt.Errorf("%w: transaction %s made no burn toward gno.land on the bridge's token programs", errUnknownTransfer, sig)
	}
	b := burns[0]
	t := ch.Token(b.Token)
	if t == nil {
		return nil, fmt.Errorf("%w: transaction %s burned a token that is not one of the bridge's", errUnknownTransfer, sig)
	}
	c, err := agent.ClaimOf(ctx, s.gno, t.Realm, b.ID, 0)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", t.Realm, err)
	}
	return s.inbound(ctx, ch, other, *t, b.ID, "signature", c, &b)
}

func (s *server) outbound(ctx context.Context, ch *ChainConfig, other Other, t TokenConfig, id string, idb [32]byte, kind string, sd *agent.SendView) (*transferJSON, error) {
	tr := &transferJSON{Ref: id, Kind: kind, Direction: "out", Token: t.Symbol, ID: id, From: sd.From, Amount: sd.Amount, State: "dispatched"}
	tr.Steps = append(tr.Steps, stepJSON{Name: "dispatched on gno.land", Done: true, Link: fmt.Sprintf(s.cfg.Gno.RealmLink, t.Realm), Text: fmt.Sprintf("%s locked %d base units of %s", sd.From, sd.Amount, t.Symbol)})
	switch {
	case sd.WrittenOff:
		tr.State, tr.Note = "written off", "this send was refunded on gno.land and delivered after all; governance wrote it off"
	case sd.Refunded:
		tr.State, tr.Note = "refunded", "governance refunded this send on gno.land; it will not be delivered"
	}
	step := stepJSON{Name: "delivered on " + ch.Name}
	d, err := other.DeliveryOf(ctx, idb)
	switch {
	case err != nil:
		step.Text = ch.Name + " could not be read: " + err.Error()
	case d != nil:
		step.Done, step.Link = true, d.Link
		step.Text = "minted; transaction " + d.Sig
		if d.Sig == "" {
			step.Text = "minted (the delivery record stands)"
		}
		if tr.State == "dispatched" {
			tr.State = "delivered"
		}
	default:
		step.Text = "waiting for a gno.land header past the send and the relayer's proof; minutes, hourly retries if parked"
	}
	tr.Steps = append(tr.Steps, step)
	var dl *DeliveryEvent
	if d != nil {
		dl = &DeliveryEvent{ID: id, Sig: d.Sig, Slot: d.Slot, Time: d.Time, Kind: "mint", Link: d.Link}
	}
	st := s.activityNow(ctx, ch, other)
	f := s.outFacts(st, id, s.findDispatch(ctx, ch, other, s.activityOf(ch.ID), st, id), dl)
	tr.Walk, tr.At, tr.Height, tr.Nonce, tr.Trusted, tr.Proof = f.Walk, f.At, f.Height, f.Nonce, f.Trusted, f.Proof
	if tr.To == "" {
		tr.To = f.To
	}
	return tr, nil
}

func (s *server) inbound(ctx context.Context, ch *ChainConfig, other Other, t TokenConfig, id, kind string, c *agent.ClaimView, b *Burn) (*transferJSON, error) {
	tr := &transferJSON{Ref: id, Kind: kind, Direction: "in", Token: t.Symbol, ID: id, State: "burned"}
	burned := stepJSON{Name: "burned on " + s.chainName(other), Done: true}
	if b != nil {
		tr.Ref, tr.To, tr.Amount = b.Sig, b.To, b.Amount
		burned.Link = other.TxLink(b.Sig)
		burned.Text = fmt.Sprintf("%d base units of %s burned toward %s", b.Amount, t.Symbol, b.To)
		if b.BadTo {
			burned.Text = "burned toward bytes that are no gno.land address: the realm sweeps it (governance pays it to its owner by id)"
		}
	} else {
		burned.Text = "known by its message id"
	}
	tr.Steps = append(tr.Steps, burned)
	credited := stepJSON{Name: "credited on gno.land"}
	paid := stepJSON{Name: "paid"}
	if c == nil {
		credited.Text = "waiting for 2 of the 3 signers to confirm the burn and the relayer's Process; minutes"
		paid.Text = "after the credit, under the meters: the fee first, the rest in turns"
	} else {
		tr.To, tr.Amount, tr.Paid = c.To, c.Amount, c.Paid
		credited.Done, credited.Link = true, fmt.Sprintf(s.cfg.Gno.RealmLink, t.Realm)
		if h, err := deliveredAt(ctx, s.gno, s.cfg.Gno.Mailbox, id, 0); err == nil && h > 0 {
			credited.Text = fmt.Sprintf("a claim of %d base units for %s, credited at height %d", c.Amount, c.To, h)
		} else {
			credited.Text = fmt.Sprintf("a claim of %d base units for %s", c.Amount, c.To)
		}
		tr.State = "credited"
		switch {
		case c.Swept > 0:
			tr.State = "swept"
			credited.Text = fmt.Sprintf("%d base units in the sweep queue: paid to the owner only by a governance decision", c.Swept)
			paid.Text = "by a governance decision"
		case c.State == "voided" || c.State == "withheld" || c.State == "forged":
			tr.State = "voided"
			paid.Text = "governance voided this claim (" + c.State + "); a restore reopens it"
		case c.Paid >= c.Amount && c.Amount > 0:
			tr.State = "paid"
			paid.Done, paid.Text = true, fmt.Sprintf("paid in full: %d base units (the fee was the first installment)", c.Paid)
		case c.Paid > 0:
			tr.State = "paying"
			paid.Text = fmt.Sprintf("paid %d of %d so far, in installments under the meters (a tenth of the lock a day; a quarter of that per recipient)", c.Paid, c.Amount)
		default:
			paid.Text = "waiting its turn under the meters: the fee first, then the rest in installments"
		}
	}
	tr.Steps = append(tr.Steps, credited, paid)
	s.inWalkOf(ctx, ch, other, t, id, c != nil, b, tr)
	return tr, nil
}

// inWalkOf fills a burn's walkthrough from the index and fresh reads: the burn (the index's when tracked by id),
// the signers asked about its nonce until it is credited, the claim as the route renders it now, and the meters
// while it waits to be paid.
func (s *server) inWalkOf(ctx context.Context, ch *ChainConfig, other Other, t TokenConfig, id string, credited bool, b *Burn, tr *transferJSON) {
	threshold := thresholdOf(s.lastRule())
	st := s.activityNow(ctx, ch, other)
	if b == nil {
		if x, ok := st.other.Burns[id]; ok {
			b = &x
		}
	}
	var cr *claimRec
	if credited {
		if cj, err := s.claimRecord(ctx, t.Realm, id); err == nil && cj != nil {
			cr = foldClaim(st.claims[id], t.Symbol, cj, s.now().Unix()) // the index's reading keeps the payout's time when it saw one
		}
	}
	var signed []int
	if b != nil && cr == nil {
		signed = vouched(s.lamps(ctx, ch, int64(b.Nonce)))
	}
	burn := Burn{ID: id, Token: t.Symbol}
	if b != nil {
		burn = *b
	}
	f := s.inFacts(other, &burn, cr, signed, threshold, len(ch.Signers))
	tr.Walk, tr.At, tr.Height, tr.Vouched, tr.Threshold, tr.Signers = f.Walk, f.At, f.Height, f.Vouched, f.Threshold, f.Signers
	tr.Nonce = -1
	if b != nil {
		tr.Nonce = int64(b.Nonce)
	}
	if cr != nil && !cr.Final {
		if rv, err := agent.ReadRoute(ctx, s.gno, agent.Route{Symbol: t.Symbol, Gno: t.Realm, Token: t.Program}, 0); err == nil {
			tr.Meter = &meterJSON{Level: rv.Meter, Cap: rv.Cap, Recipient: rv.Cap / 4, Remaining: cr.Amount - cr.Paid, At: s.now().Unix()}
		}
	}
}

// chainName is the far side's name for the steps (the one implementation names itself in its config).
func (s *server) chainName(other Other) string {
	for id, o := range s.others {
		if o == other {
			if ch := s.cfg.Chain(id); ch != nil {
				return ch.Name
			}
		}
	}
	return "the other chain"
}
