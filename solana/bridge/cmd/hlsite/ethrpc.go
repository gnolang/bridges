package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/gnolang/bridges/solana/bridge/agent"
	"github.com/gnolang/bridges/solana/bridge/hl"
)

// Ethereum over plain JSON-RPC (bridge/SITE.md, E-M4): the few calls and events the site reads, encoded and decoded
// here by hand — go-ethereum is no dependency of this module, the live Solana tools' — with keccak-256 from
// golang.org/x/crypto/sha3 through bridge/hl. Every selector and topic below is pinned in the tests against
// Foundry's `cast sig` / `cast keccak`, and every decoder against `cast abi-encode` output.

// ethCaller is one Ethereum provider: a JSON-RPC method with its params, its result decoded into out (a
// *json.RawMessage keeps a null). The mock deployment supplies one in process.
type ethCaller interface {
	Call(ctx context.Context, method string, params []any, out any) error
}

// ethHTTP is a provider over HTTP POST, JSON-RPC 2.0. No error of its carries the URL or a part of it that may be a
// key (most providers put an API key in the path or the query): a transport error names the operation alone, and
// whatever the provider answers is scrubbed of the URL's long parts before it is shown, then redacted as the
// Solana side's errors are (agent.Redact).
type ethHTTP struct {
	url     string
	secrets []string
	c       *http.Client
	id      atomic.Uint64
}

func newEthHTTP(raw string) *ethHTTP {
	p := &ethHTTP{url: raw, c: &http.Client{Timeout: 20 * time.Second}}
	if u, err := url.Parse(raw); err == nil {
		if u.User != nil {
			p.secrets = append(p.secrets, u.User.String())
		}
		for _, seg := range strings.Split(u.Path, "/") {
			if len(seg) >= 12 {
				p.secrets = append(p.secrets, seg)
			}
		}
		for _, vs := range u.Query() {
			for _, v := range vs {
				if len(v) >= 12 {
					p.secrets = append(p.secrets, v)
				}
			}
		}
	}
	return p
}

// scrub takes the URL's long parts and every URL out of a text the provider or the transport produced.
func (p *ethHTTP) scrub(s string) string {
	for _, k := range p.secrets {
		s = strings.ReplaceAll(s, k, "…")
	}
	return agent.Redact(s)
}

// rpcError is a JSON-RPC error object.
type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *rpcError) Error() string {
	return fmt.Sprintf("the provider refused (%d): %s", e.Code, e.Message)
}

func (p *ethHTTP) Call(ctx context.Context, method string, params []any, out any) error {
	if params == nil {
		params = []any{}
	}
	body, err := json.Marshal(struct {
		JSONRPC string `json:"jsonrpc"`
		ID      uint64 `json:"id"`
		Method  string `json:"method"`
		Params  []any  `json:"params"`
	}{"2.0", p.id.Add(1), method, params})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, "POST", p.url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("%s: the provider's URL does not parse", method)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := p.c.Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err // the operation's own error, without the URL
		}
		return fmt.Errorf("%s: the provider did not answer: %s", method, p.scrub(err.Error()))
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return fmt.Errorf("%s: reading the provider's answer: %s", method, p.scrub(err.Error()))
	}
	var r struct {
		Result json.RawMessage `json:"result"`
		Error  *rpcError       `json:"error"`
	}
	if resp.StatusCode != http.StatusOK {
		// a JSON-RPC error carried by a non-200 is the provider's answer, kept (its message scrubbed, as a 200's): drpc
		// refuses an eth_getLogs past its range with HTTP 400 and {"error":{"code":35,"message":"ranges over 10000
		// blocks are not supported on free plan"}}, which rangeRefused must see to halve the window, as ethx does
		// (E4-AS3); any other body may echo a key and is not shown
		if json.Unmarshal(b, &r) == nil && r.Error != nil {
			r.Error.Message = p.scrub(r.Error.Message)
			return fmt.Errorf("%s: the provider answered HTTP %d: %w", method, resp.StatusCode, r.Error)
		}
		return fmt.Errorf("%s: the provider answered HTTP %d", method, resp.StatusCode)
	}
	if err := json.Unmarshal(b, &r); err != nil {
		return fmt.Errorf("%s: the provider's answer is not JSON-RPC", method)
	}
	if r.Error != nil {
		r.Error.Message = p.scrub(r.Error.Message)
		return fmt.Errorf("%s: %w", method, r.Error)
	}
	if len(r.Result) == 0 {
		return fmt.Errorf("%s: the provider answered no result", method)
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(r.Result, out); err != nil {
		return fmt.Errorf("%s: the provider's result does not read: %v", method, err)
	}
	return nil
}

