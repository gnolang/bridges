package agent

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/gagliardetto/solana-go"
	"github.com/gagliardetto/solana-go/rpc"
	"github.com/gagliardetto/solana-go/rpc/jsonrpc"

	"github.com/gnolang/bridges/solana/bridge/gno"
	"github.com/gnolang/bridges/solana/bridge/hl"
	"github.com/gnolang/bridges/solana/bridge/sol"
)

// What the realms render for the machines (realm/hlmailbox/render.gno, p/hlroute's JSON, p/decisions' JSON):
// every read here is of a JSON endpoint, at the latest height or pinned at one.

// ------------------------------------------------------------- gno.land --

// gnoReader is the read side of a gno node, which a fake supplies in tests.
type gnoReader interface {
	RenderAt(ctx context.Context, pkgPath, path string, height int64) (string, error)
	Status(ctx context.Context) (gno.Head, error)
	Block(ctx context.Context, height int64) (gno.Head, error)
}

// gnoCaller is a gno node a key signs through.
type gnoCaller interface {
	gnoReader
	Call(ctx context.Context, pkgPath, fn string, gasWanted int64, args ...string) (*gno.Result, error)
}

// headWait is the longest a machine waits for a gno block after its Solana snapshot (mainnet's blocks come every
// 3.4 s; a wait past this means the node is stalled, which the monitor alarms on its own).
const headWait = 15 * time.Second

// headAfter is the cut's second step: the own node's head once its block time is after t. It polls the node
// until then, or until headWait has passed, in which case the head returned is not after t and the cut fails.
func headAfter(ctx context.Context, g gnoReader, t time.Time) (gno.Head, error) {
	deadline := time.Now().Add(headWait)
	for {
		head, err := g.Status(ctx)
		if err != nil {
			return head, err
		}
		if head.Time.After(t) || time.Now().After(deadline) {
			return head, nil
		}
		select {
		case <-ctx.Done():
			return head, ctx.Err()
		case <-time.After(300 * time.Millisecond):
		}
	}
}

// Clocks is every realm's "clocks" (p/decisions' ClocksJSON): the compiled clock factor and the delays it gives,
// in seconds — the 7-day delay and the 14-day deadline divided by the factor (1 on mainnet, 8 on a devnet build;
// the meters are rates per real day and are not divided).
type Clocks struct {
	Factor   int64 `json:"factor"`
	Delay    int64 `json:"delay"`
	Deadline int64 `json:"deadline"`
}

// DeadlineDuration is the realms' proposal deadline as this chain divides it, or ProposalDeadline when the realm
// renders none (a realm from before the clocks).
func (c Clocks) DeadlineDuration() time.Duration {
	if c.Deadline > 0 {
		return time.Duration(c.Deadline) * time.Second
	}
	return ProposalDeadline
}

func (c Clocks) String() string {
	if c.Factor == 0 {
		return "not rendered (a realm from before the clocks)"
	}
	return fmt.Sprintf("factor %d: the 7-day delay is %s, the 14-day deadline %s", c.Factor, (time.Duration(c.Delay) * time.Second).String(), (time.Duration(c.Deadline) * time.Second).String())
}

// mailboxJSON is hlmailbox's Render("json").
type mailboxJSON struct {
	Domain     uint32 `json:"domain"`
	Hook       string `json:"hook"`
	Address    string `json:"address"`
	Count      uint32 `json:"count"`
	Root       string `json:"root"`
	Delivered  int    `json:"delivered"`
	Live       bool   `json:"live"`
	LaunchedAt int64  `json:"launchedAt"`
	// Governor is the compiled governor (M1k): the address whose own call through the realm's Govern decides
	// until its one-way Yield on hlmailbox, "" when gno.land/r/gov/dao governs from the first block; Yielded the
	// block height of the yield, 0 until it. Every bridge realm renders both; the mailbox's is what they read.
	Governor     string `json:"governor"`
	Yielded      int64  `json:"yielded"`
	LaunchOrigin uint32 `json:"launchOrigin"`
	Clocks       Clocks `json:"clocks"`
	Decisions    int64  `json:"decisions"`
	Signers      struct {
		Keys      []string `json:"keys"`
		Threshold int      `json:"threshold"`
		GroupA    []string `json:"groupA"`
		GroupB    []string `json:"groupB"`
	} `json:"signers"`
	Verifiers map[string]struct {
		PkgPath    string `json:"pkgpath"`
		Registered bool   `json:"registered"`
	} `json:"verifiers"`
	Registered []string              `json:"registered"`
	Seats      []string              `json:"seats"`
	Routes     map[string]routeFlags `json:"routes"`
	// DroppedRoutes are the successors a re-point dropped — routes no more, tombstones (M1i, R35): dropped pkgpath
	// → the predecessor that re-pointed away from it. A dropped path is no hop of any line (its sends are refused
	// on gno.land, its messages the line's last realm's); the enrolled router being one is AS1's alarm.
	DroppedRoutes map[string]string `json:"droppedRoutes"`
	// SuccessorRoutes is the route each announced successor announced for (AQ5: pkgpath → "GNOT" or "BUBBLE";
	// a migrate names only one announced for its own route).
	SuccessorRoutes map[string]string `json:"successorRoutes"`
}

// routeFlags is one route's flags as hlmailbox renders them, with its place in a migration (M1g: AddRoute
// records the predecessor of a successor and the successor of a migrated route, the moment the migrate applies).
type routeFlags struct {
	Epoch       int64    `json:"epoch"`
	EpochAt     int64    `json:"epochAt"`
	Sends       bool     `json:"sends"`
	Credits     bool     `json:"credits"`
	Payouts     bool     `json:"payouts"`
	Flags       []string `json:"flags"` // "fn by seat N (addr) at T[ naming ids]"
	Predecessor string   `json:"predecessor"`
	Successor   string   `json:"successor"`
	// Enrolled is the record r/hlgov's kind 6 left when it enrolled this route as the token's router on Solana
	// (M1h, AR1): the height (hlmailbox.NoteEnrolled, from the kind-6 closure), and EnrolledAt the unix time of
	// that dispatch; 0 for a route never enrolled. A refund of the sends a successor imported is refused by the
	// realm until the note is 7 days old, and hlwatch reads a refund proposal against it.
	Enrolled   enrollMark `json:"enrolled"`
	EnrolledAt int64      `json:"enrolledAt"`
	// Pulled says this successor pulled page 0 (M1i, R35: a kind 6 enrols it only then, and no re-point drops it
	// after); Dropped lists the successors this route's re-points dropped (tombstones, the mailbox's droppedRoutes
	// naming each with this route as the predecessor).
	Pulled  bool     `json:"pulled"`
	Dropped []string `json:"dropped"`
}

// Enrollment is the route's kind-6 enrolment as read: the height and the time, the time from `enrolledAt` when
// the mark itself names none.
func (r routeFlags) Enrollment() (height, at int64) {
	height, at = r.Enrolled.Height, r.Enrolled.At
	if at == 0 {
		at = r.EnrolledAt
	}
	return height, at
}

