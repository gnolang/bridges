package main

// The value locked through the bridge over time (Jae, 2026-10-15: "i want to see the graph"): per chain, each
// token's lock on gno.land from the launch on, served at GET /api/<chain>/history and drawn on Home and Status.
//
// A node answers a realm's render only as of its newest block: vm/qrender ignores the height it is asked for
// (measured on gnoland-1 through rpc.gno.land and Onbloc alike, 2026-10-15), so the past cannot be read back.
// The history is recorded instead. Every minute the sampler takes the status's locks and keeps a point when one
// moved, at the status's time; the points persist in -state DIR (history-<chain>.json), so a restart loses none.
//
// Before the first reading the history is rebuilt once from the chain's own records, anchored at that reading's
// locks and walked backwards: each send's amount at the time its record carries, each claim's paid part at the
// time this reader saw it paid, else at its credit time. The chain keeps no payout time, so such a point is
// marked estimated, and a change first seen after the sampler was down is estimated too. The rebuild reads the
// activity's index, which keeps the newest transfers of each kind: every transfer of a young deployment. An
// older deployment's history starts at its first reading, with what came before as the launch's level.

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/gnolang/bridges/solana/bridge/agent"
)

// histPoint is the locks after a change, by symbol in base units.
type histPoint struct {
	At    int64            `json:"at"`
	Locks map[string]int64 `json:"locks"`
	Est   bool             `json:"est,omitempty"` // its time is not one the chain records
}

// chainHistory is one chain's record, as it persists.
type chainHistory struct {
	Chain   string      `json:"chain"`
	From    int64       `json:"from"`    // the launch
	Rebuilt bool        `json:"rebuilt"` // the points before Sampled come from the records
	Sampled int64       `json:"sampled"` // the first reading: the points from here on are observed
	Last    int64       `json:"last"`    // the newest reading
	Points  []histPoint `json:"points"`
}

const (
	historyEvery = time.Minute // how often the sampler reads
	// historyGap is how long between two readings makes a change seen after it an estimate (the sampler was down)
	historyGap = 5 * time.Minute
	// historyTries is how many readings wait for the activity's first full index before the history starts
	// without the rebuild
	historyTries = 10
	// historySave is how often an unchanged history is written anyway, so the gap test survives a restart
	historySave = 10 * time.Minute
)

// historyState is the server's part: the records by chain, where they persist, and the rebuild's waits.
type historyState struct {
	dir   string
	byID  map[string]*chainHistory
	tries map[string]int
	saved map[string]int64
	said  map[string]string // the last failure logged per chain, so a standing one is logged once
}

func (h *historyState) path(chain string) string {
	if h.dir == "" {
		return ""
	}
	return filepath.Join(h.dir, "history-"+chain+".json")
}

// load reads a chain's record from its file, once; nil when there is none.
func (h *historyState) load(chain string) *chainHistory {
	if c, ok := h.byID[chain]; ok {
		return c
	}
	p := h.path(chain)
	if p == "" {
		return nil
	}
	b, err := os.ReadFile(p)
	if err != nil {
		if !os.IsNotExist(err) {
			log.Printf("hlsite: history %s: %v (starting over)", chain, err)
		}
		return nil
	}
	var c chainHistory
	if err := json.Unmarshal(b, &c); err != nil || c.Chain != chain || len(c.Points) == 0 {
		log.Printf("hlsite: history %s: unreadable (starting over)", chain)
		return nil
	}
	h.byID[chain] = &c
	return &c
}

// save writes a chain's record whole: a new file renamed over the old, so a crash leaves one or the other.
func (h *historyState) save(c *chainHistory) error {
	p := h.path(c.Chain)
	if p == "" {
		return nil
	}
	b, err := json.Marshal(c)
	if err != nil {
		return err
	}
	tmp := p + ".new"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, p); err != nil {
		return err
	}
	h.saved[c.Chain] = c.Last
	return nil
}

// histStatus is what the sampler reads of a status.
type histStatus struct {
	At  int64 `json:"at"`
	Gno struct {
		LaunchedAt int64 `json:"launchedAt"`
	} `json:"gno"`
	Tokens []struct {
		Symbol string `json:"symbol"`
		Lock   int64  `json:"lock"`
		Claims int64  `json:"claims"`
	} `json:"tokens"`
}

