package main

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"testing"
	"time"
)

// dialFrom dials an address from a loopback source address of its own (127.0.0.2: another client, as a stranger is).
func dialFrom(t *testing.T, src, addr string) (net.Conn, error) {
	t.Helper()
	d := net.Dialer{LocalAddr: &net.TCPAddr{IP: net.ParseIP(src)}, Timeout: 2 * time.Second}
	return d.Dial("tcp", addr)
}

// E4-GT2 (go-trust-poc/zz_audit4_serve_test.go): one stranger opening maxConns keep-alive connections to the public
// signature endpoint, a GET on each, held every slot, and a relayer's fetch made meanwhile timed out. The public endpoint
// serves one request a connection and holds at most perClient connections of one client: the relayer is answered.
func TestF15bOneClientHoldsNoSlots(t *testing.T) {
	addr := freeAddr(t)
	stop := servePublic(addr, http.NotFoundHandler(), "", "", "", nil)
	defer stop()
	var held []net.Conn
	answered := 0
	for i := 0; i < maxConns; i++ {
		c, err := dialFrom(t, "127.0.0.2", addr)
		if err != nil {
			continue
		}
		defer c.Close()
		fmt.Fprintf(c, "GET /v1/eth/1 HTTP/1.1\r\nHost: x\r\n\r\n")
		_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
		if resp, err := http.ReadResponse(bufio.NewReader(c), nil); err == nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			answered++
		}
		held = append(held, c)
	}
	t.Logf("the stranger opened %d connections (one GET each), %d answered", len(held), answered)
	start := time.Now()
	cl := &http.Client{Timeout: 10 * time.Second}
	resp, err := cl.Get("http://" + addr + "/v1/eth/7")
	if err != nil {
		t.Fatalf("the relayer's fetch while the stranger holds his connections: error after %s: %v", time.Since(start).Round(time.Second), err)
	}
	resp.Body.Close()
	t.Logf("the relayer's fetch answered after %s: %s", time.Since(start).Round(time.Millisecond), resp.Status)
}

// The public endpoint closes a connection after its one answer (no keep-alive); a status listener keeps its own.
func TestF15bPublicEndpointKeepsNoConnection(t *testing.T) {
	addr := freeAddr(t)
	stop := servePublic(addr, http.NotFoundHandler(), "", "", "", nil)
	defer stop()
	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	resp := get(t, c, "")
	if !resp.Close {
		t.Fatalf("the public endpoint offers keep-alive: %v", resp.Header)
	}
	_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := c.Read(make([]byte, 1)); err != io.EOF {
		t.Fatalf("the connection is open after its answer: %v", err)
	}
	status := freeAddr(t)
	stopStatus := serveOn(status, http.NotFoundHandler(), "", "")
	defer stopStatus()
	sc, err := net.Dial("tcp", status)
	if err != nil {
		t.Fatal(err)
	}
	defer sc.Close()
	if resp := get(t, sc, ""); resp.Close {
		t.Fatal("a status listener lost its keep-alive")
	}
}

