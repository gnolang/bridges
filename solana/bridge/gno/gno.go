// Package gno reads gno.land realms over the node's JSON-RPC (vm/qrender, pinned at a height when asked) and calls
// them through gnokey, the way the box's keeper does: one binary that already signs for gno.land, a key in its
// keybase, no library. gnokey simulates every transaction before it broadcasts it (its -simulate test default,
// passed explicitly here), so a call the chain would refuse costs no gas.
package gno

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Client reads from one node and signs with one gnokey key.
type Client struct {
	RPC      string // http://host:26657
	ChainID  string
	Gnokey   string  // the gnokey binary
	Home     string  // gnokey's home ("" for its default)
	Key      string  // the key's name in that keybase; it must have no password (the hotkey's way)
	GasFee   string  // an explicit fee, e.g. "1000000ugnot"; "" computes GasPrice × gas wanted
	GasPrice float64 // ugnot per gas unit; 0 means the chain's minimum, 0.001 (PLAN.md §5.7)
	// MaxNodeFee is the most a node's own minimum gas price may raise one call's fee to, in ugnot (F14); 0 is the
	// greater of ten times the fee at the block price and 1 GNOT. A call the node refuses for its own minimum
	// pays the fee it names when under the bound, and is parked with the node's text above it.
	MaxNodeFee int64
	HTTP       *http.Client
}

// MinGasPrice is gnoland-1's minimum, in ugnot per gas (measured, PLAN.md §5.7).
const MinGasPrice = 0.001

// Deadlines on every call (G12: one stalled endpoint must not hold a machine's loop): an RPC read ends within
// RPCTimeout; a gnokey call — a simulation, a broadcast and the wait for its commit — within GnokeyTimeout.
const (
	RPCTimeout    = 30 * time.Second
	GnokeyTimeout = 90 * time.Second
)

func (c *Client) http() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: RPCTimeout}
}

// Fee is the fee string gnokey is given for gasWanted: the explicit one, or the price times the gas, rounded up.
func (c *Client) Fee(gasWanted int64) string {
	return c.feeAt(gasWanted, 0)
}

// feeAt is the fee for gasWanted at the higher of the client's price and a chain price (F9: the block gas price
// read before the call; 0 when none was read).
func (c *Client) feeAt(gasWanted int64, chainPrice float64) string {
	if c.GasFee != "" {
		return c.GasFee
	}
	p := c.GasPrice
	if p <= 0 {
		p = MinGasPrice
	}
	if chainPrice > p {
		p = chainPrice
	}
	return strconv.FormatInt(int64(math.Ceil(p*float64(gasWanted))), 10) + "ugnot"
}

// ------------------------------------------------------------------ rpc --

type rpcError struct {
	Message string `json:"message"`
	Data    string `json:"data"`
}

// A node that rate-limits this machine is left alone for the wait it names (2026-10-14: rpc.gno.land banned the
// box's address for 600 s at a time from 05:40 UTC, and every service polling through the ban every few seconds
// kept it from lifting until they moved to another node at 06:16). The wait is process-wide and per node URL, so
// every client of the node in this process holds back; a call inside it fails at once, as any failed read does.
var (
	limitMu   sync.Mutex
	limitTill = map[string]time.Time{}
	now       = time.Now
)

const (
	limitDefault = 30 * time.Second // a 429 that names no wait
	limitMax     = 15 * time.Minute // the longest wait taken from a node's answer
)

func resetLimits() {
	limitMu.Lock()
	defer limitMu.Unlock()
	limitTill = map[string]time.Time{}
}

// limitedUntil is the end of a node's standing wait; ok false when none stands.
func limitedUntil(node string) (time.Time, bool) {
	limitMu.Lock()
	defer limitMu.Unlock()
	till, ok := limitTill[node]
	if ok && now().Before(till) {
		return till, true
	}
	delete(limitTill, node)
	return time.Time{}, false
}

// noteLimit starts a node's wait: d, limitDefault when d is 0, never past limitMax.
func noteLimit(node string, d time.Duration) {
	if d <= 0 {
		d = limitDefault
	}
	d = min(d, limitMax)
	limitMu.Lock()
	defer limitMu.Unlock()
	limitTill[node] = now().Add(d)
}