// rangeRefused says whether an eth_getLogs failure is the provider's limit on a range or on a result's size (the
// window is then halved), rather than the provider failing.
func rangeRefused(err error) bool {
	var re *rpcError
	if !errors.As(err, &re) {
		return false
	}
	m := strings.ToLower(re.Message)
	for _, k := range []string{"range", "more than", "too many", "exceed", "too large", "limit", "response size", "too big"} {
		if strings.Contains(m, k) {
			return true
		}
	}
	return false
}

// unavailable is a failure of the provider (not of the request): the builder answers 503 for it.
type unavailable struct{ err error }

func (u unavailable) Error() string { return u.err.Error() }
func (u unavailable) Unwrap() error { return u.err }

// ------------------------------------------------------------------ hex --

func qty(n uint64) string { return "0x" + strconv.FormatUint(n, 16) }

// parseQty reads a JSON-RPC quantity ("0x1a"), at most 64 bits.
func parseQty(s string) (uint64, error) {
	if !strings.HasPrefix(s, "0x") || len(s) < 3 || len(s) > 18 {
		return 0, fmt.Errorf("%q is not a quantity", s)
	}
	return strconv.ParseUint(s[2:], 16, 64)
}

// parseData reads JSON-RPC data ("0x…", an even count of hex digits).
func parseData(s string) ([]byte, error) {
	if !strings.HasPrefix(s, "0x") {
		return nil, fmt.Errorf("%.20q is not 0x data", s)
	}
	return hex.DecodeString(s[2:])
}

func parseHash(s string) ([32]byte, error) {
	var h [32]byte
	b, err := parseData(s)
	if err != nil || len(b) != 32 {
		return h, fmt.Errorf("%.70q is not a 32-byte hash", s)
	}
	copy(h[:], b)
	return h, nil
}

func hex0x(b []byte) string { return "0x" + hex.EncodeToString(b) }

// ------------------------------------------------------------- addresses --

// ethAddr is an Ethereum address.
type ethAddr [20]byte

// String is the address in its EIP-55 checksum case, as wallets and explorers show it.
func (a ethAddr) String() string { return eip55(a[:]) }

// word is the address as Hyperlane carries it: left-padded with twelve zero bytes.
func (a ethAddr) word() [32]byte {
	var w [32]byte
	copy(w[12:], a[:])
	return w
}

// eip55 is twenty bytes as "0x" and 40 hex digits in EIP-55 case: a letter is upper case where the matching nibble
// of keccak-256 of the lower-case hex (as ASCII) is 8 or more.
func eip55(a []byte) string {
	lower := hex.EncodeToString(a)
	h := hl.Keccak([]byte(lower))
	out := []byte(lower)
	for i, c := range out {
		if c < 'a' || c > 'f' {
			continue
		}
		nib := h[i/2] >> 4
		if i%2 == 1 {
			nib = h[i/2] & 0x0f
		}
		if nib >= 8 {
			out[i] = c - 'a' + 'A'
		}
	}
	return "0x" + string(out)
}

// parseEthAddress reads an Ethereum address a person typed — the config's contracts — as "0x" and 40 hex digits in
// its EIP-55 case exactly (eth/bridge/PLAN.md §5.2: "the site and the tools accept only EIP-55 checksummed input";
// round 3's E3-CF6): an address in one case carries no checksum, so a typo in it would pass unseen; it is refused
// unless its EIP-55 form is itself one case (no letter, or every letter's hash nibble on one side), and the refusal
// names no checksummed form — that form would be the typo's checksum too.
func parseEthAddress(s string) (ethAddr, error) {
	a, err := parseEthWire(s)
	if err != nil {
		return ethAddr{}, err
	}
	if a.String() != strings.TrimSpace(s) {
		return ethAddr{}, errors.New("all in one case: it carries no EIP-55 checksum, so a typo in it would go unseen (write it in its checksummed, mixed case)")
	}
	return a, nil
}

// parseEthWire reads an Ethereum address a machine gave — the account a wallet answers (wallets often answer in lower
// case), a provider's JSON — as "0x" and 40 hex digits: all lower case, all upper case, or mixed case that must be the
// address's EIP-55 checksum exactly (a typo the checksum caught is refused).
func parseEthWire(s string) (ethAddr, error) {
	var a ethAddr
	s = strings.TrimSpace(s)
	if len(s) != 42 || s[:2] != "0x" {
		return a, errors.New("not 0x and 40 hex digits")
	}
	b, err := hex.DecodeString(s[2:])
	if err != nil {
		return a, errors.New("not 0x and 40 hex digits")
	}
	copy(a[:], b)
	h := s[2:]
	if strings.ContainsAny(h, "abcdef") && strings.ContainsAny(h, "ABCDEF") && a.String()[2:] != h {
		return a, errors.New("mixed case that is not its EIP-55 checksum (a typo?)")
	}
	return a, nil
}

