// Package gnoside is the gno.land side the Ethereum bridge's machines read and call (eth/bridge/PLAN.md §5.1–§5.7),
// carried from the Solana agent's chain.go, decisions.go, views.go and migrate.go and pointed at this deployment's
// realms (gno.land/r/<namespace>/eth/hl*): the JSON the realms render — hlmailbox's, a route's, its claims and
// sends, every realm's decisions, r/hlgov's — read through github.com/gnolang/bridges/solana/bridge/gno's client as it is; the lines of
// a route's migrations; GovDAO's proposals; the Dispatch events in blocks' results (dispatch.go); and the one
// transaction the gno client does not make, a MsgRun (run.go: one Reemit per nonce in one transaction, §5.7).
//
// Every type is exported: the machines of both directions read these (package agent, and the signers, the
// monitor and hlwatch of the Ethereum → gno.land lane).
package gnoside

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/gnolang/bridges/solana/bridge/gno"
	"github.com/gnolang/bridges/solana/bridge/gnolight"
	"github.com/gnolang/bridges/solana/bridge/hl"
)

// Reader is the read side of a gno node (a fake in tests).
type Reader interface {
	RenderAt(ctx context.Context, pkgPath, path string, height int64) (string, error)
	Status(ctx context.Context) (gno.Head, error)
	Block(ctx context.Context, height int64) (gno.Head, error)
}

// Caller is a gno node a key signs through.
type Caller interface {
	Reader
	Call(ctx context.Context, pkgPath, fn string, gasWanted int64, args ...string) (*gno.Result, error)
}

// Light is the node as the updater and the relayer read it for the light client: blocks, commits, sets, results.
type Light interface {
	Reader
	BlockAt(ctx context.Context, height int64) (*gnolight.Block, error)
	CommitAt(ctx context.Context, height int64) (*gnolight.Commit, []*gnolight.Vote, error)
	ValidatorsAt(ctx context.Context, height int64) (*gnolight.ValidatorSet, error)
	BlockResults(ctx context.Context, height int64) ([]*gnolight.TxResult, error)
}

// ------------------------------------------------------------------ hlmailbox --

// Clocks is every realm's "clocks": the compiled clock factor and the delays it gives, in seconds.
type Clocks struct {
	Factor   int64 `json:"factor"`
	Delay    int64 `json:"delay"`
	Deadline int64 `json:"deadline"`
}

// ProposalDeadline is p/decisions' Deadline at factor 1: how old a builder-made proposal may be when it executes.
const ProposalDeadline = 14 * 24 * time.Hour

// DeadlineDuration is the realms' proposal deadline as this chain divides it, or ProposalDeadline when the realm
// renders none.
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

// Mailbox is hlmailbox's Render("json").
type Mailbox struct {
	Domain       uint32 `json:"domain"`
	Hook         string `json:"hook"`
	Address      string `json:"address"`
	Count        uint32 `json:"count"`
	Root         string `json:"root"`
	Delivered    int    `json:"delivered"`
	Live         bool   `json:"live"`
	LaunchedAt   int64  `json:"launchedAt"`
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
	Registered      []string              `json:"registered"`
	Seats           []string              `json:"seats"`
	Routes          map[string]RouteFlags `json:"routes"`
	DroppedRoutes   map[string]string     `json:"droppedRoutes"`
	SuccessorRoutes map[string]string     `json:"successorRoutes"`
	// EthContracts are the Ethereum deployment's contracts the realm compiles (E-M1's ethdeploy.gno), name → EIP-55
	// address: mailbox, token0, token1, client, gate, hlAdmin, proxyAdmin, merkleTreeHook, protocolFee and the five
	// launch implementations (mailboxImpl … gateImpl).
	EthContracts map[string]string `json:"ethContracts"`
}

// DAOPath is gno.land/r/gov/dao's pkgpath, the only governor after the yield.
const DAOPath = "gno.land/r/gov/dao"