var (
	rateLimitRE  = regexp.MustCompile(`(?i)rate limits? exceeded`)
	retryAfterRE = regexp.MustCompile(`(?i)retry after (\d+) ?s`)
)

// rateLimited reads a node's answer for a rate limit — HTTP 429, or an error text saying one was exceeded — and
// its wait: the Retry-After header's seconds, else the text's "retry after N seconds", else 0 (limitDefault).
func rateLimited(status int, header http.Header, text string) (time.Duration, bool) {
	if status != http.StatusTooManyRequests && !rateLimitRE.MatchString(text) {
		return 0, false
	}
	if n, err := strconv.Atoi(strings.TrimSpace(header.Get("Retry-After"))); err == nil && n > 0 {
		return time.Duration(n) * time.Second, true
	}
	if m := retryAfterRE.FindStringSubmatch(text); m != nil {
		if n, err := strconv.Atoi(m[1]); err == nil && n > 0 {
			return time.Duration(n) * time.Second, true
		}
	}
	return 0, true
}

// Request is one JSON-RPC request for a method this package has no reader of; result receives the "result" member.
func (c *Client) Request(ctx context.Context, method string, params map[string]any, result any) error {
	return c.call(ctx, method, params, result)
}

// call is one JSON-RPC request to the node; params are amino-JSON (an int64 goes as a string).
func (c *Client) call(ctx context.Context, method string, params map[string]any, result any) error {
	if till, ok := limitedUntil(c.RPC); ok {
		return fmt.Errorf("gno rpc %s: the node rate-limited this machine; no request to it until %s UTC", method, till.UTC().Format(time.TimeOnly))
	}
	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
	req, err := http.NewRequestWithContext(ctx, "POST", c.RPC, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	var out struct {
		Result json.RawMessage `json:"result"`
		Error  *rpcError       `json:"error"`
	}
	decodeErr := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&out)
	text := ""
	if out.Error != nil {
		text = out.Error.Message + " " + out.Error.Data
	}
	if wait, limited := rateLimited(resp.StatusCode, resp.Header, text); limited {
		noteLimit(c.RPC, wait)
	}
	if decodeErr != nil {
		return fmt.Errorf("gno rpc %s: %w", method, decodeErr)
	}
	if out.Error != nil {
		return fmt.Errorf("gno rpc %s: %s %s", method, out.Error.Message, out.Error.Data)
	}
	if result != nil {
		if err := json.Unmarshal(out.Result, result); err != nil {
			return fmt.Errorf("gno rpc %s: %w", method, err)
		}
	}
	return nil
}

// Head is a block's height, time and hash, as /status and /block give them.
type Head struct {
	Height     int64
	Time       time.Time
	Hash       string // the block hash, lowercase hex
	CatchingUp bool
}

// Status is the node's latest block.
func (c *Client) Status(ctx context.Context) (Head, error) {
	var out struct {
		SyncInfo struct {
			LatestBlockHash   []byte    `json:"latest_block_hash"`
			LatestBlockHeight string    `json:"latest_block_height"`
			LatestBlockTime   time.Time `json:"latest_block_time"`
			CatchingUp        bool      `json:"catching_up"`
		} `json:"sync_info"`
	}
	if err := c.call(ctx, "status", map[string]any{}, &out); err != nil {
		return Head{}, err
	}
	h, err := strconv.ParseInt(out.SyncInfo.LatestBlockHeight, 10, 64)
	if err != nil {
		return Head{}, fmt.Errorf("gno status: height %q", out.SyncInfo.LatestBlockHeight)
	}
	return Head{Height: h, Time: out.SyncInfo.LatestBlockTime, Hash: strings.ToLower(fmt.Sprintf("%x", out.SyncInfo.LatestBlockHash)), CatchingUp: out.SyncInfo.CatchingUp}, nil
}

