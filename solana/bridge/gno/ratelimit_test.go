package gno

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// rpc.gno.land's answer on 2026-10-14 from 05:40 UTC: HTTP 429 and a JSON-RPC error naming the wait. Every service
// on the box kept polling through it, every few seconds, and the ban did not lift until they moved to another node
// at 06:16 (the bridge's first stall).
const banBody = `{"jsonrpc":"2.0","id":1,"error":{"code":-32000,"message":"Per-IP rate limit exceeded; retry after 600 seconds.","data":""}}`

const headBody = `{"jsonrpc":"2.0","id":1,"result":{"sync_info":{"latest_block_height":"7","latest_block_time":"2026-10-14T06:00:00Z"}}}`

// banNode answers 429 with body while banned, a head otherwise, and counts what it was asked.
func banNode(t *testing.T, body string, header http.Header) (*httptest.Server, *atomic.Int32, *atomic.Bool) {
	t.Helper()
	var hits atomic.Int32
	var banned atomic.Bool
	banned.Store(true)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if banned.Load() {
			for k, v := range header {
				w.Header()[k] = v
			}
			w.WriteHeader(http.StatusTooManyRequests)
			w.Write([]byte(body))
			return
		}
		w.Write([]byte(headBody))
	}))
	t.Cleanup(srv.Close)
	return srv, &hits, &banned
}

// fakeClock replaces the package clock for one test.
func fakeClock(t *testing.T) *time.Time {
	t.Helper()
	at := time.Date(2026, 10, 14, 6, 0, 0, 0, time.UTC)
	old := now
	now = func() time.Time { return at }
	t.Cleanup(func() { now = old; resetLimits() })
	return &at
}

func TestRateLimitBacksOff(t *testing.T) {
	clock := fakeClock(t)
	srv, hits, banned := banNode(t, banBody, nil)
	ctx := context.Background()
	c := &Client{RPC: srv.URL}
	if _, err := c.Status(ctx); err == nil || !strings.Contains(err.Error(), "retry after 600 seconds") {
		t.Fatalf("the ban's own answer: %v", err)
	}
	// Inside the ban: no request leaves this process, through this client or any other of the same node.
	for i := 0; i < 5; i++ {
		*clock = clock.Add(100 * time.Second)
		if _, err := c.Status(ctx); err == nil || !strings.Contains(err.Error(), "rate-limited") {
			t.Fatalf("call %d inside the ban: %v", i, err)
		}
		if _, err := (&Client{RPC: srv.URL}).Block(ctx, 7); err == nil {
			t.Fatalf("another client's call %d inside the ban answered", i)
		}
	}
	if n := hits.Load(); n != 1 {
		t.Fatalf("the node was asked %d times during its ban; once is the most", n)
	}
	// Another node is not held by this one's ban.
	other, otherHits, otherBanned := banNode(t, banBody, nil)
	otherBanned.Store(false)
	if h, err := (&Client{RPC: other.URL}).Status(ctx); err != nil || h.Height != 7 || otherHits.Load() != 1 {
		t.Fatalf("another node: %v %v (%d asks)", h, err, otherHits.Load())
	}
	// After the wait the node is asked again.
	banned.Store(false)
	*clock = clock.Add(101 * time.Second)
	if h, err := c.Status(ctx); err != nil || h.Height != 7 {
		t.Fatalf("after the ban: %v %v", h, err)
	}
	if n := hits.Load(); n != 2 {
		t.Fatalf("the node was asked %d times; want 2", n)
	}
}

func TestRateLimitWaits(t *testing.T) {
	cases := []struct {
		name   string
		body   string
		header http.Header
		want   time.Duration
	}{
		{"the text's wait", banBody, nil, 600 * time.Second},
		{"the Retry-After header first", banBody, http.Header{"Retry-After": {"120"}}, 120 * time.Second},
		{"a 429 that names no wait", `{"jsonrpc":"2.0","id":1,"error":{"code":-32000,"message":"slow down","data":""}}`, nil, limitDefault},
		{"a 429 that is not JSON", `<html><body>Too Many Requests</body></html>`, nil, limitDefault},
		{"a wait past the cap", strings.Replace(banBody, "600", "86400", 1), nil, limitMax},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clock := fakeClock(t)
			srv, hits, _ := banNode(t, tc.body, tc.header)
			c := &Client{RPC: srv.URL}
			ctx := context.Background()
			if _, err := c.Status(ctx); err == nil {
				t.Fatal("a 429 answered")
			}
			*clock = clock.Add(tc.want - time.Second)
			if _, err := c.Status(ctx); err == nil || hits.Load() != 1 {
				t.Fatalf("a second before the wait ends: %v, %d asks", err, hits.Load())
			}
			*clock = clock.Add(time.Second)
			c.Status(ctx)
			if n := hits.Load(); n != 2 {
				t.Fatalf("the wait ended at %s: %d asks, want 2", tc.want, n)
			}
		})
	}
}

// A node's own error that is not a rate limit holds nothing back.
func TestOtherErrorsDoNotWait(t *testing.T) {
	fakeClock(t)
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Write([]byte(`{"jsonrpc":"2.0","id":1,"error":{"code":-32603,"message":"Internal error","data":"height 9 must be less than or equal to the current blockchain height 7"}}`))
	}))
	defer srv.Close()
	c := &Client{RPC: srv.URL}
	for i := 0; i < 3; i++ {
		if _, err := c.Block(context.Background(), 9); err == nil {
			t.Fatal("an error answered")
		}
	}
	if n := hits.Load(); n != 3 {
		t.Fatalf("asked %d times; want 3", n)
	}
}

// Request is the client's call of a method this package has no reader of (hlsite's `tx`): it waits on a ban too.
func TestRequestWaitsOnBan(t *testing.T) {
	fakeClock(t)
	srv, hits, _ := banNode(t, banBody, nil)
	c := &Client{RPC: srv.URL}
	var out map[string]any
	for i := 0; i < 3; i++ {
		if err := c.Request(context.Background(), "tx", map[string]any{"hash": "AA=="}, &out); err == nil {
			t.Fatal("a ban answered")
		}
	}
	if n := hits.Load(); n != 1 {
		t.Fatalf("asked %d times during the ban; want 1", n)
	}
}
