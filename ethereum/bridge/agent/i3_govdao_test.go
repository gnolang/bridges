package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gnolang/bridges/ethereum/bridge/gnoside"
)

// i3GovDAO renders GovDAO's list as impl v0 does — newest first, five a page, each page linking the next — over
// proposals numbered 0..n-1, the open ones (id → the realm its executor was created in) ACTIVE with their own page, the
// rest ACCEPTED and REJECTED in turn; it answers a counter of the list's pages read.
func i3GovDAO(r *monRig, n int, open map[int]string) func() int {
	item := func(id int) string {
		status := "REJECTED"
		if id%2 == 0 {
			status = "ACCEPTED"
		}
		if _, ok := open[id]; ok {
			status = "ACTIVE"
		}
		return fmt.Sprintf("### [Prop #%d - p%d](/r/gov/dao:%d)\nAuthor: g1jg8mtutu9khhfwc4nxmuhcpftf0pajdhfvsqf5\n\nStatus: %s\n\nTiers eligible to vote: T1, T2, T3\n\n---\n\n", id, id, id, status)
	}
	for page := 1; 5*(page-1) < n; page++ {
		var b strings.Builder
		b.WriteString("# GovDAO\n## Members\n[> Go to Memberstore <](/r/gov/dao/memberstore/v0)\n## Proposals\n")
		for id := n - 1 - 5*(page-1); id >= 0 && id > n-1-5*page; id-- {
			b.WriteString(item(id))
		}
		if 5*page < n {
			fmt.Fprintf(&b, "[%d](?page=%d)\n", page+1, page+1)
		}
		path := ""
		if page > 1 {
			path = fmt.Sprintf("?page=%d", page)
		}
		r.gno.set(gnoside.DAOPath, path, b.String())
	}
	for id, realm := range open {
		r.gno.set(gnoside.DAOPath, fmt.Sprint(id), fmt.Sprintf("## Prop #%d - p%d\nAuthor: g1jg8mtutu9khhfwc4nxmuhcpftf0pajdhfvsqf5\n\nrefund\n\nExecutor created in: `%s`\n\n---\n\n### Stats\n- **Proposal is open for votes**\n- YES PERCENT: 0%%\n", id, id, realm))
	}
	var mu sync.Mutex
	pages := 0
	r.gno.mu.Lock()
	r.gno.afterRender = func(pkg, path string) {
		if pkg == gnoside.DAOPath && (path == "" || strings.HasPrefix(path, "?page=")) {
			mu.Lock()
			pages++
			mu.Unlock()
		}
	}
	r.gno.mu.Unlock()
	return func() int {
		mu.Lock()
		defer mu.Unlock()
		n := pages
		pages = 0
		return n
	}
}

// hlwatch read GovDAO's list fifty pages at most, so past 250 proposals (impl v0's five a page) the list was never read
// whole and the page never green (E5-GT2's open question): a pre-armed builder-made proposal older than the 250 newest
// would not be listed. The list is read whole once, and later from the floor the state keeps: down to the lowest
// proposal still open.
func TestI3HlwatchReadsGovDAOPast250(t *testing.T) {
	r := newMonRig(t)
	r.m.State = filepath.Join(t.TempDir(), "hlwatch.json")
	route := m3bNS + "hlgnot"
	pages := i3GovDAO(r, 300, map[int]string{297: route})
	listed := func(v *WatchView) []int64 {
		var ids []int64
		for _, p := range v.Proposals {
			ids = append(ids, p.ID)
		}
		return ids
	}
	v, _, _ := r.view(t)
	read := pages()
	t.Logf("300 proposals: listed %v, %d list pages read; GovDAO not read: %q", listed(v), read, v.DAOErr)
	if v.DAOErr != "" || len(v.Proposals) != 1 || v.Proposals[0].ID != 297 {
		t.Fatalf("past 250 proposals the list is not read whole: %q (listed %v)", v.DAOErr, listed(v))
	}
	var kept struct {
		Floor *struct {
			DAO  string
			Next int64
			Open []int64
		} `json:"daoFloor"`
	}
	state, _ := os.ReadFile(r.m.State)
	if err := json.Unmarshal(state, &kept); err != nil || kept.Floor == nil || kept.Floor.DAO != gnoside.DAOPath || kept.Floor.Next != 300 || len(kept.Floor.Open) != 1 || kept.Floor.Open[0] != 297 {
		t.Fatalf("the floor is not kept in the state (%v): %+v", err, kept.Floor)
	}
	// five newer proposals: read from the floor down to #296
	pages = i3GovDAO(r, 305, map[int]string{297: route})
	r.step(10 * time.Second)
	v, _, _ = r.view(t)
	read = pages()
	t.Logf("305 proposals: listed %v, %d list pages read; GovDAO not read: %q", listed(v), read, v.DAOErr)
	if v.DAOErr != "" || len(v.Proposals) != 1 || read != 2 {
		t.Fatalf("from the floor: listed %v, %d pages, %q", listed(v), read, v.DAOErr)
	}
	for _, b := range v.Blind {
		if strings.Contains(b, "GovDAO") {
			t.Fatalf("the page is blind on GovDAO: %s", b)
		}
	}
}
