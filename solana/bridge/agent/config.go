// Package agent runs the bridge's off-chain machines (bridge/PLAN.md §5.6, §5.7, §5.9): the signers, which read
// gno.land through their own node and Solana through two providers that must agree and sign ed25519 checkpoints
// for hlmailbox's verifier once per message, stopping the moment the supply identity breaks; the relayers, which
// carry messages, pay claims, apply queued decisions and re-emit, a primary and a backup 60 s apart; and the
// monitors, which compute the identity every 10 s over an ordered cut and may set the watch key's flags. None of
// them can move tokens: the chains check the signatures, the routes and that no message is delivered twice.
package agent

import (
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"

	"github.com/gagliardetto/solana-go"

	"github.com/gnolang/bridges/solana/bridge/gno"
	"github.com/gnolang/bridges/solana/bridge/hl"
	"github.com/gnolang/bridges/solana/bridge/sol"
)

// Config is one deployment's facts, shared by every machine of it: the realms, the programs, the signers. The
// launch notice publishes it; each machine adds its own keys by name.
type Config struct {
	Gno struct {
		RPC          string   `json:"rpc"`          // this machine's own gno node
		Sources      []string `json:"sources"`      // the monitor's other sources: rpc.gno.land, then a second public endpoint (the third source)
		ChainID      string   `json:"chainId"`      //
		Mailbox      string   `json:"mailbox"`      // r/hlmailbox's pkgpath
		Fees         string   `json:"fees"`         // r/hlfees
		Gov          string   `json:"gov"`          // r/hlgov
		DAO          string   `json:"dao"`          // gno.land/r/gov/dao
		Gnokey       string   `json:"gnokey"`       //
		Home         string   `json:"home"`         // gnokey's keybase
		Key          string   `json:"key"`          // the relayer's gas-only key
		Address      string   `json:"address"`      // its address (the monitor watches its runway)
		WatchKey     string   `json:"watchKey"`     // the monitor's watch key (seat 5 or 6), in the same keybase
		WatchAddress string   `json:"watchAddress"` // its address
		GasPrice     float64  `json:"gasPrice"`     // ugnot per gas; the chain's minimum when 0
		GasFee       string   `json:"gasFee"`       // an explicit fee overrides the price
		MaxNodeFee   int64    `json:"maxNodeFee"`   // ugnot: the most a node's own minimum gas price may raise one call's fee to (F14); 0 is the greater of 10× the block-price fee and 1 GNOT
		AheadBlocks  int64    `json:"aheadBlocks"`  // the signers' allowance for the client's trusted height past a head (§5.6: an hour of blocks; DefaultAheadBlocks when 0)
	} `json:"gno"`
	Solana struct {
		RPCs       []string `json:"rpcs"`       // two providers: a signer and a monitor need both to agree
		Mailbox    string   `json:"mailbox"`    // our deployment of Hyperlane's mailbox
		Client     string   `json:"client"`     // the light-client ISM
		Pausable   string   `json:"pausable"`   // the pausable ISM
		Admin      string   `json:"admin"`      // the admin program
		Domain     uint32   `json:"domain"`     //
		Payer      string   `json:"payer"`      // the relayers' SOL key file (primary and backup share it)
		Updater    string   `json:"updater"`    // the updater's SOL pubkey, whose runway is watched
		UpdaterKey string   `json:"updaterKey"` // the updater's SOL key file (the updater machine only)
		WatchKey   string   `json:"watchKey"`   // the monitor's Solana watch key file (a watch slot of the two Solana objects)
	} `json:"solana"`
	// Routes are the token routes: the gno realm and the synthetic token program of each.
	Routes []Route `json:"routes"`
	// Signers are the signer machines, by the ed25519 key each holds in hlmailbox's set; the chain's set and rule
	// decide whose signatures count, this list only says where to fetch them.
	Signers []SignerRef `json:"signers"`
	// Webhook takes every alarm as one JSON POST; "" logs only.
	Webhook string `json:"webhook"`
	// AllowHTTP lets signer URLs be plain http (the local run); otherwise https or a loopback address.
	AllowHTTP bool `json:"allowHTTP"`
	// Runway overrides the planned spend of a float (per day, in its unit) for the runway alarm; see floats.go.
	Runway map[string]float64 `json:"runway,omitempty"`
}

// Route is one token route.
type Route struct {
	Symbol string `json:"symbol"` // "GNOT" or "BUBBLE"
	Gno    string `json:"gno"`    // the route realm's pkgpath
	Token  string `json:"token"`  // the synthetic token program on Solana
	// Successor and Predecessor name the token's other realm while a migration is open (AQ3: the config may name
	// both routes of a token); left empty, the successor is read from the route's own `migratedTo` and the
	// predecessor of a configured successor found among the mailbox's registered routes.
	Successor   string `json:"successor,omitempty"`
	Predecessor string `json:"predecessor,omitempty"`
	// ExpectedLock is the lock the launch notice expected of the token in its first week, base units (C341: the
	// value `hlsol setup` compiled the day cap from; the monitors' and hlwatch's mint:cap-ratio stays quiet while
	// the lock is still below it, and judges the cap against the lock once the lock has reached it).
	ExpectedLock int64 `json:"expectedLock,omitempty"`
}