// Block is the block at a height: its hash and time.
func (c *Client) Block(ctx context.Context, height int64) (Head, error) {
	var out struct {
		BlockMeta struct {
			BlockID struct {
				Hash []byte `json:"hash"`
			} `json:"block_id"`
			Header struct {
				Height string    `json:"height"`
				Time   time.Time `json:"time"`
			} `json:"header"`
		} `json:"block_meta"`
	}
	if err := c.call(ctx, "block", map[string]any{"height": strconv.FormatInt(height, 10)}, &out); err != nil {
		return Head{}, err
	}
	h, err := strconv.ParseInt(out.BlockMeta.Header.Height, 10, 64)
	if err != nil || h != height {
		return Head{}, fmt.Errorf("gno block %d: got height %q", height, out.BlockMeta.Header.Height)
	}
	return Head{Height: h, Time: out.BlockMeta.Header.Time, Hash: strings.ToLower(fmt.Sprintf("%x", out.BlockMeta.BlockID.Hash))}, nil
}

// Query is one abci_query, at a height (0 for the latest); it returns the response data.
func (c *Client) Query(ctx context.Context, path string, data []byte, height int64) ([]byte, error) {
	params := map[string]any{"path": path, "data": base64.StdEncoding.EncodeToString(data)}
	if height > 0 {
		params["height"] = strconv.FormatInt(height, 10)
	}
	var out struct {
		Response struct {
			ResponseBase struct {
				Error any
				Data  []byte
				Log   string
			}
		} `json:"response"`
	}
	if err := c.call(ctx, "abci_query", params, &out); err != nil {
		return nil, err
	}
	rb := out.Response.ResponseBase
	if rb.Error != nil {
		return nil, fmt.Errorf("gno query %s: %v %s", path, rb.Error, rb.Log)
	}
	return rb.Data, nil
}

// Render is a realm's Render(path), as of the latest block.
func (c *Client) Render(ctx context.Context, pkgPath, path string) (string, error) {
	return c.RenderAt(ctx, pkgPath, path, 0)
}

