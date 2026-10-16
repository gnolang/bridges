// Command hlethbridge runs the Ethereum bridge's machines (package agent; eth/bridge/PLAN.md §5.5–§5.9, §6.1):
//
//	hlethbridge updater -config bridge.json -state updater.json -sender-state updater-tx.json -listen 127.0.0.1:9203
//	hlethbridge relayer -config bridge.json -state relayer.json -sender-state relayer-tx.json -role primary|backup -listen 127.0.0.1:9201
//	hlethbridge signer  …   the Ethereum → gno.land lane's (signer.go)
//	hlethbridge monitor …   the same lane's (monitor.go)
//
// Each machine reads gno.land through its own node and Ethereum through the providers its config names (the first
// read, every one sent to). Keys: the updater's and the relayers' Ethereum key files (eth.updaterKey,
// eth.relayerKey: 64 hex digits, mode 0600; -key overrides), the relayer's gas-only gno key in gnokey's keybase.
// The sender's in-flight transaction is kept in -sender-state, so a restart resumes its wait. A status endpoint is
// served on a loopback address only (G16), and every URL in it, in an alarm or in a log line is cut to its host.
//
// The commands are registered by their files (commands below), so the other lane's machines are files of their own
// beside these.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"math/big"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/gnolang/bridges/ethereum/bridge/agent"
	"github.com/gnolang/bridges/ethereum/bridge/ethx"
)

// commands are the subcommands by name, each registered by its file's init.
var commands = map[string]func(args []string){}

func main() {
	if len(os.Args) < 2 || commands[os.Args[1]] == nil {
		var names []string
		for n := range commands {
			names = append(names, n)
		}
		sort.Strings(names)
		log.Fatalf("usage: hlethbridge %s [flags]", strings.Join(names, "|"))
	}
	log.SetOutput(ethx.RedactWriter{W: os.Stderr}) // no provider URL's key in any log line (G16)
	commands[os.Args[1]](os.Args[2:])
}

// common is what every machine's command reads: the config, the context, the alarm, the status endpoint.
type common struct {
	cfg    *agent.Config
	ctx    context.Context
	stop   context.CancelFunc
	alarm  *agent.Alarmer
	listen string
}

