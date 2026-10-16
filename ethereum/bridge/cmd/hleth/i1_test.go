package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/common"
)

// The integration lane I1's tests of hleth (audit round 3's fold, the cross-lane follow-ups).

// E3-GS4's Go half: HlAdmin's constructor refuses a launch start above 2^32 (fold F11: OutOfOrder, MAX_START — r/hlgov's
// sync sequence reaches at most 2^32 numbers a proposal), and the setup refuses it before its first transaction:
// before, it deployed steps 1–6 and stopped at HlAdmin's creation (step 7) with six steps' gas spent and a deployment
// that can never finish. 2^32 itself is taken: HlAdmin starts there.
func TestAnvilSetupRefusesAStartBeyondHlgovsReach(t *testing.T) {
	artifactsOrSkip(t)
	ctx := context.Background()
	deployer := common.HexToAddress("0xf39Fd6e51aad88F6F4ce6aB8827279cffFb92266")
	for _, start := range []uint64{1<<32 + 1, 1<<64 - 1} {
		url, raw := startAnvil(t)
		cfg := testSetupConfig(t, url, t.TempDir())
		cfg.StartSequence = start
		var out bytes.Buffer
		_, err := runSetup(ctx, cfg, &out)
		if err == nil || !strings.Contains(err.Error(), "above 2^32") {
			t.Fatalf("a setup starting at %d: %v\n%s", start, err, out.String())
		}
		if n, err := nonceOf(t, raw, deployer); err != nil || n != 0 {
			t.Fatalf("the setup refused at start %d sent %d transaction(s) (%v):\n%s", start, n, err, out.String())
		}
	}
	url, raw := startAnvil(t)
	cfg := testSetupConfig(t, url, t.TempDir())
	cfg.StartSequence = 1 << 32
	var out bytes.Buffer
	if _, err := runSetup(ctx, cfg, &out); err != nil {
		t.Fatalf("a setup starting at 2^32: %v\n%s", err, out.String())
	}
	d, err := LoadDeployment(cfg.Out, false)
	if err != nil {
		t.Fatal(err)
	}
	var word common.Hash
	if err := raw.Call(&word, "eth_getStorageAt", d.Addr("hlAdmin"), hlAdminSlot, "latest"); err != nil {
		t.Fatal(err)
	}
	if last := binary.BigEndian.Uint64(word[wordLastSeq:]); last != 1<<32 {
		t.Fatalf("HlAdmin set up at start 2^32 holds lastSeq %d", last)
	}
}
