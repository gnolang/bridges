package main

import (
	"flag"
	"log"
	"time"

	"github.com/gnolang/bridges/ethereum/bridge/agent"
)

// The monitor (eth/bridge/PLAN.md §5.9), the Ethereum → gno.land lane's:
//
//	hlethbridge monitor -config bridge.json -state monitor.json -listen 127.0.0.1:9202 [-every 10s] [-from-block N]
//
// It reads gno.land through its own node (and gno.sources, the public endpoints of the sources and fork rules) and
// Ethereum through the first two providers its config names, which must agree; it flags through the watch keys of
// its config (gno.watchKey in gnokey's keybase, eth.watchKey a key file) — credits and payouts and the gate on an
// invariant break, the gate and HlAdmin on a header mismatch, a gap or a foreign decision, sends while deliveries are
// paused — and alarms by the config's webhook for everything else. Its providers must serve full-history state.
func init() {
	commands["monitor"] = func(args []string) {
		fs := flag.NewFlagSet("monitor", flag.ExitOnError)
		state := fs.String("state", "monitor-state.json", "its state file")
		every := fs.Duration("every", 10*time.Second, "how often to poll")
		fromBlock := fs.Uint64("from-block", 0, "the first Ethereum block a fresh state scans (0: the config's eth.deployBlock, finishSetup's block, else that block found on the chain)")
		c, _ := setup("monitor", fs, args, "127.0.0.1:9202")
		defer c.stop()
		m := &agent.Monitor{Cfg: c.cfg, State: *state, Every: *every, Alarm: c.alarm, FromBlock: *fromBlock}
		closeSrv := c.serve(m.Handler())
		err := m.Run(c.ctx)
		closeSrv()
		log.Print(err)
	}
}
