package gnoside

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/gnolang/bridges/solana/bridge/gno"
)

// The relayer's batch re-emit (eth/bridge/PLAN.md §5.7): when dispatches from more than one gno.land block wait and
// none of their headers is accepted, one gno transaction calls hlmailbox's Reemit for every one of them, so their
// Dispatch events share one block — one results leaf — and one header update serves them all. The transaction carries
// one MsgCall per nonce (a tm2 transaction carries many messages, delivered in one DeliverTx), never a MsgRun:
// gnoland-1's vm param run_submitters lets only GovDAO's three T1 members send a MsgRun (gno.land/pkg/gnoland/app.go,
// checkRunSubmitters, "is not authorized to send MsgRun"; the assumptions audit's E3-AS1). github.com/gnolang/bridges/solana/bridge/gno, the
// shared client, makes one MsgCall a transaction, so this transaction is composed here and signed and broadcast by the
// same gnokey: `maketx call -broadcast=false` writes a message as the running gnokey encodes it (the others are its
// copies with their own nonce), `sign` signs the whole under the key's account number and sequence (auth/accounts),
// `broadcast -dry-run` measures its gas, and `broadcast` sends it with a fifth more.

// Runner sends the batch re-emit through gnokey with a gno client's node, chain id, keybase, key and price.
type Runner struct {
	Client  *gno.Client
	TempDir string // where the transaction is written for gnokey ("" for the system's)
}

var (
	runTxHashRE  = regexp.MustCompile(`TX HASH:\s+(\S+)`)
	runGasUsedRE = regexp.MustCompile(`GAS USED:\s+(\d+)`)
	runHeightRE  = regexp.MustCompile(`HEIGHT:\s+(\d+)`)
)

