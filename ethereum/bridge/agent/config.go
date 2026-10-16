// Package agent runs the Ethereum bridge's off-chain machines (eth/bridge/PLAN.md §5.5–§5.9, §6.1), carried from the
// Solana bridge's agent (bridge/agent): the updater, which feeds gno.land's headers to the light client on
// Ethereum; the relayer, which delivers gno.land's messages to Ethereum under accepted headers, re-emits waiting
// dispatches in one batch so one update serves them all, delivers decisions to HlAdmin one copy per number,
// executes matured ones and applies queued decisions on gno.land; and — the Ethereum → gno.land lane's — the
// signers, the inbound relayer and the monitor. None of them can move tokens: the chains check the signatures,
// the routes and that no message is delivered twice.
//
// The files: config*.go, alarm.go, status.go, state.go, floats.go and attempt.go are shared by every machine;
// outbox.go is the gno → Ethereum backlog the updater and the relayer both read; updater.go, relayer.go,
// relayer_out.go and executor.go are this lane's machines. A machine of the other direction is a file of its own
// beside them (relayer jobs register through RegisterRelayerJob, relayer.go).
package agent

import (
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/big"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"

	"github.com/gnolang/bridges/solana/bridge/gno"
	"github.com/gnolang/bridges/solana/bridge/hl"
	"github.com/gnolang/bridges/ethereum/bridge/ethx"
)

