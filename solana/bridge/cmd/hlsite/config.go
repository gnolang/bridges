package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strings"

	"github.com/gagliardetto/solana-go"

	"github.com/gnolang/bridges/solana/bridge/sol"
)

// Config is the site's (bridge/SITE.md): the gno.land node on one side, and the chains on the far side — Solana,
// and Ethereum once a deployment lists it — each with its tokens, its contracts or programs, and its own
// deployment's realms on gno.land. Nothing in it is a key.
//
// Two shapes load. The first (the live site's since M5): the gno section names the one deployment's mailbox, fee
// realm and r/hlgov, and every chain uses them. The second (E-M4): each chain names its own in its "gno" section —
// the Ethereum deployment is a second set of realms beside the Solana one's — and the shared gno section keeps the
// node, the chain id, the BUBBLE token realm, the links and the gas. A chain without its own section takes the
// shared one's realms; two chains with the same mailbox are refused (each deployment has its own).
type Config struct {
	Gno struct {
		RPC       string `json:"rpc"`       // the gno node the backend reads; the page never does
		ChainID   string `json:"chainId"`   // what the wallet signs for
		Mailbox   string `json:"mailbox"`   // r/hlmailbox of a chain that names none of its own
		Fees      string `json:"fees"`      // r/hlfees, likewise
		Gov       string `json:"gov"`       // r/hlgov, likewise
		Bubble    string `json:"bubble"`    // the BUBBLE token realm, whose Approve a BUBBLE send needs
		RealmLink string `json:"realmLink"` // a realm's page, %s the pkgpath (gnoweb: https://gno.land/%s)
		TxLink    string `json:"txLink"`    // a transaction's page, %s the hash; "" for no link
		GasWanted int64  `json:"gasWanted"` // the gas a TransferRemote is sent with (60M: the local loop's figure)
	} `json:"gno"`
	Chains []ChainConfig `json:"chains"`
}

// ChainConfig is one chain on the far side.
type ChainConfig struct {
	ID       string        `json:"id"`       // the URL segment: /api/<id>/…
	Kind     string        `json:"kind"`     // which implementation ("solana" or "ethereum"; the id when empty)
	Name     string        `json:"name"`     // for the picker
	RPC      string        `json:"rpc"`      // the backend's provider; the page never calls it
	Domain   uint32        `json:"domain"`   // the chain's Hyperlane domain
	Explorer string        `json:"explorer"` // an explorer's base URL (https://explorer.solana.com, https://etherscan.io); "" for no links
	Cluster  string        `json:"cluster"`  // Solana: the explorer's cluster parameter ("devnet"); "" on mainnet
	Tokens   []TokenConfig `json:"tokens"`
	// Programs are the far side's four parts: Solana's program ids, or Ethereum's contract addresses (mailbox the
	// Mailbox proxy, pausable the PausableGate proxy, admin HlAdmin, client the GnoLightClient proxy) with the ten
	// more an Ethereum deployment created — the ProxyAdmin, the two hooks, the five proxies' launch implementations
	// (token 0 is GNOT's, token 1 BUBBLE's), the Ed25519Verifier and HlAdminExec, named as hleth's deployment record
	// names them — which only the page reads: no transfer may name any of the sixteen as its recipient (ER10; the realm
	// refuses all sixteen, eth/realm/hlmailbox ethdeploy.gno; round 3's E3-CF6).
	Programs struct {
		Mailbox         string `json:"mailbox"`
		Pausable        string `json:"pausable"`
		Admin           string `json:"admin"`
		Client          string `json:"client"`
		ProxyAdmin      string `json:"proxyAdmin,omitempty"`
		MerkleTreeHook  string `json:"merkleTreeHook,omitempty"`
		ProtocolFee     string `json:"protocolFee,omitempty"`
		MailboxImpl     string `json:"mailboxImpl,omitempty"`
		Token0Impl      string `json:"token0Impl,omitempty"`
		Token1Impl      string `json:"token1Impl,omitempty"`
		ClientImpl      string `json:"clientImpl,omitempty"`
		GateImpl        string `json:"gateImpl,omitempty"`
		Ed25519Verifier string `json:"ed25519Verifier,omitempty"`
		HlAdminExec     string `json:"hlAdminExec,omitempty"`
	} `json:"programs"`
	// Signers are the signer machines' public signature endpoints (each serves GET <url>/v1/<lane>/<nonce>, lane
	// "sol" or "eth": what the relayers read), which the site asks for its lamps (M7); optional — without them the
	// lamps say nothing.
	Signers []string `json:"signers"`
	// Gno is this chain's deployment on gno.land: its mailbox, fee realm and r/hlgov (the routes are the tokens'
	// realms). Absent, the shared gno section's are this chain's (the first shape).
	Gno *ChainRealms `json:"gno,omitempty"`
	// Ethereum's: the EIP-155 chain id the wallet must be on (the domain when absent: Hyperlane's Ethereum and
	// Sepolia domains are their chain ids), the block the deployment began at (no log before it is ever asked for),
	// and the most blocks one eth_getLogs may span (1,000 when absent; halved when the provider refuses a range).
	ChainID   uint64 `json:"chainId,omitempty"`
	FromBlock uint64 `json:"fromBlock,omitempty"`
	LogRange  uint64 `json:"logRange,omitempty"`

	realms ChainRealms // what Realms answers, set by the check
}

