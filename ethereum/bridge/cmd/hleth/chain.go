package main

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/rpc"

	"github.com/gnolang/bridges/ethereum/bridge/ethx"
)

// One provider, as the tool reads it and sends through it. Its URL may carry the provider's key: it is never
// printed (ethx.Redact on every error), and the audit prints every request it makes as a curl line against $RPC,
// so a member repeats each by hand with their own provider.

type conn struct {
	raw     *rpc.Client
	c       *ethx.Client
	chainID *big.Int
}

// printingTransport prints every JSON-RPC request before it is sent, one curl line per request (a batch's
// elements one by one: each is a request of its own when sent alone).
type printingTransport struct{ w io.Writer }

func (t printingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Body != nil && t.w != nil {
		b, _ := io.ReadAll(req.Body)
		req.Body = io.NopCloser(bytes.NewReader(b))
		for _, line := range curlLines(b) {
			fmt.Fprintln(t.w, line)
		}
	}
	return http.DefaultTransport.RoundTrip(req)
}

// curlLines are a request body's calls as curl lines against $RPC (never the provider's URL, G25: a paid
// provider's key rides in it, and an audit log is posted to show its PASS).
func curlLines(body []byte) []string {
	var one map[string]any
	var many []map[string]any
	switch {
	case json.Unmarshal(body, &one) == nil:
		many = []map[string]any{one}
	case json.Unmarshal(body, &many) == nil:
	default:
		return nil
	}
	var out []string
	for _, m := range many {
		params, _ := json.Marshal(m["params"])
		out = append(out, fmt.Sprintf("  rpc> curl -s \"$RPC\" -H 'content-type: application/json' -d '{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":%q,\"params\":%s}'", fmt.Sprint(m["method"]), params))
	}
	return out
}

// dial connects to one provider; with print set, every request is printed as a curl line first.
func dial(ctx context.Context, url string, print io.Writer) (*conn, error) {
	if url == "" {
		return nil, errors.New("-rpc is required: an Ethereum provider")
	}
	hc := &http.Client{Timeout: ethx.RPCTimeout}
	if print != nil {
		hc.Transport = printingTransport{w: print}
	}
	raw, err := rpc.DialOptions(ctx, url, rpc.WithHTTPClient(hc))
	if err != nil {
		return nil, fmt.Errorf("the provider %s: %s", ethx.Redact(url), ethx.Redact(err.Error()))
	}
	c := ethx.NewClient(ethx.Provider{Name: "rpc", URL: url}, raw)
	id, err := c.ChainID(ctx)
	if err != nil {
		return nil, err
	}
	return &conn{raw: raw, c: c, chainID: id}, nil
}

// view calls a view at a state and answers its raw return data.
func (cn *conn) view(ctx context.Context, at ethx.At, to common.Address, a *Artifact, method string, args ...any) ([]byte, error) {
	data, err := a.ABI.Pack(method, args...)
	if err != nil {
		return nil, fmt.Errorf("%s.%s: %w", a.Name, method, err)
	}
	return cn.c.CallAt(ctx, to, data, at)
}

// read decodes a view's single answer into T.
func read[T any](ctx context.Context, cn *conn, at ethx.At, to common.Address, a *Artifact, method string, args ...any) (T, error) {
	var zero T
	out, err := cn.view(ctx, at, to, a, method, args...)
	if err != nil {
		return zero, fmt.Errorf("%s.%s at %s: %w", a.Name, method, to.Hex(), err)
	}
	return unpackOne[T](a, method, out)
}

// code is an address's runtime code at a state.
func (cn *conn) code(ctx context.Context, at ethx.At, a common.Address) ([]byte, error) {
	return cn.c.CodeAt(ctx, a, at)
}

// ----------------------------------------------------------------------------------------- the sending --

// txer sends the setup's transactions from the deployer key through ethx.Sender — the machines' rules: the estimate
// plus 20% inside EIP-7825's cap, the fee cap twice the base fee plus the tip, no send above the maximum base fee,
// 12.5% replacements, the receipt awaited and a mined revert read — the contract creations too (a transaction with
// no "to", ethx.Call.Create; never forge create or cast). A creation's nonce and the hash of each version are written
// into the deployment's record before the version is broadcast (ethx.Call.BeforeBroadcast), so a stop between the
// broadcast and the receipt is resumed by the next run (resumeCreate), never sent twice at two addresses; the
// sender's own state file (<out>.sender.json) keeps the transaction in flight.
type txer struct {
	cn     *conn
	key    *ecdsa.PrivateKey
	from   common.Address
	sender *ethx.Sender
	sent   int // transactions mined by this run
	say    func(format string, a ...any)
}

