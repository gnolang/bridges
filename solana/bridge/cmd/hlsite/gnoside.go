package main

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"
	"strings"

	"github.com/gnolang/bridges/solana/bridge/agent"
	"github.com/gnolang/bridges/solana/bridge/gno"
)

// gnoNode is the read side of gno.land the site needs: the realms' renders at a height (agent's reader), a
// transaction's events (a send's message id from the wallet's transaction hash) and the gas price. A fake
// supplies it in tests and under -mock.
type gnoNode interface {
	agent.GnoReader
	TxEvents(ctx context.Context, hash string) (*gnoTx, error)
	GasPrice(ctx context.Context) (gno.GasPrice, error)
}

// gnoTx is a transaction as the node answers `tx`: its height, its events, and why it failed if it did.
type gnoTx struct {
	Height int64
	Failed string
	Events []gnoEvent
}

type gnoEvent struct {
	Type    string
	PkgPath string
	Attrs   map[string]string
}

// Attr is an attribute of the first event of a type.
func (t *gnoTx) Attr(eventType, key string) (string, bool) {
	for _, e := range t.Events {
		if e.Type == eventType {
			v, ok := e.Attrs[key]
			return v, ok
		}
	}
	return "", false
}

// rpcGno is a gno node over its JSON-RPC: the agent's client for the renders, and the `tx` call here.
type rpcGno struct {
	*gno.Client
}

func newGno(rpc, chainID string) rpcGno {
	return rpcGno{&gno.Client{RPC: rpc, ChainID: chainID}}
}

func (g rpcGno) GasPrice(ctx context.Context) (gno.GasPrice, error) { return g.BlockGasPrice(ctx) }

// TxEvents reads a committed transaction by its hash (base64, as the wallet returns it), through the client's
// request, so a node's rate limit holds this read back as it holds the renders.
func (g rpcGno) TxEvents(ctx context.Context, hash string) (*gnoTx, error) {
	var res struct {
		Height   string `json:"height"`
		TxResult struct {
			ResponseBase struct {
				Error  json.RawMessage   `json:"Error"`
				Events []json.RawMessage `json:"Events"`
				Log    string            `json:"Log"`
			} `json:"ResponseBase"`
		} `json:"tx_result"`
	}
	if err := g.Request(ctx, "tx", map[string]any{"hash": hash}, &res); err != nil {
		return nil, err
	}
	t := &gnoTx{}
	t.Height, _ = strconv.ParseInt(res.Height, 10, 64)
	rb := res.TxResult.ResponseBase
	if len(rb.Error) > 0 && string(rb.Error) != "null" {
		t.Failed = strings.TrimSpace(rb.Log)
		if t.Failed == "" {
			t.Failed = string(rb.Error)
		}
	}
	for _, raw := range rb.Events {
		t.Events = append(t.Events, parseEvent(raw))
	}
	return t, nil
}

// parseEvent reads one event loosely (as the agent's reader does): an object with a "type", a "pkg_path" and a
// list of {key, value} under any field.
func parseEvent(raw json.RawMessage) gnoEvent {
	e := gnoEvent{Attrs: map[string]string{}}
	var m map[string]any
	if json.Unmarshal(raw, &m) != nil {
		return e
	}
	e.Type, _ = m["type"].(string)
	e.PkgPath, _ = m["pkg_path"].(string)
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
	return e
}

// deliveredAt is the height hlmailbox credited a message id at (0 if never).
func deliveredAt(ctx context.Context, g gnoNode, mailbox, id string, height int64) (int64, error) {
	s, err := g.RenderAt(ctx, mailbox, "delivered/"+id, height)
	if err != nil {
		return 0, err
	}
	return strconv.ParseInt(strings.TrimSpace(s), 10, 64)
}

// agentConfig is the agent package's config for this chain's realms, so its readers (ReadPending, ReadRoute)
// serve the site.
func agentConfig(c *Config, ch *ChainConfig) *agent.Config {
	var ac agent.Config
	ac.Gno.RPC, ac.Gno.ChainID, ac.Gno.Mailbox, ac.Gno.Fees, ac.Gno.Gov = c.Gno.RPC, c.Gno.ChainID, c.Gno.Mailbox, c.Gno.Fees, c.Gno.Gov
	for _, t := range ch.Tokens {
		ac.Routes = append(ac.Routes, agent.Route{Symbol: t.Symbol, Gno: t.Realm, Token: t.Program})
	}
	return &ac
}

// messageID reads a tracking reference as a message id: 64 hex digits, with or without 0x.
func messageID(ref string) ([32]byte, bool) {
	var id [32]byte
	s := strings.ToLower(strings.TrimPrefix(strings.TrimPrefix(ref, "0x"), "0X"))
	b, err := hex.DecodeString(s)
	if err != nil || len(b) != 32 {
		return id, false
	}
	copy(id[:], b)
	return id, true
}

// gnoTxHash reads a reference as a gno transaction hash: 32 bytes in base64 (what the wallet returns), or in
// hex; the base64 form is what the node is asked for.
func gnoTxHash(ref string) (string, bool) {
	if b, err := base64.StdEncoding.DecodeString(ref); err == nil && len(b) == 32 {
		return ref, true
	}
	if id, ok := messageID(ref); ok {
		return base64.StdEncoding.EncodeToString(id[:]), true
	}
	return "", false
}

var errNoSuchTx = errors.New("no such transaction")