// RenderAt is a realm's Render(path) as of the state at a height (0 for the latest): the node keeps the versions its
// pruning window allows, so a monitor can read every realm at one and the same height.
func (c *Client) RenderAt(ctx context.Context, pkgPath, path string, height int64) (string, error) {
	b, err := c.Query(ctx, "vm/qrender", []byte(pkgPath+":"+path), height)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// RenderJSON renders and decodes JSON.
func (c *Client) RenderJSON(ctx context.Context, pkgPath, path string, v any) error {
	return c.RenderJSONAt(ctx, pkgPath, path, 0, v)
}

// RenderJSONAt renders at a height and decodes JSON.
func (c *Client) RenderJSONAt(ctx context.Context, pkgPath, path string, height int64, v any) error {
	s, err := c.RenderAt(ctx, pkgPath, path, height)
	if err != nil {
		return err
	}
	if err := json.Unmarshal([]byte(s), v); err != nil {
		return fmt.Errorf("%s:%s: %w: %.200s", pkgPath, path, err, s)
	}
	return nil
}

var coinRE = regexp.MustCompile(`(\d+)ugnot`)

// Balance is an address's ugnot, as the bank keeper reports it.
func (c *Client) Balance(ctx context.Context, addr string) (int64, error) {
	b, err := c.Query(ctx, "bank/balances/"+addr, nil, 0)
	if err != nil {
		return 0, err
	}
	s := strings.Trim(strings.TrimSpace(string(b)), "\"")
	m := coinRE.FindStringSubmatch(s)
	if m == nil {
		return 0, nil // no ugnot at all
	}
	return strconv.ParseInt(m[1], 10, 64)
}

// --------------------------------------------------------------- gnokey --

// Result is what a broadcast transaction came back with.
type Result struct {
	Hash    string
	Height  int64
	GasUsed int64
	Events  []Event
	Output  string // gnokey's whole output, for the log
}

// Event is one event a call emitted, loosely decoded.
type Event struct {
	Type  string
	Attrs map[string]string
}

// Attr is the value of an attribute of the first event of a type, if any.
func (r *Result) Attr(eventType, key string) (string, bool) {
	for _, e := range r.Events {
		if e.Type == eventType {
			v, ok := e.Attrs[key]
			return v, ok
		}
	}
	return "", false
}

var (
	txHashRE  = regexp.MustCompile(`TX HASH:\s+(\S+)`)
	gasUsedRE = regexp.MustCompile(`GAS USED:\s+(\d+)`)
	heightRE  = regexp.MustCompile(`HEIGHT:\s+(\d+)`)
	eventsRE  = regexp.MustCompile(`EVENTS:\s+(\[.*\])`)
)

// parseEvents reads gnokey's EVENTS line without depending on the exact field names: an event is an object with
// a "type" and a list of key/value objects under any field.
func parseEvents(line string) []Event {
	var raw []map[string]any
	if json.Unmarshal([]byte(line), &raw) != nil {
		return nil
	}
	var out []Event
	for _, m := range raw {
		e := Event{Attrs: map[string]string{}}
		if t, ok := m["type"].(string); ok {
			e.Type = t
		}
		for _, v := range m {
			list, ok := v.([]any)
			if !ok {
				continue
			}
			for _, it := range list {
				kv, ok := it.(map[string]any)
				if !ok {
					continue
				}
				k, _ := kv["key"].(string)
				val, _ := kv["value"].(string)
				if k != "" {
					e.Attrs[k] = val
				}
			}
		}
		out = append(out, e)
	}
	return out
}

// CallError is a call gnokey or the chain refused, with the output that says why.
type CallError struct {
	Pkg, Fn string
	Err     error
	Output  string
}

func (e *CallError) Error() string {
	// a refusal made here heads its output with the REFUSING line, the two fees in it (F14): printed first, so
	// the parked alarm carries them and not gnokey's tail alone (G35)
	if strings.HasPrefix(e.Output, nodeFeeRefused) {
		head, _, _ := strings.Cut(e.Output, "\n")
		return fmt.Sprintf("gnokey %s.%s: %v: %s | %s", e.Pkg, e.Fn, e.Err, head, lastLines(e.Output, 12))
	}
	return fmt.Sprintf("gnokey %s.%s: %v: %s", e.Pkg, e.Fn, e.Err, lastLines(e.Output, 12))
}

func (e *CallError) Unwrap() error { return e.Err }

// transientRE names the failures that may pass next time: the way (a dead node, a timeout, the mempool), a
// sequence race (tm2's "invalid sequence" and its "verify correct account, sequence, and chain-id" — the exact
// texts, G21: a realm's refusal wording "sequence", a kind-10 builder's, is deterministic), and the price and
// the gas the chain refused (F9: "insufficient fee" when the block gas price rose past the fee, "out of gas"
// when the measured gas fell short — the next call re-prices and re-measures).
var transientRE = regexp.MustCompile(`(?i)connection refused|connection reset|no such host|timeout|timed out|i/o error|EOF|dial tcp|unavailable|too many requests|503|502|504|mempool|invalid sequence|verify correct account, sequence, and chain-id|insufficient fee|out of gas|broken pipe|network is unreachable|context deadline|temporarily`)

// Deterministic says whether a failed call would fail the same way again (the chain or the VM refused it: a
// panic, a refused argument, a paused function) rather than having been lost on the way (a dead node, a timeout,
// a sequence race) or refused on its price or gas, which the next call sets again (F9). Three deterministic
// failures park a message (PLAN.md §5.7); a transient one is retried. A node's own minimum fee above the bound is
// refused here, not by the chain, and parks the same way (F14: before it, every "insufficient fee" was transient,
// so a node minimum above the block price retried on backoff for ever — never paid, never parked).
func Deterministic(err error) bool {
	var ce *CallError
	if !errors.As(err, &ce) {
		return false
	}
	if strings.HasPrefix(ce.Output, nodeFeeRefused) || nodeOtherDenomRE.MatchString(ce.Output) {
		return true
	}
	return !transientRE.MatchString(ce.Output) && !transientRE.MatchString(ce.Err.Error())
}

// nodeMinFeeRE reads the ante's refusal under a node's own minimum gas price (tm2 auth/ante.go: "insufficient
// fees; got: {Gas-Wanted: N, Gas-Fee Fugnot}, fee required: R with … as minimum gas price set by the node"), the
// fee the call carried and the one the node asks, in the fee's denom (ugnot).
var nodeMinFeeRE = regexp.MustCompile(`Gas-Fee (\d+)ugnot}, fee required: (\d+) with .* as minimum gas price set by the node`)

// nodeOtherDenomRE is the ante's third refusal (tm2 auth/ante.go:579): the node's minimum gas prices name no
// price in the fee's denom — "insufficient fees; got: {…}, required (one of): …" — which no re-pricing in ugnot
// cures: deterministic, parked with the node's text (G35: unparsed, it fell under transientRE's "insufficient
// fee" and retried for ever, F14's door left ajar).
var nodeOtherDenomRE = regexp.MustCompile(`insufficient fees; got: \{[^}]*\}, required \(one of\): `)

// nodeFeeRefused heads the output of a call refused here for a node minimum above the bound (F14).
const nodeFeeRefused = "REFUSING the node's minimum gas price"

// NodeMinFee reads a refusal under a node's own minimum gas price (F14): the fee the node asks and the one the
// call carried, in ugnot; 0 and 0 when the refusal is another (the block price's, or none).
func NodeMinFee(err error) (need, carried int64) {
	var ce *CallError
	if !errors.As(err, &ce) {
		return 0, 0
	}
	m := nodeMinFeeRE.FindStringSubmatch(ce.Output)
	if m == nil {
		return 0, 0
	}
	carried, _ = strconv.ParseInt(m[1], 10, 64)
	need, _ = strconv.ParseInt(m[2], 10, 64)
	return need, carried
}

// nodeFeeBound is the most a node's minimum may raise a call's fee to (F14): MaxNodeFee, or the greater of ten
// times the fee the call carried at the block price and 1 GNOT.
func (c *Client) nodeFeeBound(carried int64) int64 {
	if c.MaxNodeFee > 0 {
		return c.MaxNodeFee
	}
	return max(10*carried, 1_000_000)
}

// nodeFeeDecision says whether a refused call pays the fee a node's minimum asks (F14): under the bound it does.
func nodeFeeDecision(need, bound int64) bool { return need <= bound }

// Call runs one MsgCall through gnokey and returns the result: a simulation first, under gasWanted as the ceiling
// (four times it when the ceiling itself is what the simulation runs out of, bounded below the block's), whose
// measured gas sizes the broadcast with a fifth's margin (F9: the running build's metering, not a compiled
// number, sets the gas; gasWanted alone is the fallback when the simulation reports none), the fee at the chain's
// block gas price (auth/gasprice) or the client's own, whichever is higher — then the broadcast, which gnokey
// simulates once more. gnokey prints "OK!" and the hash only for a transaction the chain took; anything else is
// a CallError carrying its output.
func (c *Client) Call(ctx context.Context, pkgPath, fn string, gasWanted int64, args ...string) (*Result, error) {
	gas := gasWanted
	sim, err := c.run(ctx, pkgPath, fn, gasWanted, "only", "", args...)
	if err != nil && isOutOfGas(err) && gasWanted*4 <= MaxBlockGas {
		sim, err = c.run(ctx, pkgPath, fn, gasWanted*4, "only", "", args...)
	}
	if err != nil {
		return nil, err
	}
	if sim.GasUsed > 0 {
		gas = GasFor(sim.GasUsed)
	}
	res, err := c.run(ctx, pkgPath, fn, gas, "test", "", args...)
	// the node's own minimum gas price (F14): CheckTx refuses with the fee it asks; paid once when under the bound,
	// parked with the node's text above it
	if need, carried := NodeMinFee(err); need > 0 {
		bound := c.nodeFeeBound(carried)
		if !nodeFeeDecision(need, bound) {
			var ce *CallError
			errors.As(err, &ce)
			return nil, &CallError{Pkg: pkgPath, Fn: fn, Err: errors.New("the node's minimum gas price is above the bound"),
				Output: fmt.Sprintf("%s: fee required %d ugnot, the bound %d ugnot (gno.maxNodeFee; by default the greater of ten times the block-price fee of %d and 1 GNOT): parked with the node's text\n%s", nodeFeeRefused, need, bound, carried, ce.Output)}
		}
		res, err = c.run(ctx, pkgPath, fn, gas, "test", strconv.FormatInt(need, 10)+"ugnot", args...)
	}
	return res, err
}

// MaxBlockGas is gnoland-1's block gas (consensus_params; the only per-transaction cap, F9).
const MaxBlockGas = 3_000_000_000

// GasFor is the gas a broadcast carries for a measured use: a fifth more and a million for gnokey's own
// difference between the simulation and the block (its storage deposit events among them), under the block's.
func GasFor(used int64) int64 {
	gas := used + used/5 + 1_000_000
	if gas > MaxBlockGas {
		gas = MaxBlockGas
	}
	return gas
}

func isOutOfGas(err error) bool {
	var ce *CallError
	return errors.As(err, &ce) && (strings.Contains(strings.ToLower(ce.Output), "out of gas") || strings.Contains(strings.ToLower(ce.Err.Error()), "out of gas"))
}

// GasPrice is a chain's gas price as auth/gasprice answers it: Price ugnot for Gas units (0.001 ugnot a unit on
// gnoland-1 at rest: 1ugnot/1000gas). It rises when a block uses more than 70% of its gas (F9).
type GasPrice struct {
	Gas    int64
	Amount int64
	Denom  string
}

// PerGas is the price per gas unit (0 for an empty answer).
func (p GasPrice) PerGas() float64 {
	if p.Gas <= 0 {
		return 0
	}
	return float64(p.Amount) / float64(p.Gas)
}

// ParseGasPrice reads auth/gasprice's answer in the forms the node gives: amino JSON ({"gas":"1000","price":
// {"denom":"ugnot","amount":"1"}}), the coin as a string ({"gas":1000,"price":"1ugnot"}), or the whole as a
// string ("1ugnot/1000gas").
func ParseGasPrice(b []byte) (GasPrice, error) {
	s := strings.TrimSpace(string(b))
	if s == "" || s == "null" {
		return GasPrice{}, nil
	}
	var whole string
	if json.Unmarshal(b, &whole) == nil {
		return parseGasPriceText(whole)
	}
	var j struct {
		Gas   json.RawMessage `json:"gas"`
		Price json.RawMessage `json:"price"`
	}
	if err := json.Unmarshal(b, &j); err != nil {
		return GasPrice{}, fmt.Errorf("gasprice: %w: %.100s", err, s)
	}
	p := GasPrice{}
	var err error
	if p.Gas, err = jsonInt(j.Gas); err != nil {
		return GasPrice{}, fmt.Errorf("gasprice: gas %s", j.Gas)
	}
	var coinText string
	if json.Unmarshal(j.Price, &coinText) == nil {
		if p.Amount, p.Denom, err = parseCoin(coinText); err != nil {
			return GasPrice{}, err
		}
		return p, nil
	}
	var coin struct {
		Denom  string          `json:"denom"`
		Amount json.RawMessage `json:"amount"`
	}
	if err := json.Unmarshal(j.Price, &coin); err != nil {
		return GasPrice{}, fmt.Errorf("gasprice: price %s", j.Price)
	}
	if p.Amount, err = jsonInt(coin.Amount); err != nil {
		return GasPrice{}, fmt.Errorf("gasprice: amount %s", coin.Amount)
	}
	p.Denom = coin.Denom
	return p, nil
}

func parseGasPriceText(s string) (GasPrice, error) {
	price, gas, ok := strings.Cut(s, "/")
	if !ok || !strings.HasSuffix(gas, "gas") {
		return GasPrice{}, fmt.Errorf("gasprice: %q is not price/gas", s)
	}
	p := GasPrice{}
	var err error
	if p.Amount, p.Denom, err = parseCoin(price); err != nil {
		return GasPrice{}, err
	}
	if p.Gas, err = strconv.ParseInt(strings.TrimSuffix(gas, "gas"), 10, 64); err != nil {
		return GasPrice{}, fmt.Errorf("gasprice: %q", s)
	}
	return p, nil
}

func parseCoin(s string) (int64, string, error) {
	i := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	if i == 0 || i == len(s) {
		return 0, "", fmt.Errorf("gasprice: %q is not a coin", s)
	}
	n, err := strconv.ParseInt(s[:i], 10, 64)
	return n, s[i:], err
}

func jsonInt(raw json.RawMessage) (int64, error) {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return strconv.ParseInt(s, 10, 64)
	}
	var n int64
	err := json.Unmarshal(raw, &n)
	return n, err
}

