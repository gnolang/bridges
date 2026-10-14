package main

import (
	"context"
	"fmt"
	"time"

	"github.com/gnolang/bridges/solana/bridge/agent"
)

// What /api/<chain>/status serves: the gno.land side at one height after the far side's snapshot (the cut's
// order, PLAN.md §5.9), per token the lock against the supply with the identity, the caps, the fees, the flags
// on both sides, the pending decisions and who governs.

type statusJSON struct {
	Chain     string          `json:"chain"`
	Name      string          `json:"name"`
	At        int64           `json:"at"`
	Gno       gnoStatus       `json:"gno"`
	Other     *OtherSnapshot  `json:"other"`
	Tokens    []tokenStatus   `json:"tokens"`
	Decisions []pendingGnoDec `json:"decisions"`
	Errors    []string        `json:"errors"`
}

type gnoStatus struct {
	Height       int64      `json:"height"`
	Time         int64      `json:"time"`
	Live         bool       `json:"live"`
	LaunchedAt   int64      `json:"launchedAt"`
	Governor     string     `json:"governor"`
	Yielded      int64      `json:"yielded"`
	GovernorLine string     `json:"governorLine"`
	DAOGoverns   bool       `json:"daoGoverns"`
	Verifier     string     `json:"verifier"`
	Rule         string     `json:"rule"`
	Signers      int        `json:"signers"`
	Seats        []string   `json:"seats"`
	Flags        []flagView `json:"flags"`
	Gov          *govStatus `json:"gov"`
	Dispatched   uint32     `json:"dispatched"` // the messages hlmailbox sent: the routes' sends and r/hlgov's decisions
	Received     int        `json:"received"`   // the messages it took in: the burns credited
}

type flagView struct {
	Route string   `json:"route"`
	Fn    string   `json:"fn"`
	Seat  int      `json:"seat"`
	Addr  string   `json:"addr"`
	At    int64    `json:"at"`
	IDs   []string `json:"ids"`
}

type govStatus struct {
	Last      uint64 `json:"last"`
	Next      uint64 `json:"next"`
	Recipient string `json:"recipient"`
}

type pendingGnoDec struct {
	Realm    string `json:"realm"`
	ID       int64  `json:"id"`
	Kind     string `json:"kind"`
	Text     string `json:"text"`
	Recorded int64  `json:"recorded"`
	Due      int64  `json:"due"`
}

type tokenStatus struct {
	Symbol       string     `json:"symbol"`
	Realm        string     `json:"realm"`
	Program      string     `json:"program"`
	Mint         string     `json:"mint"`
	Live         bool       `json:"live"`
	Paused       string     `json:"paused"`
	Lock         int64      `json:"lock"`
	Supply       *int64     `json:"supply"` // null when the far side could not be read
	Owed         int64      `json:"owed"`
	Voided       int64      `json:"voided"`
	Lost         int64      `json:"lost"`
	Sweep        int64      `json:"sweep"`
	Gap          int64      `json:"gap"`
	Identity     string     `json:"identity"` // "holds", "BROKEN", or "unread"
	IdentityLine string     `json:"identityLine"`
	Cap          int64      `json:"cap"`
	Meter        int64      `json:"meter"`
	CapFloor     int64      `json:"capFloor"`
	Fee          int64      `json:"fee"`
	Minimum      int64      `json:"minimum"`
	Maximum      int64      `json:"maximum"`
	OpenClaims   int64      `json:"openClaims"`
	Claims       int64      `json:"claims"`
	Sends        int64      `json:"sends"`
	MigratedTo   string     `json:"migratedTo"`
	Limit        *LimitJSON `json:"limit"`
	Mismatch     string     `json:"mismatch"`
	Height       int64      `json:"height"`
	Slot         uint64     `json:"slot"`
}

// identityLine says the numbers of §5.9's identity in one line, for the page and for a grep.
func identityLine(id agent.Identity, verdict string) string {
	line := fmt.Sprintf("%s: lock %d = supply %d + owed %d + voided %d − lost %d (right side %d; slot %d, height %d)", id.Symbol, id.Lock, id.Supply, id.Owed, id.Voided, id.Lost, id.Right(), id.Slot, id.Height)
	switch {
	case verdict == "BROKEN":
		return fmt.Sprintf("BROKEN: the right side exceeds the lock by %d — %s", -id.Gap(), line)
	case id.Gap() > 0:
		return fmt.Sprintf("holds; %d in flight — %s", id.Gap(), line)
	}
	return "holds — " + line
}