func setup(name string, fs *flag.FlagSet, args []string, defaultListen string) (*common, *flag.FlagSet) {
	config := fs.String("config", "bridge.json", "the deployment's shared config")
	listen := fs.String("listen", defaultListen, "a loopback address for GET /v1/status (\"\" for none)")
	_ = fs.Parse(args)
	cfg, err := agent.LoadConfig(*config)
	if err != nil {
		log.Fatal(err)
	}
	if *listen != "" && !loopback(*listen) {
		log.Fatalf("%s: -listen %s is not a loopback address: the status is served to the machine's operator only (G16)", name, *listen)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	return &common{cfg: cfg, ctx: ctx, stop: stop, alarm: &agent.Alarmer{URL: cfg.Webhook, Source: name}, listen: *listen}, fs
}

// serve serves a handler on the loopback address until the returned func is called, within the servers' limits (serve.go).
func (c *common) serve(h http.Handler) func() {
	return serveOn(c.listen, h, "", "")
}

// eth dials the config's providers and checks each one's chain id against the config's: the first, which the
// machine reads, must answer; another that does not is left out of the broadcasts (logged), and one on another chain
// stops the machine.
func (c *common) eth() []*ethx.Client {
	if len(c.cfg.Eth.Providers) == 0 {
		log.Fatal("config: eth.providers names no provider")
	}
	var out []*ethx.Client
	for i, p := range c.cfg.Eth.Providers {
		cl, err := ethx.Dial(c.ctx, p)
		var id *big.Int
		if err == nil {
			id, err = cl.ChainID(c.ctx)
		}
		if err != nil {
			if i == 0 {
				log.Fatalf("%s: %v", p, err)
			}
			log.Printf("%s does not answer (left out of the broadcasts): %v", p, err)
			continue
		}
		if id.Uint64() != c.cfg.Eth.ChainID {
			log.Fatalf("%s serves chain %s, the config says %d", p, id, c.cfg.Eth.ChainID)
		}
		out = append(out, cl)
	}
	return out
}

// sender loads a key file and makes its sender, its stuck transactions alarmed.
func (c *common) sender(keyFile, state string, clients []*ethx.Client) *ethx.Sender {
	if keyFile == "" {
		log.Fatal("no key file: the config's eth.relayerKey or eth.updaterKey, or -key")
	}
	key, err := ethx.LoadKey(keyFile)
	if err != nil {
		log.Fatal(err)
	}
	s, err := ethx.NewSender(key, c.cfg.EthChainID(), clients, c.cfg.SendConfig(state))
	if err != nil {
		log.Fatal(err)
	}
	s.OnStuck = func(label string, nonce uint64, n int) {
		c.alarm.Alarm(fmt.Sprintf("eth:stuck:%d", nonce), fmt.Sprintf("%s (nonce %d of %s) survived %d replacements unmined: every later transaction of the key waits behind it", label, nonce, s.From.Hex(), n))
	}
	return s
}

func or(d, def time.Duration) time.Duration {
	if d == 0 {
		return def
	}
	return d
}

// loopback says whether a listen address is a loopback one.
func loopback(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func init() {
	commands["updater"] = func(args []string) {
		fs := flag.NewFlagSet("updater", flag.ExitOnError)
		key := fs.String("key", "", "the updater's Ethereum key file (else the config's eth.updaterKey)")
		state := fs.String("state", "updater-state.json", "its state file")
		senderState := fs.String("sender-state", "updater-tx.json", "its sender's in-flight record")
		every := fs.Duration("every", 0, "how often to look at the chains (5s)")
		minGap := fs.Duration("min-gap", 0, "the least time between two updates but an announcing one (2m; a local run shortens it)")
		batchWait := fs.Duration("batch-wait", 0, "how long split waiting dispatches wait for the relayer's batch (90s; a local run shortens it)")
		c, _ := setup("updater", fs, args, "127.0.0.1:9203")
		defer c.stop()
		clients := c.eth()
		keyFile := *key
		if keyFile == "" {
			keyFile = c.cfg.Eth.UpdaterKey
		}
		u := &agent.Updater{Cfg: c.cfg, Sender: c.sender(keyFile, *senderState, clients), Eth: clients[0], State: *state, Every: or(*every, 5*time.Second), MinGap: *minGap, BatchWait: *batchWait, Alarm: c.alarm}
		closeSrv := c.serve(u.Handler())
		err := u.Run(c.ctx)
		closeSrv()
		log.Print(err)
	}
	commands["relayer"] = func(args []string) {
		fs := flag.NewFlagSet("relayer", flag.ExitOnError)
		key := fs.String("key", "", "the relayers' Ethereum key file (else the config's eth.relayerKey; primary and backup share it)")
		state := fs.String("state", "relayer-state.json", "its state file")
		senderState := fs.String("sender-state", "relayer-tx.json", "its sender's in-flight record")
		role := fs.String("role", "primary", "primary or backup (the backup acts 60 s later, and sends only while the shared key is quiet)")
		gnoGas := fs.Int64("gno-gas", 45_000_000, "gas wanted for a gno call (the simulation measures it)")
		every := fs.Duration("every", 0, "how often to look at the chains (3s)")
		c, _ := setup("relayer", fs, args, "127.0.0.1:9201")
		defer c.stop()
		clients := c.eth()
		keyFile := *key
		if keyFile == "" {
			keyFile = c.cfg.Eth.RelayerKey
		}
		r := &agent.Relayer{Cfg: c.cfg, Sender: c.sender(keyFile, *senderState, clients), Eth: clients[0], State: *state, Role: *role, GnoGas: *gnoGas, Every: or(*every, 3*time.Second), Alarm: c.alarm}
		closeSrv := c.serve(r.Handler())
		err := r.Run(c.ctx)
		closeSrv()
		log.Print(err)
	}
}
