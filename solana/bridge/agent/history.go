package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/gagliardetto/solana-go"
	"github.com/gagliardetto/solana-go/rpc"
	"github.com/gagliardetto/solana-go/rpc/jsonrpc"
)

// A provider's history and the scan's cursor (the round-6 live-fixes audit's E6-SF1, high, live on 2026-10-15). Every
// machine lists each route's token program with getSignaturesForAddress from a cursor — the newest signature it listed —
// given as `until`. publicnode keeps about a day of ledger (its first available block ≈400,000 slots back, measured
// 2026-10-15) and refuses an `until` below it with −32020 "Transaction … not found", and a scan that failed kept its
// cursor: a route with no transaction for a day (BUBBLE has had none since its launch) left every machine that lists
// through publicnode failing for good — signers 1 and 2 halted at the hour, monitor 2 without a verdict, the backup
// relayer deaf, Solana → gno.land on signer 3's signature alone. A listing without `until` is complete from the
// provider's first available block on (measured: publicnode lists exactly the GNOT program's signatures at or above it,
// and none of BUBBLE's), so a cursor now carries the slot its route was read through — every transaction the provider
// lists for the route at or below it was read — which moves with every reading (a listing held to the supplies' slot
// by minContextSlot reaches that slot), not only with the route's own transactions. A provider that does not know the
// cursor's signature is listed back from its newest signature without `until`, to that slot; one whose history begins
// above it cannot say what lies between, and is refused for the route (solana:history:<i>): its token has no verdict on
// the monitors and in hlwatch, the signer's round has none — never a reading on the other provider's word — until a
// provider that keeps the route's history is named. The relayer, which reads one provider, does not take up that
// route's new burns meanwhile (the other relayer, or anyone, may bring them).

// scanCursor is how far one provider's listing of a route was read: Sig, the newest signature it listed (the next
// listing's `until`; "" for none), and Through, a slot through which every transaction it lists for the route was read
// (0 when not known — a state written before it: the listing must then find the cursor's own signature).
type scanCursor struct {
	Sig     string
	Through uint64
}

// historyGap is a provider that cannot list a route back to where it was read: its history begins above the slot the
// route was read through, or its listing does not show the cursor's signature (E6-SF1). No reading rests on it.
type historyGap struct{ why string }

func (g *historyGap) Error() string { return g.why }

// asHistoryGap is the history gap an error carries, if any.
func asHistoryGap(err error) (*historyGap, bool) {
	var g *historyGap
	ok := errors.As(err, &g)
	return g, ok
}

// sigUnknown says whether a listing failed because the provider does not know a signature it was given (`until` or
// `before`): publicnode's −32020 "Transaction <signature> not found" (measured), or any answer naming the signature.
func sigUnknown(err error, sig solana.Signature) bool {
	if err == nil || sig.IsZero() {
		return false
	}
	var re *jsonrpc.RPCError
	if errors.As(err, &re) && re.Code == -32020 {
		return true
	}
	return strings.Contains(err.Error(), sig.String())
}

const (
	listLimit = 1000 // a listing's page, the most a provider gives
	// backPagesUnknown bounds a listing back from the newest signature that looks for the cursor's own signature (a state
	// with no slot read through): ten thousand signatures; one with the slot stops there.
	backPagesUnknown = 10
	backPagesMax     = 1000
)

// listed is a listing of a route's signatures: those past the cursor, newest first; the newest signature the provider
// lists for the route at all (nil when none, or none past the cursor); and whether it was read back from the newest
// without `until`.
type listed struct {
	sigs   []*rpc.TransactionSignature
	newest *rpc.TransactionSignature
	back   bool
}

// listPage is one page of a route's signatures, newest first, held to minSlot (minContextSlot) when it is not 0.
func listPage(ctx context.Context, c solReader, route solana.PublicKey, until, before solana.Signature, minSlot uint64) ([]*rpc.TransactionSignature, error) {
	limit := listLimit
	opts := &rpc.GetSignaturesForAddressOpts{Limit: &limit, Commitment: rpc.CommitmentFinalized, Until: until, Before: before}
	if minSlot > 0 {
		opts.MinContextSlot = &minSlot
	}
	return c.GetSignaturesForAddressWithOpts(ctx, route, opts)
}

