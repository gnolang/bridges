package agent

import (
	"context"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/gnolang/bridges/solana/bridge/hl"
	"github.com/gnolang/bridges/solana/bridge/sol"
)

// The refund's gate is Solana's enrolment, not the realm's note (F27, AS2, K38): a refund of a successor's
// imported sends is safe only once the token program's enrolled router for gno.land's domain is the refunding
// successor — before that the predecessor is still the router, and a refunded send's message is deliverable by
// anyone's Reemit with the published tool: minted twice, the write-off alone closing the identity. The realm
// reads r/hlgov's enrolment note alone (the kind 6's dispatch; 14 days before the refund, R37), never Solana:
// a kind 6 executes 7 days after its delivery there, deferred by any lone flag or a hold, cancelled by a kind 1,
// lost to a foreign number — the note's days always expire first. So hlwatch reads the router itself and the
// kind 6's state, before the refund proposal and before GovDAO's apply of the queued decision (AT2: the realm's
// Execute refuses a refund; the apply-refund builder is GovDAO's at-once act, and the member reads this gate
// before proposing or voting it — the relayer never applies a refund).

// RouterView is a token program's enrolled router for gno.land's domain as read on Solana.
type RouterView struct {
	Enrolled bool
	Router   [32]byte
	Realm    string // the realm of the token's line the router is, "" when it is none of them
}

// Text names the router.
func (rv RouterView) Text() string {
	if !rv.Enrolled {
		return "none (no router is enrolled for gno.land's domain)"
	}
	if rv.Realm != "" {
		return hex.EncodeToString(rv.Router[:]) + " (" + rv.Realm + ")"
	}
	return hex.EncodeToString(rv.Router[:]) + " (no realm of the token's line)"
}

// Is says whether the router is a realm, by its package address.
func (rv RouterView) Is(realm string) bool {
	return rv.Enrolled && rv.Router == RealmKey(realm)
}

// RealmKey is a realm's package address as a 32-byte key (the sender a Hyperlane message carries).
func RealmKey(path string) [32]byte {
	k, _ := hl.GnoAddressToBytes32(hl.GnoPackageAddress(path))
	return k
}

// routerView reads a token program's enrolled router for a domain and names it among a line's realms.
func routerView(data []byte, domain uint32, realms []string) (RouterView, error) {
	tok, err := sol.ParseToken(data)
	if err != nil {
		return RouterView{}, err
	}
	router, ok := tok.RouterFor(domain)
	rv := RouterView{Enrolled: ok, Router: router}
	if ok {
		for _, p := range realms {
			if RealmKey(p) == router {
				rv.Realm = p
			}
		}
	}
	return rv, nil
}

// Kind6State is where r/hlgov's latest kind 6 enrolling a router stands on the admin program.
type Kind6State struct {
	Number uint64 // r/hlgov's number, 0 when none was dispatched (or r/hlgov was not read)
	State  string
}

// kind6Back is how many of r/hlgov's numbers are walked back for the kind 6 that enrolled a route (a refund
// proposal or queued refund stands for days; the walk runs only then).
const kind6Back = 200

// FindEnrolment finds r/hlgov's latest kind 6 enrolling router as the token's for gno.land's domain among its
// last kind6Back decisions at a height: the number, 0 when none.
func FindEnrolment(ctx context.Context, g gnoReader, gov string, last uint64, H int64, token byte, domain uint32, router [32]byte) (uint64, error) {
	for n := last; n >= 1 && n+uint64(kind6Back) > last; n-- {
		body, _, err := govDecision(ctx, g, gov, n, H)
		if err != nil {
			return 0, err
		}
		if body == nil || body[0] != sol.KindEnrollRouter {
			continue
		}
		p, err := sol.ParseParams(sol.KindEnrollRouter, body[9:])
		if err != nil {
			continue
		}
		if p.Token == token && p.Domain == domain && p.Router == router {
			return n, nil
		}
	}
	return 0, nil
}

// Kind6StateOf judges a dispatched kind 6's number against the admin program: not recorded yet (above its last
// number), pending (an account stands, deferred or not), cancelled (a stored range), or recorded and gone
// (executed, or spent) — in which case the router tells which. 0 is absent.
func Kind6StateOf(n uint64, adminLast uint64, pending []PendingSolDecision, cancelled []sol.CancelledRange) Kind6State {
	if n == 0 {
		return Kind6State{State: "absent: r/hlgov dispatched no kind 6 enrolling this realm as the token's router"}
	}
	if n > adminLast {
		return Kind6State{n, fmt.Sprintf("dispatched as r/hlgov's #%d, not yet recorded on the admin program (its last number is %d): the relayer delivers it, then 7 days pass on Solana's clock", n, adminLast)}
	}
	for _, d := range pending {
		if d.Number == n {
			s := fmt.Sprintf("pending on the admin program as #%d, matures at %s", n, time.Unix(d.MaturesAt, 0).UTC().Format(time.RFC3339))
			if d.Deferred != "" {
				s += " (" + d.Deferred + ")"
			}
			return Kind6State{n, s}
		}
	}
	for _, r := range cancelled {
		if r.From <= n && n <= r.To {
			return Kind6State{n, fmt.Sprintf("CANCELLED on the admin program (#%d is in a cancelled range): the note stands on gno.land, the enrolment never happened — propose the kind 6 again", n)}
		}
	}
	return Kind6State{n, fmt.Sprintf("recorded on the admin program as #%d and gone (executed, or spent on its own checks) while the router is not this realm: spent, or a later kind 6 enrolled another — read the admin program's records", n)}
}

// RefundGate is the refund's verdict on Solana for a realm: ok when the token's enrolled router for gno.land's
// domain is the realm; the text says why not — the router found and the kind 6's state — or that it is.
func RefundGate(symbol, realm string, domain uint32, rv *RouterView, k6 Kind6State) (text string, ok bool) {
	if rv == nil {
		return "NOT JUDGED ON SOLANA: the " + symbol + " token program's enrolled router was not read (the refund's gate is the enrolment on Solana, F27); a refund now is judged on the note alone", false
	}
	if rv.Is(realm) {
		return fmt.Sprintf("ENROLLED ON SOLANA: the %s token program's router for gno.land's domain %d is this realm (the kind 6 executed; r/hlgov's #%d)", symbol, domain, k6.Number), true
	}
	return fmt.Sprintf("NOT ENROLLED ON SOLANA: the %s token program's router for gno.land's domain %d is %s, not this realm, and the kind 6 enrolling it is %s — a refund now lets a refunded sender deliver his send through the predecessor's router with the published tool (anyone's Reemit): minted twice, closed by the write-off alone; the refund waits on the router whatever the note says (F27, AS2)", symbol, domain, rv.Text(), k6.State), false
}