func newTxer(cn *conn, key *ecdsa.PrivateKey, cfg ethx.SendConfig, say func(string, ...any)) (*txer, error) {
	s, err := ethx.NewSender(key, cn.chainID, []*ethx.Client{cn.c}, cfg)
	if err != nil {
		return nil, err
	}
	return &txer{cn: cn, key: key, from: ethx.KeyAddress(key), sender: s, say: say}, nil
}

// quiet refuses to send while the deployer key has transactions pending that this run did not send: the deployer
// key sends nothing else during the setup (the sender would replace one at its nonce).
func (t *txer) quiet(ctx context.Context, label string) error {
	if t.sender.InFlight() != "" {
		return nil
	}
	mined, err := t.cn.c.NonceAt(ctx, t.from, ethx.Latest)
	if err != nil {
		return err
	}
	if pending, err := t.cn.c.NonceAt(ctx, t.from, ethx.Pending); err == nil && pending > mined {
		return fmt.Errorf("%s: the deployer has %d transaction(s) pending that this run did not send: the deployer key sends nothing else during the setup; run it again once they are mined", label, pending-mined)
	}
	return nil
}

// call sends one call and waits for it.
func (t *txer) call(ctx context.Context, label string, to common.Address, data []byte, value *big.Int) (*types.Receipt, error) {
	if err := t.quiet(ctx, label); err != nil {
		return nil, err
	}
	rc, err := t.sender.Send(ctx, ethx.Call{To: to, Data: data, Value: value, Label: label})
	if err != nil {
		return rc, fmt.Errorf("%s: %w", label, err)
	}
	t.sent++
	return rc, nil
}

// errPlan is a transaction the nonce plan would not have sent: the deployer's nonce is not the one the plan gives it.
var errPlan = errors.New("the nonce plan is broken")

// atNonce refuses a version signed at another nonce than the plan's (ethx.Call.BeforeBroadcast): nothing is broadcast.
func atNonce(label string, planned uint64) func(uint64, common.Hash) error {
	return func(n uint64, _ common.Hash) error {
		if n != planned {
			return fmt.Errorf("%w: %s goes at the deployer's nonce %d, and would be sent at %d", errPlan, label, planned, n)
		}
		return nil
	}
}

// callAt sends one call at exactly the planned nonce (the hand-over of the ProxyAdmin, step 1): simulated first by the
// sender's estimate (a call that would revert is refused, its nonce untouched), refused before broadcasting at any
// other nonce.
func (t *txer) callAt(ctx context.Context, label string, to common.Address, data []byte, planned uint64) (*types.Receipt, error) {
	if err := t.quiet(ctx, label); err != nil {
		return nil, err
	}
	rc, err := t.sender.Send(ctx, ethx.Call{To: to, Data: data, Label: label, BeforeBroadcast: atNonce(label, planned)})
	if err != nil {
		return rc, fmt.Errorf("%s: %w", label, err)
	}
	t.sent++
	return rc, nil
}

// create deploys one contract through the sender (a creation: no "to") at exactly the planned nonce: simulated first
// by the sender's estimate (a constructor that would revert is refused, its nonce untouched), refused before
// broadcasting at any other nonce. Before each version is broadcast the deployment's record names the creation — its
// field, its nonce, the address CREATE gives it, the version's hash — so a run stopped before the receipt is resumed
// by the next (resumeCreate). It answers the contract's address and its receipt.
func (t *txer) create(ctx context.Context, d *Deployment, save func() error, field, label string, data []byte, planned uint64) (common.Address, *types.Receipt, error) {
	if err := t.quiet(ctx, label); err != nil {
		return common.Address{}, nil, err
	}
	onPlan := atNonce(label, planned)
	record := func(nonce uint64, h common.Hash) error {
		if err := onPlan(nonce, h); err != nil {
			return err
		}
		p := d.Pending
		if p == nil || p.Field != field || p.Nonce != nonce {
			p = &PendingCreate{Field: field, Label: label, Nonce: nonce, Address: crypto.CreateAddress(t.from, nonce).Hex(), FirstAt: time.Now().Unix()}
		}
		p.Hashes = append(p.Hashes, h)
		d.Pending = p
		return save()
	}
	rc, err := t.sender.Send(ctx, ethx.Call{Create: true, Data: data, Label: label, BeforeBroadcast: record})
	if err != nil {
		if rv, ok := ethx.AsRevert(err); ok && rv.Mined {
			d.Pending = nil
			_ = save()
			return common.Address{}, rc, fmt.Errorf("%s was mined and reverted: its constructor refused (%w)", label, err)
		}
		if errors.Is(err, ethx.ErrStillPending) {
			return common.Address{}, nil, fmt.Errorf("%s: %w — run the setup again: it resumes the creation at its nonce", label, err)
		}
		if d.Pending != nil && d.Pending.Field == field && len(d.Pending.Hashes) > 0 {
			return common.Address{}, nil, fmt.Errorf("%s: %w (its record stays: the next run resumes it)", label, err)
		}
		d.Pending = nil
		_ = save()
		return common.Address{}, nil, fmt.Errorf("%s: %w", label, err)
	}
	return t.adopt(d, save, rc)
}