// ChainRealms are one deployment's realms on gno.land beside its routes.
type ChainRealms struct {
	Mailbox string `json:"mailbox"` // r/hlmailbox
	Fees    string `json:"fees"`    // r/hlfees
	Gov     string `json:"gov"`     // r/hlgov
}

// TokenConfig is one route: the token on the far side and its realm on gno.land.
type TokenConfig struct {
	Symbol  string `json:"symbol"`
	Mint    string `json:"mint"`    // Solana: the synthetic mint (derived from the program; checked when given). Ethereum: the token contract
	Program string `json:"program"` // Solana: the token program. Ethereum: the HypERC20 proxy's address, the route's router
	Realm   string `json:"realm"`   // the route realm on gno.land
}

const (
	defaultRealmLink = "https://gno.land/%s"
	defaultGasWanted = 60_000_000
	decimals         = 6
	// defaultLogRange is how many blocks one eth_getLogs spans at most unless the config says otherwise: public
	// providers refuse wide ranges (1,000 to 10,000 blocks is common), and the site halves it on a refusal.
	defaultLogRange = 1000
)

var chainIDRE = regexp.MustCompile(`^[a-z0-9-]{1,32}$`)

// LoadConfig reads and checks a config file.
func LoadConfig(path string) (*Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var c Config
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if err := c.check(); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &c, nil
}

func (c *Config) check() error {
	if c.Gno.RPC == "" || c.Gno.ChainID == "" {
		return errors.New("config: gno.rpc and gno.chainId are needed")
	}
	if c.Gno.RealmLink == "" {
		c.Gno.RealmLink = defaultRealmLink
	}
	if c.Gno.GasWanted <= 0 {
		c.Gno.GasWanted = defaultGasWanted
	}
	if len(c.Chains) == 0 {
		return errors.New("config: no chains")
	}
	ids := map[string]bool{}
	mailboxes := map[string]string{} // a mailbox's chain: each deployment has its own
	for i := range c.Chains {
		ch := &c.Chains[i]
		if !chainIDRE.MatchString(ch.ID) {
			return fmt.Errorf("config: chain id %q (a-z, 0-9 and - only)", ch.ID)
		}
		if ids[ch.ID] {
			return fmt.Errorf("config: chain %s twice", ch.ID)
		}
		ids[ch.ID] = true
		if ch.Kind == "" {
			ch.Kind = ch.ID
		}
		if ch.Kind != "solana" && ch.Kind != "ethereum" {
			return fmt.Errorf("config: chain %s: kind %q is not implemented (solana and ethereum are)", ch.ID, ch.Kind)
		}
		if ch.Name == "" {
			ch.Name = ch.ID
		}
		if ch.RPC == "" || ch.Domain == 0 {
			return fmt.Errorf("config: chain %s: rpc and domain are needed", ch.ID)
		}
		// the deployment's realms on gno.land: the chain's own, else the shared section's
		ch.realms = ChainRealms{Mailbox: c.Gno.Mailbox, Fees: c.Gno.Fees, Gov: c.Gno.Gov}
		if ch.Gno != nil {
			ch.realms = *ch.Gno
		}
		if ch.realms.Mailbox == "" {
			return fmt.Errorf("config: chain %s: no gno.land mailbox (the chain's gno.mailbox, or the shared gno.mailbox)", ch.ID)
		}
		for _, p := range []string{ch.realms.Mailbox, ch.realms.Fees, ch.realms.Gov} {
			if p != "" && !strings.HasPrefix(p, "gno.land/r/") {
				return fmt.Errorf("config: chain %s: realm %q is not under gno.land/r/", ch.ID, p)
			}
		}
		if other, ok := mailboxes[ch.realms.Mailbox]; ok {
			return fmt.Errorf("config: chains %s and %s name the same gno.land mailbox %s: each deployment has its own (name the chain's in its gno section)", other, ch.ID, ch.realms.Mailbox)
		}
		mailboxes[ch.realms.Mailbox] = ch.ID
		for _, s := range ch.Signers {
			if u, err := url.Parse(s); err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
				return fmt.Errorf("config: chain %s: signer endpoint %q is not an http(s) URL", ch.ID, s)
			}
		}
		if len(ch.Tokens) == 0 {
			return fmt.Errorf("config: chain %s: no tokens", ch.ID)
		}
		var err error
		if ch.Kind == "ethereum" {
			err = ch.checkEthereum()
		} else {
			err = ch.checkSolana()
		}
		if err != nil {
			return err
		}
		syms := map[string]bool{}
		for j := range ch.Tokens {
			t := &ch.Tokens[j]
			if t.Symbol == "" || syms[t.Symbol] {
				return fmt.Errorf("config: chain %s: token symbol %q missing or twice", ch.ID, t.Symbol)
			}
			syms[t.Symbol] = true
			if !strings.HasPrefix(t.Realm, "gno.land/r/") {
				return fmt.Errorf("config: chain %s: token %s: realm %q is not under gno.land/r/", ch.ID, t.Symbol, t.Realm)
			}
			if t.Symbol == "BUBBLE" && c.Gno.Bubble == "" {
				return errors.New("config: gno.bubble (the BUBBLE token realm) is needed for the BUBBLE route")
			}
		}
	}
	return nil
}

