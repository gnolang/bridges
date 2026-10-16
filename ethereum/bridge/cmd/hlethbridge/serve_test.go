package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

// freeAddr is a loopback address no one listens on.
func freeAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	l.Close()
	return addr
}

// get sends one keep-alive GET on a connection and reads its answer.
func get(t *testing.T, c net.Conn, header string) *http.Response {
	t.Helper()
	fmt.Fprintf(c, "GET /v1/eth/1 HTTP/1.1\r\nHost: x\r\n%s\r\n", header)
	_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
	resp, err := http.ReadResponse(bufio.NewReader(c), nil)
	if err != nil {
		t.Fatalf("no answer: %v", err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	return resp
}

// E3-GT5: the signer's public signature endpoint closed no idle keep-alive connection (ReadHeaderTimeout alone): a
// stranger held 3,000 of them open (go-trust-poc/idleconn_main.go.txt), each a goroutine, buffers and a descriptor
// the loop's provider and gno reads need. Every machine's server closes an idle connection after its idle time,
// bounds a request's reading and its answer's writing, refuses headers past 8 KiB, and holds at most maxConns
// connections at once.
func TestServerLimits(t *testing.T) {
	s := newServer(http.NotFoundHandler())
	if s.ReadHeaderTimeout <= 0 || s.ReadTimeout <= 0 || s.WriteTimeout <= 0 || s.IdleTimeout <= 0 || s.MaxHeaderBytes != 8<<10 {
		t.Fatalf("the server's limits: %+v", s)
	}
	t.Run("an idle connection is closed", func(t *testing.T) {
		was := idleTimeout
		idleTimeout = 200 * time.Millisecond
		defer func() { idleTimeout = was }()
		addr := freeAddr(t)
		stop := serveOn(addr, http.NotFoundHandler(), "", "")
		defer stop()
		c, err := net.Dial("tcp", addr)
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		if resp := get(t, c, ""); resp.StatusCode != http.StatusNotFound {
			t.Fatalf("the answer: %s", resp.Status)
		}
		time.Sleep(600 * time.Millisecond) // three idle times
		_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
		if _, err := c.Read(make([]byte, 1)); !errors.Is(err, io.EOF) {
			t.Fatalf("an idle keep-alive connection is still open past its idle time: %v", err)
		}
	})
	t.Run("a header past 8 KiB is refused", func(t *testing.T) {
		addr := freeAddr(t)
		stop := serveOn(addr, http.NotFoundHandler(), "", "")
		defer stop()
		c, err := net.Dial("tcp", addr)
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		if resp := get(t, c, "X-Pad: "+strings.Repeat("a", 64<<10)+"\r\n"); resp.StatusCode != http.StatusRequestHeaderFieldsTooLarge {
			t.Fatalf("a 64 KiB header: %s", resp.Status)
		}
	})
	t.Run("at most maxConns connections at once", func(t *testing.T) {
		was := maxConns
		maxConns = 2
		defer func() { maxConns = was }()
		addr := freeAddr(t)
		stop := serveOn(addr, http.NotFoundHandler(), "", "")
		defer stop()
		var held []net.Conn
		for i := 0; i < 2; i++ {
			c, err := net.Dial("tcp", addr)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			get(t, c, "")
			held = append(held, c)
		}
		third, err := net.Dial("tcp", addr) // the kernel's backlog takes it; the server does not
		if err != nil {
			t.Fatal(err)
		}
		defer third.Close()
		fmt.Fprintf(third, "GET /v1/eth/1 HTTP/1.1\r\nHost: x\r\n\r\n")
		_ = third.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
		if _, err := third.Read(make([]byte, 1)); err == nil {
			t.Fatal("a third connection was served while two held both slots")
		}
		held[0].Close() // a slot frees: the third is served
		_ = third.SetReadDeadline(time.Now().Add(2 * time.Second))
		resp, err := http.ReadResponse(bufio.NewReader(third), nil)
		if err != nil || resp.StatusCode != http.StatusNotFound {
			t.Fatalf("the third connection once a slot freed: %v %v", resp, err)
		}
		resp.Body.Close()
	})
}
