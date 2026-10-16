package agent

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/gnolang/bridges/solana/bridge/gno"
)

// ledgerNode is a gno node whose realms keep decision ledgers: each realm's whole "decisions.json", and for a realm
// with pages its "decisions.json/<from>" (twenty from <from> on, "[]" past the last) — or a failure per path.
type ledgerNode struct {
	ledgers map[string][]string // realm → its decisions' JSON objects, numbered 1, 2, …
	paged   map[string]bool     // realm → renders pages (else "unknown path; see the page" for them)
	fail    map[string]error    // "realm:path" → the render's failure
	text    map[string]string   // "realm:path" → what it renders instead
	asked   []string
}

func (n *ledgerNode) RenderAt(_ context.Context, pkg, path string, _ int64) (string, error) {
	n.asked = append(n.asked, pkg+":"+path)
	if err := n.fail[pkg+":"+path]; err != nil {
		return "", err
	}
	if s, ok := n.text[pkg+":"+path]; ok {
		return s, nil
	}
	ds, ok := n.ledgers[pkg]
	if !ok {
		return "", fmt.Errorf("no render %s:%s", pkg, path)
	}
	switch {
	case path == "decisions.json":
		return "[" + strings.Join(ds, ",") + "]", nil
	case strings.HasPrefix(path, "decisions.json/"):
		if !n.paged[pkg] {
			return "unknown path; see the page", nil
		}
		from, err := strconv.Atoi(strings.TrimPrefix(path, "decisions.json/"))
		if err != nil || from < 1 || from > len(ds) {
			return "[]", nil
		}
		return "[" + strings.Join(ds[from-1:min(len(ds), from-1+20)], ",") + "]", nil
	}
	return "unknown path; see the page", nil
}

func (n *ledgerNode) Status(context.Context) (gno.Head, error) { return gno.Head{Height: 100}, nil }
func (n *ledgerNode) Block(context.Context, int64) (gno.Head, error) {
	return gno.Head{Height: 100}, nil
}

func ledgerOf(count int, pending ...int) []string {
	p := map[int]bool{}
	for _, i := range pending {
		p[i] = true
	}
	var out []string
	for i := 1; i <= count; i++ {
		state := "applied"
		if p[i] {
			state = "pending"
		}
		out = append(out, fmt.Sprintf(`{"id":%d,"kind":"k%d","text":"t","state":%q,"made":1,"recorded":1,"due":2,"applied":0,"cancelled":0,"note":""}`, i, i, state))
	}
	return out
}

// E4-TL7: the site reads the Ethereum deployment's ledgers by page — a realm past ≈360 decisions, whose whole
// decisions.json is out of vm/qrender's gas, is read page by page to "[]"; a realm with no pages ("unknown path") is
// read whole; one realm that does not read is one error beside the others' decisions, never a blank list.
// ReadPending (the Solana deployment's, unchanged) reads every ledger whole and stops at the first failure.
func TestReadPendingByPage(t *testing.T) {
	const mailbox, route, fees, gov, succ = "gno.land/r/t/eth/hlmailbox", "gno.land/r/t/eth/hlgnot", "gno.land/r/t/eth/hlfees", "gno.land/r/t/eth/hlgov", "gno.land/r/t/eth/hlgnot2"
	n := &ledgerNode{
		ledgers: map[string][]string{mailbox: ledgerOf(400, 3, 399), route: ledgerOf(2, 2), fees: nil, gov: ledgerOf(45, 44), succ: ledgerOf(21, 21)},
		paged:   map[string]bool{mailbox: true, fees: true, gov: true, succ: true}, // the route renders no pages (a realm from before them)
		fail: map[string]error{
			mailbox + ":decisions.json": errors.New("gno query vm/qrender: out of gas in location: ReadFlat"), // ≈360 decisions and up
			gov + ":decisions.json/21":  errors.New("gno rpc abci_query: 502 Bad Gateway"),
		},
		text: map[string]string{},
	}
	cfg := &Config{}
	cfg.Gno.Mailbox, cfg.Gno.Fees, cfg.Gno.Gov = mailbox, fees, gov
	cfg.Routes = []Route{{Symbol: "GNOT", Gno: route}}
	ctx := context.Background()

	got, err := ReadPendingByPage(ctx, n, cfg, 100, succ)
	var listed []string
	for _, d := range got {
		listed = append(listed, fmt.Sprintf("%s#%d", d.Realm[strings.LastIndex(d.Realm, "/")+1:], d.ID))
	}
	if strings.Join(listed, " ") != "hlmailbox#3 hlmailbox#399 hlgnot#2 hlgnot2#21" {
		t.Errorf("the pending decisions read by page: %v", listed)
	}
	if j, ok := err.(interface{ Unwrap() []error }); !ok || len(j.Unwrap()) != 1 || !strings.Contains(err.Error(), gov+": gno rpc abci_query: 502 Bad Gateway") {
		t.Errorf("r/hlgov's failing page: %v (want one error, naming it)", err)
	}
	for _, p := range n.asked {
		if p == mailbox+":decisions.json" {
			t.Errorf("the paged mailbox's whole ledger was read")
		}
	}
	if !strings.Contains(strings.Join(n.asked, " "), route+":decisions.json/1 "+route+":decisions.json ") {
		t.Errorf("the route without pages is not read whole after its first page: %v", n.asked)
	}
	// the Solana reader as it was: every ledger whole, the mailbox's out of gas, nothing listed
	if ds, err := ReadPending(ctx, n, cfg, 100, succ); err == nil || ds != nil {
		t.Errorf("ReadPending over a ledger out of gas: %v %v", ds, err)
	}

	// a page out of place, and "unknown path" after a page: no page of the ledger, that realm's error alone
	delete(n.fail, gov+":decisions.json/21")
	n.ledgers[gov][20] = strings.Replace(n.ledgers[gov][20], `"id":21`, `"id":22`, 1)
	n.text[succ+":decisions.json/21"] = "unknown path; see the page"
	got, err = ReadPendingByPage(ctx, n, cfg, 100, succ)
	if err == nil || !strings.Contains(err.Error(), gov+": decisions.json/21 holds decision #22 at place 0 (want #21)") ||
		!strings.Contains(err.Error(), succ+": it renders decisions.json/21 as an unknown path after 1 pages of its decisions") || len(got) != 3 {
		t.Errorf("a page out of place, an unknown path after a page: %d listed, %v", len(got), err)
	}
}