// checkSolana checks a Solana chain's programs and derives each token's mint (checked when given).
func (ch *ChainConfig) checkSolana() error {
	for _, p := range []struct{ name, id string }{{"mailbox", ch.Programs.Mailbox}, {"pausable", ch.Programs.Pausable}, {"admin", ch.Programs.Admin}, {"client", ch.Programs.Client}} {
		if _, err := solana.PublicKeyFromBase58(p.id); err != nil {
			return fmt.Errorf("config: chain %s: programs.%s %q is not a program id", ch.ID, p.name, p.id)
		}
	}
	for j := range ch.Tokens {
		t := &ch.Tokens[j]
		prog, err := solana.PublicKeyFromBase58(t.Program)
		if err != nil {
			return fmt.Errorf("config: chain %s: token %s: program %q is not a program id", ch.ID, t.Symbol, t.Program)
		}
		mint := sol.Synthetic{Program: prog}.Mint().String()
		if t.Mint == "" {
			t.Mint = mint
		} else if t.Mint != mint {
			return fmt.Errorf("config: chain %s: token %s: mint %s is not the program's (%s)", ch.ID, t.Symbol, t.Mint, mint)
		}
	}
	return nil
}

// checkEthereum checks an Ethereum chain's contracts — all sixteen of the deployment, each 0x and 40 hex digits in its
// EIP-55 case (one case carries no checksum and is refused: §5.2, E3-CF6), none zero, no two at one address — and
// makes each token's mint its own contract (an ERC-20 is its own mint; a given mint must be the same address). Only
// GNOT and BUBBLE are routes there: the gate's limits are kept by token index.
func (ch *ChainConfig) checkEthereum() error {
	norm := func(what string, s *string, need bool) error {
		if *s == "" && !need {
			return nil
		}
		a, err := parseEthAddress(*s)
		if err != nil {
			return fmt.Errorf("config: chain %s: %s %q: %v", ch.ID, what, *s, err)
		}
		if a == (ethAddr{}) {
			return fmt.Errorf("config: chain %s: %s is the zero address", ch.ID, what)
		}
		*s = a.String()
		return nil
	}
	p := &ch.Programs
	for _, x := range []struct {
		name string
		s    *string
	}{{"programs.mailbox", &p.Mailbox}, {"programs.pausable", &p.Pausable}, {"programs.admin", &p.Admin}, {"programs.client", &p.Client},
		{"programs.proxyAdmin", &p.ProxyAdmin}, {"programs.merkleTreeHook", &p.MerkleTreeHook}, {"programs.protocolFee", &p.ProtocolFee},
		{"programs.mailboxImpl", &p.MailboxImpl}, {"programs.token0Impl", &p.Token0Impl}, {"programs.token1Impl", &p.Token1Impl},
		{"programs.clientImpl", &p.ClientImpl}, {"programs.gateImpl", &p.GateImpl}, {"programs.ed25519Verifier", &p.Ed25519Verifier},
		{"programs.hlAdminExec", &p.HlAdminExec}} {
		if err := norm(x.name, x.s, true); err != nil {
			return err
		}
	}
	for j := range ch.Tokens {
		t := &ch.Tokens[j]
		if tokenIndex(t.Symbol) < 0 {
			return fmt.Errorf("config: chain %s: token %s: only GNOT and BUBBLE are routes on Ethereum", ch.ID, t.Symbol)
		}
		if err := norm("token "+t.Symbol+": program", &t.Program, true); err != nil {
			return err
		}
		if t.Mint == "" {
			t.Mint = t.Program
		} else if err := norm("token "+t.Symbol+": mint", &t.Mint, true); err != nil {
			return err
		} else if t.Mint != t.Program {
			return fmt.Errorf("config: chain %s: token %s: mint %s is not its contract (%s): an ERC-20 is its own mint", ch.ID, t.Symbol, t.Mint, t.Program)
		}
	}
	// the sixteen are distinct deployments (the realm refuses a deploy naming one twice): two names at one address is
	// a record field pasted twice, which leaves one contract of the sixteen out of the page's refusals
	names := map[string]string{}
	for name, a := range ch.EthContracts() {
		if other, ok := names[a]; ok {
			n1, n2 := min(name, other), max(name, other)
			return fmt.Errorf("config: chain %s: %s and %s are the same address %s: every contract of the deployment has its own", ch.ID, n1, n2, a)
		}
		names[a] = name
	}
	if ch.ChainID == 0 {
		ch.ChainID = uint64(ch.Domain)
	}
	if ch.LogRange == 0 {
		ch.LogRange = defaultLogRange
	}
	if ch.Cluster != "" {
		return fmt.Errorf("config: chain %s: cluster is Solana's explorer parameter; an Ethereum chain names its explorer by its URL", ch.ID)
	}
	return nil
}