// Config is one deployment's facts, shared by every machine of it: the realms, the contracts, the signers. The
// launch notice publishes it; each machine adds its own keys by file.
type Config struct {
	Gno struct {
		RPC          string   `json:"rpc"`          // this machine's own gno node
		Sources      []string `json:"sources"`      // the monitor's other sources (rpc.gno.land, then a second public one)
		ChainID      string   `json:"chainId"`      //
		Mailbox      string   `json:"mailbox"`      // gno.land/r/<ns>/eth/hlmailbox
		Fees         string   `json:"fees"`         // …/eth/hlfees
		Gov          string   `json:"gov"`          // …/eth/hlgov
		DAO          string   `json:"dao"`          // gno.land/r/gov/dao
		Gnokey       string   `json:"gnokey"`       //
		Home         string   `json:"home"`         // gnokey's keybase
		Key          string   `json:"key"`          // the relayer's gas-only key
		Address      string   `json:"address"`      // its address (its runway is watched)
		WatchKey     string   `json:"watchKey"`     // the monitor's watch key, in the same keybase
		WatchAddress string   `json:"watchAddress"` //
		GasPrice     float64  `json:"gasPrice"`     // ugnot per gas; the chain's minimum when 0
		GasFee       string   `json:"gasFee"`       // an explicit fee overrides the price
		MaxNodeFee   int64    `json:"maxNodeFee"`   // ugnot: the most a node's own minimum may raise one call's fee to
		AheadBlocks  int64    `json:"aheadBlocks"`  // the signers' allowance for the client's trusted height past a head
	} `json:"gno"`
	Eth struct {
		// Providers are the Ethereum RPC providers: the first is read by the relayer and the updater, every one is
		// given each transaction; a signer and a monitor need two, which must agree (§5.6).
		Providers      []ethx.Provider `json:"providers"`
		ChainID        uint64          `json:"chainId"`        // 1, 11155111, 31337
		Domain         uint32          `json:"domain"`         // Ethereum's Hyperlane domain: 1 on mainnet, 11155111 on Sepolia, 31337 locally
		Mailbox        string          `json:"mailbox"`        // our Mailbox (its proxy)
		MerkleTreeHook string          `json:"merkleTreeHook"` //
		ProtocolFee    string          `json:"protocolFee"`    //
		Client         string          `json:"client"`         // GnoLightClient (its proxy)
		Gate           string          `json:"gate"`           // PausableGate (its proxy)
		Admin          string          `json:"admin"`          // HlAdmin
		ProxyAdmin     string          `json:"proxyAdmin"`     //
		RelayerKey     string          `json:"relayerKey"`     // the relayers' key file (primary and backup share it, §5.5)
		UpdaterKey     string          `json:"updaterKey"`     // the updater's key file (the updater machine only)
		WatchKey       string          `json:"watchKey"`       // the monitor's Ethereum watch key file
		Relayer        string          `json:"relayer"`        // the relayers' address (its runway is watched)
		Updater        string          `json:"updater"`        // the updater's address
		MaxBaseFeeGwei float64         `json:"maxBaseFeeGwei"` // no route delivery or update for routes while the base fee is above it (§5.5; 5 by default)
		// EmergencyMaxBaseFeeGwei is the cap of what never parks under maxBaseFee (the red team's ER6): a decision's
		// delivery to HlAdmin and the header it needs, the announcing header, HlAdmin.execute, a flag — GovDAO's
		// cancels must land in a spike. 50 by default (PLAN.md §5.5; the plan red team's E2-RT8): the 30-day maximum was
		// 24.9, and the updater's and the relayers' keys each keep a decision's header at gnoland-1's set at it (1.45M gas
		// × 1.2 at twice the base fee: ≈0.174 ETH at 50 gwei; float:updater, float:relayer) — a cap the float cannot pay is
		// no cap.
		EmergencyMaxBaseFeeGwei float64 `json:"emergencyMaxBaseFeeGwei"`
		MaxTipGwei              float64 `json:"maxTipGwei"` // the most tip a transaction pays (2 by default)
		MinTipGwei              float64 `json:"minTipGwei"` // the least (0.01 by default)
		// MaxSigsPerCall bounds the signatures one update call carries (§6.3's staging); 0 is one call until its
		// estimate plus 20% does not fit the transaction cap, when the bound halves.
		MaxSigsPerCall int `json:"maxSigsPerCall"`
		// MaxLeafBytes is the biggest results leaf the relayer proves from (a bigger one is re-emitted); 0 is 8,192.
		MaxLeafBytes int `json:"maxLeafBytes"`
		// ReemitBatch is the most nonces one batch re-emit carries, and the most Dispatch events a route send's leaf may
		// carry to be proven from (§5.7); 0 is 4, the cheapest batch.
		ReemitBatch int `json:"reemitBatch"`
		// HeartbeatHours is the heartbeat (§5.5, §6.1): a waiting send this old buys its own header within the budget,
		// whatever anyone else's updates did — the oldest waiting send's age, never the client's freshness (E2-RT4). 6 by
		// default. No header is bought with nothing waiting.
		HeartbeatHours float64 `json:"heartbeatHours"`
		// ProveOld lets the relayer prove a dispatch's own old header (proveHeader) instead of re-emitting it (§5.7).
		ProveOld bool `json:"proveOld"`
		// Prices are each token's price in ETH (a whole token, 10^6 base units) — the GNOT/ETH and BUBBLE/ETH rates: a
		// header is bought for waiting route sends only while their fees on gno.land cover the whole cost it commits, the
		// header's and every delivery's, at the live base fee (budget.go; §5.5). The updater and the relayer refuse to
		// start unless every route's token is priced above zero (CheckPrices; round 2's fact check, E2-FC7): the rule
		// never lapses silently.
		Prices map[string]float64 `json:"prices,omitempty"`
		// PricesAt is the date the operator set eth.prices (2006-01-02, or RFC 3339): the rates are an operator's
		// reading of the markets (BUBBLE's a thin GnoSwap pool), and a stale one values every send's fee at what it is
		// no longer worth — so prices:stale alarms while they are undated or older than PricesStaleAfter (E3-GS11,
		// E3-FC7). A date that does not parse, or lies in the future, is refused at start.
		PricesAt string `json:"pricesAt,omitempty"`
		// UpdateGas is the operator's own figure for a header update's gas, overriding the model (budget.go: ≈221,000 a
		// signature, ≈15,600 a validator, 250,000 besides — E-M2b's measurements); 0 is the model. Never a receipt's: a
		// stranger chooses the gas of the updates it sends (E2-RT4).
		UpdateGas uint64 `json:"updateGas"`
		// DeployBlock is the block of HlAdmin.finishSetup (`deployBlock` in hleth setup's deployment.json and in the
		// config part `hleth setup -config-out` writes; PLAN.md §5.6): where every scan of a fresh install starts — the
		// signers' and the monitors' burn index, the header record (seeded from the client's counter and running hash
		// at the block before), the stages and the decisions, hlwatch's, the inbound relayer's burns. The scans' start
		// only: what is no burn of this deployment is drawn by the launch line, HlAdmin's launchNonce() (a message
		// numbered below it was dispatched before the launch — a deployer's mint and burn-back while it still owned the
		// mailbox and the tokens, possibly in finishSetup's own block), read from eth.admin and filtered by every scan
		// (ethx.LaunchLine; M2-NOTES.md Fold F5, F5.1 item 12). A machine's -from-block overrides it; with neither, the
		// first block at which HlAdmin's setupDone() is true is searched for (eth_call at past blocks: a full-history
		// provider) — a planted setupDone could only move that start earlier, which the line makes harmless.
		DeployBlock uint64 `json:"deployBlock,omitempty"`
	} `json:"eth"`
	// Routes are the token routes: the gno realm and the HypERC20 (its proxy) of each.
	Routes []Route `json:"routes"`
	// Signers are the signer machines, by the ed25519 key each holds in hlmailbox's set.
	Signers []SignerRef `json:"signers"`
	// Webhook takes every alarm as one JSON POST; "" logs only.
	Webhook string `json:"webhook"`
	// AllowHTTP lets signer URLs be plain http and a pair name one provider twice (a local run).
	AllowHTTP bool `json:"allowHTTP"`
	// Runway overrides a float's planned spend per day, in its base unit (wei or ugnot); see floats.go.
	Runway map[string]float64 `json:"runway,omitempty"`
}

