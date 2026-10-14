package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/gagliardetto/solana-go"

	"github.com/gnolang/bridges/solana/bridge/sol"
)

// Config is the site's (bridge/SITE.md): the gno.land node and its realms on one side, and the chains on the
// far side — Solana alone for now — each with its tokens and programs. Nothing in it is a key.
type Config struct {
	Gno struct {
		RPC       string `json:"rpc"`       // the gno node the backend reads; the page never does
		ChainID   string `json:"chainId"`   // what the wallet signs for
		Mailbox   string `json:"mailbox"`   // r/hlmailbox
		Fees      string `json:"fees"`      // r/hlfees
		Gov       string `json:"gov"`       // r/hlgov
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
	Kind     string        `json:"kind"`     // which implementation ("solana"; the id when empty)
	Name     string        `json:"name"`     // for the picker
	RPC      string        `json:"rpc"`      // the backend's provider; the page never calls it
	Domain   uint32        `json:"domain"`   // the chain's Hyperlane domain
	Explorer string        `json:"explorer"` // an explorer's base URL (https://explorer.solana.com); "" for no links
	Cluster  string        `json:"cluster"`  // the explorer's cluster parameter ("devnet"); "" on mainnet
	Tokens   []TokenConfig `json:"tokens"`
	Programs struct {
		Mailbox  string `json:"mailbox"`
		Pausable string `json:"pausable"`
		Admin    string `json:"admin"`
		Client   string `json:"client"`
	} `json:"programs"`
}

// TokenConfig is one route: the token on the far side and its realm on gno.land.
type TokenConfig struct {
	Symbol  string `json:"symbol"`
	Mint    string `json:"mint"`    // the synthetic mint (derived from the program; checked when given)
	Program string `json:"program"` // the token program on the far side
	Realm   string `json:"realm"`   // the route realm on gno.land
}

const (
	defaultRealmLink = "https://gno.land/%s"
	defaultGasWanted = 60_000_000
	decimals         = 6
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
	if c.Gno.RPC == "" || c.Gno.ChainID == "" || c.Gno.Mailbox == "" {
		return errors.New("config: gno.rpc, gno.chainId and gno.mailbox are needed")
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
		if ch.Kind != "solana" {
			return fmt.Errorf("config: chain %s: kind %q is not implemented (solana is)", ch.ID, ch.Kind)
		}
		if ch.Name == "" {
			ch.Name = ch.ID
		}
		if ch.RPC == "" || ch.Domain == 0 {
			return fmt.Errorf("config: chain %s: rpc and domain are needed", ch.ID)
		}
		for _, p := range []struct{ name, id string }{{"mailbox", ch.Programs.Mailbox}, {"pausable", ch.Programs.Pausable}, {"admin", ch.Programs.Admin}, {"client", ch.Programs.Client}} {
			if _, err := solana.PublicKeyFromBase58(p.id); err != nil {
				return fmt.Errorf("config: chain %s: programs.%s %q is not a program id", ch.ID, p.name, p.id)
			}
		}
		if len(ch.Tokens) == 0 {
			return fmt.Errorf("config: chain %s: no tokens", ch.ID)
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
			if t.Symbol == "BUBBLE" && c.Gno.Bubble == "" {
				return errors.New("config: gno.bubble (the BUBBLE token realm) is needed for the BUBBLE route")
			}
		}
	}
	return nil
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

// Realms are every bridge realm's pkgpath the config names (the mailbox, the fee realm, r/hlgov, the token
// realm and every route): a burn to any of their addresses is refused (PLAN.md §5.2, K21).
func (c *Config) Realms() []string {
	out := []string{c.Gno.Mailbox}
	for _, p := range []string{c.Gno.Fees, c.Gno.Gov, c.Gno.Bubble} {
		if p != "" {
			out = append(out, p)
		}
	}
	for _, ch := range c.Chains {
		for _, t := range ch.Tokens {
			out = append(out, t.Realm)
		}
	}
	return out
}