// EthContracts are an Ethereum chain's sixteen contracts by the page's names — the seven programs, the two tokens by
// symbol and the seven more by the deployment record's names — each in its EIP-55 form once the config is checked.
func (ch *ChainConfig) EthContracts() map[string]string {
	p := ch.Programs
	m := map[string]string{"mailbox": p.Mailbox, "gate": p.Pausable, "admin": p.Admin, "client": p.Client, "proxyAdmin": p.ProxyAdmin,
		"merkleTreeHook": p.MerkleTreeHook, "protocolFee": p.ProtocolFee, "mailboxImpl": p.MailboxImpl, "token0Impl": p.Token0Impl,
		"token1Impl": p.Token1Impl, "clientImpl": p.ClientImpl, "gateImpl": p.GateImpl, "ed25519Verifier": p.Ed25519Verifier,
		"hlAdminExec": p.HlAdminExec}
	for _, t := range ch.Tokens {
		m[t.Symbol] = t.Program
	}
	return m
}

// Chain is a chain by id, or nil.
func (c *Config) Chain(id string) *ChainConfig {
	for i := range c.Chains {
		if c.Chains[i].ID == id {
			return &c.Chains[i]
		}
	}
	return nil
}

// Token is a chain's token by symbol, or nil.
func (ch *ChainConfig) Token(symbol string) *TokenConfig {
	for i := range ch.Tokens {
		if ch.Tokens[i].Symbol == symbol {
			return &ch.Tokens[i]
		}
	}
	return nil
}

// Realms are the chain's deployment's mailbox, fee realm and r/hlgov on gno.land (its own, or the shared ones).
func (ch *ChainConfig) Realms() ChainRealms { return ch.realms }

// Realms are every bridge realm's pkgpath the config names — each deployment's mailbox, fee realm, r/hlgov and
// launch hlverifier, the token realm and every route, each once: a burn to any of their addresses is refused (PLAN.md
// §5.2, K21), whichever deployment it comes back through.
func (c *Config) Realms() []string {
	var out []string
	seen := map[string]bool{}
	add := func(p string) {
		if p != "" && !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	for _, ch := range c.Chains {
		r := ch.Realms()
		add(r.Mailbox)
		add(r.Fees)
		add(r.Gov)
		add(launchVerifier(r.Mailbox))
	}
	add(c.Gno.Bubble)
	for _, ch := range c.Chains {
		for _, t := range ch.Tokens {
			add(t.Realm)
		}
	}
	return out
}

// launchVerifier is a deployment's launch hlverifier: the realm its mailbox compiles as its first verifier, beside
// it in its namespace (hlmailbox's launchVerifier, namespace + "hlverifier"). A credit naming it is swept by the
// Ethereum deployment's routes (§5.2: "one of this deployment's realms (its hlverifier included)", or one of the
// Solana deployment's six) and paid into the verifier's account by the Solana deployment's, where no code moves it:
// either way no burn should name it (round 3's E3-CF6).
func launchVerifier(mailbox string) string {
	if i := strings.LastIndex(mailbox, "/"); i > 0 {
		return mailbox[:i+1] + "hlverifier"
	}
	return ""
}
