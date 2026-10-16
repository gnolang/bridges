package hlgov

import (
	"strings"
	"testing"
)

// The render-gas probes of the pages the machines read here (E4-RL2): json and decision/<n> (the monitors and the
// executor), the page (hleth's audit reads its Ethereum seats there) and decisions.json/<from> (the executor), each at
// its bound — twenty dispatched decisions of the longest text a builder writes: an upgrade carrying the most call
// bytes the decision body holds (maxUpgradeCall), whose text writes them in hex. The file runs after mainnet_test.gno,
// with the governor yielded: each decision is GovDAO's.

var (
	gasFirst int64  // the first of the twenty in the ledger
	gasSent  uint64 // the last number dispatched
)

func TestGovPagesSetup(cur realm, t *testing.T) {
	proxy := ea(proxies[0])
	hash := "0x" + strings.Repeat("ab", 32)
	call := "0x" + strings.Repeat("cd", maxUpgradeCall)
	gasFirst = ledger.Last() + 1
	for i := 0; i < pageDecisions; i++ {
		impl := "0x" + strings.Repeat("7", 38) + string("0123456789abcdef"[i/16]) + string("0123456789abcdef"[i%16])
		pass(cur, NewUpgradeRequest(cross(cur), proxy, impl, hash, call))
	}
	gasSent = last
	if ledger.Last() != gasFirst+pageDecisions-1 || len(ledger.Get(gasFirst).Text) < 900 {
		t.Fatalf("the twenty upgrades: ledger at %d (from %d)", ledger.Last(), gasFirst)
	}
}

// TestRenderGasGovPage: the page, its twenty newest decisions and twenty sent all of the longest text.
func TestRenderGasGovPage(cur realm, t *testing.T) {
	if p := Render(""); len(p) < 2*pageDecisions*900 {
		t.Fatalf("the page: %d bytes", len(p))
	}
}

// TestRenderGasGovDecision: decision/<n> of a dispatched upgrade at the most call bytes.
func TestRenderGasGovDecision(cur realm, t *testing.T) {
	if p := Render("decision/" + u64s(gasSent)); len(p) < 2_000 {
		t.Fatalf("decision/%d: %d bytes", gasSent, len(p))
	}
}

// TestRenderGasGovJSON: the json the monitors read r/hlgov by.
func TestRenderGasGovJSON(cur realm, t *testing.T) {
	if p := Render("json"); len(p) < 500 {
		t.Fatalf("json: %s", p)
	}
}

// TestRenderGasGovDecisions: decisions.json/<from> over the twenty upgrades: as many as 16 KB of text takes.
func TestRenderGasGovDecisions(cur realm, t *testing.T) {
	if p := Render("decisions.json/" + itoa(gasFirst)); len(p) < 15_000 {
		t.Fatalf("decisions.json/%d: %d bytes", gasFirst, len(p))
	}
}

// TestGovPageBounded (E4-RL2): the page's decisions are the newest twenty, newest first, and a count of the rest —
// never p/decisions' walk of the whole ledger, which every read of the page (hleth's audit) would pay for, more with
// every decision this realm makes.
func TestGovPageBounded(cur realm, t *testing.T) {
	p := Render("")
	i := strings.Index(p, "\n## Decisions\n\n")
	if i < 0 || ledger.Last() <= pageDecisions {
		t.Fatalf("no decisions section, or no more than %d decisions (%d)", pageDecisions, ledger.Last())
	}
	sec := p[i+len("\n## Decisions\n\n"):]
	if n := strings.Count(sec, "- #"); n != pageDecisions || strings.Contains(sec, "### ") || !strings.HasPrefix(sec, "- #"+itoa(ledger.Last())+" upgrade, applied: ") || !strings.Contains(sec, "- … and "+itoa(ledger.Last()-pageDecisions)+" before them") {
		t.Fatalf("the page's decisions (%d listed of %d):\n%s", n, ledger.Last(), sec)
	}
}