// addrOf32 reads 32 bytes as an address: the top twelve must be zero.
func addrOf32(w [32]byte) (ethAddr, bool) {
	var a ethAddr
	for _, c := range w[:12] {
		if c != 0 {
			return a, false
		}
	}
	copy(a[:], w[12:])
	return a, true
}

// ------------------------------------------------------------------ ABI --

// selector is a function's four bytes: keccak-256 of its signature.
func selector(sig string) [4]byte {
	h := hl.Keccak([]byte(sig))
	return [4]byte(h[:4])
}

// eventTopic is an event's topic 0: keccak-256 of its signature.
func eventTopic(sig string) [32]byte { return hl.Keccak([]byte(sig)) }

var (
	selTotalSupply   = selector("totalSupply()")
	selBalanceOf     = selector("balanceOf(address)")
	selQuoteTransfer = selector("quoteTransferRemote(uint32,bytes32,uint256)")
	selTransfer      = selector("transferRemote(uint32,bytes32,uint256)")
	selNonce         = selector("nonce()")
	selProcessedAt   = selector("processedAt(bytes32)")
	selKeys          = selector("keys()")
	selFlags         = selector("flags()")
	selHold          = selector("hold()")
	selLimits        = selector("limits(uint8)")
	selToken         = selector("token(uint8)")
	selClockFactor   = selector("clockFactor()")
	selTrustedHeight = selector("trustedHeight()")
	selTrustedTime   = selector("trustedTime()")
	selTrustedHash   = selector("trustedHash()")
	selQuarantineEnd = selector("quarantineEnd()")
	selEntry         = selector("entry(bytes32)")
	selLastSeq       = selector("lastSeq()")
	selPending       = selector("pending()")
	selDecision      = selector("decision(uint64)")
	selSetupDone     = selector("setupDone()")
	selProcess       = selector("process(bytes,bytes)")
	selUpdate        = selector("update(bytes[],(uint32,bytes32,int64,(uint16,int64,int32,bytes32,bytes32)[]),(bytes32,int64)[],(bytes32,int64)[])")

	topicDispatch       = eventTopic("Dispatch(address,uint32,bytes32,bytes)")
	topicProcess        = eventTopic("Process(uint32,bytes32,address)")
	topicProcessID      = eventTopic("ProcessId(bytes32)")
	topicHeaderRecorded = eventTopic("HeaderRecorded(bytes32,int64,uint64,uint64,bytes32,bool)")
	topicReceived       = eventTopic("ReceivedTransferRemote(uint32,bytes32,uint256)")
)

// u64Word is a value as one ABI word.
func u64Word(v uint64) [32]byte {
	var w [32]byte
	binary.BigEndian.PutUint64(w[24:], v)
	return w
}

// calldata is a selector followed by static words.
func calldata(sel [4]byte, words ...[32]byte) []byte {
	b := append([]byte{}, sel[:]...)
	for _, w := range words {
		b = append(b, w[:]...)
	}
	return b
}

// wordAt is the 32 bytes at an offset.
func wordAt(b []byte, off uint64) ([32]byte, error) {
	var w [32]byte
	if off > uint64(len(b)) || uint64(len(b))-off < 32 {
		return w, fmt.Errorf("abi: a word at %d past %d bytes", off, len(b))
	}
	copy(w[:], b[off:off+32])
	return w, nil
}

// u64Of reads a word as an unsigned value of at most 64 bits.
func u64Of(w [32]byte) (uint64, error) {
	for _, c := range w[:24] {
		if c != 0 {
			return 0, errors.New("abi: a value past 64 bits")
		}
	}
	return binary.BigEndian.Uint64(w[24:]), nil
}

// i64Of reads a word as a signed value of at most 64 bits (two's complement, sign-extended).
func i64Of(w [32]byte) (int64, error) {
	fill := byte(0)
	if w[24]&0x80 != 0 {
		fill = 0xff
	}
	for _, c := range w[:24] {
		if c != fill {
			return 0, errors.New("abi: a signed value past 64 bits")
		}
	}
	return int64(binary.BigEndian.Uint64(w[24:])), nil
}

func addrWord(w [32]byte) (ethAddr, error) {
	a, ok := addrOf32(w)
	if !ok {
		return a, errors.New("abi: an address word with its top twelve bytes set")
	}
	return a, nil
}

func boolWord(w [32]byte) (bool, error) {
	v, err := u64Of(w)
	if err != nil || v > 1 {
		return false, errors.New("abi: a bool that is neither 0 nor 1")
	}
	return v == 1, nil
}

func bigWord(w [32]byte) *big.Int { return new(big.Int).SetBytes(w[:]) }