// adopt records a mined creation the record names: its address, its step, the deployment's first block.
func (t *txer) adopt(d *Deployment, save func() error, rc *types.Receipt) (common.Address, *types.Receipt, error) {
	p := d.Pending
	if p == nil {
		return common.Address{}, rc, errors.New("a creation was mined that the record does not name")
	}
	addr := common.HexToAddress(p.Address)
	if rc.ContractAddress != (common.Address{}) && rc.ContractAddress != addr {
		d.Pending = nil
		_ = save()
		return common.Address{}, rc, fmt.Errorf("%s landed at %s, not the %s its nonce gives", p.Label, rc.ContractAddress.Hex(), addr.Hex())
	}
	d.Pending = nil
	*d.field(p.Field) = addr.Hex()
	d.stepDone(p.Field, rc.TxHash.Hex(), rc.BlockNumber.Uint64())
	if p.Field == "proxyAdmin" || d.FirstBlock == 0 {
		d.FirstBlock = rc.BlockNumber.Uint64()
	}
	t.sent++
	return addr, rc, save()
}

// resumeCreate settles a creation a stopped run left in the record: the transaction the sender kept in flight
// finished (or still pending: run again); mined at the recorded address → adopted; its nonce taken by another
// transaction, or never broadcast → forgotten (the step sends it again).
func (t *txer) resumeCreate(ctx context.Context, d *Deployment, save func() error) error {
	p := d.Pending
	if p == nil {
		// a call (not a creation) a stopped run left in flight: settled before anything else is planned
		if _, err := t.sender.Resume(ctx); err != nil && !isMinedRevert(err) {
			return err
		}
		return nil
	}
	t.say("resuming %s, broadcast by an earlier run at nonce %d (to land at %s)", p.Label, p.Nonce, p.Address)
	rc, err := t.sender.Resume(ctx)
	switch {
	case errors.Is(err, ethx.ErrStillPending):
		return fmt.Errorf("%s: %w — run the setup again once it is mined", p.Label, err)
	case err != nil && !isMinedRevert(err):
		return err
	}
	addr := common.HexToAddress(p.Address)
	if rc == nil || rc.ContractAddress != addr {
		rc = nil
		for i := len(p.Hashes) - 1; i >= 0 && rc == nil; i-- {
			r, err := t.cn.c.Receipt(ctx, p.Hashes[i])
			if err == nil {
				rc = r
			} else if !ethx.IsNotFound(err) {
				return err
			}
		}
	}
	if rc != nil {
		if rc.Status != types.ReceiptStatusSuccessful {
			d.Pending = nil
			_ = save()
			return fmt.Errorf("%s was mined and reverted (tx %s, block %v): its constructor refused", p.Label, rc.TxHash.Hex(), rc.BlockNumber)
		}
		_, _, err := t.adopt(d, save, rc)
		if err == nil {
			t.say("%s: adopted at %s (tx %s, block %d)", p.Label, addr.Hex(), rc.TxHash.Hex(), rc.BlockNumber.Uint64())
		}
		return err
	}
	if code, err := t.cn.code(ctx, ethx.Latest, addr); err != nil {
		return err
	} else if len(code) > 0 {
		return fmt.Errorf("%s: code is at %s but no receipt of its recorded transactions is served yet: run the setup again", p.Label, addr.Hex())
	}
	mined, err := t.cn.c.NonceAt(ctx, t.from, ethx.Latest)
	if err != nil {
		return err
	}
	if pending, err := t.cn.c.NonceAt(ctx, t.from, ethx.Pending); err == nil && mined <= p.Nonce && pending > p.Nonce {
		return fmt.Errorf("%s: a transaction of the deployer is pending at its nonce %d (an earlier run's): run the setup again once it is mined", p.Label, p.Nonce)
	}
	t.say("%s: never mined (its nonce %d taken by another transaction, or the pool dropped it): the step sends it again", p.Label, p.Nonce)
	d.Pending = nil
	return save()
}

func isMinedRevert(err error) bool {
	r, ok := ethx.AsRevert(err)
	return ok && r.Mined
}