// listAfter lists a route's signatures past a cursor: with the cursor's signature as `until` while the provider knows
// it; back from the newest to the slot the route was read through when it does not, or when the cursor names a slot
// and no signature; every signature from a cursor of neither (a scan from nothing).
func listAfter(ctx context.Context, c solReader, route solana.PublicKey, cur scanCursor, minSlot uint64) (listed, error) {
	var until solana.Signature
	switch {
	case cur.Sig != "":
		s, err := solana.SignatureFromBase58(cur.Sig)
		if err != nil {
			return listed{}, err
		}
		until = s
	case cur.Through > 0:
		return listBack(ctx, c, route, cur, minSlot)
	}
	var out listed
	var before solana.Signature
	for {
		page, err := listPage(ctx, c, route, until, before, minSlot)
		if err != nil {
			if sigUnknown(err, until) || sigUnknown(err, before) {
				return listBack(ctx, c, route, cur, minSlot)
			}
			return listed{}, err
		}
		out.sigs = append(out.sigs, page...)
		if len(page) < listLimit {
			break
		}
		before = page[len(page)-1].Signature
	}
	if len(out.sigs) > 0 {
		out.newest = out.sigs[0]
	}
	return out, nil
}

// firstBlockReader is a provider's getFirstAvailableBlock (the RPC client's): the lowest slot its ledger serves — 0 for
// one with long-term storage (api.mainnet-beta, measured), ≈a day back on publicnode.
type firstBlockReader interface {
	GetFirstAvailableBlock(ctx context.Context) (uint64, error)
}

// listBack lists a route's signatures back from the newest, without `until`, to the slot the route was read through —
// or, with none known, to the cursor's own signature — and answers those above that point. A listing that ends first is
// complete only when the provider's history begins at or below the slot after the one read through (its first available
// block: a quiet route's listing on a provider with a day of ledger is empty and complete); else what lies between is
// unknown — a historyGap, never a guess (E6-SF1).
func listBack(ctx context.Context, c solReader, route solana.PublicKey, cur scanCursor, minSlot uint64) (listed, error) {
	out := listed{back: true}
	pages := backPagesMax
	if cur.Through == 0 {
		pages = backPagesUnknown
	}
	var before solana.Signature
	for range pages {
		page, err := listPage(ctx, c, route, solana.Signature{}, before, minSlot)
		if err != nil {
			return listed{}, err
		}
		if out.newest == nil && len(page) > 0 {
			out.newest = page[0]
		}
		for _, s := range page {
			if (cur.Through > 0 && s.Slot <= cur.Through) || (cur.Through == 0 && s.Signature.String() == cur.Sig) {
				return out, nil // reached: everything from here down was read
			}
			out.sigs = append(out.sigs, s)
		}
		if len(page) < listLimit {
			if cur.Through == 0 {
				return listed{}, &historyGap{fmt.Sprintf("its listing ends without the cursor %s, and no slot the route was read through is known (a state from before E6-SF1): what lies between is not listed", shortSig(cur.Sig))}
			}
			fb, ok := c.(firstBlockReader)
			if !ok {
				return listed{}, &historyGap{"it answers no getFirstAvailableBlock, so a listing that ends cannot be told complete"}
			}
			first, err := fb.GetFirstAvailableBlock(ctx)
			if err != nil {
				return listed{}, &historyGap{fmt.Sprintf("its first available block could not be read (%v), so whether its listing reaches back to slot %d is unknown", err, cur.Through)}
			}
			if first <= cur.Through+1 {
				return out, nil // its history reaches the slot after the one read through: the listing is whole
			}
			return listed{}, &historyGap{fmt.Sprintf("its history begins at slot %d, above slot %d through which the route was read: the transactions between are not listed (−32020 \"Transaction … not found\" for the cursor, or a listing that ends there)", first, cur.Through)}
		}
		before = page[len(page)-1].Signature
	}
	if cur.Through == 0 {
		return listed{}, &historyGap{fmt.Sprintf("%d signatures listed back from the newest without the cursor %s (no slot the route was read through is known)", pages*listLimit, shortSig(cur.Sig))}
	}
	return listed{}, &historyGap{fmt.Sprintf("%d signatures listed back from the newest without reaching slot %d through which the route was read", pages*listLimit, cur.Through)}
}

// shortSig is a signature's first characters, for a line.
func shortSig(s string) string {
	if len(s) > 12 {
		return s[:12] + "…"
	}
	return s
}