// Route is one token route.
type Route struct {
	Symbol       string `json:"symbol"`                 // "GNOT" or "BUBBLE"
	Gno          string `json:"gno"`                    // the route realm's pkgpath
	Token        string `json:"token"`                  // the HypERC20 (its proxy's address)
	Successor    string `json:"successor,omitempty"`    // the token's other realm while a migration is open
	Predecessor  string `json:"predecessor,omitempty"`  //
	ExpectedLock int64  `json:"expectedLock,omitempty"` // the lock the launch notice expected in the first week, base units
}

// SignerRef is where one signer serves its signatures and which key it signs with.
type SignerRef struct {
	URL string `json:"url"`
	Key string `json:"key"` // its ed25519 key in hex, as hlmailbox lists it
}

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
	if err := c.Check(); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &c, nil
}

// Check fills the defaults and checks the config: the gno side, the addresses (0x and 40 hex digits, EIP-55 when
// mixed case), the routes, the signers' keys and URLs.
func (c *Config) Check() error {
	if c.Gno.Gnokey == "" {
		c.Gno.Gnokey = "gnokey"
	}
	if c.Gno.DAO == "" {
		c.Gno.DAO = "gno.land/r/gov/dao"
	}
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
			if _, err := ethx.ParseAddress(r.Token); err != nil {
				return fmt.Errorf("config: route %s: token: %w", r.Symbol, err)
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
	for _, a := range []struct{ name, v string }{{"mailbox", c.Eth.Mailbox}, {"merkleTreeHook", c.Eth.MerkleTreeHook}, {"protocolFee", c.Eth.ProtocolFee},
		{"client", c.Eth.Client}, {"gate", c.Eth.Gate}, {"admin", c.Eth.Admin}, {"proxyAdmin", c.Eth.ProxyAdmin}, {"relayer", c.Eth.Relayer}, {"updater", c.Eth.Updater}} {
		if a.v == "" {
			continue
		}
		if _, err := ethx.ParseAddress(a.v); err != nil {
			return fmt.Errorf("config: eth.%s: %w", a.name, err)
		}
	}
	for _, p := range c.Eth.Providers {
		if p.Name == "" || p.URL == "" {
			return fmt.Errorf("config: an eth provider needs a name and a URL (%s)", p)
		}
	}
	if c.HasContracts() && (c.Eth.Domain == 0 || c.Eth.ChainID == 0) {
		return errors.New("config: eth.domain and eth.chainId are needed beside the contracts")
	}
	if c.Eth.MaxBaseFeeGwei < 0 || c.Eth.EmergencyMaxBaseFeeGwei < 0 || c.Eth.MaxTipGwei < 0 || c.Eth.MinTipGwei < 0 || c.Eth.MaxSigsPerCall < 0 || c.Eth.MaxLeafBytes < 0 || c.Eth.ReemitBatch < 0 || c.Eth.HeartbeatHours < 0 {
		return errors.New("config: a negative eth bound")
	}
	for sym, p := range c.Eth.Prices {
		if p <= 0 || c.RouteBySymbol(sym) == nil {
			return fmt.Errorf("config: eth.prices[%s] = %v: a positive price of a configured route's token", sym, p)
		}
	}
	if c.Eth.EmergencyMaxBaseFeeGwei > 0 && c.Eth.EmergencyMaxBaseFeeGwei < c.Eth.MaxBaseFeeGwei {
		return errors.New("config: eth.emergencyMaxBaseFeeGwei is below eth.maxBaseFeeGwei")
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
		return fmt.Errorf("config: signer url %q", ethx.Redact(raw))
	}
	if u.Scheme == "https" || allowHTTP {
		return nil
	}
	h := u.Hostname()
	if h == "localhost" || h == "127.0.0.1" || h == "::1" {
		return nil
	}
	return fmt.Errorf("config: signer url %q is not https (set allowHTTP for a local run)", ethx.Redact(raw))
}

// EdKey is the signer's ed25519 key.
func (s SignerRef) EdKey() (ed25519.PublicKey, error) {
	b, err := hex.DecodeString(strings.TrimPrefix(s.Key, "0x"))
	if err != nil || len(b) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("config: signer ed25519 key %q", s.Key)
	}
	return ed25519.PublicKey(b), nil
}

