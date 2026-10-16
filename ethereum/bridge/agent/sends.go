package agent

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/gnolang/bridges/solana/bridge/hl"
	"github.com/gnolang/bridges/ethereum/bridge/ethx"
	"github.com/gnolang/bridges/ethereum/bridge/gnoside"
)

// The sends in flight toward Ethereum, as the identity counts them over the ordered cut (eth/bridge/PLAN.md §5.6; the
// plan red team's E2-RT9): every route send hlmailbox dispatched at or below the cut's gno height H — a message to a
// route's token, read at H — whose delivery Ethereum's mailbox has not marked at the cut's finalized block F (both
// providers, pinned to F's hash), and which gno.land has not refunded at H (a refund takes the amount out of the lock;
// read only where a mark can be set). Such a send is in the lock and not in the supply, so the identity's right side
// is short by it; the signers and the monitors both count it in flight, subtracting it from the lock's room — on
// Ethereum a send waits hours by design (its fees, its batch, its six hours), and a signer that left it out would let
// a forged mint up to the sum of the waiting sends pass unflagged and be burned back as a burn it signs.

// sendIndex is every route send read so far, by gno nonce, with the walk's place: the monitor's state carries it (its
// fields promoted into the state's JSON as they always were), the signer's state carries its own.
type sendIndex struct {
	Sends        map[string]*monSend `json:"sends"`    // by gno nonce
	SendNext     uint32              `json:"sendNext"` // the next hlmailbox nonce to read
	RefundCounts map[string]int64    `json:"refundCounts,omitempty"`
}

func (x *sendIndex) init() {
	if x.Sends == nil {
		x.Sends = map[string]*monSend{}
	}
	if x.RefundCounts == nil {
		x.RefundCounts = map[string]int64{}
	}
}

// sendsPerRound bounds the messages a signer's index reads in one round (a fresh index catches up over rounds, with no
// verdict until it reaches the mailbox's count at H); the monitor reads them all (0).
const sendsPerRound = 1000