// Reemit re-emits the nonces' Dispatch events in one transaction of Reemit MsgCalls, in the order given: a simulation
// under gasWanted measures its gas, the broadcast carries a fifth more (gno.GasFor) at the higher of the client's
// price and the chain's block price; a node's own minimum fee is paid once under the bound (the gno client's F14
// rule). A refusal is a *gno.CallError, so gno.Deterministic sorts it.
func (r *Runner) Reemit(ctx context.Context, mailbox string, nonces []uint32, gasWanted int64) (*gno.Result, error) {
	if len(nonces) == 0 {
		return nil, errors.New("no nonce to re-emit")
	}
	dir, err := os.MkdirTemp(r.TempDir, "hlreemit-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	msgs, caller, err := r.reemitMsgs(ctx, mailbox, nonces)
	if err != nil {
		return nil, err
	}
	number, sequence, err := r.account(ctx, caller)
	if err != nil {
		return nil, err
	}
	file := filepath.Join(dir, "tx.json")
	send := func(gas int64, fee string, dryRun bool) (*gno.Result, error) {
		if err := writeTx(file, msgs, gas, fee); err != nil {
			return nil, err
		}
		if out, err := r.gnokey(ctx, "\n", r.signArgs(file, number, sequence)...); err != nil {
			return nil, &gno.CallError{Pkg: mailbox, Fn: "Reemit", Err: fmt.Errorf("gnokey sign: %w", err), Output: out}
		}
		return r.broadcast(ctx, mailbox, file, dryRun)
	}
	sim, err := send(gasWanted, r.fee(ctx, gasWanted), true)
	if err != nil {
		return nil, err
	}
	gas := gasWanted
	if sim.GasUsed > 0 {
		gas = gno.GasFor(sim.GasUsed)
	}
	res, err := send(gas, r.fee(ctx, gas), false)
	if need, carried := gno.NodeMinFee(err); need > 0 {
		bound := r.Client.MaxNodeFee
		if bound <= 0 {
			bound = max(10*carried, 1_000_000)
		}
		if need > bound {
			return nil, &gno.CallError{Pkg: mailbox, Fn: "Reemit", Err: errors.New("the node's minimum gas price is above the bound"),
				Output: fmt.Sprintf("REFUSING the node's minimum gas price: fee required %d ugnot, the bound %d ugnot", need, bound)}
		}
		res, err = send(gas, strconv.FormatInt(need, 10)+"ugnot", false)
	}
	return res, err
}

// reemitMsgs are the transaction's messages: gnokey's own encoding of a Reemit MsgCall of the first nonce, copied for
// each nonce with its own argument, and the caller (the key's address) they name.
func (r *Runner) reemitMsgs(ctx context.Context, mailbox string, nonces []uint32) ([]map[string]any, string, error) {
	args := []string{"maketx", "call", "-pkgpath", mailbox, "-func", "Reemit", "-args", strconv.FormatUint(uint64(nonces[0]), 10),
		"-gas-fee", "1ugnot", "-gas-wanted", "1", "-broadcast=false"}
	if r.Client.Home != "" {
		args = append(args, "-home", r.Client.Home)
	}
	args = append(args, r.Client.Key)
	out, err := r.gnokey(ctx, "", args...)
	if err != nil {
		return nil, "", &gno.CallError{Pkg: mailbox, Fn: "Reemit", Err: fmt.Errorf("gnokey maketx call: %w", err), Output: out}
	}
	var tx struct {
		Msg []map[string]any `json:"msg"`
	}
	if err := json.Unmarshal([]byte(lastJSONLine(out)), &tx); err != nil || len(tx.Msg) != 1 {
		return nil, "", &gno.CallError{Pkg: mailbox, Fn: "Reemit", Err: fmt.Errorf("gnokey wrote no one-message transaction (%v)", err), Output: out}
	}
	tmpl := tx.Msg[0]
	caller, _ := tmpl["caller"].(string)
	if tmpl["@type"] != "/vm.m_call" || tmpl["func"] != "Reemit" || tmpl["pkg_path"] != mailbox || !strings.HasPrefix(caller, "g1") {
		return nil, "", &gno.CallError{Pkg: mailbox, Fn: "Reemit", Err: errors.New("gnokey wrote another message than Reemit's MsgCall"), Output: out}
	}
	msgs := make([]map[string]any, len(nonces))
	for i, n := range nonces {
		m := map[string]any{}
		for k, v := range tmpl {
			m[k] = v
		}
		m["args"] = []string{strconv.FormatUint(uint64(n), 10)}
		msgs[i] = m
	}
	return msgs, caller, nil
}

// lastJSONLine is the last line of gnokey's output that opens a JSON object (the transaction it composed).
func lastJSONLine(out string) string {
	lines := strings.Split(strings.TrimSpace(out), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if l := strings.TrimSpace(lines[i]); strings.HasPrefix(l, "{") {
			return l
		}
	}
	return ""
}

// account is the key's account number and sequence (auth/accounts/<address>, the node's answer).
func (r *Runner) account(ctx context.Context, addr string) (number, sequence uint64, err error) {
	b, err := r.Client.Query(ctx, "auth/accounts/"+addr, nil, 0)
	if err != nil {
		return 0, 0, err
	}
	var acc struct {
		BaseAccount struct {
			AccountNumber json.Number `json:"account_number"`
			Sequence      json.Number `json:"sequence"`
		} `json:"BaseAccount"`
	}
	if err := json.Unmarshal(b, &acc); err != nil {
		return 0, 0, fmt.Errorf("auth/accounts/%s: %w: %.200s", addr, err, b)
	}
	if number, err = strconv.ParseUint(acc.BaseAccount.AccountNumber.String(), 10, 64); err != nil {
		return 0, 0, fmt.Errorf("auth/accounts/%s: account number %q", addr, acc.BaseAccount.AccountNumber)
	}
	if sequence, err = strconv.ParseUint(acc.BaseAccount.Sequence.String(), 10, 64); err != nil {
		return 0, 0, fmt.Errorf("auth/accounts/%s: sequence %q", addr, acc.BaseAccount.Sequence)
	}
	return number, sequence, nil
}

// writeTx writes the unsigned transaction (Amino JSON, as gnokey reads it).
func writeTx(file string, msgs []map[string]any, gas int64, fee string) error {
	b, err := json.Marshal(map[string]any{"msg": msgs, "fee": map[string]string{"gas_wanted": strconv.FormatInt(gas, 10), "gas_fee": fee}, "signatures": nil, "memo": ""})
	if err != nil {
		return err
	}
	return os.WriteFile(file, b, 0o600)
}

func (r *Runner) signArgs(file string, number, sequence uint64) []string {
	args := []string{"sign", "-tx-path", file, "-chainid", r.Client.ChainID, "-account-number", strconv.FormatUint(number, 10),
		"-account-sequence", strconv.FormatUint(sequence, 10), "-insecure-password-stdin"}
	if r.Client.Home != "" {
		args = append(args, "-home", r.Client.Home)
	}
	return append(args, r.Client.Key)
}

// broadcast sends the signed transaction (dryRun: the node's simulation only) and reads gnokey's report.
func (r *Runner) broadcast(ctx context.Context, mailbox, file string, dryRun bool) (*gno.Result, error) {
	args := []string{"broadcast", "-remote", strings.TrimPrefix(r.Client.RPC, "http://")}
	if dryRun {
		args = append(args, "-dry-run")
	}
	args = append(args, file)
	s, err := r.gnokey(ctx, "", args...)
	res := &gno.Result{Output: s}
	if m := runGasUsedRE.FindStringSubmatch(s); m != nil {
		res.GasUsed, _ = strconv.ParseInt(m[1], 10, 64)
	}
	if m := runHeightRE.FindStringSubmatch(s); m != nil {
		res.Height, _ = strconv.ParseInt(m[1], 10, 64)
	}
	if err != nil || !strings.Contains(s, "OK!") {
		if err == nil {
			err = errors.New("no OK! from gnokey")
		}
		return nil, &gno.CallError{Pkg: mailbox, Fn: "Reemit", Err: err, Output: s}
	}
	if dryRun {
		return res, nil
	}
	m := runTxHashRE.FindStringSubmatch(s)
	if m == nil || m[1] == "" || res.Height <= 0 {
		return nil, &gno.CallError{Pkg: mailbox, Fn: "Reemit", Err: errors.New("no transaction hash or height from gnokey"), Output: s}
	}
	res.Hash = m[1]
	return res, nil
}

// gnokey runs one gnokey command under the client's deadline, stdin given.
func (r *Runner) gnokey(ctx context.Context, stdin string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, gno.GnokeyTimeout)
	defer cancel()
	bin := r.Client.Gnokey
	if bin == "" {
		bin = "gnokey"
	}
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Stdin = strings.NewReader(stdin)
	out, err := cmd.CombinedOutput()
	if ctx.Err() != nil && err != nil {
		err = fmt.Errorf("%w: gnokey ran past %s (timed out)", err, gno.GnokeyTimeout)
	}
	return string(out), err
}

// fee is the fee for gas at the higher of the client's price and the chain's block price.
func (r *Runner) fee(ctx context.Context, gas int64) string {
	c := r.Client
	if c.GasFee != "" {
		return c.GasFee
	}
	p := c.GasPrice
	if p <= 0 {
		p = gno.MinGasPrice
	}
	if gp, err := c.BlockGasPrice(ctx); err == nil && (gp.Denom == "" || gp.Denom == "ugnot") && gp.PerGas() > p {
		p = gp.PerGas()
	}
	return strconv.FormatInt(int64(math.Ceil(p*float64(gas))), 10) + "ugnot"
}