// ------------------------------------------------------------------- gno.land --

// GnoClient reads this machine's own node and signs with the relayer's gas key.
func (c *Config) GnoClient() *gno.Client { return c.gnoClient(c.Gno.RPC, c.Gno.Key) }

// WatchClient reads the own node and signs with the watch key.
func (c *Config) WatchClient() *gno.Client { return c.gnoClient(c.Gno.RPC, c.Gno.WatchKey) }

// SourceClient reads one of the gno sources (no key).
func (c *Config) SourceClient(rpc string) *gno.Client { return c.gnoClient(rpc, "") }

func (c *Config) gnoClient(rpc, key string) *gno.Client {
	return &gno.Client{RPC: rpc, ChainID: c.Gno.ChainID, Gnokey: c.Gno.Gnokey, Home: c.Gno.Home, Key: key, GasFee: c.Gno.GasFee, GasPrice: c.Gno.GasPrice, MaxNodeFee: c.Gno.MaxNodeFee}
}

// GovSender is r/hlgov's address as Hyperlane carries it (twelve zero bytes, then the twenty).
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

// RouteByToken is the route whose HypERC20 is this address, or nil.
func (c *Config) RouteByToken(a common.Address) *Route {
	for i := range c.Routes {
		if c.Routes[i].Token != "" && c.Routes[i].TokenAddress() == a {
			return &c.Routes[i]
		}
	}
	return nil
}

// TokenAddress is a route's HypERC20 (zero when none is configured).
func (r Route) TokenAddress() common.Address {
	if r.Token == "" {
		return common.Address{}
	}
	return common.HexToAddress(r.Token)
}

// TokenIndex is a route symbol's place in the gate's limits and HlAdmin's tokens: 0 GNOT, 1 BUBBLE.
func TokenIndex(symbol string) (int, bool) {
	switch symbol {
	case "GNOT":
		return 0, true
	case "BUBBLE":
		return 1, true
	}
	return 0, false
}

// DefaultAheadBlocks is §5.6's "an hour of blocks" at gno.land's 3.43 s a block.
const DefaultAheadBlocks = 1050

// AheadAllowance is the signers' allowance for the client's trusted height past a head.
func (c *Config) AheadAllowance() uint64 {
	if c.Gno.AheadBlocks > 0 {
		return uint64(c.Gno.AheadBlocks)
	}
	return DefaultAheadBlocks
}

// ------------------------------------------------------------------- Ethereum --

// Address is an Ethereum address of the config (zero when it names none).
func Address(s string) common.Address {
	if s == "" {
		return common.Address{}
	}
	return common.HexToAddress(s)
}

// HasContracts says whether the config names the contracts the gno → Ethereum direction needs.
func (c *Config) HasContracts() bool {
	return c.Eth.Mailbox != "" && c.Eth.Client != "" && c.Eth.Gate != "" && c.Eth.Admin != ""
}