// DAOGoverns is whether GovDAO's proxy decides now: no governor was compiled, or the governor yielded.
func DAOGoverns(governor string, yielded int64) bool { return governor == "" || yielded != 0 }

// DAOGoverns is whether GovDAO decides on this mailbox's realms now.
func (m *Mailbox) DAOGoverns() bool { return DAOGoverns(m.Governor, m.Yielded) }

// RouteFlags is one route's flags as hlmailbox renders them, with its place in a migration.
type RouteFlags struct {
	Epoch       int64      `json:"epoch"`
	EpochAt     int64      `json:"epochAt"`
	Sends       bool       `json:"sends"`
	Credits     bool       `json:"credits"`
	Payouts     bool       `json:"payouts"`
	Flags       []string   `json:"flags"` // "fn by seat N (addr) at T[ naming ids]"
	Predecessor string     `json:"predecessor"`
	Successor   string     `json:"successor"`
	Enrolled    EnrollMark `json:"enrolled"`
	EnrolledAt  int64      `json:"enrolledAt"`
	Pulled      bool       `json:"pulled"`
	Dropped     []string   `json:"dropped"`
}

// Enrollment is the route's kind-6 enrolment as read: the height and the time.
func (r RouteFlags) Enrollment() (height, at int64) {
	height, at = r.Enrolled.Height, r.Enrolled.At
	if at == 0 {
		at = r.EnrolledAt
	}
	return height, at
}

// EnrollMark is a route's kind-6 enrolment record: a bare number (a height, or a unix time when a billion and more),
// an object naming the height and the time, or null/false/"" for a route never enrolled.
type EnrollMark struct {
	Height int64
	At     int64
}

// Set says whether the route was enrolled at all.
func (e EnrollMark) Set() bool { return e.Height > 0 || e.At > 0 }

func (e *EnrollMark) UnmarshalJSON(b []byte) error {
	s := strings.TrimSpace(string(b))
	switch {
	case s == "null", s == "false", s == `""`, s == "0":
		*e = EnrollMark{}
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
		*e = EnrollMark{Height: o.Height, At: at}
		return nil
	}
	n, err := strconv.ParseInt(strings.Trim(s, `"`), 10, 64)
	if err != nil {
		return fmt.Errorf("an enrolment record of %s", s)
	}
	if n >= 1_000_000_000 {
		*e = EnrollMark{At: n}
	} else {
		*e = EnrollMark{Height: n}
	}
	return nil
}

var flagLineRE = regexp.MustCompile(`^(\w+) by seat (\d+) \(([^)]*)\) at (\d+)(?: naming (.*))?$`)

// Flag is one standing flag on a route, parsed.
type Flag struct {
	Fn   string
	Seat int
	Addr string
	At   int64
	IDs  []string
}