// sampleHistory takes one reading of a chain: the first one rebuilds what came before (or waits for the
// activity's index to do so), the later ones keep a point when a lock moved.
func (s *server) sampleHistory(ctx context.Context, ch *ChainConfig, other Other) error {
	body, stale, failure := s.statusNow(ctx, ch, other)
	if body == nil || stale {
		if failure == "" {
			failure = "the status is stale"
		}
		return fmt.Errorf("no fresh status: %s", failure)
	}
	var st histStatus
	if err := json.Unmarshal(body, &st); err != nil {
		return err
	}
	locks, claims := map[string]int64{}, map[string]int64{}
	for _, t := range st.Tokens {
		locks[t.Symbol], claims[t.Symbol] = t.Lock, t.Claims
	}
	at := st.At
	if at == 0 {
		at = s.now().Unix()
	}
	s.histMu.Lock()
	h := s.hist.load(ch.ID)
	s.histMu.Unlock()
	if h == nil {
		return s.startHistory(ctx, ch, other, st.Gno.LaunchedAt, at, locks, claims)
	}
	s.histMu.Lock()
	defer s.histMu.Unlock()
	if at < h.Last {
		return nil // an older reading than the newest one kept (a cached status): nothing new
	}
	last := h.Points[len(h.Points)-1]
	if !sameLocks(last.Locks, locks) {
		h.Points = append(h.Points, histPoint{At: at, Locks: locks, Est: at-h.Last > int64(historyGap/time.Second)})
		h.Last = at
		return s.hist.save(h)
	}
	h.Last = at
	if at-s.hist.saved[ch.ID] >= int64(historySave/time.Second) {
		return s.hist.save(h)
	}
	return nil
}

// startHistory begins a chain's record at its first reading: rebuilt from the activity's index when that holds
// every transfer, else — after historyTries readings — the launch's level and the reading alone. The index is
// read outside the lock (its first reading may take seconds); the record is kept only if none began meanwhile.
func (s *server) startHistory(ctx context.Context, ch *ChainConfig, other Other, launch, at int64, locks, claims map[string]int64) error {
	act := s.activityNow(ctx, ch, other)
	s.histMu.Lock()
	defer s.histMu.Unlock()
	if s.hist.load(ch.ID) != nil {
		return nil
	}
	var points []histPoint
	rebuilt := false
	if activityComplete(act, claims) {
		points, rebuilt = rebuildHistory(launch, at, locks, act), true
	} else if s.hist.tries[ch.ID]++; s.hist.tries[ch.ID] < s.historyTries() {
		return fmt.Errorf("waiting for the activity's first full reading to rebuild the history (%d of %d)", s.hist.tries[ch.ID], s.historyTries())
	} else {
		points = []histPoint{{At: at, Locks: locks}}
		if launch > 0 && launch < at {
			points = append([]histPoint{{At: launch, Locks: copyLocks(locks), Est: true}}, points...)
		}
	}
	h := &chainHistory{Chain: ch.ID, From: launch, Rebuilt: rebuilt, Sampled: at, Last: at, Points: points}
	s.hist.byID[ch.ID] = h
	return s.hist.save(h)
}

// historyTries is how many readings wait for the rebuild: none under the mock and the tests, whose canned world
// counts claims its records do not hold (they test the rebuild directly).
func (s *server) historyTries() int {
	if s.syncRefresh {
		return 1
	}
	return historyTries
}

func sameLocks(a, b map[string]int64) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if w, ok := b[k]; !ok || w != v {
			return false
		}
	}
	return true
}

// activityComplete is whether the activity's reading holds every dispatch hlmailbox made (none missing below
// its count) and every claim each route counts, read without an error: then the sends and the claims in it are
// all there are — a deployment young enough that the index keeps them all.
func activityComplete(act *activityState, claims map[string]int64) bool {
	if act == nil || len(act.errors) > 0 {
		return false
	}
	for n := uint32(0); n < act.count; n++ {
		if _, ok := act.dispatch[n]; !ok {
			return false
		}
	}
	have := map[string]int64{}
	for _, c := range act.claims {
		have[c.Token]++
	}
	for sym, n := range claims {
		if have[sym] < n {
			return false
		}
	}
	return true
}

