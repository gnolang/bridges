// Command hlbridge runs the bridge's machines (package agent; PLAN.md §5.6, §5.7, §5.9):
//
//	hlbridge signer    -config bridge.json -key signer.json -store DIR -listen 0.0.0.0:9100 [-tls-cert C -tls-key K] [-status-listen 127.0.0.1:9140]
//	hlbridge relayer   -config bridge.json -state relayer.json -role primary|backup -listen 127.0.0.1:9101
//	hlbridge monitor   -config bridge.json -state monitor.json -listen 127.0.0.1:9102
//	hlbridge updater   -config bridge.json -key updater.json -state updater.json -listen 127.0.0.1:9103 [-interval 10m]
//	hlbridge providers bridge-signer-1.json bridge-signer-2.json ...   check §5.6's provider rule across machines
//
// Each machine reads gno.land through its own node and Solana through the two providers its config names; the
// signer and the monitor need both to agree. Keys come from hlkey's files (the signer) and gnokey's keybase (the
// relayer's gas key, the monitor's watch key). The signer's -listen is its public signature endpoint, HTTPS
// unless loopback (-tls-cert, G21); its status is served on -status-listen, a loopback address, never beside the
// public endpoint (G5). The relayer's, monitor's and updater's -listen must be loopback addresses (G16: a
// status endpoint is for the machine's operator, and every URL in it, in an alarm or in a log line is cut to its
// host before it is written).
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gnolang/bridges/solana/bridge/agent"
	"github.com/gnolang/bridges/solana/bridge/hl"
	"github.com/gnolang/bridges/solana/bridge/sol"
)