// ParseFlag reads one of hlmailbox's flag lines.
func ParseFlag(line string) (Flag, bool) {
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
func (r RouteFlags) StandingFlags() []Flag {
	var out []Flag
	for _, l := range r.Flags {
		if f, ok := ParseFlag(l); ok {
			out = append(out, f)
		}
	}
	return out
}

// Rule is hlmailbox's signer set and rule as the verifier applies it: a threshold over the set, or one key of each
// group.
type Rule struct {
	Keys      []ed25519.PublicKey
	Threshold int
	GroupA    []int
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

// Rule is the mailbox's signer rule.
func (m *Mailbox) Rule() (Rule, error) {
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

// MailboxAt reads hlmailbox's json at a height (0 for the latest).
func MailboxAt(ctx context.Context, g Reader, mailbox string, height int64) (*Mailbox, error) {
	s, err := g.RenderAt(ctx, mailbox, "json", height)
	if err != nil {
		return nil, err
	}
	var m Mailbox
	if err := JSON(s, &m); err != nil {
		return nil, err
	}
	return &m, nil
}

// ---------------------------------------------------------------------- routes --

// Route is a route realm's Render("json"): p/hlroute's figures with the realm's own fields.
type Route struct {
	Address         string `json:"address"`
	Live            bool   `json:"live"`
	Governor        string `json:"governor"`
	Yielded         int64  `json:"yielded"`
	Balance         int64  `json:"balance"`
	Paused          string `json:"paused"`
	Symbol          string `json:"symbol"`
	Domain          uint32 `json:"domain"`
	Router          string `json:"router"`
	OldRouter       string `json:"oldRouter"`
	OldUntil        int64  `json:"oldUntil"`
	Lock            int64  `json:"lock"`
	SentTotal       int64  `json:"sentTotal"`
	PaidTotal       int64  `json:"paidTotal"`
	Owed            int64  `json:"owed"`
	Voided          int64  `json:"voided"`
	Lost            int64  `json:"lost"`
	Sweep           int64  `json:"sweep"`
	Cap             int64  `json:"cap"`
	Meter           int64  `json:"meter"`
	CapFloor        int64  `json:"capFloor"`
	Fee             int64  `json:"fee"`
	FeeFloor        int64  `json:"feeFloor"`
	Minimum         int64  `json:"minimum"`
	Maximum         int64  `json:"maximum"`
	OpenClaims      int64  `json:"openClaims"`
	Claims          int64  `json:"claims"`
	Sends           int64  `json:"sends"`
	MigratedTo      string `json:"migratedTo"`
	Importing       bool   `json:"importing"`
	Imported        int    `json:"imported"`
	Refunded        int64  `json:"refunded"`
	RefundedTotal   int64  `json:"refundedTotal"`
	WrittenOff      int64  `json:"writtenOff"`
	WrittenOffTotal int64  `json:"writtenOffTotal"`
	Clocks          Clocks `json:"clocks"`
	Decisions       int64  `json:"decisions"`
	// RouterAddress is the token's router on Ethereum in EIP-55 case (Router keeps its 32 bytes in hex).
	RouterAddress string `json:"routerAddress"`
	// WaitingLast is the last place the route's waiting index gave (waiting/<from>, E4-RL1; 0 before any, and on a
	// route built before the index).
	WaitingLast int64 `json:"waitingLast"`
}

// IsSuccessor says whether this route took, or is taking, a predecessor's pages.
func (r *Route) IsSuccessor() bool { return r.Importing || r.Imported > 0 }

// ImportEnded says whether a successor's import has ended.
func (r *Route) ImportEnded() bool { return r.Imported > 0 && !r.Importing }

// PausedFor says whether a function of the route is paused.
func (r *Route) PausedFor(fn string) bool {
	for _, p := range strings.Split(r.Paused, ",") {
		if strings.TrimSpace(p) == fn {
			return true
		}
	}
	return false
}

// RouteAt reads a route realm's json at a height.
func RouteAt(ctx context.Context, g Reader, route string, height int64) (*Route, error) {
	s, err := g.RenderAt(ctx, route, "json", height)
	if err != nil {
		return nil, err
	}
	var r Route
	if err := JSON(s, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

// Claim is a route's Render("claim/<id>").
type Claim struct {
	ID     string `json:"id"`
	To     string `json:"to"`
	Amount int64  `json:"amount"`
	Paid   int64  `json:"paid"`
	State  string `json:"state"`
	By     string `json:"by"`
	At     int64  `json:"at"`
	Height int64  `json:"height"`
	Seq    int64  `json:"seq"`
	Swept  int64  `json:"swept"`
}

// ClaimAt is a claim by id at a height; nil if the route knows no such id.
func ClaimAt(ctx context.Context, g Reader, route, id string, height int64) (*Claim, error) {
	s, err := g.RenderAt(ctx, route, "claim/"+id, height)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(s) == "null" {
		return nil, nil
	}
	var c Claim
	if err := JSON(s, &c); err != nil {
		return nil, err
	}
	return &c, nil
}

// ClaimsPage is how many waiting claims a route's Render("claims/<from>") holds. The page walks every waiting claim
// before <from> (p/hlroute's ClaimsPage), so its query gas grows with the offset — out of the 3B past ≈1,600 waiting
// claims on gno v1.5.1 (the realms audit's E4-RL1): a reader reads it only on a route built before the waiting index,
// and its first pages only (the monitor's walkClaims).
const ClaimsPage = 100

// WaitingEntry is one entry of a route's waiting index, Render("waiting/<from>") (E4-RL1, the realms' fold F15d):
// claim/<id>'s object with its place first, or {"pos","id","state":"gone"} for a claim no longer on the route. Every
// claim open on the route is in the index once; an entry whose state is not "open" has left the queue and stays until
// the route takes it out, for the reader to skip; a claim open again (a restore, a credit by vote) takes a new place
// above every place given before.
type WaitingEntry struct {
	Pos int64 `json:"pos"`
	Claim
}

// WaitingPage is the most entries a route's waiting/<from> holds; a page holding fewer is the index's end.
const WaitingPage = 100

// WaitingAt reads one page of a route's waiting index at a height: the entries from place from on (0 reads as 1), in
// increasing place, each at or past from — a page whose places do not is no page of the index. Its cost is a seek and
// the page wherever from is (≈219M query gas a full page on a gno v1.5.1 node, F15d). unknown is true when the route
// renders the path as unknown: a route built before the index, which keeps claims/<from> alone.
func WaitingAt(ctx context.Context, g Reader, route string, from, height int64) (entries []WaitingEntry, unknown bool, err error) {
	if from < 1 {
		from = 1
	}
	path := "waiting/" + strconv.FormatInt(from, 10)
	s, err := g.RenderAt(ctx, route, path, height)
	if err != nil {
		return nil, false, err
	}
	if UnknownPath(s) {
		return nil, true, nil
	}
	if err := JSON(s, &entries); err != nil {
		return nil, false, fmt.Errorf("%s:%s: %w", route, path, err)
	}
	if len(entries) > WaitingPage {
		return nil, false, fmt.Errorf("%s:%s holds %d entries (a page holds at most %d)", route, path, len(entries), WaitingPage)
	}
	last := from - 1
	for i, e := range entries {
		if e.Pos <= last {
			return nil, false, fmt.Errorf("%s:%s holds place %d at entry %d after place %d (a page holds increasing places from %d)", route, path, e.Pos, i, last, from)
		}
		last = e.Pos
	}
	return entries, false, nil
}

// Send is a route's Render("send/<id>"): the send record under a message id, with the refund and write-off marks.
type Send struct {
	ID         string `json:"id"`
	From       string `json:"from"`
	Amount     int64  `json:"amount"`
	At         int64  `json:"at"`
	Imported   bool   `json:"imported"`
	Refunded   Mark   `json:"refunded"`
	WrittenOff Mark   `json:"writtenOff"`
}

// Mark is a send's refund or write-off mark: a time (0 for not), or a bool.
type Mark bool

func (m *Mark) UnmarshalJSON(b []byte) error {
	s := strings.TrimSpace(string(b))
	switch {
	case s == "true":
		*m = true
	case s == "false", s == "0", s == "null", s == `""`:
		*m = false
	default:
		n, err := strconv.ParseInt(strings.Trim(s, `"`), 10, 64)
		if err != nil {
			return fmt.Errorf("a mark of %s", s)
		}
		*m = n != 0
	}
	return nil
}

// SendAt is a route's send record by message id at a height; nil when the route holds none. A realm that renders no
// such page is an error: the refund mark is read before anything is sent, never assumed (AQ2).
func SendAt(ctx context.Context, g Reader, route, id string, height int64) (*Send, error) {
	s, err := g.RenderAt(ctx, route, "send/"+id, height)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(s) == "null" {
		return nil, nil
	}
	var sd Send
	if err := JSON(s, &sd); err != nil {
		return nil, fmt.Errorf("%s renders no send record at send/%s: %w", route, id, err)
	}
	return &sd, nil
}

// ------------------------------------------------------------------- decisions --

// Decision is one entry of a realm's Render("decisions.json").
type Decision struct {
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
func (d Decision) Queued() bool { return d.State == "pending" && d.Due > d.Recorded }

// DecisionsPage is the most decisions a realm's Render("decisions.json/<from>") holds: up to this many from decision
// number <from> on (E3-RL4: the whole ledger, Render("decisions.json"), runs out of query gas near 360 decisions). A
// page may hold fewer — the realm ends one before its text would pass 16 KB — so only an empty page ends the ledger.
const DecisionsPage = 20

// decisionsPages bounds the pages one read walks: a realm whose pages never end is no ledger.
const decisionsPages = 100_000

// DecisionsAt reads a realm's decision ledger at a height: every decision, oldest first.
func DecisionsAt(ctx context.Context, g Reader, realm string, height int64) ([]Decision, error) {
	return DecisionsFrom(ctx, g, realm, 1, height)
}

// DecisionsFrom reads a realm's decisions numbered from on (oldest first) at a height, page by page —
// Render("decisions.json/<from>"), at most DecisionsPage at a time, until an empty page ("[]"; a short page is not the
// end: the realm cuts a page at 16 KB of text) — so no read grows with the ledger (E3-RL4). A realm built before the pages renders "unknown path" for them: its whole Render("decisions.json") is read
// instead, and the decisions below from are dropped.
func DecisionsFrom(ctx context.Context, g Reader, realm string, from, height int64) ([]Decision, error) {
	if from < 1 {
		from = 1
	}
	var all []Decision
	for page := 0; ; page++ {
		if page == decisionsPages {
			return nil, fmt.Errorf("%s: its decisions run past %d pages", realm, decisionsPages)
		}
		path := "decisions.json/" + strconv.FormatInt(from, 10)
		s, err := g.RenderAt(ctx, realm, path, height)
		if err != nil {
			return nil, err
		}
		if UnknownPath(s) {
			if page > 0 {
				return nil, fmt.Errorf("%s renders %s as an unknown path after %d pages of its decisions", realm, path, page)
			}
			return wholeDecisions(ctx, g, realm, from, height)
		}
		var ds []Decision
		if err := JSON(s, &ds); err != nil {
			return nil, fmt.Errorf("%s:%s: %w", realm, path, err)
		}
		for i, d := range ds {
			// the page's decisions are numbered from on, one after another: anything else is no page of the ledger
			if d.ID != from+int64(i) {
				return nil, fmt.Errorf("%s:%s holds decision #%d at place %d (want #%d)", realm, path, d.ID, i, from+int64(i))
			}
		}
		if len(ds) == 0 {
			return all, nil
		}
		all = append(all, ds...)
		from += int64(len(ds))
	}
}

// wholeDecisions is an older realm's whole ledger (Render("decisions.json")), from decision number from on.
func wholeDecisions(ctx context.Context, g Reader, realm string, from, height int64) ([]Decision, error) {
	s, err := g.RenderAt(ctx, realm, "decisions.json", height)
	if err != nil {
		return nil, err
	}
	var ds []Decision
	if err := JSON(s, &ds); err != nil {
		return nil, err
	}
	out := ds[:0]
	for _, d := range ds {
		if d.ID >= from {
			out = append(out, d)
		}
	}
	return out, nil
}

// UnknownPath says whether a render is a realm's answer to a path it does not know ("unknown path; see the page").
func UnknownPath(s string) bool { return strings.HasPrefix(strings.TrimSpace(s), "unknown path") }

// DueDecisions are the queued decisions whose delay has passed by the chain's own clock (block time).
func DueDecisions(ds []Decision, chainTime int64) []Decision {
	var out []Decision
	for _, d := range ds {
		if d.Queued() && chainTime >= d.Due {
			out = append(out, d)
		}
	}
	return out
}

// DeliveredAt is the height a message id was delivered at on gno.land (0 if never), as of a height.
func DeliveredAt(ctx context.Context, g Reader, mailbox, id string, height int64) (int64, error) {
	s, err := g.RenderAt(ctx, mailbox, "delivered/"+id, height)
	if err != nil {
		return 0, err
	}
	return strconv.ParseInt(strings.TrimSpace(s), 10, 64)
}

// --------------------------------------------------------------------- messages --

// Msg is one dispatched message as hlmailbox keeps it, checked: the id is the message's, the nonce the one asked
// for, the origin the mailbox's own domain; Height is the block its Dispatch event is in.
type Msg struct {
	Raw    []byte
	Msg    hl.Message
	ID     [32]byte
	Root   [32]byte
	Height int64
}

// ParseMsg reads hlmailbox's msg/<nonce> render.
func ParseMsg(s string, domain, nonce uint32) (*Msg, error) {
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
	if err := JSON(s, &j); err != nil {
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
	return &Msg{Raw: raw, Msg: m, ID: id, Root: root, Height: j.Height}, nil
}

// MessageAt reads one dispatched message by nonce at the head.
func MessageAt(ctx context.Context, g Reader, mailbox string, domain, nonce uint32) (*Msg, error) {
	s, err := g.RenderAt(ctx, mailbox, "msg/"+strconv.FormatUint(uint64(nonce), 10), 0)
	if err != nil {
		return nil, err
	}
	return ParseMsg(s, domain, nonce)
}

// IDHex is a message id in hex.
func (m *Msg) IDHex() string { return hex.EncodeToString(m.ID[:]) }

// ---------------------------------------------------------------------- r/hlgov --

// Gov is r/hlgov's json: its counter, its decision recipient (HlAdmin's address, EIP-55) and the former ones, the
// five proxies kind 8 upgrades (name → EIP-55 address: mailbox, token0, token1, client, gate).
type Gov struct {
	Address     string            `json:"address"`
	Live        bool              `json:"live"`
	Governor    string            `json:"governor"`
	Yielded     int64             `json:"yielded"`
	Destination uint32            `json:"destination"`
	Recipient   string            `json:"recipient"`
	Former      []string          `json:"former"`
	Proxies     map[string]string `json:"proxies"`
	Start       uint64            `json:"start"`
	Last        uint64            `json:"last"`
	Next        uint64            `json:"next"`
	Clocks      Clocks            `json:"clocks"`
	Decisions   int64             `json:"decisions"`
}

// GovAt reads r/hlgov's json at a height.
func GovAt(ctx context.Context, g Reader, gov string, height int64) (*Gov, error) {
	s, err := g.RenderAt(ctx, gov, "json", height)
	if err != nil {
		return nil, err
	}
	var v Gov
	if err := JSON(s, &v); err != nil {
		return nil, err
	}
	return &v, nil
}

// GovDecision is one decision r/hlgov dispatched (decision/<k>): its number, kind, body, last message id, count.
type GovDecision struct {
	Number uint64 `json:"number"`
	Kind   byte   `json:"kind"`
	Body   string `json:"body"`
	ID     string `json:"id"`
	Count  int    `json:"count"`
	At     int64  `json:"at"`
	Text   string `json:"text"`
}

// GovDecisionAt reads r/hlgov's decision k at a height; nil when it renders none.
func GovDecisionAt(ctx context.Context, g Reader, gov string, k uint64, height int64) (*GovDecision, error) {
	s, err := g.RenderAt(ctx, gov, "decision/"+strconv.FormatUint(k, 10), height)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(s) == "null" {
		return nil, nil
	}
	var d GovDecision
	if err := JSON(s, &d); err != nil {
		return nil, err
	}
	return &d, nil
}

// -------------------------------------------------------------------- addresses --

// RealmKey is a realm's address as Hyperlane carries it: twelve zero bytes, then the twenty of its package address.
func RealmKey(pkgPath string) [32]byte {
	k, _ := hl.GnoAddressToBytes32(hl.GnoPackageAddress(pkgPath))
	return k
}

// JSON decodes a render, naming the first bytes of one that does not decode.
func JSON(s string, v any) error {
	if err := json.Unmarshal([]byte(s), v); err != nil {
		return fmt.Errorf("%w: %.200s", err, s)
	}
	return nil
}
