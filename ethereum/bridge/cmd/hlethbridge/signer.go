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

	"github.com/gnolang/bridges/solana/bridge/hl"
	"github.com/gnolang/bridges/ethereum/bridge/agent"
)

// The signer (eth/bridge/PLAN.md §5.6), the Ethereum → gno.land lane's:
//
//	hlethbridge signer -config bridge.json -key signer.json -store DIR -listen 0.0.0.0:9300 [-tls-cert C -tls-key K]
//	                   [-proxy 127.0.0.1] [-relayers IP,CIDR] [-status-listen 127.0.0.1:9340] [-every 3s] [-from-block N]
//	hlethbridge providers bridge-signer-1.json bridge-signer-2.json bridge-signer-3.json
//
// It reads gno.land through its own node and Ethereum through the first two providers its config names, which must
// agree (ethx.Pair; the same provider twice is refused unless allowHTTP says a local run); signs ed25519 (its key a
// file of hlkey's) over this bridge's checkpoint of every burn toward gno.land at or below the agreed finalized
// block; serves the signatures at GET /v1/eth/<nonce> on -listen (HTTPS unless loopback) and its status on
// -status-listen, a loopback address, never beside the public endpoint. Its providers must serve full-history state:
// the finalized block trails the head by 64–95 blocks, past what pruned public endpoints keep. `providers` checks
// §5.6's rule across the signer machines' configs: no pair shared by two signers, no provider in more than two.
func init() {
	commands["signer"] = func(args []string) {
		fs := flag.NewFlagSet("signer", flag.ExitOnError)
		config := fs.String("config", "bridge.json", "the deployment's shared config (this machine's two providers first)")
		key := fs.String("key", "", "its ed25519 key file (hlkey new)")
		store := fs.String("store", "", "where its signatures and state live")
		listen := fs.String("listen", "127.0.0.1:9300", "the public signature endpoint (HTTPS unless loopback)")
		statusListen := fs.String("status-listen", "127.0.0.1:9340", "a loopback address for GET /v1/status, apart from the public endpoint (\"\" for none)")
		tlsCert := fs.String("tls-cert", "", "serve the public endpoint over HTTPS with this certificate")
		tlsKey := fs.String("tls-key", "", "and this key")
		proxy := fs.String("proxy", "", "the IP of a reverse proxy in front of the public endpoint (nginx on the machine): its connections are not capped per client, each request it forwards counting against the client X-Forwarded-For's last entry names, read from it alone (E4-GT2)")
		relayers := fs.String("relayers", "", "the relayers' addresses (IPs or CIDR blocks, comma separated): slots of their own at the public endpoint, which no stranger can hold (E4-GT2)")
		every := fs.Duration("every", 3*time.Second, "how often to look at the chains (a round)")
		fromBlock := fs.Uint64("from-block", 0, "the first Ethereum block a fresh store scans (0: the config's eth.deployBlock, finishSetup's block, else that block found on the chain)")
		_ = fs.Parse(args)
		cfg, err := agent.LoadConfig(*config)
		if err != nil {
			log.Fatal(err)
		}
		k, err := hl.LoadValidatorKey(*key)
		if err != nil {
			log.Fatal(err)
		}
		if *store == "" {
			log.Fatal("signer: -store is needed")
		}
		if !loopback(*listen) && *tlsCert == "" {
			log.Fatalf("signer: -listen %s is not a loopback address and no -tls-cert is given: signatures are served over HTTPS (§5.6)", *listen)
		}
		if *statusListen != "" && !loopback(*statusListen) {
			log.Fatalf("signer: -status-listen %s is not a loopback address: the status is served beside no public endpoint", *statusListen)
		}
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		s := &agent.Signer{Cfg: cfg, Key: k, Store: *store, Every: *every, Alarm: &agent.Alarmer{URL: cfg.Webhook, Source: "signer"}, FromBlock: *fromBlock}
		if *proxy != "" && net.ParseIP(*proxy) == nil {
			log.Fatalf("signer: -proxy %q is not an IP address", *proxy)
		}
		nets, err := ParseNets(*relayers)
		if err != nil {
			log.Fatalf("signer: -relayers: %v", err)
		}
		closePub := servePublic(*listen, s.Handler(), *tlsCert, *tlsKey, *proxy, nets)
		closeStatus := serveOn(*statusListen, s.StatusHandler(), "", "")
		err = s.Run(ctx)
		closePub()
		closeStatus()
		log.Print(err)
	}
	commands["providers"] = func(args []string) {
		if len(args) == 0 {
			log.Fatal("usage: hlethbridge providers bridge-signer-1.json bridge-signer-2.json bridge-signer-3.json")
		}
		configs := map[string]*agent.Config{}
		for _, p := range args {
			c, err := agent.LoadConfig(p)
			if err != nil {
				log.Fatal(err)
			}
			configs[p] = c
		}
		if err := agent.ProviderRule(configs); err != nil {
			log.Fatal(err)
		}
		fmt.Printf("§5.6's provider rule holds across %d signers: two providers each, no pair shared, no provider in more than two\n", len(configs))
	}
}

// serveOn serves a handler on an address (HTTPS when a certificate is given) until the returned func is called, within
// the servers' limits (serve.go): a loopback status listener's. The public signature endpoint is servePublic's.
func serveOn(addr string, h http.Handler, cert, key string) func() {
	return serveWith(addr, h, cert, key, func(_ *http.Server, l net.Listener) net.Listener { return limitListener(l, maxConns) })
}

// serveWith serves a handler on an address with a server within the limits (newServer) and the listener wrap gives.
func serveWith(addr string, h http.Handler, cert, key string, wrap func(*http.Server, net.Listener) net.Listener) func() {
	if addr == "" {
		return func() {}
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatal(err)
	}
	srv := newServer(h)
	l := wrap(srv, ln)
	go func() {
		var err error
		if cert != "" {
			err = srv.ServeTLS(l, cert, key)
		} else {
			err = srv.Serve(l)
		}
		if err != nil && err != http.ErrServerClosed {
			log.Fatal(err)
		}
	}()
	return func() { _ = srv.Close() }
}