// dynAt is a dynamic value's place: the offset in the word at head, from base (the enclosing tuple's start); its
// length is the word there, its bytes follow.
func dynAt(b []byte, base, head uint64) (start, n uint64, err error) {
	w, err := wordAt(b, base+head)
	if err != nil {
		return 0, 0, err
	}
	off, err := u64Of(w)
	if err != nil || off > uint64(len(b)) {
		return 0, 0, errors.New("abi: an offset out of range")
	}
	lw, err := wordAt(b, base+off)
	if err != nil {
		return 0, 0, err
	}
	n, err = u64Of(lw)
	if err != nil || n > uint64(len(b)) {
		return 0, 0, errors.New("abi: a length out of range")
	}
	return base + off + 32, n, nil
}

// bytesAt is the dynamic bytes whose offset is in the word at head, from base.
func bytesAt(b []byte, base, head uint64) ([]byte, error) {
	start, n, err := dynAt(b, base, head)
	if err != nil {
		return nil, err
	}
	if start > uint64(len(b)) || uint64(len(b))-start < n {
		return nil, errors.New("abi: bytes past the data")
	}
	return b[start : start+n], nil
}

// encBytes is a bytes value's tail: its length, then its bytes padded to a word.
func encBytes(v []byte) []byte {
	l := u64Word(uint64(len(v)))
	out := append(l[:], v...)
	if r := len(v) % 32; r != 0 {
		out = append(out, make([]byte, 32-r)...)
	}
	return out
}

// ------------------------------------------------------------- the reads --

// ethBlock is a block as the site keeps it: its number, hash and time.
type ethBlock struct {
	Number uint64
	Hash   [32]byte
	Time   int64
}

type ethBlockJSON struct {
	Number    string `json:"number"`
	Hash      string `json:"hash"`
	Timestamp string `json:"timestamp"`
}

func (j ethBlockJSON) parse() (ethBlock, error) {
	var b ethBlock
	var err error
	if b.Number, err = parseQty(j.Number); err != nil {
		return b, err
	}
	if b.Hash, err = parseHash(j.Hash); err != nil {
		return b, err
	}
	t, err := parseQty(j.Timestamp)
	if err != nil || t > 1<<62 {
		return b, fmt.Errorf("a block time %q", j.Timestamp)
	}
	b.Time = int64(t)
	return b, nil
}

// ethLog is one log as eth_getLogs or a receipt answers it.
type ethLog struct {
	Address   ethAddr
	Topics    [][32]byte
	Data      []byte
	Block     uint64
	BlockHash [32]byte
	Tx        string // the transaction's hash, 0x and lower-case hex
	Index     uint64
	Removed   bool
}

type ethLogJSON struct {
	Address     string   `json:"address"`
	Topics      []string `json:"topics"`
	Data        string   `json:"data"`
	BlockNumber string   `json:"blockNumber"`
	BlockHash   string   `json:"blockHash"`
	TxHash      string   `json:"transactionHash"`
	LogIndex    string   `json:"logIndex"`
	Removed     bool     `json:"removed"`
}

func (j ethLogJSON) parse() (ethLog, error) {
	var l ethLog
	ab, err := parseData(j.Address)
	if err != nil || len(ab) != 20 {
		return l, fmt.Errorf("a log's address %.50q", j.Address)
	}
	copy(l.Address[:], ab)
	for _, t := range j.Topics {
		h, err := parseHash(t)
		if err != nil {
			return l, err
		}
		l.Topics = append(l.Topics, h)
	}
	if l.Data, err = parseData(j.Data); err != nil {
		return l, err
	}
	if l.Block, err = parseQty(j.BlockNumber); err != nil {
		return l, fmt.Errorf("a log's block: %w", err)
	}
	if l.BlockHash, err = parseHash(j.BlockHash); err != nil {
		return l, err
	}
	th, err := parseHash(j.TxHash)
	if err != nil {
		return l, err
	}
	l.Tx = hex0x(th[:])
	l.Index, _ = parseQty(j.LogIndex)
	l.Removed = j.Removed
	return l, nil
}

type ethReceiptJSON struct {
	Status      string       `json:"status"`
	BlockNumber string       `json:"blockNumber"`
	BlockHash   string       `json:"blockHash"`
	Logs        []ethLogJSON `json:"logs"`
}

type ethTxJSON struct {
	Hash        string `json:"hash"`
	To          string `json:"to"`
	Input       string `json:"input"`
	BlockNumber string `json:"blockNumber"`
}

// isNull is whether a raw result is JSON null (a block, a receipt or a transaction the provider does not hold).
func isNull(r json.RawMessage) bool { return len(r) == 0 || string(bytes.TrimSpace(r)) == "null" }