// SignerRef is where one signer serves its signatures and which key it signs with.
type SignerRef struct {
	URL string `json:"url"`
	Key string `json:"key"` // its ed25519 key in hex, as hlmailbox lists it
}

func LoadConfig(path string) (*Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var c Config
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if c.Gno.Gnokey == "" {
		c.Gno.Gnokey = "gnokey"
	}
	if c.Gno.DAO == "" {
		c.Gno.DAO = "gno.land/r/gov/dao"
	}
	if err := c.check(); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &c, nil
}

func (c *Config) check() error {
	if c.Gno.RPC == "" || c.Gno.ChainID == "" || c.Gno.Mailbox == "" {
		return errors.New("config: gno.rpc, gno.chainId and gno.mailbox are needed")
	}
	if len(c.Routes) == 0 {
		return errors.New("config: no routes")
	}
	seen := map[string]bool{}
	for _, r := range c.Routes {
		if r.Symbol == "" || r.Gno == "" {
			return errors.New("config: a route needs a symbol and a gno realm")
		}
		if seen[r.Symbol] {
			return errors.New("config: route " + r.Symbol + " twice")
		}
		seen[r.Symbol] = true
		if r.Token != "" {
			if _, err := solana.PublicKeyFromBase58(r.Token); err != nil {
				return fmt.Errorf("config: route %s: token %q", r.Symbol, r.Token)
			}
		}
		for _, p := range []struct{ name, path string }{{"successor", r.Successor}, {"predecessor", r.Predecessor}} {
			if p.path == "" {
				continue
			}
			if !strings.HasPrefix(p.path, "gno.land/r/") || strings.Contains(p.path, "#") || p.path == r.Gno {
				return fmt.Errorf("config: route %s: %s %q is not another realm under gno.land/r/", r.Symbol, p.name, p.path)
			}
		}
		if r.ExpectedLock < 0 {
			return fmt.Errorf("config: route %s: expectedLock %d", r.Symbol, r.ExpectedLock)
		}
	}
	for _, p := range []struct{ name, id string }{{"mailbox", c.Solana.Mailbox}, {"client", c.Solana.Client}, {"pausable", c.Solana.Pausable}, {"admin", c.Solana.Admin}, {"updater", c.Solana.Updater}} {
		if p.id != "" {
			if _, err := solana.PublicKeyFromBase58(p.id); err != nil {
				return fmt.Errorf("config: solana.%s %q", p.name, p.id)
			}
		}
	}
	for _, s := range c.Signers {
		if _, err := s.EdKey(); err != nil {
			return err
		}
		if err := checkSignerURL(s.URL, c.AllowHTTP); err != nil {
			return err
		}
	}
	return nil
}

// checkSignerURL is the client's rule (§5.7): HTTPS, unless the signer is on this machine or the config says a
// local run.
func checkSignerURL(raw string, allowHTTP bool) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return fmt.Errorf("config: signer url %q", raw)
	}
	if u.Scheme == "https" || allowHTTP {
		return nil
	}
	h := u.Hostname()
	if h == "localhost" || h == "127.0.0.1" || h == "::1" {
		return nil
	}
	return fmt.Errorf("config: signer url %q is not https (set allowHTTP for a local run)", raw)
}

// EdKey is the signer's ed25519 key.
func (s SignerRef) EdKey() (ed25519.PublicKey, error) {
	b, err := hex.DecodeString(strings.TrimPrefix(s.Key, "0x"))
	if err != nil || len(b) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("config: signer ed25519 key %q", s.Key)
	}
	return ed25519.PublicKey(b), nil
}

// DefaultAheadBlocks is §5.6's "an hour of blocks" at gno.land's 3.43 s a block (3,600 / 3.43 ≈ 1,050): how far
// the client's trusted height may run past the own node's head and a second source's before a signer stops.
const DefaultAheadBlocks = 1050

// AheadAllowance is the signers' allowance for the client's trusted height past a head: gno.aheadBlocks, or
// DefaultAheadBlocks.
func (c *Config) AheadAllowance() uint64 {
	if c.Gno.AheadBlocks > 0 {
		return uint64(c.Gno.AheadBlocks)
	}
	return DefaultAheadBlocks
}

// GnoClient reads this machine's own node and signs with the relayer's gas key.
func (c *Config) GnoClient() *gno.Client {
	return c.gnoClient(c.Gno.RPC, c.Gno.Key)
}

// WatchClient reads the own node and signs with the watch key.
func (c *Config) WatchClient() *gno.Client { return c.gnoClient(c.Gno.RPC, c.Gno.WatchKey) }