// enrollMark is a route's kind-6 enrolment record as hlmailbox may render it (AR1): a bare number — the height,
// or the unix time when it is one (a billion and more: no height reaches it for decades) — or an object naming
// the height and the time; null, false or "" for a route never enrolled.
type enrollMark struct {
	Height int64
	At     int64
}

// Set says whether the route was enrolled at all.
func (e enrollMark) Set() bool { return e.Height > 0 || e.At > 0 }

func (e *enrollMark) UnmarshalJSON(b []byte) error {
	s := strings.TrimSpace(string(b))
	switch {
	case s == "null", s == "false", s == `""`, s == "0":
		*e = enrollMark{}
		return nil
	case strings.HasPrefix(s, "{"):
		var o struct {
			Height     int64 `json:"height"`
			At         int64 `json:"at"`
			Time       int64 `json:"time"`
			EnrolledAt int64 `json:"enrolledAt"`
		}
		if err := json.Unmarshal(b, &o); err != nil {
			return err
		}
		at := o.At
		if at == 0 {
			at = o.Time
		}
		if at == 0 {
			at = o.EnrolledAt
		}
		*e = enrollMark{Height: o.Height, At: at}
		return nil
	}
	n, err := strconv.ParseInt(strings.Trim(s, `"`), 10, 64)
	if err != nil {
		return fmt.Errorf("an enrolment record of %s", s)
	}
	if n >= 1_000_000_000 {
		*e = enrollMark{At: n}
	} else {
		*e = enrollMark{Height: n}
	}
	return nil
}

var flagLineRE = regexp.MustCompile(`^(\w+) by seat (\d+) \(([^)]*)\) at (\d+)(?: naming (.*))?$`)

// Flag is one standing flag, parsed.
type Flag struct {
	Fn   string
	Seat int
	Addr string
	At   int64
	IDs  []string
}

// parseFlag reads one of hlmailbox's flag lines.
func parseFlag(line string) (Flag, bool) {
	m := flagLineRE.FindStringSubmatch(line)
	if m == nil {
		return Flag{}, false
	}
	f := Flag{Fn: m[1], Addr: m[3]}
	f.Seat, _ = strconv.Atoi(m[2])
	f.At, _ = strconv.ParseInt(m[4], 10, 64)
	if m[5] != "" {
		f.IDs = strings.Split(m[5], ",")
	}
	return f, true
}

// StandingFlags parses a route's flag lines.
func (r routeFlags) StandingFlags() []Flag {
	var out []Flag
	for _, l := range r.Flags {
		if f, ok := parseFlag(l); ok {
			out = append(out, f)
		}
	}
	return out
}

// Rule is hlmailbox's signer set and rule as the verifier applies it: a threshold over the set, or one key of
// each group (PLAN.md §11.13).
type Rule struct {
	Keys      []ed25519.PublicKey
	Threshold int
	GroupA    []int // indexes into Keys
	GroupB    []int
}

func (r Rule) TwoGroups() bool { return len(r.GroupA) > 0 && len(r.GroupB) > 0 }

func (r Rule) String() string {
	if r.TwoGroups() {
		return fmt.Sprintf("one of group A %v and one of group B %v of %d signers", r.GroupA, r.GroupB, len(r.Keys))
	}
	return fmt.Sprintf("%d of %d signers", r.Threshold, len(r.Keys))
}

// Index is a key's place in the set, −1 if none.
func (r Rule) Index(k ed25519.PublicKey) int {
	for i, x := range r.Keys {
		if bytes.Equal(x, k) {
			return i
		}
	}
	return -1
}

func (m *mailboxJSON) rule() (Rule, error) {
	var r Rule
	for _, s := range m.Signers.Keys {
		b, err := hex.DecodeString(s)
		if err != nil || len(b) != ed25519.PublicKeySize {
			return r, fmt.Errorf("hlmailbox: signer key %q", s)
		}
		r.Keys = append(r.Keys, ed25519.PublicKey(b))
	}
	r.Threshold = m.Signers.Threshold
	group := func(ks []string) ([]int, error) {
		var out []int
		for _, s := range ks {
			b, err := hex.DecodeString(s)
			if err != nil {
				return nil, err
			}
			i := r.Index(ed25519.PublicKey(b))
			if i < 0 {
				return nil, errors.New("hlmailbox: a group key outside the set: " + s)
			}
			out = append(out, i)
		}
		return out, nil
	}
	var err error
	if r.GroupA, err = group(m.Signers.GroupA); err != nil {
		return r, err
	}
	if r.GroupB, err = group(m.Signers.GroupB); err != nil {
		return r, err
	}
	if len(r.Keys) == 0 || (!r.TwoGroups() && (r.Threshold < 1 || r.Threshold > len(r.Keys))) {
		return r, errors.New("hlmailbox: no usable signer rule")
	}
	return r, nil
}

func mailboxAt(ctx context.Context, g gnoReader, mailbox string, height int64) (*mailboxJSON, error) {
	s, err := g.RenderAt(ctx, mailbox, "json", height)
	if err != nil {
		return nil, err
	}
	var m mailboxJSON
	if err := jsonUnmarshal(s, &m); err != nil {
		return nil, err
	}
	return &m, nil
}

// routeJSON is a route realm's Render("json"): p/hlroute's figures with the realm's own fields.
type routeJSON struct {
	Address    string `json:"address"`
	Live       bool   `json:"live"`
	Governor   string `json:"governor"` // the compiled governor and the yield's height, as hlmailbox renders them (M1k)
	Yielded    int64  `json:"yielded"`
	Balance    int64  `json:"balance"`
	Paused     string `json:"paused"`
	Symbol     string `json:"symbol"`
	Domain     uint32 `json:"domain"`
	Router     string `json:"router"`
	OldRouter  string `json:"oldRouter"`
	OldUntil   int64  `json:"oldUntil"`
	Lock       int64  `json:"lock"`
	SentTotal  int64  `json:"sentTotal"`
	PaidTotal  int64  `json:"paidTotal"`
	Owed       int64  `json:"owed"`
	Voided     int64  `json:"voided"`
	Lost       int64  `json:"lost"`
	Sweep      int64  `json:"sweep"`
	Cap        int64  `json:"cap"`
	Meter      int64  `json:"meter"`
	CapFloor   int64  `json:"capFloor"`
	Fee        int64  `json:"fee"`
	FeeFloor   int64  `json:"feeFloor"`
	Minimum    int64  `json:"minimum"`
	Maximum    int64  `json:"maximum"`
	OpenClaims int64  `json:"openClaims"`
	Claims     int64  `json:"claims"`
	Sends      int64  `json:"sends"`
	MigratedTo string `json:"migratedTo"`
	// Importing says a predecessor's pages are arriving (AP4: the realm refuses credits until the last is in) and
	// Imported how many pages this route took so far (K28, AQ1, AQ3: a successor mid-import is shown and judged
	// over the pair; the relayer brings a burn to it once the import has ended).
	Importing bool `json:"importing"`
	Imported  int  `json:"imported"`
	// Refunded counts the sends GovDAO refunded here and RefundedTotal what they summed to (AQ2: a successor's
	// refunds over the sends it imported; each left sentTotal and the lock); WrittenOff the refunded sends Solana
	// delivered after all that GovDAO booked as lost, and WrittenOffTotal their sum (M1h, AR1: lost rose by it).
	Refunded        int64  `json:"refunded"`
	RefundedTotal   int64  `json:"refundedTotal"`
	WrittenOff      int64  `json:"writtenOff"`
	WrittenOffTotal int64  `json:"writtenOffTotal"`
	Clocks          Clocks `json:"clocks"`
	Decisions       int64  `json:"decisions"`
}