// status composes one chain's status: the far side's snapshot first, then gno.land at its head.
func (s *server) status(ctx context.Context, ch *ChainConfig, other Other) (*statusJSON, error) {
	now := s.now()
	snap := other.Snapshot(ctx, now)
	head, err := s.gno.Status(ctx)
	if err != nil {
		return nil, fmt.Errorf("gno.land: %w", err)
	}
	H := head.Height
	st := &statusJSON{Chain: ch.ID, Name: ch.Name, At: now.Unix(), Other: snap, Errors: append([]string{}, snap.Errors...), Tokens: []tokenStatus{}, Decisions: []pendingGnoDec{}}
	st.Gno = gnoStatus{Height: H, Time: head.Time.Unix(), Seats: []string{}, Flags: []flagView{}}
	mb, err := agent.ReadMailbox(ctx, s.gno, s.cfg.Gno.Mailbox, H)
	if err != nil {
		return nil, fmt.Errorf("gno.land: %s: %w", s.cfg.Gno.Mailbox, err)
	}
	s.rememberMailbox(mb)
	st.Gno.Live, st.Gno.LaunchedAt, st.Gno.Governor, st.Gno.Yielded = mb.Live, mb.LaunchedAt, mb.Governor, mb.Yielded
	st.Gno.GovernorLine, st.Gno.DAOGoverns = mb.GovernorLine(), mb.DAOGoverns()
	st.Gno.Verifier, st.Gno.Rule, st.Gno.Signers = mb.ActiveVerifier, mb.Rule, len(mb.SignerKeys)
	st.Gno.Dispatched, st.Gno.Received = mb.Count, mb.Delivered
	if mb.Seats != nil {
		st.Gno.Seats = mb.Seats
	}
	for _, f := range mb.Flags {
		st.Gno.Flags = append(st.Gno.Flags, flagView{Route: f.Route, Fn: f.Fn, Seat: f.Seat, Addr: f.Addr, At: f.At, IDs: f.IDs})
	}
	ac := agentConfig(s.cfg, ch)
	for _, t := range ch.Tokens {
		ts := tokenStatus{Symbol: t.Symbol, Realm: t.Realm, Program: t.Program, Mint: t.Mint, Height: H, Slot: snap.Slot, Identity: "unread"}
		rv, err := agent.ReadRoute(ctx, s.gno, agent.Route{Symbol: t.Symbol, Gno: t.Realm, Token: t.Program}, H)
		if err != nil {
			st.Errors = append(st.Errors, t.Symbol+": "+err.Error())
			st.Tokens = append(st.Tokens, ts)
			continue
		}
		ts.Live, ts.Paused, ts.Lock, ts.Owed, ts.Voided, ts.Lost, ts.Sweep = rv.Live, rv.Paused, rv.Lock, rv.Owed, rv.Voided, rv.Lost, rv.Sweep
		ts.Cap, ts.Meter, ts.Fee, ts.Minimum, ts.Maximum = rv.Cap, rv.Meter, rv.Fee, rv.Minimum, rv.Maximum
		ts.OpenClaims, ts.Claims, ts.Sends, ts.MigratedTo = rv.OpenClaims, rv.Claims, rv.Sends, rv.MigratedTo
		if l, ok := snap.Limits[t.Symbol]; ok {
			l := l
			ts.Limit = &l
			if rv.Maximum > 0 && uint64(rv.Maximum) > l.MaxPerMessage {
				ts.Mismatch = fmt.Sprintf("the per-transfer maximum on gno.land (%d) is above %s's (%d): a transfer between the two would wait on %s until a raise lands", rv.Maximum, ch.Name, l.MaxPerMessage, ch.Name)
			}
		}
		if supply, ok := snap.Supplies[t.Symbol]; ok {
			supply := supply
			ts.Supply = &supply
			id := agent.Identity{Symbol: t.Symbol, Lock: rv.Lock, Owed: rv.Owed, Voided: rv.Voided, Lost: rv.Lost, Height: H, Supply: supply, Slot: snap.Slot}
			ts.Gap = id.Gap()
			ts.Identity = "holds"
			if id.SignerStops() {
				ts.Identity = "BROKEN"
			}
			ts.IdentityLine = identityLine(id, ts.Identity)
		}
		st.Tokens = append(st.Tokens, ts)
	}
	if pend, err := agent.ReadPending(ctx, s.gno, ac, H); err != nil {
		st.Errors = append(st.Errors, "the pending decisions: "+err.Error())
	} else {
		for _, d := range pend {
			st.Decisions = append(st.Decisions, pendingGnoDec{Realm: d.Realm, ID: d.ID, Kind: d.Kind, Text: d.Text, Recorded: d.Recorded, Due: d.Due})
		}
	}
	if s.cfg.Gno.Gov != "" {
		if gv, err := agent.ReadGov(ctx, s.gno, s.cfg.Gno.Gov, H); err != nil {
			st.Errors = append(st.Errors, "r/hlgov: "+err.Error())
		} else {
			st.Gno.Gov = &govStatus{Last: gv.Last, Next: gv.Next, Recipient: gv.Recipient}
		}
	}
	return st, nil
}

// statusTTL is how long a status is served from the cache: ten seconds, the monitors' own poll (§5.9).
const statusTTL = 10 * time.Second
