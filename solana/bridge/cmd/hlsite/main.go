// Command hlsite serves bridge.gno.world (bridge/SITE.md): the static pages under bridge/site and a read-only
// JSON API over the chains its config lists — gno.land on one side, Solana on the other for now — proxying every
// chain read itself (the page never calls an RPC; its policy is connect-src 'self'), plus one builder that
// returns an unsigned Solana transaction for a burn back to gno.land, which the user's wallet signs and sends.
// It holds no key, rate-limits the API and logs nothing personal.
//
//	hlsite -config site.json -site bridge/site [-listen 127.0.0.1:8095]
//	hlsite -mock -site bridge/site [-listen 127.0.0.1:0]      the canned deployment (the browser suite, a look)
//	hlsite -preview -site bridge/site [-listen …]             the canned deployment as a public preview: a banner,
//	                                                          example figures, the builder refusing (no chain read)
//
// The API:
//
//	GET  /api/chains                      the chains, their tokens, the gno.land realms, the gas to send with
//	GET  /api/<chain>/status              per token the lock, the supply, owed/voided/lost and the identity, the caps
//	                                      and fees, the flags on both sides, the pending decisions, who governs
//	GET  /api/<chain>/transfer/<ref>      one transfer by message id, gno.land transaction hash or <chain> signature
//	GET  /api/<chain>/history             each token's lock on gno.land over time, from the launch (history.go)
//	POST /api/<chain>/burn-tx             {token, sender, recipient, amount} → an unsigned transfer_remote
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/gnolang/bridges/solana/bridge/agent"
	"github.com/gnolang/bridges/solana/bridge/sol"
)

func main() {
	listen := flag.String("listen", "127.0.0.1:8095", "the address to serve on (port 0 picks one; the chosen one is printed)")
	configPath := flag.String("config", "", "the site's config (bridge/SITE.md); not needed under -mock")
	site := flag.String("site", "site", "the static files' directory (bridge/site)")
	mock := flag.Bool("mock", false, "serve the canned deployment: no node, no provider, no key")
	preview := flag.Bool("preview", false, "serve the canned deployment as a public preview before the bridge is deployed: the pages say so and the builder refuses")
	state := flag.String("state", "", "a directory to keep the locked-value history in (history-<chain>.json); none keeps it in memory, lost at a restart")
	flag.Parse()
	log.SetOutput(agent.RedactWriter{W: os.Stderr}) // no provider URL's key in a log line
	if st, err := os.Stat(*site); err != nil || !st.IsDir() {
		log.Fatalf("hlsite: -site %s is not a directory", *site)
	}
	var s *server
	if *preview {
		s, _, _ = mockServer(false, *site, time.Now)
		s.preview, s.mockInfo = true, nil
		log.Print("hlsite: PREVIEW — the canned deployment as examples, sending refused, no chain is read")
	} else if *mock {
		s, _, _ = mockServer(true, *site, time.Now)
		log.Print("hlsite: MOCK — the canned deployment, no chain is read")
	} else {
		if *configPath == "" {
			log.Fatal("hlsite: -config is required (or -mock, or -preview)")
		}
		cfg, err := LoadConfig(*configPath)
		if err != nil {
			log.Fatal(err)
		}
		g := newGno(cfg.Gno.RPC, cfg.Gno.ChainID)
		others := map[string]Other{}
		for _, ch := range cfg.Chains {
			ch := ch
			others[ch.ID] = newSolana(ch, sol.NewRPC(ch.RPC), domainOf(cfg), func() []string { return s.realmAddresses() })
		}
		s = newServer(cfg, g, others, *site)
		if *state != "" {
			if err := os.MkdirAll(*state, 0o700); err != nil {
				log.Fatalf("hlsite: -state %s: %v", *state, err)
			}
			s.hist.dir = *state
		}
	}
	ln, err := net.Listen("tcp", *listen)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("hlsite: listening on %s\n", ln.Addr())
	if !*mock && !*preview {
		go s.warm()
		go s.historyLoop()
	}
	srv := &http.Server{Handler: s.handler(), ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 60 * time.Second, IdleTimeout: 120 * time.Second}
	log.Fatal(srv.Serve(ln))
}

// domainOf is gno.land's Hyperlane domain, which the burn's message is addressed to: read from the mailbox at
// the first status; the realms' launch value until then (PLAN.md: 1198419788 on gnoland-1, 1198419796 elsewhere).
func domainOf(cfg *Config) uint32 {
	if cfg.Gno.ChainID == "gnoland-1" {
		return 1198419788
	}
	return 1198419796
}

// warm reads each chain's status and activity once at start, in the background, so the first visitor finds them.
func (s *server) warm() {
	for i := range s.cfg.Chains {
		ch := &s.cfg.Chains[i]
		if other := s.others[ch.ID]; other != nil {
			ctx, cancel := context.WithTimeout(context.Background(), refreshBudget)
			s.statusNow(ctx, ch, other)
			s.activityNow(ctx, ch, other)
			cancel()
		}
	}
}