// CheckTrustPath is what a signer ("signer"), a monitor and hlwatch ("monitor", "hlwatch") ask of the config before
// they start (the Go trust audit's E3-GT8): every contract the refusal rule and the watch read, named. Config.Check
// validates only what is present, and without eth.client a signer read the trusted height as 0 and never applied §5.6's
// ahead-stop, while a monitor's record, stages, foreign decisions, pause mirror, caps and header:ahead never ran — and
// hlwatch said GREEN — with no alarm; a route with no token went unjudged the same way. So: our mailbox, the light
// client, HlAdmin and Ethereum's domain, every route's token; the signer the hook its checkpoint names; the monitor
// and hlwatch the gate and r/hlgov.
func (c *Config) CheckTrustPath(role string) error {
	var missing []string
	need := func(name, v string) {
		if v == "" {
			missing = append(missing, name)
		}
	}
	need("eth.mailbox", c.Eth.Mailbox)
	need("eth.client", c.Eth.Client)
	need("eth.admin", c.Eth.Admin)
	if c.Eth.Domain == 0 {
		missing = append(missing, "eth.domain")
	}
	for _, r := range c.Routes {
		need("routes["+r.Symbol+"].token", r.Token)
	}
	if role == "signer" {
		need("eth.merkleTreeHook", c.Eth.MerkleTreeHook)
	} else {
		need("eth.gate", c.Eth.Gate)
		need("gno.gov", c.Gno.Gov)
	}
	if len(missing) > 0 {
		return fmt.Errorf("config: the %s refuses to start without %s: a rule that reads one would never run, and say nothing (E3-GT8)", role, strings.Join(missing, ", "))
	}
	return nil
}

// EthChainID is the chain id as a big integer.
func (c *Config) EthChainID() *big.Int { return new(big.Int).SetUint64(c.Eth.ChainID) }

// SendConfig is the sender's bounds from the config, with its state file.
func (c *Config) SendConfig(state string) ethx.SendConfig {
	sc := ethx.SendConfig{State: state}
	if c.Eth.MaxBaseFeeGwei > 0 {
		sc.MaxBaseFee = ethx.GweiOf(c.Eth.MaxBaseFeeGwei)
	}
	if c.Eth.MaxTipGwei > 0 {
		sc.MaxTip = ethx.GweiOf(c.Eth.MaxTipGwei)
	}
	if c.Eth.MinTipGwei > 0 {
		sc.MinTip = ethx.GweiOf(c.Eth.MinTipGwei)
	}
	return sc
}

// DefaultEmergencyGwei is the emergency cap's default (E2-RT8): what the updater's and the relayers' floors pay a
// decision's header at (≈0.174 ETH at gnoland-1's set).
const DefaultEmergencyGwei = 50

// EmergencyMaxBaseFee is the cap of the calls that never park under maxBaseFee (ER6), in wei.
func (c *Config) EmergencyMaxBaseFee() *big.Int {
	if c.Eth.EmergencyMaxBaseFeeGwei > 0 {
		return ethx.GweiOf(c.Eth.EmergencyMaxBaseFeeGwei)
	}
	return ethx.GweiOf(DefaultEmergencyGwei)
}

// ScanFrom is where a fresh install's scans start: a machine's -from-block when given (non-zero), else the config's
// eth.deployBlock; 0 when neither names one (the caller searches for the mailbox's first block).
func (c *Config) ScanFrom(flag uint64) uint64 {
	if flag > 0 {
		return flag
	}
	return c.Eth.DeployBlock
}

// CheckPrices is what the updater and the relayer ask of the config before they start (PLAN.md §5.5; round 2's fact
// check, E2-FC7): every route's token priced in ETH above zero. Without the rates, "a header is bought only when the
// waiting sends' fees cover it" could not be judged, and an empty table once turned the rule off — every waiting send
// buying its header, the float bled by a stream of minimum sends — so they fail closed instead.
func (c *Config) CheckPrices() error {
	var missing []string
	for _, r := range c.Routes {
		if p, ok := c.Eth.Prices[r.Symbol]; !ok || !(p > 0) || math.IsInf(p, 0) {
			missing = append(missing, r.Symbol)
		}
	}
	if len(c.Routes) == 0 {
		return errors.New("config: no routes to price")
	}
	if len(missing) > 0 {
		return fmt.Errorf("config: eth.prices has no positive rate for %s (each route's token in ETH, a whole token: the GNOT/ETH and BUBBLE/ETH rates): a header is bought only when the waiting sends' fees cover it, and without the rates that rule cannot be judged — refused rather than buying a header for every send", strings.Join(missing, ", "))
	}
	if c.Eth.PricesAt != "" {
		at, err := c.pricesAt()
		if err != nil {
			return err
		}
		if at.After(time.Now().Add(24 * time.Hour)) {
			return fmt.Errorf("config: eth.pricesAt %q lies in the future: the date eth.prices were set", c.Eth.PricesAt)
		}
	}
	return nil
}

