package agent

// Fold F18a (round 6): the Go trust path's E6-GT4, items 2 and 3 (item 1, the marks above the index read largest first,
// is f18a_cut_test.go's TestF18aAboveMarksReadLargestFirst).

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"

	"github.com/gnolang/bridges/ethereum/bridge/ethx"
)

// E6-GT4, item 2: a key raised at alarm and at flag level by turns — the sources rule over a flaky third source (own
// node odd: flag; unresolved: alarm) — is posted once as an alarm and once at its escalation, then at its repeat: not at
// every flag raise (the parent: every other poll). A first escalation is still posted at once (F17b's test).
func TestF18aAlternatingKeyPostsOncePerRepeat(t *testing.T) {
	var mu sync.Mutex
	var posts []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		b, _ := io.ReadAll(req.Body)
		var j map[string]any
		_ = json.Unmarshal(b, &j)
		mu.Lock()
		posts = append(posts, fmt.Sprint(j["level"]))
		mu.Unlock()
	}))
	defer srv.Close()
	a := &Alarmer{URL: srv.URL, Source: "test"}
	for i := 0; i < 10; i++ {
		if i%2 == 0 {
			a.Alarm("sources", "the sources disagree (unresolved)")
		} else {
			a.Flag("sources", "own node is the odd one out")
		}
	}
	time.Sleep(300 * time.Millisecond) // the posts are sent in the background
	mu.Lock()
	got := append([]string{}, posts...)
	mu.Unlock()
	sort.Strings(got) // each post is sent from a goroutine of its own
	t.Logf("webhook posts for ten raises by turns: %v", got)
	if len(got) != 2 || got[0] != "alarm" || got[1] != "flag" {
		t.Fatalf("a key raised by turns posted %v, want one alarm and one flag", got)
	}
}

// E6-GT4, item 3: `hlwatch check` reads a target at the pair's finalized block and now says which block, and its age by
// its own time; past half an hour it says what that means (the parent printed the verdict alone).
func TestF18aCheckSaysTheFinalizedBlocksAge(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	fresh := &ethx.Block{Number: 1000, Hash: common.HexToHash("0xab"), Time: uint64(now.Add(-14 * time.Minute).Unix())}
	stale := &ethx.Block{Number: 900, Hash: common.HexToHash("0xcd"), Time: uint64(now.Add(-2 * time.Hour).Unix())}
	f, s := FinalizedAgeLine(fresh, now), FinalizedAgeLine(stale, now)
	t.Logf("%s\n%s", f, s)
	if !strings.Contains(f, "finalized block 1000") || !strings.Contains(f, "14m0s old") || strings.Contains(f, "stalled") {
		t.Fatalf("a fresh block's line: %s", f)
	}
	if !strings.Contains(s, "2h0m0s old") || !strings.Contains(s, "reads as not yet created") {
		t.Fatalf("a stale block's line: %s", s)
	}
}
