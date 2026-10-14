package gno

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"
)

// gnokey's EVENTS line as gnodev printed it on 2026-10-12 (an Approval event, then a storage event).
const eventsLine = `[{"type":"Approval","attrs":[{"key":"token","value":"gno.land/r/g1leu8d2vsplhehcfkjg50mwgdpxdkt8tztu95wr/bubble.BUBBLE"},{"key":"owner","value":"g1jg8mtutu9khhfwc4nxmuhcpftf0pajdhfvsqf5"},{"key":"spender","value":"g1jg8mtutu9khhfwc4nxmuhcpftf0pajdhfvsqf5"},{"key":"value","value":"1"}],"pkg_path":"gno.land/p/g1leu8d2vsplhehcfkjg50mwgdpxdkt8tztu95wr/grc20votes/v0"},{"bytes_delta":4978,"fee_delta":{"denom":"ugnot","amount":497800},"pkg_path":"gno.land/r/g1leu8d2vsplhehcfkjg50mwgdpxdkt8tztu95wr/bubble"}]`

func TestParseEvents(t *testing.T) {
	out := "OK!\nGAS WANTED: 45000000\nGAS USED:   7070441\nHEIGHT:     3\nEVENTS:     " + eventsLine + "\nINFO:       \nTX HASH:    JVi0O8IFQ8yyQJ1PpvS2LkCRV9cM8TlFDj3w6OFaAE4=\n"
	res := &Result{}
	if m := eventsRE.FindStringSubmatch(out); m != nil {
		res.Events = parseEvents(m[1])
	}
	if v, ok := res.Attr("Approval", "value"); !ok || v != "1" {
		t.Fatalf("Approval.value = %q %v; events %+v", v, ok, res.Events)
	}
	if _, ok := res.Attr("Claimed", "count"); ok {
		t.Fatal("an event that is not there")
	}
	if m := txHashRE.FindStringSubmatch(out); m == nil || m[1] != "JVi0O8IFQ8yyQJ1PpvS2LkCRV9cM8TlFDj3w6OFaAE4=" {
		t.Fatalf("hash %v", m)
	}
	if m := heightRE.FindStringSubmatch(out); m == nil || m[1] != "3" {
		t.Fatalf("height %v", m)
	}
}

func TestDeterministic(t *testing.T) {
	refused := &CallError{Pkg: "r", Fn: "f", Err: errors.New("exit status 1"), Output: "panic: hlmailbox: no message with nonce 5\n--= Error =--\nData: ...\n--= /Error =--"}
	if !Deterministic(refused) {
		t.Fatal("a refused call is not deterministic")
	}
	dead := &CallError{Pkg: "r", Fn: "f", Err: errors.New("exit status 1"), Output: "Post \"http://127.0.0.1:26699\": dial tcp 127.0.0.1:26699: connect: connection refused"}
	if Deterministic(dead) {
		t.Fatal("a dead node is deterministic")
	}
	if Deterministic(errors.New("plain")) {
		t.Fatal("a plain error is deterministic")
	}
}

func TestFee(t *testing.T) {
	c := &Client{}
	if f := c.Fee(45_000_000); f != "45000ugnot" {
		t.Fatalf("fee at the chain's minimum: %s", f)
	}
	c.GasPrice = 0.01
	if f := c.Fee(100_000_000); f != "1000000ugnot" {
		t.Fatalf("fee at 0.01: %s", f)
	}
	c.GasFee = "7ugnot"
	if f := c.Fee(1); f != "7ugnot" {
		t.Fatalf("explicit fee: %s", f)
	}
}

// TestLiveNode reads a running node (GNO_RPC=http://127.0.0.1:26657 GNO_PKG=gno.land/r/.../hlmailbox).
func TestLiveNode(t *testing.T) {
	rpc, pkg := os.Getenv("GNO_RPC"), os.Getenv("GNO_PKG")
	if rpc == "" || pkg == "" {
		t.Skip("GNO_RPC and GNO_PKG name a running node and a realm")
	}
	c := &Client{RPC: rpc}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	head, err := c.Status(ctx)
	if err != nil || head.Height < 1 || head.Hash == "" {
		t.Fatalf("status: %+v %v", head, err)
	}
	b, err := c.Block(ctx, head.Height)
	if err != nil || b.Hash != head.Hash || !b.Time.Equal(head.Time) {
		t.Fatalf("block %d: %+v %v (head %+v)", head.Height, b, err, head)
	}
	at, err := c.RenderAt(ctx, pkg, "json", head.Height)
	if err != nil || at == "" {
		t.Fatalf("render at %d: %q %v", head.Height, at, err)
	}
	if _, err := c.RenderAt(ctx, pkg, "json", head.Height+1_000_000); err == nil {
		t.Fatal("a render past the head succeeded")
	}
	if _, err := c.Balance(ctx, "g1jg8mtutu9khhfwc4nxmuhcpftf0pajdhfvsqf5"); err != nil {
		t.Fatal(err)
	}
}
