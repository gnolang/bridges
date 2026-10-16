package main

import (
	"bytes"
	"context"
	"fmt"
	"math/big"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/rpc"
)

// E3-CF4: the gate's launch day cap is the smaller of 100,000 and a tenth of the lock the launch notice expects, and
// its maximum the smaller of 25,000 and the cap (§5.4, as `hlsol setup` compiles the pausable ISM's, F10) — each
// token's own, since the gate keeps a pair a token; a maximum below the route realms' compiled one is refused (AR4);
// and with no expected lock no chain but a local one is set up.
func TestSetupLimitsFromTheExpectedLock(t *testing.T) {
	limits := func(chain int64, c setupConfig) (setupConfig, string, error) {
		var b bytes.Buffer
		s := &setup{cfg: c, out: &b, cn: &conn{chainID: big.NewInt(chain)}}
		err := s.limits()
		return s.cfg, b.String(), err
	}
	const one = 1_000_000 // base units a token
	for _, x := range []struct {
		lock         uint64
		max, cap     uint64
		refusedBelow bool
	}{
		{2_000_000 * one, 25_000 * one, 100_000 * one, false}, // a tenth above 100,000: the ceiling
		{1_000_000 * one, 25_000 * one, 100_000 * one, false}, // a tenth at it
		{300_000 * one, 25_000 * one, 30_000 * one, false},    // a tenth between: the tenth
		{250_000 * one, 25_000 * one, 25_000 * one, false},    // a tenth at the maximum
		{50_000 * one, 5_000 * one, 5_000 * one, true},        // a tenth below the maximum: below the realms' too
	} {
		mx, dc := launchLimits(x.lock)
		if mx != x.max || dc != x.cap {
			t.Fatalf("lock %d: %d a message, %d a day (want %d and %d)", x.lock, mx, dc, x.max, x.cap)
		}
		c, _, err := limits(1, setupConfig{ExpectedLock: [2]uint64{x.lock, 1_000_000 * one}})
		switch {
		case x.refusedBelow && (err == nil || !strings.Contains(err.Error(), "below the route realm's compiled maximum")):
			t.Fatalf("lock %d: a maximum below the realms' was not refused: %v", x.lock, err)
		case !x.refusedBelow && (err != nil || c.MaxPerMessage[0] != x.max || c.DayCap[0] != x.cap || c.DayCap[1] != 100_000*one):
			t.Fatalf("lock %d on mainnet: %+v %v", x.lock, c, err)
		}
	}
	// a small lock with the realms lowered to match (-launch-maximum): taken
	if c, _, err := limits(1, setupConfig{ExpectedLock: [2]uint64{50_000 * one, 50_000 * one}, LaunchMaximum: 5_000 * one}); err != nil || c.MaxPerMessage != [2]uint64{5_000 * one, 5_000 * one} || c.DayCap != [2]uint64{5_000 * one, 5_000 * one} {
		t.Fatalf("a small lock with the realms lowered: %+v %v", c, err)
	}
	// no expected lock: refused on mainnet and Sepolia, a local chain's ceilings on anvil's
	for _, chain := range []int64{1, 11155111} {
		if _, _, err := limits(chain, setupConfig{}); err == nil || !strings.Contains(err.Error(), "-expected-lock-gnot and -expected-lock-bubble (base units) are required on chain "+strconv.FormatInt(chain, 10)) {
			t.Fatalf("chain %d with no expected lock: %v", chain, err)
		}
	}
	if c, text, err := limits(localChainID, setupConfig{}); err != nil || c.MaxPerMessage != [2]uint64{25_000 * one, 25_000 * one} || c.DayCap != [2]uint64{100_000 * one, 100_000 * one} || !strings.Contains(text, "a local chain's, no lock expected") {
		t.Fatalf("a local chain with no lock: %+v %v\n%s", c, err, text)
	}
	// one lock without the other, and explicit limits that are not what the lock compiles: refused
	if _, _, err := limits(1, setupConfig{ExpectedLock: [2]uint64{1_000_000 * one, 0}}); err == nil || !strings.Contains(err.Error(), "go together") {
		t.Fatalf("one lock alone: %v", err)
	}
	if _, _, err := limits(1, setupConfig{ExpectedLock: [2]uint64{300_000 * one, 1_000_000 * one}, DayCap: [2]uint64{100_000 * one, 100_000 * one}}); err == nil || !strings.Contains(err.Error(), "are not what its expected lock 300000000000 compiles") {
		t.Fatalf("a day cap that is not the lock's: %v", err)
	}
	if c, _, err := limits(1, setupConfig{ExpectedLock: [2]uint64{300_000 * one, 1_000_000 * one}, DayCap: [2]uint64{30_000 * one, 100_000 * one}, MaxPerMessage: [2]uint64{25_000 * one, 25_000 * one}}); err != nil || c.DayCap[0] != 30_000*one {
		t.Fatalf("explicit limits that are the lock's: %+v %v", c, err)
	}
}

// E3-CF4 on a chain: anvil as Sepolia (chain id 11155111). With no expected lock the setup used to deploy all sixteen
// contracts with a day cap of 100,000 a token whatever the lock; now it refuses before its first transaction. (Written
// to the setup's API before the fold too: run on eb7cebb4 the setup goes through.)
func TestAnvilSetupRefusesNoExpectedLockOffALocalChain(t *testing.T) {
	artifactsOrSkip(t)
	url, raw := startAnvilChain(t, 11155111)
	dir := t.TempDir()
	cfg := testSetupConfig(t, url, dir)
	cfg.LocalDomain = 11155111
	var out bytes.Buffer
	_, err := runSetup(context.Background(), cfg, &out)
	if err == nil || !strings.Contains(err.Error(), "required on chain 11155111") {
		t.Fatalf("a setup on chain 11155111 with no expected lock: %v\n%s", err, out.String())
	}
	if n, err := nonceOf(t, raw, common.HexToAddress("0xf39Fd6e51aad88F6F4ce6aB8827279cffFb92266")); err != nil || n != 0 {
		t.Fatalf("the refused setup sent %d transaction(s) (%v)", n, err)
	}
}

// startAnvilChain is startAnvil at another chain id.
func startAnvilChain(t *testing.T, chainID int) (string, *rpc.Client) {
	t.Helper()
	bin, err := exec.LookPath("anvil")
	if err != nil {
		bin = "/root/.foundry/bin/anvil"
		if _, err := os.Stat(bin); err != nil {
			t.Skip("no anvil")
		}
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	cmd := exec.Command(bin, "--port", strconv.Itoa(port), "--hardfork", "osaka", "--chain-id", strconv.Itoa(chainID), "--silent")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	url := fmt.Sprintf("http://127.0.0.1:%d", port)
	c, err := rpc.Dial(url)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; ; i++ {
		var id hexutil.Big
		if err := c.CallContext(context.Background(), &id, "eth_chainId"); err == nil {
			break
		}
		if i > 100 {
			t.Fatal("anvil did not start")
		}
		time.Sleep(100 * time.Millisecond)
	}
	return url, c
}