// IsSuccessor says whether this route took, or is taking, a predecessor's pages.
func (r *routeJSON) IsSuccessor() bool { return r.Importing || r.Imported > 0 }

// ImportEnded says whether a successor's import has ended: it began (a page arrived) and no more is coming
// (AQ1: a burn addressed to the predecessor is delivered to the successor from then on).
func (r *routeJSON) ImportEnded() bool { return r.Imported > 0 && !r.Importing }

func (r *routeJSON) pausedFor(fn string) bool {
	for _, p := range strings.Split(r.Paused, ",") {
		if strings.TrimSpace(p) == fn {
			return true
		}
	}
	return false
}

func routeAt(ctx context.Context, g gnoReader, route string, height int64) (*routeJSON, error) {
	s, err := g.RenderAt(ctx, route, "json", height)
	if err != nil {
		return nil, err
	}
	var r routeJSON
	if err := jsonUnmarshal(s, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

// claimJSON is a route's Render("claim/<id>").
type claimJSON struct {
	ID     string `json:"id"`
	To     string `json:"to"`
	Amount int64  `json:"amount"`
	Paid   int64  `json:"paid"`
	State  string `json:"state"`
	By     string `json:"by"`
	At     int64  `json:"at"`
	Height int64  `json:"height"` // the height it was credited at (M1c; a range flag or void names heights)
	Seq    int64  `json:"seq"`
	Swept  int64  `json:"swept"` // a sweep-queue entry instead of a claim
}

// sweep says whether the id's entry is a sweep-queue entry, not a claim: p/hlroute renders a sweep entry with its amount
// in "swept" and no "state" (sweptJSON), a claim always with its state — so a sweep of 0 (a burn of nothing, credited by
// whoever brought its signatures, E5-SF1) is one too, which Swept != 0 missed.
func (c *claimJSON) sweep() bool { return c.State == "" }

// claimAt is a claim by id at a height; nil if the route knows no such id.
func claimAt(ctx context.Context, g gnoReader, route, id string, height int64) (*claimJSON, error) {
	s, err := g.RenderAt(ctx, route, "claim/"+id, height)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(s) == "null" {
		return nil, nil
	}
	var c claimJSON
	if err := jsonUnmarshal(s, &c); err != nil {
		return nil, err
	}
	return &c, nil
}

// sendJSON is a route's Render("send/<id>") (AQ2): the send record under a message id, with the refund mark the
// realm sets when GovDAO's refund decision paid the sender back (the amount left the lock then, and the message
// must never deliver on Solana).
type sendJSON struct {
	ID       string     `json:"id"`
	From     string     `json:"from"`
	Amount   int64      `json:"amount"`
	At       int64      `json:"at"`
	Imported bool       `json:"imported"` // brought by a predecessor's page: the only sends a refund may name
	Refunded refundMark `json:"refunded"` // the unix time it was refunded, 0 if not
	// WrittenOff is the mark GovDAO's write-off decision leaves on a send Solana delivered after its refund (M1h,
	// AR1: the amount booked as lost, so the identity closes); the time, or a bool, 0 or false if not.
	WrittenOff refundMark `json:"writtenOff"`
}

// refundMark is a send's refund mark as a realm may render it: the time it was refunded (0 for not), or a bool.
type refundMark bool

func (m *refundMark) UnmarshalJSON(b []byte) error {
	s := strings.TrimSpace(string(b))
	switch {
	case s == "true":
		*m = true
	case s == "false", s == "0", s == "null", s == `""`:
		*m = false
	default:
		n, err := strconv.ParseInt(strings.Trim(s, `"`), 10, 64)
		if err != nil {
			return fmt.Errorf("a refund mark of %s", s)
		}
		*m = n != 0
	}
	return nil
}

// sendAt is a route's send record by message id at a height; nil when the route holds none (a send exported to
// a successor, or an id that is no send of this route). A realm that renders no such page is an error: the mark
// is read before anything is staged or counted (AQ2), never assumed.
func sendAt(ctx context.Context, g gnoReader, route, id string, height int64) (*sendJSON, error) {
	s, err := g.RenderAt(ctx, route, "send/"+id, height)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(s) == "null" {
		return nil, nil
	}
	var sd sendJSON
	if err := jsonUnmarshal(s, &sd); err != nil {
		return nil, fmt.Errorf("%s renders no send record at send/%s (a realm from before the refund mark?): %w", route, id, err)
	}
	return &sd, nil
}

// decisionJSON is one entry of a realm's Render("decisions.json").
type decisionJSON struct {
	ID        int64  `json:"id"`
	Kind      string `json:"kind"`
	Text      string `json:"text"`
	State     string `json:"state"`
	Made      int64  `json:"made"`
	Recorded  int64  `json:"recorded"`
	Due       int64  `json:"due"`
	Applied   int64  `json:"applied"`
	Cancelled int64  `json:"cancelled"`
	Note      string `json:"note"`
}

// Queued is whether the decision waits on its delay.
func (d decisionJSON) Queued() bool { return d.State == "pending" && d.Due > d.Recorded }

func decisionsAt(ctx context.Context, g gnoReader, realm string, height int64) ([]decisionJSON, error) {
	s, err := g.RenderAt(ctx, realm, "decisions.json", height)
	if err != nil {
		return nil, err
	}
	var ds []decisionJSON
	if err := jsonUnmarshal(s, &ds); err != nil {
		return nil, err
	}
	return ds, nil
}

// deliveredAt is the height a message id was delivered at on gno.land (0 if never), as of a height.
func deliveredAt(ctx context.Context, g gnoReader, mailbox, id string, height int64) (int64, error) {
	s, err := g.RenderAt(ctx, mailbox, "delivered/"+id, height)
	if err != nil {
		return 0, err
	}
	return strconv.ParseInt(strings.TrimSpace(s), 10, 64)
}

// gnoMsg is one dispatched message as hlmailbox keeps it, checked: the id is the message's, the nonce is the one
// asked for, and the origin is the mailbox's own domain.
type gnoMsg struct {
	Raw    []byte
	Msg    hl.Message
	ID     [32]byte
	Root   [32]byte
	Height int64
}

func parseGnoMsg(s string, domain, nonce uint32) (*gnoMsg, error) {
	var j struct {
		Nonce   uint32 `json:"nonce"`
		ID      string `json:"id"`
		Root    string `json:"root"`
		Height  int64  `json:"height"`
		Message string `json:"message"`
	}
	if strings.TrimSpace(s) == "null" {
		return nil, fmt.Errorf("gno message %d: none", nonce)
	}
	if err := jsonUnmarshal(s, &j); err != nil {
		return nil, err
	}
	raw, err := hex.DecodeString(j.Message)
	if err != nil {
		return nil, err
	}
	m, err := hl.ParseMessage(raw)
	if err != nil {
		return nil, err
	}
	id := hl.ID(raw)
	root, err := hl.ParseKey32(j.Root)
	if err != nil {
		return nil, err
	}
	if hex.EncodeToString(id[:]) != j.ID || m.Nonce != nonce || j.Nonce != nonce || m.Origin != domain || m.Version != hl.Version {
		return nil, fmt.Errorf("gno message %d does not check out", nonce)
	}
	return &gnoMsg{Raw: raw, Msg: m, ID: id, Root: root, Height: j.Height}, nil
}

func gnoMessage(ctx context.Context, g gnoReader, mailbox string, domain, nonce uint32) (*gnoMsg, error) {
	s, err := g.RenderAt(ctx, mailbox, "msg/"+strconv.FormatUint(uint64(nonce), 10), 0)
	if err != nil {
		return nil, err
	}
	return parseGnoMsg(s, domain, nonce)
}

// --------------------------------------------------------------- Solana --

// solDispatch is a message a route dispatched on Solana, read from the mailbox's own record of it.
type solDispatch struct {
	Sig   string     `json:"sig"`
	Slot  uint64     `json:"slot"`
	Raw   hexBytes   `json:"message"`
	Msg   hl.Message `json:"-"`
	ID    [32]byte   `json:"-"`
	Nonce uint32     `json:"nonce"`
}

func (d *solDispatch) fill() error {
	m, err := hl.ParseMessage(d.Raw)
	if err != nil {
		return err
	}
	d.Msg, d.ID, d.Nonce = m, hl.ID(d.Raw), m.Nonce
	return nil
}

// IDHex is the message id in hex.
func (d *solDispatch) IDHex() string { return hex.EncodeToString(d.ID[:]) }

// token is the burn's recipient on gno.land and its amount, from the token message. A recipient whose top
// twelve bytes are not zero is no gno address (§13: Bytes32ToAddress requires them zero): to is "" and bad is
// true — the realm sweeps such a burn, and the watch expects the sweep.
func (d *solDispatch) token() (to string, amount int64, bad bool, ok bool) {
	rcpt, amt, _, err := hl.ParseTokenMessage(d.Msg.Body)
	if err != nil || amt > 1<<63-1 {
		return "", 0, false, false
	}
	to, err = hl.Bytes32ToGnoAddress(rcpt)
	if err != nil {
		return "", int64(amt), true, true
	}
	return to, int64(amt), false, true
}

var dispatchTag = []byte("DISPATCH")

// scanSkip is a transaction a scan could not read (K5: no single transaction's error pins the cursor — it is
// skipped with an alarm, kept on a side list and read again each round until it reads).
type scanSkip struct {
	Sig   string `json:"sig"`
	Slot  uint64 `json:"slot"`
	Err   string `json:"err"`
	Tries int    `json:"tries"`
	// Retry is when a transaction past the provider's retention is tried again (G36: a null answer, retried every
	// poll for good, was a thousand reads a poll on a fresh install); 0 for the other skips, retried every poll.
	Retry int64 `json:"retry,omitempty"`
}

// retentionRetry is how long a transaction the provider answers null for waits before it is tried again (G36).
const retentionRetry = time.Hour

// nullRetries is how many null answers a transaction the provider just listed is tried again at once before it is
// named past the retention and tried hourly (the review of lane s6, F2: a backend behind the provider's listing answers
// null for a transaction the listing just gave — taken for the retention, a burn the other provider showed stood on its
// word alone for an hour, its token without a verdict and the signer that reads it halted at the hour).
const nullRetries = 5

// skipFor is a transaction's skip after its tries-th failed read: one the provider no longer serves (a null answer,
// solana-go's ErrNotFound) is tried again at once nullRetries times, then named as past the retention and waits an hour;
// the rest are retried every poll.
func skipFor(sig string, slot uint64, err error, now time.Time, tries int) scanSkip {
	sk := scanSkip{Sig: sig, Slot: slot, Err: err.Error(), Tries: tries}
	if errors.Is(err, rpc.ErrNotFound) {
		if tries <= nullRetries {
			sk.Err = fmt.Sprintf("getTransaction answered null (%d of %d tries before it is taken as past the retention; a backend behind the listing?): %v", tries, nullRetries, err)
			return sk
		}
		sk.Err = "past the provider's retention (getTransaction answered null; retried hourly, G36): " + err.Error()
		sk.Retry = now.Add(retentionRetry).Unix()
	}
	return sk
}

// solScan finds the messages a route dispatched to destination in the transactions finalized past a cursor (its
// signature, and the slot it was read through: history.go; neither for all of them), oldest first, and returns the
// new cursor and the transactions it could not read, which the cursor passes over. A message counts only as the
// mailbox's own dispatched-message account holds it — not as a log line, since any program can log anything,
// and not every mailbox build logs through the noop program. minSlot, when not 0, is the slot the listing must
// reach (minContextSlot: a provider whose finalized view is below it fails the scan): the slot the route's supply
// was read at, so that every burn the supply has burned is in the index (E4-SF3: a listing answered from a
// backend behind the supply's left a burn out of the supply and out of flight alike — a forged mint's room). bound
// says the provider honours minContextSlot on the listing, so the route is read through minSlot from here on (the
// minContextSlot check's verdict; an answer that may ignore the bound moves the cursor's slot only to the newest
// signature it lists). A provider that cannot list the route back to the cursor is a *historyGap (E6-SF1).
func solScan(ctx context.Context, c solReader, mailbox sol.Mailbox, route solana.PublicKey, origin, destination uint32, cur scanCursor, minSlot uint64, bound bool) ([]solDispatch, scanCursor, []scanSkip, error) {
	l, err := listAfter(ctx, c, route, cur, minSlot)
	if err != nil {
		return nil, cur, nil, err
	}
	sigs := l.sigs
	var out []solDispatch
	var skipped []scanSkip
	next := cur
	for i := len(sigs) - 1; i >= 0; i-- { // oldest first
		s := sigs[i]
		if s.Err == nil {
			ds, err := solDispatchesIn(ctx, c, mailbox, s.Signature)
			if err != nil {
				skipped = append(skipped, skipFor(s.Signature.String(), s.Slot, err, time.Now(), 1))
			}
			for _, d := range ds {
				if d.Msg.Origin == origin && d.Msg.Destination == destination && solana.PublicKeyFromBytes(d.Msg.Sender[:]).Equals(route) {
					d.Slot = s.Slot
					out = append(out, d)
				}
			}
		}
	}
	switch {
	case l.newest != nil:
		// the newest the provider lists: every transaction at or below its slot was read (those past the cursor now, the
		// rest before), so it is the next `until` — read back from, it may lie at or below the slot read through; the slot
		// it claims is held to the supplies' slot, which both providers answered at or above (the review of lane s7, R3: a
		// liar's slot of 10^12 kept in the state outlived its replacement, every burn below it never listed again)
		next.Sig = l.newest.Signature.String()
		if minSlot > 0 {
			next.Through = max(next.Through, min(l.newest.Slot, minSlot))
		} else {
			next.Through = max(next.Through, l.newest.Slot)
		}
	case l.back:
		next.Sig = "" // listed back and nothing at all: the old cursor is one the provider does not know
	}
	if bound && minSlot > throughMargin {
		// honoured, the bound says the provider's bank reached minSlot, not that its address index did (one a moment behind
		// its root, or a custom indexer's): a margin below it is listed again by the next listing back, so a burn one listing
		// left out is listed by the next (the review of lane s7, R2)
		next.Through = max(next.Through, minSlot-throughMargin)
	}
	return out, next, skipped, nil
}

// throughMargin is how far below the bound a listing's slot read through stays: ≈3.5 minutes of ≈0.21 s slots.
const throughMargin = 1000

// minContextCheck is the self-check of the listing guard (E5-SF2): solScan holds a route's listing to the slot its supply
// was read at by minContextSlot (E4-SF3), and a getSignaturesForAddress answer is a bare list with no context slot, so
// nothing in it says whether the bound was honoured — Alchemy ignores it on the listing (honouring it on account reads;
// api.mainnet-beta, publicnode and Helius refuse, measured on mainnet 2026-10-15). Each provider is asked at a machine's
// first reading and hourly for a listing 100,000 slots past the snapshot's slot (≈6 hours, the audit's measured probe),
// a view no provider has: honoured, it is refused with −32016; answered, the provider ignores the bound — alarmed
// (solana:min-context-ignored:<i>). A reading that rests on the dispatch index (the identity, the signers' verdict, the
// forged-credit rule) needs one provider that honours it on a monitor and in hlwatch, both on a signer (the round-6
// live-fixes audit's E6-SF2: refusing either refused both live monitors, which pair Alchemy): a burn the honouring
// provider lists and an ignoring one has not listed yet is one-sided — no verdict, never short (E5-SF4) — and nothing is
// read from the ignoring provider alone; but a lying honouring provider could hide a burn for the readings its ignoring
// partner lags, which on a monitor is one more way to its token's no verdict (one-sided the poll after) and on a signer
// would be a forged burn signed (TestS7OneHonouringProviderThatLiesHidesABurnForAReading), so a signer still needs both. The check fails
// closed (the review of lane s6, F4): a provider is refused until one −32016 shows it honours the bound — a probe
// answered otherwise (another error code, a timeout, a status) is asked again each minute meanwhile — and keeps a
// decided answer through a probe that cannot tell.
type minContextCheck struct {
	next  time.Time
	state [2]int    // minContextUnknown, minContextHonours or minContextIgnores, by provider
	why   [2]string // why the last probe could not tell, while undecided
}

const (
	minContextUnknown = iota
	minContextHonours
	minContextIgnores
)

const (
	minContextEvery = time.Hour
	minContextRetry = time.Minute
	minContextAhead = 100_000 // slots past the snapshot's: ≈6 hours of 0.21 s slots
)

// check probes when due and says why a reading resting on the dispatch index is refused, "" when it is not: both says
// the reading needs both providers to honour the bound (a signer), else one (a monitor, hlwatch: E6-SF2).
func (mc *minContextCheck) check(ctx context.Context, providers [2]*rpc.Client, cfg *Config, slot uint64, now time.Time, alarm *Alarmer, both bool) string {
	probe, ok := listedRoute(cfg)
	if !ok {
		return "" // no route lists anything: nothing rests on the guard
	}
	if !now.Before(mc.next) {
		mc.next = now.Add(minContextEvery)
		for i, c := range providers {
			if c == nil {
				continue
			}
			key := "solana:min-context-ignored:" + strconv.Itoa(i+1)
			honours, err := minContextHonoured(ctx, c, probe, slot+minContextAhead)
			switch {
			case err != nil:
				if mc.state[i] == minContextUnknown {
					mc.why[i] = err.Error()
					mc.next = now.Add(minContextRetry)
				}
			case honours:
				mc.state[i], mc.why[i] = minContextHonours, ""
				alarm.Clear(key)
			case both:
				mc.state[i] = minContextIgnores
				alarm.Alarm(key, fmt.Sprintf("Solana provider %d answers getSignaturesForAddress for a view it does not have (minContextSlot %d ignored): its listing cannot be held to the supplies' slot, so a burn out of the supply could be out of the dispatch index — a signer's verdict is refused until the config names a provider that honours it (api.mainnet-beta, publicnode and Helius do; Alchemy does not) (E5-SF2)", i+1, slot+minContextAhead))
			default:
				mc.state[i] = minContextIgnores
				alarm.AlarmEvery(key, fmt.Sprintf("Solana provider %d answers getSignaturesForAddress for a view it does not have (minContextSlot %d ignored): the identities rest on the other provider holding its listing to the supplies' slot — a burn this one has not listed yet is one-sided, no verdict and never short (E5-SF4) — none is taken while neither honours it, and the forged-credit rule waits while the config names it (a burn the honouring one hid would be in no index); a lying honouring provider could still hide a burn for the readings this one lags, so name two that honour it where possible (api.mainnet-beta, publicnode and Helius do; Alchemy does not) (E5-SF2, E6-SF2)", i+1, slot+minContextAhead), 24*time.Hour)
			}
		}
	}
	honouring := 0
	var why []string
	for i := range providers {
		switch mc.state[i] {
		case minContextHonours:
			honouring++
		case minContextIgnores:
			why = append(why, fmt.Sprintf("Solana provider %d ignores minContextSlot on getSignaturesForAddress (E5-SF2)", i+1))
		case minContextUnknown:
			why = append(why, fmt.Sprintf("Solana provider %d has not shown yet that it honours minContextSlot on getSignaturesForAddress (its probe: %s; asked again each minute)", i+1, orNone(mc.why[i])))
		}
	}
	switch {
	case both && len(why) > 0:
		return why[0] + ": no signer's verdict rests on a dispatch index its listing feeds until the config names a provider that honours it"
	case !both && honouring == 0:
		return strings.Join(why, "; ") + ": no reading rests on the dispatch index until one of the providers shows it honours the bound (E6-SF2)"
	}
	return ""
}

// honouring says, by provider, whether it has shown it honours minContextSlot on the listing: a listing of it held to a
// slot is read through that slot (E6-SF1).
func (mc *minContextCheck) honouring() [2]bool {
	return [2]bool{mc.state[0] == minContextHonours, mc.state[1] == minContextHonours}
}

// both says whether both providers have shown they honour it: what a judgement that a burn is in neither provider's
// index — the forged-credit rule, a flag — needs (E6-SF2: with one provider ignoring the bound and lagging past the
// credits' margin, a burn the honouring one hid would be in no index, and its honest credit would read forged on one
// liar's word).
func (mc *minContextCheck) both() bool {
	return mc.state[0] == minContextHonours && mc.state[1] == minContextHonours
}

// listedRoute is the token program of the first route that has one: the address the guard's probe lists.
func listedRoute(cfg *Config) (solana.PublicKey, bool) {
	for _, r := range cfg.Routes {
		if r.Token != "" {
			return r.TokenKey(), true
		}
	}
	return solana.PublicKey{}, false
}

// minContextHonoured asks a provider for an address's signatures at minContextSlot min, past its view: −32016 ("Minimum
// context slot has not been reached") is the bound honoured, a list is the bound ignored, anything else no answer.
func minContextHonoured(ctx context.Context, c *rpc.Client, addr solana.PublicKey, min uint64) (bool, error) {
	limit := 1
	_, err := c.GetSignaturesForAddressWithOpts(ctx, addr, &rpc.GetSignaturesForAddressOpts{Limit: &limit, Commitment: rpc.CommitmentFinalized, MinContextSlot: &min})
	if err == nil {
		return false, nil
	}
	var re *jsonrpc.RPCError
	if errors.As(err, &re) && re.Code == -32016 {
		return true, nil
	}
	return false, err
}

// retrySkipped reads a side list's transactions again; the ones that read now come back as dispatches (filtered
// as solScan filters), the rest stay on the list with their count raised — one past the provider's retention
// waits its hour before the next read (G36).
func retrySkipped(ctx context.Context, c solReader, mailbox sol.Mailbox, route solana.PublicKey, origin, destination uint32, skipped []scanSkip, now time.Time) ([]solDispatch, []scanSkip) {
	var out []solDispatch
	var keep []scanSkip
	for _, sk := range skipped {
		sig, err := solana.SignatureFromBase58(sk.Sig)
		if err != nil {
			continue // not a signature: dropped
		}
		if sk.Retry > now.Unix() {
			keep = append(keep, sk)
			continue
		}
		ds, err := solDispatchesIn(ctx, c, mailbox, sig)
		if err != nil {
			keep = append(keep, skipFor(sk.Sig, sk.Slot, err, now, sk.Tries+1))
			continue
		}
		for _, d := range ds {
			if d.Msg.Origin == origin && d.Msg.Destination == destination && solana.PublicKeyFromBytes(d.Msg.Sender[:]).Equals(route) {
				d.Slot = sk.Slot
				out = append(out, d)
			}
		}
	}
	return out, keep
}

// dispatchDiffers compares two providers' sights of one dispatch (G17): the slot it was finalized at, the amount
// and the recipient of its token message. It answers "" when they agree, else what differs.
func dispatchDiffers(a, b solDispatch) string {
	var parts []string
	if a.Slot != b.Slot {
		parts = append(parts, fmt.Sprintf("slot %d against %d", b.Slot, a.Slot))
	}
	ato, aamt, _, _ := a.token()
	bto, bamt, _, _ := b.token()
	if aamt != bamt {
		parts = append(parts, fmt.Sprintf("amount %d against %d", bamt, aamt))
	}
	if ato != bto {
		parts = append(parts, fmt.Sprintf("recipient %q against %q", bto, ato))
	}
	return strings.Join(parts, ", ")
}

// agreedDispatch reads a dispatch's transaction through both providers at once (the pair compares the slot,
// the bytes and the accounts) and answers the dispatch of id as both show it; a disagreement is errProviders,
// and a transaction that carries no such dispatch an error too.
func agreedDispatch(ctx context.Context, both solReader, mailbox sol.Mailbox, route solana.PublicKey, origin, destination uint32, sigStr string, id [32]byte) (solDispatch, error) {
	sig, err := solana.SignatureFromBase58(sigStr)
	if err != nil {
		return solDispatch{}, fmt.Errorf("dispatch %x: no transaction signature to read it again by", id[:6])
	}
	ds, err := solDispatchesIn(ctx, both, mailbox, sig)
	if err != nil {
		return solDispatch{}, err
	}
	for _, d := range ds {
		if d.ID == id && d.Msg.Origin == origin && d.Msg.Destination == destination && solana.PublicKeyFromBytes(d.Msg.Sender[:]).Equals(route) {
			return d, nil
		}
	}
	return solDispatch{}, fmt.Errorf("transaction %s carries no dispatch %x", sigStr, id[:6])
}

// skipText names a side list for an alarm: the transactions past the provider's retention counted, the rest
// each named (a fresh install on a short-history provider listed thousands, G36).
func skipText(skipped []scanSkip) string {
	var parts []string
	retention := 0
	for _, s := range skipped {
		if s.Retry != 0 {
			retention++
			continue
		}
		parts = append(parts, fmt.Sprintf("%s (slot %d, %d tries: %s)", s.Sig, s.Slot, s.Tries, s.Err))
	}
	if retention > 0 {
		parts = append(parts, fmt.Sprintf("%d past the provider's retention (null answers; retried hourly — a fresh install starts its index at a checkpoint with -record-from client, G36)", retention))
	}
	return strings.Join(parts, "; ")
}

// maxTxVersion is the transaction version every read pins the RPC to (G28): v1 (SIMD-0385, on mainnet since
// 2026-09-15), which solana-go v1.25.0 decodes. Under a pin of 0 the RPC refused to return a v1 transaction
// ("not supported by the requesting client"), so one such transaction touching the client's state or a token
// program blinded the record and gap rules, muted the index rule, refused `flag -ids` and left a user's v1 burn
// unindexed for good.
const maxTxVersion = uint64(1)

// buildGapRE is the RPC's refusal of a transaction version above the pin (-32015): a version this build cannot
// read, which no retry cures — the skip's alarm names it as a build gap, never a transient (G28).
var buildGapRE = regexp.MustCompile(`(?i)not supported by the requesting client|-32015`)

// getTransaction reads one finalized transaction at the pinned version; a version above it is named as a
// build gap.
func getTransaction(ctx context.Context, c solReader, sig solana.Signature) (*rpc.GetTransactionResult, error) {
	v := maxTxVersion
	res, err := c.GetTransaction(ctx, sig, &rpc.GetTransactionOpts{Encoding: solana.EncodingBase64, Commitment: rpc.CommitmentFinalized, MaxSupportedTransactionVersion: &v})
	if err != nil && buildGapRE.MatchString(err.Error()) {
		return nil, fmt.Errorf("BUILD GAP: transaction %s is of a version above %d, which this build cannot read (rebuild with a solana-go that decodes it; no retry cures it): %w", sig, maxTxVersion, err)
	}
	return res, err
}

// solDispatchesIn is every dispatch the mailbox recorded in one finalized transaction.
func solDispatchesIn(ctx context.Context, c solReader, mailbox sol.Mailbox, sig solana.Signature) ([]solDispatch, error) {
	res, err := getTransaction(ctx, c, sig)
	if err != nil {
		return nil, fmt.Errorf("transaction %s: %w", sig, err)
	}
	if res == nil || res.Meta == nil || res.Meta.Err != nil {
		return nil, nil
	}
	tx, err := res.Transaction.GetTransaction()
	if err != nil {
		return nil, err
	}
	keys := append(solana.PublicKeySlice{}, tx.Message.AccountKeys...)
	keys = append(keys, res.Meta.LoadedAddresses.Writable...)
	keys = append(keys, res.Meta.LoadedAddresses.ReadOnly...)
	// The dispatched-message account a dispatch creates is one of the transaction's accounts: the mailbox owns
	// it, and it records the slot it was made in, so an older one the transaction merely names is passed over.
	// GetMultipleAccounts is asked in chunks of sol.MultiMax (a public provider blocks more than ten), at or above the
	// transaction's own slot (minContextSlot): a backend behind it has no such account yet, and its answer took the
	// burn for no dispatch, passed for good — out of one provider's index (its token without a verdict, E5-SF4) or of
	// both (the reading short by it, a forged mint's room; the review of lane s6, F2 and F3); now it fails the read, the
	// transaction skipped and read again next round.
	slot := res.Slot
	var out []solDispatch
	for start := 0; start < len(keys); start += sol.MultiMax {
		end := start + sol.MultiMax
		if end > len(keys) {
			end = len(keys)
		}
		accts, err := c.GetMultipleAccountsWithOpts(ctx, keys[start:end], &rpc.GetMultipleAccountsOpts{Commitment: rpc.CommitmentFinalized, MinContextSlot: &slot})
		if err != nil {
			return nil, fmt.Errorf("accounts of %s: %w", sig, err)
		}
		for _, a := range accts.Value {
			if a == nil || !a.Owner.Equals(mailbox.Program) {
				continue
			}
			ad := a.Data.GetBinary()
			if len(ad) < 53+hl.HeaderLen || ad[0] != 1 || !bytes.Equal(ad[1:9], dispatchTag) {
				continue
			}
			if binary.LittleEndian.Uint64(ad[13:21]) != res.Slot {
				continue
			}
			d := solDispatch{Sig: sig.String(), Slot: res.Slot, Raw: append(hexBytes{}, ad[53:]...)}
			if err := d.fill(); err != nil {
				continue
			}
			if binary.LittleEndian.Uint32(ad[9:13]) != d.Nonce {
				continue
			}
			out = append(out, d)
		}
	}
	return out, nil
}

// supplySnapshot is Solana's synthetic supplies, both providers agreeing, with the slot each rests on.
type supplySnapshot struct {
	Supplies map[string]int64 // by route symbol
	// Slots is the slot each route's supply rests on, by symbol: the lower of the two providers' answers for the
	// read that held its mint. Every mint is read in one getMultipleAccounts per provider, which answers from one
	// bank (E4-SF1, E4-SF3: one read per route put GNOT's supply at one slot and BUBBLE's at the next, and a
	// reading that held both to the lower counted a BUBBLE send minted between them twice — a false break — and a
	// burn between them nowhere — a forged mint hidden); were the mints ever more than one chunk, each keeps its
	// chunk's slot. A route's burns in flight, its burns credited after the snapshot and its sends minted are all
	// judged by its own slot, never another route's.
	Slots map[string]uint64
	Slot  uint64 // the lowest of Slots: the floor's, the cut's and the signing gate's slot
	At    time.Time
	// Time is the time Solana stamped on Slot: the lowest any of the snapshot's reads answered (the Clock sysvar, read
	// in the same call as the mints: one bank, so a call's time is its slot's), zero when one did not read. Solana's
	// clock never goes back along the chain, so honest answers make the lowest the time of the lowest slot; a lone
	// provider claiming a later time on its slot cannot raise it past the other's. A burn credited on gno.land before
	// Time — less a margin — was finalized, so produced, before Slot (E4-GT1: the cut places the credits the dispatch
	// index cannot by it).
	Time time.Time
}

// SlotOf is the slot a route's supply rests on (Slot for a route the snapshot holds no slot of).
func (s *supplySnapshot) SlotOf(symbol string) uint64 {
	if sl, ok := s.Slots[symbol]; ok {
		return sl
	}
	return s.Slot
}

// solSupplies reads every route's synthetic supply (finalized) from both providers, each provider in one batched
// read of the mint accounts — ten accounts a call, as a public provider allows: the Clock sysvar and up to nine
// mints, so every call stamps its own slot's time (the routes are two: one call) — and the amounts must agree. A
// disagreement is an error naming the token, never a verdict. Each supply is the mint account's own field
// (sol.MintSupply), never getTokenSupply, which a public provider may refuse as an indexed call while it serves
// every account read.
func solSupplies(ctx context.Context, providers [2]*rpc.Client, routes []Route) (*supplySnapshot, error) {
	snap := &supplySnapshot{Supplies: map[string]int64{}, Slots: map[string]uint64{}, At: time.Now()}
	var syms []string
	var mints []solana.PublicKey
	for _, r := range routes {
		if r.Token == "" {
			continue
		}
		syms = append(syms, r.Symbol)
		mints = append(mints, sol.Synthetic{Program: r.TokenKey()}.Mint())
	}
	const perCall = sol.MultiMax - 1 // and the Clock sysvar
	var amounts [2][]int64
	clockOK := len(mints) > 0
	for i, p := range providers {
		for start := 0; start < len(mints); start += perCall {
			end := min(start+perCall, len(mints))
			keys := append([]solana.PublicKey{solana.SysVarClockPubkey}, mints[start:end]...)
			res, err := p.GetMultipleAccountsWithOpts(ctx, keys, &rpc.GetMultipleAccountsOpts{Encoding: solana.EncodingBase64, Commitment: rpc.CommitmentFinalized})
			if err != nil {
				return nil, fmt.Errorf("the supplies from provider %d: %w", i+1, err)
			}
			if res == nil || len(res.Value) != len(keys) {
				return nil, fmt.Errorf("the supplies from provider %d: %d of %d accounts answered", i+1, lenOf(res), len(keys))
			}
			slot := res.Context.Slot
			if t, ok := solClock(res.Value[0], slot); !ok {
				clockOK = false // a clock that does not read: no time at all
			} else if snap.Time.IsZero() || t.Before(snap.Time) {
				snap.Time = t
			}
			for k, a := range res.Value[1:] {
				j := start + k
				if a == nil {
					return nil, fmt.Errorf("%s supply from provider %d: no mint account", syms[j], i+1)
				}
				n, err := sol.MintSupply(a.Owner, a.Data.GetBinary())
				if err != nil {
					return nil, fmt.Errorf("%s supply from provider %d: %w", syms[j], i+1, err)
				}
				if n > 1<<62 {
					return nil, fmt.Errorf("%s supply from provider %d: %d is out of range", syms[j], i+1, n)
				}
				amounts[i] = append(amounts[i], int64(n))
				if s, ok := snap.Slots[syms[j]]; !ok || slot < s {
					snap.Slots[syms[j]] = slot
				}
			}
		}
	}
	for j, sym := range syms {
		if amounts[0][j] != amounts[1][j] {
			return nil, fmt.Errorf("%s supply: the providers disagree (%d and %d)", sym, amounts[0][j], amounts[1][j])
		}
		snap.Supplies[sym] = amounts[0][j]
		if snap.Slot == 0 || snap.Slots[sym] < snap.Slot {
			snap.Slot = snap.Slots[sym]
		}
	}
	if !clockOK {
		snap.Time = time.Time{}
	}
	snap.At = time.Now()
	return snap, nil
}

func lenOf(r *rpc.GetMultipleAccountsResult) int {
	if r == nil {
		return 0
	}
	return len(r.Value)
}

// solClock is the time the Clock sysvar's account stamps on its slot — its layout: the slot, the epoch's start
// timestamp, the epoch, the leader schedule's epoch, the unix timestamp, eight bytes each — checked to be the
// sysvar's, of the slot the read answered at (one bank: the same slot); ok false otherwise.
func solClock(a *rpc.Account, slot uint64) (time.Time, bool) {
	if a == nil || !a.Owner.Equals(solana.SysVarPubkey) {
		return time.Time{}, false
	}
	d := a.Data.GetBinary()
	if len(d) < 40 || binary.LittleEndian.Uint64(d[0:8]) != slot {
		return time.Time{}, false
	}
	return time.Unix(int64(binary.LittleEndian.Uint64(d[32:40])), 0), true
}

// solProcessedMany says, for up to 100 message ids, which ones our mailbox processed as of a finalized block:
// their processed-message records fetched in batches (F23: one read per id was a thousand a poll under a backlog),
// an account owned by the mailbox being the record — the slot each was processed in, which the record carries (0
// when it does not read as one), and the slot each record was read at (readAt: through a pair, the lower of the
// two answers; a batch is ten records, and two batches are two reads at their own slots). An absent record says
// the message was not processed by its read's slot, and nothing of the slots after it. minSlot, when not 0, asks
// the providers to answer at or above it (minContextSlot; a provider below it fails the read).
func solProcessedMany(ctx context.Context, c solReader, mailbox sol.Mailbox, ids [][32]byte, minSlot uint64) (done []bool, slots, readAt []uint64, err error) {
	keys := make([]solana.PublicKey, 0, len(ids))
	for _, id := range ids {
		keys = append(keys, mailbox.ProcessedMessage(id))
	}
	opts := &rpc.GetMultipleAccountsOpts{Encoding: solana.EncodingBase64, Commitment: rpc.CommitmentFinalized}
	if minSlot > 0 {
		opts.MinContextSlot = &minSlot
	}
	res, readAt, err := multiAt(ctx, c, keys, opts)
	if err != nil {
		return nil, nil, nil, err
	}
	if len(res.Value) != len(keys) || len(readAt) != len(keys) {
		return nil, nil, nil, fmt.Errorf("%d processed records asked, %d answered", len(keys), len(res.Value))
	}
	done, slots = make([]bool, len(keys)), make([]uint64, len(keys))
	for i, a := range res.Value {
		done[i] = a != nil && a.Owner.Equals(mailbox.Program)
		if done[i] {
			// a slot above its own read's was never processed by it: no slot read (0), as a record that does not
			// parse — held to the read that answered this record, never another batch's (two batches are two slots)
			if slots[i] = processedSlot(a.Data.GetBinary(), ids[i]); slots[i] > readAt[i] {
				slots[i] = 0
			}
		}
	}
	return done, slots, readAt, nil
}

// multiAt reads accounts in batches with the slot each was read at: through both providers for a pair (each
// account the lower of the two answers' slots), else through the one reader.
func multiAt(ctx context.Context, c solReader, keys []solana.PublicKey, opts *rpc.GetMultipleAccountsOpts) (*rpc.GetMultipleAccountsResult, []uint64, error) {
	if p, ok := c.(pair); ok {
		return p.GetMultipleAccountsAt(ctx, keys, opts)
	}
	return sol.GetMultipleChunkedAt(ctx, c, keys, opts)
}

var processedTag = []byte("PROCESSD")

// processedSlot is the slot a processed-message record says its message was processed in (Hyperlane's
// ProcessedMessage behind AccountData's initialized byte: the discriminator, the sequence, the message id, the
// slot), 0 for a record that does not read as the id's. The monitors hold it to their snapshot's slot (E3-GT9): the
// marks are read after the snapshot, and kept, so a send processed after the supply's slot is still in flight
// against that supply.
func processedSlot(data []byte, id [32]byte) uint64 {
	if len(data) < 57 || data[0] != 1 || !bytes.Equal(data[1:9], processedTag) || !bytes.Equal(data[17:49], id[:]) {
		return 0
	}
	return binary.LittleEndian.Uint64(data[49:57])
}

// solDelivered says whether our mailbox processed a message id, as of a finalized block.
func solDelivered(ctx context.Context, c solReader, mailbox sol.Mailbox, id [32]byte) (bool, error) {
	_, err := c.GetAccountInfoWithOpts(ctx, mailbox.ProcessedMessage(id), &rpc.GetAccountInfoOpts{Commitment: rpc.CommitmentFinalized})
	if errors.Is(err, rpc.ErrNotFound) {
		return false, nil
	}
	return err == nil, err
}

// ---------------------------------------------------------------- bits --

type hexBytes []byte

func (h hexBytes) MarshalText() ([]byte, error) { return []byte(hex.EncodeToString(h)), nil }
func (h *hexBytes) UnmarshalText(b []byte) error {
	v, err := hex.DecodeString(string(b))
	if err != nil {
		return err
	}
	*h = v
	return nil
}

var errNotYet = errors.New("not yet")

// writeJSON writes a state file atomically: the bytes to a temporary file, fsynced, renamed over the file, the
// directory fsynced — so a power loss leaves the old file or the new one, never a zero-length one.
func writeJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
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
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	if d, err := os.Open(filepath.Dir(path)); err == nil {
		_ = d.Sync()
		d.Close()
	}
	return nil
}

// errCorrupt is a state or signature file that is not what was written: named as corruption, so a human deletes
// or restores it knowingly (a zero-length state file is a power loss, never an empty state).
var errCorrupt = errors.New("corrupt")

// readJSON reads a state file; a missing one is an empty state, a damaged one is an error naming the corruption.
func readJSON(path string, v any) error {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if len(bytes.TrimSpace(b)) == 0 {
		return fmt.Errorf("%s is %w: zero length (a write that never finished); restore it or delete it knowingly", path, errCorrupt)
	}
	if err := json.Unmarshal(b, v); err != nil {
		return fmt.Errorf("%s is %w: %v; restore it or delete it knowingly", path, errCorrupt, err)
	}
	return nil
}

func jsonUnmarshal(s string, v any) error {
	if err := json.Unmarshal([]byte(s), v); err != nil {
		return fmt.Errorf("%w: %.200s", err, s)
	}
	return nil
}