// rebuildHistory is the history before the first reading: anchored at that reading's locks and walked back
// through the records — each send's amount at its record's time, each claim's paid part when this reader saw it
// paid, else at its credit time (estimated), a swept claim's part at its credit time (estimated) — down to the
// launch, whose level is what the records do not explain (none, when they explain it all).
func rebuildHistory(launch, at int64, locks map[string]int64, act *activityState) []histPoint {
	type delta struct {
		at    int64
		token string
		v     int64
		est   bool
	}
	var ds []delta
	for _, d := range act.dispatch {
		if d.Kind == "send" && d.At > 0 && d.At <= at && d.Token != "" {
			ds = append(ds, delta{d.At, d.Token, d.Amount, false})
		}
	}
	for _, c := range act.claims {
		if c.Paid > 0 {
			t, est := c.PaidAt, false
			if t == 0 {
				t, est = c.At, true
			}
			if t > 0 && t <= at {
				ds = append(ds, delta{t, c.Token, -c.Paid, est})
			}
		}
		if c.Swept > 0 && c.At > 0 && c.At <= at {
			ds = append(ds, delta{c.At, c.Token, -c.Swept, true})
		}
	}
	sort.SliceStable(ds, func(i, j int) bool { return ds[i].at < ds[j].at })
	cur := map[string]int64{}
	for k, v := range locks {
		cur[k] = v
	}
	// walk back: each point is the locks after its time's changes
	var rev []histPoint
	for i := len(ds) - 1; i >= 0; {
		t, est := ds[i].at, false
		p := histPoint{At: t, Locks: copyLocks(cur)}
		for ; i >= 0 && ds[i].at == t; i-- {
			if _, ok := cur[ds[i].token]; ok {
				cur[ds[i].token] -= ds[i].v
			}
			est = est || ds[i].est
		}
		p.Est = est
		rev = append(rev, p)
	}
	start := launch
	if start <= 0 || (len(ds) > 0 && start > ds[0].at) {
		start = at
		if len(ds) > 0 {
			start = ds[0].at
		}
	}
	for k, v := range cur {
		if v < 0 {
			cur[k] = 0 // the records claim more left than came in: no level below nothing
		}
	}
	points := []histPoint{{At: start, Locks: copyLocks(cur)}}
	for i := len(rev) - 1; i >= 0; i-- {
		if rev[i].At == start {
			points[0] = rev[i]
			continue
		}
		points = append(points, rev[i])
	}
	if last := points[len(points)-1]; last.At < at && !sameLocks(last.Locks, locks) {
		points = append(points, histPoint{At: at, Locks: copyLocks(locks)})
	}
	return points
}

func copyLocks(m map[string]int64) map[string]int64 {
	c := make(map[string]int64, len(m))
	for k, v := range m {
		c[k] = v
	}
	return c
}

// historyLoop samples every chain each minute, for the life of the process; a standing failure is logged once.
func (s *server) historyLoop() {
	for {
		for i := range s.cfg.Chains {
			ch := &s.cfg.Chains[i]
			other := s.others[ch.ID]
			if other == nil {
				continue
			}
			ctx, cancel := context.WithTimeout(context.Background(), refreshBudget)
			err := s.sampleHistory(ctx, ch, other)
			cancel()
			msg := ""
			if err != nil {
				msg = agent.Redact(err.Error())
			}
			s.histMu.Lock()
			if msg != s.hist.said[ch.ID] {
				if msg != "" {
					log.Printf("hlsite: history %s: %s", ch.ID, msg)
				}
				s.hist.said[ch.ID] = msg
			}
			s.histMu.Unlock()
		}
		time.Sleep(historyEvery)
	}
}

// historyJSON is what /api/<chain>/history serves: per token its points, each [time, lock] or
// [time, lock, 1] when the time is an estimate, only where the token's lock changed, and its lock now.
type historyJSON struct {
	Chain   string         `json:"chain"`
	From    int64          `json:"from"`
	Rebuilt bool           `json:"rebuilt"`
	Sampled int64          `json:"sampled"`
	Now     int64          `json:"now"`
	Tokens  []tokenHistory `json:"tokens"`
}

type tokenHistory struct {
	Symbol string    `json:"symbol"`
	Lock   int64     `json:"lock"`
	Points [][]int64 `json:"points"`
}

func (s *server) handleHistory(w http.ResponseWriter, r *http.Request) {
	ch, other, ok := s.chainOf(w, r)
	if !ok {
		return
	}
	if s.syncRefresh { // the mock and the tests: the reading is taken in the request
		ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
		err := s.sampleHistory(ctx, ch, other)
		cancel()
		if err != nil {
			jsonError(w, 503, err.Error())
			return
		}
	}
	s.histMu.Lock()
	h := s.hist.load(ch.ID)
	var out historyJSON
	if h != nil {
		out = historyOf(h, ch)
	}
	s.histMu.Unlock()
	if h == nil {
		jsonError(w, 503, "the history's first reading is under way")
		return
	}
	body, _ := json.Marshal(out)
	writeJSON(w, 200, body)
}

// historyOf is a record as served: per configured token, the points where its lock changed (the first always),
// the last reading's level carried to the newest reading's time.
func historyOf(h *chainHistory, ch *ChainConfig) historyJSON {
	out := historyJSON{Chain: h.Chain, From: h.From, Rebuilt: h.Rebuilt, Sampled: h.Sampled, Now: h.Last, Tokens: []tokenHistory{}}
	for _, t := range ch.Tokens {
		th := tokenHistory{Symbol: t.Symbol, Points: [][]int64{}}
		var prev int64
		for i, p := range h.Points {
			v, ok := p.Locks[t.Symbol]
			if !ok {
				continue
			}
			if i > 0 && len(th.Points) > 0 && v == prev {
				continue
			}
			pt := []int64{p.At, v}
			if p.Est {
				pt = append(pt, 1)
			}
			th.Points = append(th.Points, pt)
			prev = v
		}
		th.Lock = prev
		out.Tokens = append(out.Tokens, th)
	}
	return out
}