// BlockGasPrice is the chain's gas price now (auth/gasprice); an empty answer when the node has none.
func (c *Client) BlockGasPrice(ctx context.Context) (GasPrice, error) {
	b, err := c.Query(ctx, "auth/gasprice", nil, 0)
	if err != nil {
		return GasPrice{}, err
	}
	return ParseGasPrice(b)
}

// Simulate runs the call in gnokey's dry-run mode (-simulate only): nothing is broadcast; the result carries the
// simulation's gas and events.
func (c *Client) Simulate(ctx context.Context, pkgPath, fn string, gasWanted int64, args ...string) (*Result, error) {
	return c.run(ctx, pkgPath, fn, gasWanted, "only", "", args...)
}

// run is one gnokey call; fee overrides the computed one when given (the node's own minimum, F14).
func (c *Client) run(ctx context.Context, pkgPath, fn string, gasWanted int64, simulate, fee string, args ...string) (*Result, error) {
	cmdArgs := []string{"maketx", "call", "-pkgpath", pkgPath, "-func", fn}
	for _, a := range args {
		cmdArgs = append(cmdArgs, "-args", a)
	}
	if fee == "" {
		// the chain's price now (F9): a node without the query, or one that fails, leaves the client's own price
		chainPrice := 0.0
		if gp, err := c.BlockGasPrice(ctx); err == nil && (gp.Denom == "" || gp.Denom == "ugnot") {
			chainPrice = gp.PerGas()
		}
		fee = c.feeAt(gasWanted, chainPrice)
	}
	remote := strings.TrimPrefix(c.RPC, "http://") // gnokey wants host:port, or a full https:// URL
	cmdArgs = append(cmdArgs, "-gas-fee", fee, "-gas-wanted", fmt.Sprint(gasWanted),
		"-broadcast", "-simulate", simulate, "-chainid", c.ChainID, "-remote", remote, "-insecure-password-stdin")
	if c.Home != "" {
		cmdArgs = append(cmdArgs, "-home", c.Home)
	}
	cmdArgs = append(cmdArgs, c.Key)
	ctx, cancel := context.WithTimeout(ctx, GnokeyTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, c.Gnokey, cmdArgs...)
	cmd.Stdin = strings.NewReader("\n")
	out, err := cmd.CombinedOutput()
	if ctx.Err() != nil && err != nil {
		err = fmt.Errorf("%w: gnokey ran past %s (timed out)", err, GnokeyTimeout)
	}
	s := string(out)
	res := &Result{Output: s}
	if m := gasUsedRE.FindStringSubmatch(s); m != nil {
		res.GasUsed, _ = strconv.ParseInt(m[1], 10, 64)
	}
	if m := heightRE.FindStringSubmatch(s); m != nil {
		res.Height, _ = strconv.ParseInt(m[1], 10, 64)
	}
	if m := eventsRE.FindStringSubmatch(s); m != nil {
		res.Events = parseEvents(m[1])
	}
	if simulate == "only" {
		if err != nil || !strings.Contains(s, "OK!") {
			if err == nil {
				err = errors.New("no OK! from gnokey's simulation")
			}
			return nil, &CallError{Pkg: pkgPath, Fn: fn, Err: err, Output: s}
		}
		return res, nil
	}
	m := txHashRE.FindStringSubmatch(s)
	if err != nil || !strings.Contains(s, "OK!") || m == nil || m[1] == "" {
		if err == nil {
			err = errors.New("no OK! from gnokey")
		}
		return nil, &CallError{Pkg: pkgPath, Fn: fn, Err: err, Output: s}
	}
	res.Hash = m[1]
	return res, nil
}

var keyLineRE = regexp.MustCompile(`(?m)^\d+\.\s+(\S+)\s+\(\w+\)\s+-\s+addr:\s+(g1\w+)`)

// KeyAddress is the address of the client's key in its keybase, from gnokey list.
func (c *Client) KeyAddress(ctx context.Context) (string, error) {
	args := []string{"list"}
	if c.Home != "" {
		args = append(args, "-home", c.Home)
	}
	ctx, cancel := context.WithTimeout(ctx, RPCTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, c.Gnokey, args...).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("gnokey list: %v: %s", err, lastLines(string(out), 4))
	}
	for _, m := range keyLineRE.FindAllStringSubmatch(string(out), -1) {
		if m[1] == c.Key {
			return m[2], nil
		}
	}
	return "", fmt.Errorf("gnokey list: no key %q in %s", c.Key, c.Home)
}

func lastLines(s string, n int) string {
	ls := strings.Split(strings.TrimSpace(s), "\n")
	if len(ls) > n {
		ls = ls[len(ls)-n:]
	}
	return strings.Join(ls, "\n")
}