// PricesStaleAfter is how old eth.prices may be before prices:stale alarms (E3-GS11, E3-FC7): a week.
const PricesStaleAfter = 7 * 24 * time.Hour

// pricesAt is the date eth.prices were set (eth.pricesAt: a date, or RFC 3339).
func (c *Config) pricesAt() (time.Time, error) {
	for _, layout := range []string{"2006-01-02", time.RFC3339} {
		if t, err := time.Parse(layout, c.Eth.PricesAt); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("config: eth.pricesAt %q is no date (2006-01-02, or RFC 3339): the date eth.prices were set", c.Eth.PricesAt)
}

// PricesStale says whether eth.prices are undated or older than PricesStaleAfter at now, and why.
func (c *Config) PricesStale(now time.Time) (bool, string) {
	if c.Eth.PricesAt == "" {
		return true, "eth.prices carry no date (eth.pricesAt): the GNOT/ETH and BUBBLE/ETH rates every send's fee is valued at cannot be told fresh — date them when they are set"
	}
	at, err := c.pricesAt()
	if err != nil {
		return true, err.Error()
	}
	if age := now.Sub(at); age > PricesStaleAfter || age < -24*time.Hour {
		return true, fmt.Sprintf("eth.prices were set on %s, %.0f days ago: the fee rule values every send's fee at them — re-read GNOT/ETH and BUBBLE/ETH (BUBBLE from its GnoSwap pool) and update eth.prices and eth.pricesAt; until then a header may be bought that the fees no longer cover", at.Format("2006-01-02"), age.Hours()/24)
	}
	return false, ""
}

// pricesWatch stands prices:stale while eth.prices are undated or older than a week (E3-GS11, E3-FC7), judged once a
// minute (the machines' steps are seconds apart).
type pricesWatch struct{ next time.Time }

func (w *pricesWatch) check(c *Config, a *Alarmer, now time.Time) {
	if now.Before(w.next) {
		return
	}
	w.next = now.Add(time.Minute)
	if stale, why := c.PricesStale(now); stale {
		a.AlarmEvery("prices:stale", why, 24*time.Hour)
		return
	}
	a.Clear("prices:stale")
}

// FeeWei is a route's fee per send (base units of its token) in wei at the config's price; ok false when the config
// does not price this token (never, once CheckPrices passed).
func (c *Config) FeeWei(symbol string, fee int64) (*big.Int, bool) {
	p, ok := c.Eth.Prices[symbol]
	if !ok || fee <= 0 {
		return new(big.Int), ok
	}
	w := new(big.Float).Mul(new(big.Float).SetInt64(fee), big.NewFloat(p*1e12)) // fee/1e6 tokens × p ETH × 1e18 wei
	out, _ := w.Int(nil)
	return out, true
}

// MaxLeaf is the biggest results leaf the relayer proves from.
func (c *Config) MaxLeaf() int {
	if c.Eth.MaxLeafBytes > 0 {
		return c.Eth.MaxLeafBytes
	}
	return 8192
}

// ReemitBatch is the most nonces one batch re-emit carries — and the most Dispatch events a route send's leaf may
// carry to be proven from: 4 by default, the cheapest batch (PLAN.md §5.7; the plan red team's E2-RT5). Every
// Dispatch in a leaf costs each delivery proven from that leaf ≈94k gas (E-M2b: the client reads every Dispatch of the
// mailbox in the leaf whole), so a batch of N costs its deliveries N·(N−1)·94k gas against the N−1 header updates it
// saves (≈1.45M each at gnoland-1's set): the cheapest is ≈√(1.45M/94k) ≈ 4. A send in a bigger leaf — anyone's
// Reemits can pile one up — is re-emitted into the relayer's own batch, never delivered at the big leaf's price.
func (c *Config) ReemitBatch() int {
	if c.Eth.ReemitBatch > 0 {
		return c.Eth.ReemitBatch
	}
	return 4
}

// Heartbeat is the oldest waiting send's age at which it buys its own header within the budget (§5.5, §6.1).
func (c *Config) Heartbeat() time.Duration {
	if c.Eth.HeartbeatHours > 0 {
		return time.Duration(c.Eth.HeartbeatHours * float64(time.Hour))
	}
	return SendAgeHeader
}