// SourceClient reads one of the other gno sources (no key).
func (c *Config) SourceClient(rpc string) *gno.Client { return c.gnoClient(rpc, "") }

func (c *Config) gnoClient(rpc, key string) *gno.Client {
	return &gno.Client{RPC: rpc, ChainID: c.Gno.ChainID, Gnokey: c.Gno.Gnokey, Home: c.Gno.Home, Key: key, GasFee: c.Gno.GasFee, GasPrice: c.Gno.GasPrice, MaxNodeFee: c.Gno.MaxNodeFee}
}

// SolMailbox is our mailbox's program id.
func (c *Config) SolMailbox() solana.PublicKey {
	return solana.MustPublicKeyFromBase58(c.Solana.Mailbox)
}

// Programs are the six deployments by id (a zero key where the config names none): the mailbox, the two tokens
// in the admin program's order (0 GNOT, 1 BUBBLE), the client, the pausable ISM, the admin program.
func (c *Config) Programs() sol.Programs {
	key := func(s string) solana.PublicKey {
		if s == "" {
			return solana.PublicKey{}
		}
		return solana.MustPublicKeyFromBase58(s)
	}
	p := sol.Programs{Mailbox: key(c.Solana.Mailbox), Client: key(c.Solana.Client), Pausable: key(c.Solana.Pausable), Admin: key(c.Solana.Admin)}
	if r := c.RouteBySymbol("GNOT"); r != nil {
		p.Tokens[0] = r.TokenKey()
	}
	if r := c.RouteBySymbol("BUBBLE"); r != nil {
		p.Tokens[1] = r.TokenKey()
	}
	return p
}

// HasPrograms says whether the config names the three programs of ours (the Solana side needs all three).
func (c *Config) HasPrograms() bool {
	return c.Solana.Client != "" && c.Solana.Pausable != "" && c.Solana.Admin != ""
}

// GovSender is r/hlgov's address as the admin program carries it (twelve zero bytes, then the twenty).
func (c *Config) GovSender() ([32]byte, error) {
	if c.Gno.Gov == "" {
		return [32]byte{}, errors.New("config: gno.gov is needed")
	}
	return hl.GnoAddressToBytes32(hl.GnoPackageAddress(c.Gno.Gov))
}

// RouteBySymbol is the route of a token, or nil.
func (c *Config) RouteBySymbol(symbol string) *Route {
	for i := range c.Routes {
		if c.Routes[i].Symbol == symbol {
			return &c.Routes[i]
		}
	}
	return nil
}

// RouteByToken is the route whose Solana program is this key, or nil.
func (c *Config) RouteByToken(k solana.PublicKey) *Route {
	for i := range c.Routes {
		if c.Routes[i].Token != "" && solana.MustPublicKeyFromBase58(c.Routes[i].Token).Equals(k) {
			return &c.Routes[i]
		}
	}
	return nil
}

// TokenKey is a route's Solana program (zero if the route has none configured).
func (r Route) TokenKey() solana.PublicKey {
	if r.Token == "" {
		return solana.PublicKey{}
	}
	return solana.MustPublicKeyFromBase58(r.Token)
}

// TwoProviders is the signer's and the monitor's requirement: two Solana providers that are not the same. The
// same URL twice is refused unless the config says allowHTTP (the local run, which names its one validator
// twice); a deployment names two, else "two providers agree" is vacuous.
func (c *Config) TwoProviders() ([2]string, error) {
	var out [2]string
	if len(c.Solana.RPCs) < 2 {
		return out, errors.New("config: solana.rpcs needs two providers")
	}
	out[0], out[1] = c.Solana.RPCs[0], c.Solana.RPCs[1]
	if out[0] == out[1] && !c.AllowHTTP {
		return out, fmt.Errorf("config: solana.rpcs names the same provider twice (%s): two providers that must agree cannot be one (set allowHTTP for a local run)", out[0])
	}
	return out, nil
}

// ProviderPairs checks §5.6's rule across several machines' configs: no pair of Solana providers shared by two
// signers, no provider in more than two of them. It is a check at deployment, not one a signer can make alone.
func ProviderPairs(configs map[string]*Config) error {
	count := map[string][]string{}
	pairs := map[string][]string{}
	for name, c := range configs {
		p, err := c.TwoProviders()
		if err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		if p[0] == p[1] {
			return fmt.Errorf("%s: the two providers are the same", name)
		}
		for _, u := range p {
			count[u] = append(count[u], name)
		}
		a, b := p[0], p[1]
		if a > b {
			a, b = b, a
		}
		pairs[a+" + "+b] = append(pairs[a+" + "+b], name)
	}
	for u, names := range count {
		if len(names) > 2 {
			return fmt.Errorf("provider %s is in %d signers (%s): at most two", u, len(names), strings.Join(names, ", "))
		}
	}
	for p, names := range pairs {
		if len(names) > 1 {
			return fmt.Errorf("the pair %s is shared by %s", p, strings.Join(names, " and "))
		}
	}
	return nil
}