func main() {
	if len(os.Args) < 2 {
		log.Fatal("usage: hlbridge signer|relayer|monitor|providers [flags]")
	}
	cmd, args := os.Args[1], os.Args[2:]
	if cmd == "providers" {
		providers(args)
		return
	}
	fs := flag.NewFlagSet(cmd, flag.ExitOnError)
	config := fs.String("config", "bridge.json", "the deployment's shared config")
	every := fs.Duration("every", 0, "how often to look at the chains (signer 3s, relayer 3s, monitor 10s)")
	key := fs.String("key", "", "signer: its key file (hlkey new); updater: its SOL key file")
	store := fs.String("store", "", "signer: where its signatures live")
	listen := fs.String("listen", "", "where to serve /v1/status (the signer: its public signatures)")
	statusListen := fs.String("status-listen", "127.0.0.1:9140", "signer: a loopback address for /v1/status, apart from the public endpoint (\"\" for none)")
	tlsCert := fs.String("tls-cert", "", "serve HTTPS with this certificate")
	tlsKey := fs.String("tls-key", "", "and this key")
	state := fs.String("state", "", "relayer/monitor: its state file")
	role := fs.String("role", "primary", "relayer: primary or backup (the backup acts 60 s later)")
	gnoGas := fs.Int64("gno-gas", 45_000_000, "relayer: gas wanted for a gno call")
	solUnits := fs.Uint("sol-units", 400_000, "relayer: compute units for a Solana delivery")
	interval := fs.Duration("interval", agent.UpdateInterval, "updater: a header at least this often")
	recordFrom := fs.String("record-from", "", "monitor: where an empty record starts — \"client\" takes the client's own counter and running hash as the base (an install on a provider without the full history, G29); by default the record is built from the deploy")
	_ = fs.Parse(args)

	log.SetOutput(agent.RedactWriter{W: os.Stderr}) // no provider URL's key in any log line (G16)
	cfg, err := agent.LoadConfig(*config)
	if err != nil {
		log.Fatal(err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	alarm := &agent.Alarmer{URL: cfg.Webhook, Source: cmd}
	// a status endpoint is served on a loopback address only (G16); the signer's public endpoint apart
	if cmd != "signer" && *listen != "" && !loopback(*listen) {
		log.Fatalf("%s: -listen %s is not a loopback address: the status is served to the machine's operator only (G16)", cmd, *listen)
	}
	// every server within the limits of serve.go (E3-GT5): the signer's public endpoint faces strangers
	serveAt := func(addr string, h http.Handler, tls bool) func() {
		if tls && *tlsCert != "" {
			return serveOn(addr, h, *tlsCert, *tlsKey)
		}
		return serveOn(addr, h, "", "")
	}
	serve := func(h http.Handler) func() { return serveAt(*listen, h, true) }
	switch cmd {
	case "signer":
		k, err := hl.LoadValidatorKey(*key)
		if err != nil {
			log.Fatal(err)
		}
		if *store == "" {
			log.Fatal("signer: -store is needed")
		}
		s := &agent.Signer{Cfg: cfg, Key: k, Store: *store, Every: or(*every, 3*time.Second), Alarm: alarm}
		if *listen == "" {
			*listen = "127.0.0.1:9100"
		}
		if !loopback(*listen) && *tlsCert == "" {
			log.Fatalf("signer: -listen %s is not a loopback address and no -tls-cert is given: signatures are served over HTTPS (§5.6, G21)", *listen)
		}
		if *statusListen != "" && !loopback(*statusListen) {
			log.Fatalf("signer: -status-listen %s is not a loopback address: the status is served beside no public endpoint", *statusListen)
		}
		closeSrv := serve(s.Handler())
		closeStatus := serveAt(*statusListen, s.StatusHandler(), false)
		err = s.Run(ctx)
		closeSrv()
		closeStatus()
		log.Print(err)
	case "relayer":
		if *state == "" {
			*state = "relayer-state.json"
		}
		r := &agent.Relayer{Cfg: cfg, State: *state, Every: or(*every, 3*time.Second), Role: *role, GnoGas: *gnoGas, SolUnits: uint32(*solUnits), Alarm: alarm}
		closeSrv := serve(r.Handler())
		err = r.Run(ctx)
		closeSrv()
		log.Print(err)
	case "monitor":
		if *state == "" {
			*state = "monitor-state.json"
		}
		m := &agent.Monitor{Cfg: cfg, State: *state, Every: or(*every, 10*time.Second), Alarm: alarm, RecordFrom: *recordFrom}
		closeSrv := serve(m.Handler())
		err = m.Run(ctx)
		closeSrv()
		log.Print(err)
	case "updater":
		keyFile := *key
		if keyFile == "" {
			keyFile = cfg.Solana.UpdaterKey
		}
		if keyFile == "" {
			log.Fatal("updater: -key (or solana.updaterKey) is needed: its own SOL key")
		}
		k, err := sol.PrivateKeyFromFile(keyFile)
		if err != nil {
			log.Fatal(err)
		}
		if *state == "" {
			*state = "updater-state.json"
		}
		u := &agent.Updater{Cfg: cfg, Key: k, State: *state, Every: or(*every, 5*time.Second), Interval: *interval, Alarm: alarm}
		closeSrv := serve(u.Handler())
		err = u.Run(ctx)
		closeSrv()
		log.Print(err)
	default:
		log.Fatalf("unknown command %s", cmd)
	}
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

// providers checks §5.6's rule across the given configs: no pair of Solana providers shared by two signers and
// no provider in more than two of them.
func providers(files []string) {
	if len(files) < 2 {
		log.Fatal("usage: hlbridge providers CONFIG CONFIG...")
	}
	cfgs := map[string]*agent.Config{}
	for _, f := range files {
		c, err := agent.LoadConfig(f)
		if err != nil {
			log.Fatal(err)
		}
		cfgs[f] = c
	}
	if err := agent.ProviderPairs(cfgs); err != nil {
		fmt.Println("FAIL:", err)
		os.Exit(1)
	}
	fmt.Printf("ok: %d machines, no shared pair, no provider in more than two\n", len(cfgs))
}
