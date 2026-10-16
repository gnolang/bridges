package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/gnolang/bridges/solana/bridge/gnolight"
)

// E3-CF2, E3-RT5, E3-GS4: a deployment whose client is rooted in a header and a validator set of the deployer's own
// making — here -trusted-file's fabricated header over four keys the test made up, consistent and within the hour,
// exactly what initialize checks — agrees with its own record everywhere, so the audit PASSed it whenever no gno
// node was given (the creation input, the client's state and its first record all say the same forged thing). Now
// the verdict without a gno source is UNVERIFIED, never PASS; against gno.land's own block at that height (another
// header, another set) the audit FAILs, naming the hash, the time and the set; an honest deployment, whose root is
// gno.land's block, PASSes. (Written to the audit's API before the fold too: run on eb7cebb4 it fails at the first
// verdict, which PASSes.)
func TestAnvilAuditHoldsTheTrustRootToGnoLand(t *testing.T) {
	artifactsOrSkip(t)
	url, _ := startAnvil(t)
	dir := t.TempDir()
	ctx := context.Background()
	cfg := testSetupConfig(t, url, dir)
	var out bytes.Buffer
	if _, err := runSetup(ctx, cfg, &out); err != nil {
		t.Fatalf("setup: %v\n%s", err, out.String())
	}
	d, err := LoadDeployment(cfg.Out, false)
	if err != nil {
		t.Fatal(err)
	}
	acfg := withLaunchFlags(auditConfig{RPC: url, Deployment: cfg.Out, ContractsOut: cfg.ContractsOut, HyperlaneDir: cfg.HyperlaneDir, At: "latest",
		MailboxPkgPath: cfg.MailboxPkgPath, Hlgov: cfg.Hlgov, GnotRoute: cfg.GnotRoute, BubbleRoute: cfg.BubbleRoute}, cfg) // every launch value the member's (E4-TL1)
	audit := func() ([]string, string) {
		t.Helper()
		var b bytes.Buffer
		fails, err := runAudit(ctx, acfg, &b)
		if err != nil {
			t.Fatalf("audit: %v\n%s", err, b.String())
		}
		return fails, b.String()
	}

	// 1. no gno source: the root of the deployer's making is the record's, the chain's and the creation input's —
	// nothing fails, and nothing may PASS
	fails, text := audit()
	if len(fails) != 0 || strings.Contains(text, "hleth audit: PASS") {
		t.Fatalf("a client rooted in a header of the deployer's making, audited with no gno source: %d failures, and the verdict must not be PASS\n%s", len(fails), text)
	}
	if !strings.Contains(text, "hleth audit: UNVERIFIED (1)") || !strings.Contains(text, "  - the client's trust root — the header (height 100") {
		t.Fatalf("no UNVERIFIED trust root:\n%s", text)
	}

	// 2. gno.land's own block 100 is another header (another time, so another hash) over another set: FAIL, named
	real := trustedFile(t, dir, time.Now().Add(-90*time.Second), false)
	fg := honestGno(t, cfg, d)
	fg.headers[100] = headerOf(t, real)
	own := &gnolight.ValidatorSet{}
	for i := 0; i < 4; i++ {
		own.Validators = append(own.Validators, gnolight.Validator{PubKey: sha256.Sum256([]byte(fmt.Sprintf("gno.land's own validator %d", i))), Power: 10})
	}
	own.Sort() // in tm2's order, as a node serves a set
	fg.validators = nil
	for _, v := range own.Validators {
		fg.validators = append(fg.validators, v.PubKey)
	}
	acfg.GnoRPC = fg.serve(t).URL
	fails, text = audit()
	if len(fails) == 0 || !strings.Contains(text, "hleth audit: FAIL") {
		t.Fatalf("a forged trust root against gno.land's own block: %d failures\n%s", len(fails), text)
	}
	for _, want := range []string{"the trust root's hash " + d.Launch.TrustedHash + " is gno.land's block 100 hash", "the trust root's time ", "the validator set the client took", "the client's first record is gno.land's block 100"} {
		found := false
		for _, f := range fails {
			found = found || strings.Contains(f, want)
		}
		if !found {
			t.Fatalf("no failure names %q:\n%s", want, strings.Join(fails, "\n"))
		}
	}

	// 3. an honest deployment: gno.land's block 100 is the header the client took, over the set it took — PASS
	acfg.GnoRPC = honestGno(t, cfg, d).serve(t).URL
	if fails, text = audit(); len(fails) != 0 || !strings.Contains(text, "hleth audit: PASS") {
		t.Fatalf("an honest trust root: %d failures\n%s", len(fails), text)
	}
	for _, want := range []string{
		"ok   the header the client's initialize took (the record's sixteen leaves, which the client's creation input carries byte for byte) is gno.land's block 100's header, leaf for leaf",
		"ok   the trust root's time ", "ok   the trust root's validators hash ", "ok   the validator set the client took (the record's 4 validators",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("the honest audit lacks %q:\n%s", want, text)
		}
	}

	// 4. two gno sources that disagree on the block are no reading: the trust root fails, not read
	acfg.GnoRPC = honestGno(t, cfg, d).serve(t).URL + "," + fg.serve(t).URL
	if fails, text = audit(); !strings.Contains(strings.Join(fails, "\n"), "the gno sources disagree") {
		t.Fatalf("two gno sources that disagree: %d failures\n%s", len(fails), text)
	}
}
