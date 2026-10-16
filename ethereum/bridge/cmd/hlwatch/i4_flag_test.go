package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/gnolang/bridges/solana/bridge/gno"
	"github.com/gnolang/bridges/ethereum/bridge/agent"
	"github.com/gnolang/bridges/ethereum/bridge/gnoside"
)

// i4FlagGas is SetFlag's gas for a guardian's payouts flag naming 256 ids on a gno v1.5.1 node, the list kept sorted
// and without repeats (fold-r6/f18b.md: 474,586,016; ≈499M before the sort).
const i4FlagGas = 474_586_016

// i4Gnokey writes a gnokey that answers as a node would a call of SetFlag needing i4FlagGas (ClearFlag 2M): out of gas
// under a ceiling below that, else OK! with the gas used (and a hash and a height for a broadcast). Each run logs
// "<func> <-simulate> <-gas-wanted> <ids named>" to log, and the ids it was handed to ids.
func i4Gnokey(t *testing.T, dir string) (bin, log, ids string) {
	t.Helper()
	bin, log, ids = filepath.Join(dir, "gnokey"), filepath.Join(dir, "calls.log"), filepath.Join(dir, "ids")
	script := `#!/bin/sh
gas=""; sim=""; fn=""; list=""; prev=""; n=0
for a in "$@"; do
  case "$prev" in
    -gas-wanted) gas=$a ;;
    -simulate) sim=$a ;;
    -func) fn=$a ;;
    -args) n=$((n+1)); [ $n -eq 3 ] && list=$a ;;
  esac
  prev=$a
done
need=2000000; [ "$fn" = SetFlag ] && need=` + strconv.Itoa(i4FlagGas) + `
named=0; [ -n "$list" ] && named=$(printf '%s\n' "$list" | tr ',' '\n' | grep -c .)
echo "$fn $sim $gas $named" >> ` + log + `
[ -n "$list" ] && printf '%s' "$list" > ` + ids + `
if [ "$gas" -lt "$need" ]; then echo '--= Error =--'; echo 'Data: out of gas in location: CPUCycles'; exit 1; fi
echo 'OK!'; echo "GAS USED:   $need"
[ "$sim" = test ] && { echo 'HEIGHT:     9'; echo 'TX HASH:    TXFLAG'; }
exit 0
`
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin, log, ids
}

// I4 (the round-6 fold's integration; F18c's note for the trust side): hlwatch's flag sets and clears hlmailbox's flag
// through gnoside.CallUpToTheBlock — confirmed, nothing to wire: stroke hands flagGno the source client
// (cfg.SourceClient, a *gno.Client), and flagGno sizes SetFlag and ClearFlag from their simulations as far as the
// block's gas. A guardian's payouts flag naming 256 ids, hlmailbox's most (maxFlagIDs), is ≈475M on a v1.5.1 node,
// past the 180M the client alone reaches from flagGas: through the client the machines build and a gnokey that runs
// out of gas under it, the flag is simulated under 45M, 180M and 720M and broadcast once, sized from the simulation
// that fit (gno.GasFor), every one of its 256 ids in its argument; the client's own Call under flagGas — the path
// before F17d's fix — runs out of gas and sends nothing.
func TestI4FlagOf256IdsIsSent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { http.Error(w, "no gas price here", 500) }))
	defer srv.Close()
	dir := t.TempDir()
	bin, log, idsFile := i4Gnokey(t, dir)
	cfg := &agent.Config{}
	cfg.Gno.ChainID, cfg.Gno.Gnokey, cfg.Gno.GasFee = "dev", bin, "1000ugnot"
	c := cfg.SourceClient(srv.URL)
	c.Home, c.Key = dir, "guardian"
	ids := make([]string, 256)
	for i := range ids {
		ids[i] = fmt.Sprintf("%062x%02x", i+1, 0xc1)
	}
	list := strings.Join(ids, ",")
	const mailbox, route = "gno.land/r/x/eth/hlmailbox", "gno.land/r/x/eth/hlgnot"

	// the path before F17d's fix: the client alone, under flagGas, reaches 180M and runs out of gas — nothing sent
	if _, err := c.Call(context.Background(), mailbox, "SetFlag", flagGas, route, "payouts", list); err == nil || !gnoside.OutOfGas(err) {
		t.Fatalf("the client alone under %d: %v (want an out-of-gas)", flagGas, err)
	}
	if err := os.Remove(log); err != nil {
		t.Fatal(err)
	}

	r, err := flagGno(context.Background(), c, mailbox, true, route, "payouts", list)
	if err != nil || r == nil || r.Hash != "TXFLAG" {
		t.Fatalf("the flag of 256 ids was not sent: %+v %v", r, err)
	}
	b, _ := os.ReadFile(log)
	want := "SetFlag only 45000000 256\nSetFlag only 180000000 256\nSetFlag only 720000000 256\nSetFlag test " + strconv.FormatInt(gno.GasFor(i4FlagGas), 10) + " 256\n"
	if string(b) != want {
		t.Fatalf("the flag of 256 ids, gnokey's runs:\n%s\nwant:\n%s", b, want)
	}
	if got, _ := os.ReadFile(idsFile); string(got) != list {
		t.Fatalf("the ids sent are not the 256 named (%d bytes, want %d)", len(got), len(list))
	}
	// a clear names no ids: one simulation under flagGas fits
	if err := os.Remove(log); err != nil {
		t.Fatal(err)
	}
	if r, err := flagGno(context.Background(), c, mailbox, false, route, "payouts", ""); err != nil || r == nil || r.Hash != "TXFLAG" {
		t.Fatalf("the clear: %+v %v", r, err)
	}
	if b, _ := os.ReadFile(log); string(b) != "ClearFlag only 45000000 0\nClearFlag test "+strconv.FormatInt(gno.GasFor(2_000_000), 10)+" 0\n" {
		t.Fatalf("the clear, gnokey's runs:\n%s", b)
	}
}