// sync reads hlmailbox's messages after the walk's place up to the mailbox's count at H (at most max, 0 for all; done
// says whether it reached the count) — a token's sends only, the messages a route realm of its line sent (E3-CF1), the
// walk held at one whose sender it cannot judge while the token's line is unread (lineErrs, G46) — marks the ones
// Ethereum's mailbox delivered at the pinned block F through the pair, and the ones gno.land refunded at H. It answers
// how many sends it found delivered; err is a reading the walk or the marks could not make (no verdict), refundErr a
// refund mark it could not read (the send stays as it was).
func (x *sendIndex) sync(ctx context.Context, g gnoside.Reader, pair ethx.Pair, cfg *Config, mb *gnoside.Mailbox, routes map[string]*gnoside.Route, migs map[string]*gnoside.Migration, lineErrs map[string]error, H int64, pin ethx.At, now time.Time, max int) (delivered int, done bool, refundErr, err error) {
	x.init()
	read := 0
	for n := x.SendNext; n < mb.Count; n++ {
		if max > 0 && read >= max {
			break
		}
		read++
		s, err := g.RenderAt(ctx, cfg.Gno.Mailbox, "msg/"+strconv.FormatUint(uint64(n), 10), H)
		if err != nil {
			return 0, false, nil, err // the walk cannot pass a nonce it has not read
		}
		msg, err := gnoside.ParseMsg(s, mb.Domain, n)
		if err != nil {
			return 0, false, nil, err
		}
		rt := sendRoute(cfg, msg)
		if rt != nil && !routeLineSender(rt, migs[rt.Symbol], msg.Msg.Sender) {
			if lineErrs[rt.Symbol] != nil {
				break // nor past a message whose sender it cannot judge (G46): a later round reads the line
			}
			// a stranger's message to the token (E3-CF1): hlmailbox's Dispatch is open to anyone and the token refuses
			// a sender that is not its enrolled router, so counted it would stand in flight for good with whatever
			// amount its body says — every signer halted and the monitors flagging, for one transaction's fee
			rt = nil
		}
		x.SendNext = n + 1
		if rt == nil {
			continue // a decision to HlAdmin, another recipient, or a stranger's message: no send of a route
		}
		_, amount, _, err := hl.ParseTokenMessage(msg.Msg.Body)
		if err != nil || amount > uint64(maxUnits) {
			continue
		}
		x.Sends[strconv.FormatUint(uint64(n), 10)] = &monSend{Symbol: rt.Symbol, Amount: int64(amount), Height: msg.Height, Seen: now.Unix(), ID: msg.IDHex()}
	}
	done = x.SendNext >= mb.Count
	nonces := monSortedNonces(x.Sends)
	var ids [][32]byte
	var which []string
	for _, n := range nonces {
		s := x.Sends[n]
		if s.Delivered {
			continue
		}
		id, err := hl.ParseKey32(s.ID)
		if err != nil {
			continue
		}
		ids = append(ids, id)
		which = append(which, n)
	}
	for start := 0; start < len(ids); start += 100 {
		end := min(start+100, len(ids))
		marks, err := ethx.DeliveredMany(ctx, pair, Address(cfg.Eth.Mailbox), ids[start:end], pin)
		if err != nil {
			return delivered, done, nil, fmt.Errorf("the delivered marks of messages %s to %s: %w", which[start], which[end-1], err)
		}
		for i, ok := range marks {
			if ok {
				x.Sends[which[start+i]].Delivered = true
				delivered++
			}
		}
	}
	// the refund marks (a successor's duty over the sends it imported; never a live route's)
	rose := map[string]bool{}
	counts := map[string]int64{}
	for _, r := range cfg.Routes {
		counts[r.Symbol] = monRefundCount(routes[r.Symbol], migs[r.Symbol])
		last, known := x.RefundCounts[r.Symbol]
		rose[r.Symbol] = !known || counts[r.Symbol] > last
	}
	failed := map[string]bool{}
	var firstErr error
	for _, n := range nonces {
		s := x.Sends[n]
		rt := cfg.RouteBySymbol(s.Symbol)
		if rt == nil || !gnoside.Refundable(routes[s.Symbol], migs[s.Symbol]) {
			continue
		}
		switch {
		case s.doubleMinted():
			if now.Unix()-s.RefundRead < 60 {
				continue
			}
		case s.Refunded:
			continue
		case s.RefundRead != 0 && !rose[s.Symbol]:
			continue
		}
		sd, err := gnoside.SendOnLine(ctx, g, rt.Gno, migs[s.Symbol], s.ID, H)
		if err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("message %s's send record: %w", n, err)
			}
			failed[s.Symbol] = true
			continue
		}
		s.RefundRead = now.Unix()
		if sd == nil {
			continue
		}
		s.Refunded = s.Refunded || bool(sd.Refunded)
		s.WrittenOff = s.WrittenOff || bool(sd.WrittenOff)
	}
	for sym, count := range counts {
		if !failed[sym] {
			x.RefundCounts[sym] = count
		}
	}
	return delivered, done, firstErr, nil
}

// sendRoute is the route whose token a message to Ethereum is addressed to, or nil.
func sendRoute(cfg *Config, msg *gnoside.Msg) *Route {
	if msg.Msg.Destination != cfg.Eth.Domain {
		return nil
	}
	token, ok := ethx.AddressFrom32(msg.Msg.Recipient)
	if !ok {
		return nil
	}
	return cfg.RouteByToken(token)
}

// routeLineSender says whether a sender is a route realm of the token's line: the configured realm, or a realm of its
// migration (nil when the route never migrated). The relayers deliver only such a message (K34) and the signers, the
// monitors and hlwatch count only such a message as a send (E3-CF1).
func routeLineSender(rt *Route, mig *gnoside.Migration, sender [32]byte) bool {
	if gnoside.RealmKey(rt.Gno) == sender {
		return true
	}
	if mig != nil {
		for _, p := range mig.Paths() {
			if gnoside.RealmKey(p) == sender {
				return true
			}
		}
	}
	return false
}

// prune drops a delivered send that can no longer be refunded (a live route's): it counts nothing more.
func (x *sendIndex) prune(routes map[string]*gnoside.Route, migs map[string]*gnoside.Migration) {
	for n, s := range x.Sends {
		if s.Delivered && !s.Refunded && !gnoside.Refundable(routes[s.Symbol], migs[s.Symbol]) {
			delete(x.Sends, n)
		}
	}
}

// inflight sums by symbol the sends in flight: undelivered at F and not refunded at H.
func (x *sendIndex) inflight() map[string]int64 {
	out := map[string]int64{}
	for _, s := range x.Sends {
		if s.inFlight() {
			out[s.Symbol] = satAdd(out[s.Symbol], s.Amount)
		}
	}
	return out
}
