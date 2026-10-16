package agent

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
)

// claimsPages renders a route's waiting claims in the fake: n claims no burn of the index carries, 100 a page.
func (r *monRig) claimsPages(route string, n int) {
	for at := 0; at <= n; at += 100 {
		var page []string
		for i := at; i < n && i < at+100; i++ {
			page = append(page, fmt.Sprintf(`{"id":"%064x","to":"g1jg8mtutu9khhfwc4nxmuhcpftf0pajdhfvsqf5","amount":5000000,"state":"open","by":"verifier","height":59}`, i+1))
		}
		r.gno.set(route, fmt.Sprintf("claims/%d", at), "["+strings.Join(page, ",")+"]")
	}
}

// claimsReads counts the claims/<from> pages the node renders, by offset — and, since the integration lane I2, the
// waiting/<from> pages a route with the waiting index is read through.
func (r *monRig) claimsReads() func() []string {
	var mu sync.Mutex
	var reads []string
	r.gno.afterRender = func(pkg, path string) {
		if strings.HasPrefix(path, "claims/") || strings.HasPrefix(path, "waiting/") {
			mu.Lock()
			reads = append(reads, path)
			mu.Unlock()
		}
	}
	return func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string{}, reads...)
	}
}

// E4-RL1 (the Go side): a route's claims/<from> walks every waiting claim before <from>, out of query gas past ≈1,600 on
// gno v1.5.1, and the monitor walked ten pages a poll from where the last stopped (hlwatch every page), a failing page
// ending the poll before the Ethereum rules. The walk runs only while hlmailbox holds a delivery the index cannot place:
// with every delivery an indexed burn, no claim page is read at all, however many claims wait.
func TestF15bNoClaimsWalkWhileEveryDeliveryIsPlaced(t *testing.T) {
	r := newMonRig(t)
	route := m3bNS + "hlgnot"
	r.claimsPages(route, 2000)
	reads := r.claimsReads()
	for i := 0; i < 3; i++ {
		if err := r.poll(); err != nil {
			t.Fatal(err)
		}
		r.step(10_000_000_000)
	}
	t.Logf("2,000 claims waiting, every delivery placed: claims pages read %v", reads())
	if len(reads()) != 0 {
		t.Fatalf("claims pages read with nothing unplaced: %v", reads())
	}
}

// While deliveries are unplaced the walk reads the first pages only (offsets 0, 100, 200: each under a third of the
// query gas), says what it did not read, and a page that fails is said while the poll goes on to the Ethereum rules.
// (Since the integration lane I2 this walk is a route's built before the waiting index only — waiting/<from> an unknown
// path there; a route with the index is read from it, i2_waiting_test.go.)
func TestF15bClaimsWalkIsBounded(t *testing.T) {
	r := newMonRig(t)
	route := m3bNS + "hlgnot"
	r.claimsPages(route, 2000)
	r.gno.olderRoute(route)
	r.gno.set(m3bMailPath, "json", strings.Replace(m3bMailboxJSON(nil, 2, []string{"", "", "", "", monWatchGno, ""}), `"count":0`, `"count":0,"delivered":1000`, 1))
	reads := r.claimsReads()
	r.m.ReadOnly = true
	v, ok, why := r.view(t)
	t.Logf("1,000 deliveries unplaced: claims pages read %v; green %v %s", reads(), ok, why)
	for _, p := range reads() {
		if p != "claims/0" && p != "claims/100" && p != "claims/200" && p != "waiting/1" { // waiting/1: the probe that finds the route older
			t.Fatalf("a claims page past the bound was read: %v", reads())
		}
	}
	if ok || !strings.Contains(why, "not among the waiting claims read") || v.Verdict != "" {
		t.Fatalf("the walk's bound is not said, or the poll had no verdict: green %v %s (verdict %q)", ok, why, v.Verdict)
	}
	// a claims page that fails (a query out of gas): said, and the Ethereum rules still run
	r2 := newMonRig(t)
	r2.claimsPages(route, 300)
	r2.gno.olderRoute(route)
	r2.gno.set(m3bMailPath, "json", strings.Replace(m3bMailboxJSON(nil, 2, []string{"", "", "", "", monWatchGno, ""}), `"count":0`, `"count":0,"delivered":1`, 1))
	r2.gno.renderErr = func(pkg, path string) error {
		if path == "claims/100" {
			return errors.New("out of gas in location: ReadFlat")
		}
		return nil
	}
	v2, ok2, why2 := r2.view(t)
	t.Logf("a failing page: green %v %s; record rebuilt %d", ok2, why2, v2.Record.Count)
	if v2.Verdict != "" || !strings.Contains(why2, "the waiting claims of "+route) || v2.Record.Count == 0 {
		t.Fatalf("a failing claims page ended the poll before the Ethereum rules: verdict %q, %s", v2.Verdict, why2)
	}
}