// One client holds at most perClient connections at once (the next closed as accepted); another client is served.
// Behind the proxy, the proxy's connections are not capped, and its requests count against the client its
// X-Forwarded-For's last entry names — anyone else's header is ignored.
func TestF15bPerClientCaps(t *testing.T) {
	was := perClient
	perClient = 2
	defer func() { perClient = was }()
	release := make(chan struct{})
	var inflight sync.WaitGroup
	slow := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		inflight.Done()
		<-release
		w.WriteHeader(http.StatusNoContent)
	})
	t.Run("direct", func(t *testing.T) {
		addr := freeAddr(t)
		stop := servePublic(addr, http.NotFoundHandler(), "", "", "", nil)
		defer stop()
		var conns []net.Conn
		for i := 0; i < 2; i++ { // two connections held open, no request yet
			c, err := dialFrom(t, "127.0.0.2", addr)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			conns = append(conns, c)
		}
		time.Sleep(100 * time.Millisecond)
		third, err := dialFrom(t, "127.0.0.2", addr)
		if err != nil {
			t.Fatal(err)
		}
		defer third.Close()
		fmt.Fprintf(third, "GET /v1/eth/1 HTTP/1.1\r\nHost: x\r\n\r\n")
		_ = third.SetReadDeadline(time.Now().Add(2 * time.Second))
		if _, err := http.ReadResponse(bufio.NewReader(third), nil); err == nil {
			t.Fatal("a third connection of one client was served while it held two")
		}
		other, err := net.Dial("tcp", addr) // 127.0.0.1: another client
		if err != nil {
			t.Fatal(err)
		}
		defer other.Close()
		if resp := get(t, other, ""); resp.StatusCode != http.StatusNotFound {
			t.Fatalf("another client: %s", resp.Status)
		}
	})
	t.Run("behind the proxy", func(t *testing.T) {
		addr := freeAddr(t)
		stop := servePublic(addr, slow, "", "", "127.0.0.1", nil)
		defer stop()
		do := func(src, xff string) chan int {
			out := make(chan int, 1)
			go func() {
				tr := &http.Transport{DialContext: (&net.Dialer{LocalAddr: &net.TCPAddr{IP: net.ParseIP(src)}}).DialContext}
				req, _ := http.NewRequest("GET", "http://"+addr+"/v1/eth/1", nil)
				req.Header.Set("X-Forwarded-For", xff)
				resp, err := (&http.Client{Transport: tr, Timeout: 5 * time.Second}).Do(req)
				if err != nil {
					out <- -1
					return
				}
				resp.Body.Close()
				out <- resp.StatusCode
			}()
			return out
		}
		inflight.Add(3)
		a1, a2 := do("127.0.0.1", "9.9.9.9, 1.2.3.4"), do("127.0.0.1", "1.2.3.4") // client 1.2.3.4 twice, through the proxy
		b := do("127.0.0.1", "5.6.7.8")                                           // another client through the proxy
		inflight.Wait()
		if code := <-do("127.0.0.1", "1.2.3.4"); code != http.StatusTooManyRequests {
			t.Fatalf("a third request of one client through the proxy: %d", code)
		}
		inflight.Add(1)
		c := do("127.0.0.2", "1.2.3.4") // not the proxy: its header is no client's word
		inflight.Wait()
		close(release)
		for i, ch := range []chan int{a1, a2, b, c} {
			if code := <-ch; code != http.StatusNoContent {
				t.Fatalf("request %d: %d", i, code)
			}
		}
	})
}

// The review's finding 9 on E4-GT2: strangers holding many addresses (64 IPv4 addresses, or IPv6 /64s by the thousand)
// could still fill every slot. The configured relayers have slots of their own no stranger can take, a stranger past
// the general pool is closed as accepted (never queued ahead of a relayer), an IPv6 client counts by its /48, and the
// public server speaks HTTP/1 only (Go holds an idle HTTP/2 connection to the idle time whatever keep-alive says).
func TestF15bRelayersKeepTheirSlots(t *testing.T) {
	was, wasReserve := maxConns, publicReserve
	maxConns, publicReserve = 4, 2
	defer func() { maxConns, publicReserve = was, wasReserve }()
	relayers, err := ParseNets("127.0.0.1, 10.0.0.0/8")
	if err != nil || len(relayers) != 2 {
		t.Fatalf("ParseNets: %v %v", relayers, err)
	}
	addr := freeAddr(t)
	stop := servePublic(addr, http.NotFoundHandler(), "", "", "", relayers)
	defer stop()
	for _, src := range []string{"127.0.0.2", "127.0.0.3"} { // two strangers fill the general pool (no request yet)
		c, err := dialFrom(t, src, addr)
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
	}
	time.Sleep(100 * time.Millisecond)
	third, err := dialFrom(t, "127.0.0.4", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer third.Close()
	fmt.Fprintf(third, "GET /v1/eth/1 HTTP/1.1\r\nHost: x\r\n\r\n")
	_ = third.SetReadDeadline(time.Now().Add(time.Second))
	start := time.Now()
	if _, err := http.ReadResponse(bufio.NewReader(third), nil); err == nil {
		t.Fatal("a stranger past the general pool was served")
	} else if time.Since(start) > 900*time.Millisecond {
		t.Fatalf("a stranger past the general pool waited instead of being closed: %v", err)
	}
	relayer, err := net.Dial("tcp", addr) // 127.0.0.1, a relayer: its reserve
	if err != nil {
		t.Fatal(err)
	}
	defer relayer.Close()
	if resp := get(t, relayer, ""); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("the relayer: %s", resp.Status)
	}
	srv := &http.Server{}
	publicServer(srv)
	if srv.TLSNextProto == nil || len(srv.TLSNextProto) != 0 || srv.IdleTimeout != publicIdle {
		t.Fatalf("the public server's settings: %+v", srv)
	}
	if a, b := clientKey("2001:db8:1:2::1"), clientKey("2001:db8:1:ffff::9"); a != b || a != "2001:db8:1::/48" {
		t.Fatalf("an IPv6 client's key: %q %q", a, b)
	}
}