// historyGaps are a machine's standing refusals of a provider for a route's history, by "SYM#i" (i from 0): why, and
// since when (E6-SF1). A gap sets one, a listing that reaches back ends it, a listing that fails otherwise leaves it as
// it was (that reading has no verdict on its own account).
type historyGaps struct {
	why   map[string]string
	since map[string]time.Time
}

func (h *historyGaps) set(key, why string, now time.Time) {
	if h == nil {
		return
	}
	if h.why == nil {
		h.why, h.since = map[string]string{}, map[string]time.Time{}
	}
	if _, ok := h.since[key]; !ok {
		h.since[key] = now
	}
	h.why[key] = why
}

func (h *historyGaps) end(key string) {
	if h == nil {
		return
	}
	delete(h.why, key)
	delete(h.since, key)
}

// alarm raises solana:history:<p> for a provider refused for some route once the refusal has stood two minutes (a
// provider's hiccup at its first available block passes in silence), and clears it for a provider refused for none.
func (h *historyGaps) alarm(a *Alarmer, now time.Time) {
	for p := 1; p <= 2; p++ {
		text := historyText(h.why, p)
		if text == "" {
			a.Clear(historyKey(p))
			continue
		}
		for key, t := range h.since {
			if strings.HasSuffix(key, fmt.Sprintf("#%d", p-1)) && now.Sub(t) >= cutFailAfter {
				a.Alarm(historyKey(p), text)
				break
			}
		}
	}
}

// providerHost is a provider as a cursor's slot remembers it: its URL cut to scheme://host (Redact: no key kept).
func providerHost(url string) string { return strings.TrimSuffix(Redact(url), "/…") }

// keepThroughOf drops the slots read through ("SYM#i" keys of through) of each provider the config no longer names at its
// place, and answers the providers now named: a slot one provider's listing gave stands only while that provider is the
// one asked (the review of lane s7, R3: a liar's slot, kept by its place, outlived its replacement, which listed back to
// it and never listed a burn below it). A state with no record of its providers drops nothing.
func keepThroughOf(through map[string]uint64, was []string, urls []string) []string {
	now := make([]string, len(urls))
	for i, u := range urls {
		now[i] = providerHost(u)
	}
	for i := range now {
		if i >= len(was) || was[i] == "" || was[i] == now[i] {
			continue
		}
		suffix := fmt.Sprintf("#%d", i)
		for key := range through {
			if strings.HasSuffix(key, suffix) {
				delete(through, key)
			}
		}
	}
	return now
}

// ProvidersAre keeps the state's slots read through only for the providers the config names at the places they were
// read through (keepThroughOf; hlwatch calls it at its start).
func (s *WatchState) ProvidersAre(urls [2]string) {
	s.init()
	s.ThroughHosts = keepThroughOf(s.Through, s.ThroughHosts, urls[:])
}

// historyKey is the alarm of a provider refused for a route's history (E6-SF1), by its place in the config (1 or 2).
func historyKey(provider int) string { return fmt.Sprintf("solana:history:%d", provider) }

// historyText is the line of the routes a provider is refused for: gaps by "SYM#i" (i from 0), the provider 1-based.
func historyText(gaps map[string]string, provider int) string {
	var parts []string
	for _, key := range sortedKeys(gaps) {
		sym, i, ok := strings.Cut(key, "#")
		if !ok || i != fmt.Sprint(provider-1) {
			continue
		}
		parts = append(parts, sym+": "+gaps[key])
	}
	if len(parts) == 0 {
		return ""
	}
	return fmt.Sprintf("Solana provider %d cannot list the route's token program back to where it was read (%s): no reading rests on its listing of that route — its token has no verdict, never a reading on the other provider's word — until the config names a provider that keeps the route's history (one whose getSignaturesForAddress answers an `until` a week old; publicnode keeps about a day, E6-SF1)", provider, strings.Join(parts, "; "))
}

// gapsOf names, by symbol, the routes some provider is refused for: gaps by "SYM#i".
func gapsOf(gaps map[string]string) map[string]string {
	out := map[string]string{}
	for _, key := range sortedKeys(gaps) {
		sym, i, _ := strings.Cut(key, "#")
		n := 0
		_, _ = fmt.Sscan(i, &n)
		line := fmt.Sprintf("provider %d cannot list it back to where it was read (%s; solana:history:%d)", n+1, gaps[key], n+1)
		if out[sym] == "" {
			out[sym] = line
		} else {
			out[sym] += "; " + line
		}
	}
	return out
}
