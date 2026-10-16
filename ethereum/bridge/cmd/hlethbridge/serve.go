package main

import (
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// The machines' HTTP servers within limits (the Go trust audit's E3-GT5). The signer's signature endpoint is public: a
// server with ReadHeaderTimeout alone waits for a keep-alive connection's next request for ever, and a stranger held
// 3,000 idle connections open on it (≈20 KiB each, every one a descriptor the loop's provider and gno reads need). So
// every server here closes an idle connection after a minute, bounds a request's reading and its answer's writing (a
// signature is a few hundred bytes, the status a few kilobytes), refuses headers past 8 KiB, and accepts at most
// maxConns connections at once — the rest wait in the kernel's backlog, never in the process.
var (
	readHeaderTimeout = 5 * time.Second
	readTimeout       = 10 * time.Second
	writeTimeout      = 10 * time.Second
	idleTimeout       = 60 * time.Second
)

// maxHeaderBytes bounds a request's headers (a GET by nonce needs a few hundred bytes).
const maxHeaderBytes = 8 << 10

// maxConns bounds the connections a server holds at once, so the loop's own sockets — its providers', its node's —
// are never starved of descriptors (Go raises the process's soft limit of open files to its hard limit at start).
var maxConns = 1024

// newServer is a server for a handler within the limits above.
func newServer(h http.Handler) *http.Server {
	return &http.Server{Handler: h, ReadHeaderTimeout: readHeaderTimeout, ReadTimeout: readTimeout, WriteTimeout: writeTimeout, IdleTimeout: idleTimeout, MaxHeaderBytes: maxHeaderBytes}
}

// limitListener accepts at most n connections at once (golang.org/x/net/netutil's LimitListener, without the module):
// Accept waits for a slot, and a connection's Close frees it once.
func limitListener(l net.Listener, n int) net.Listener {
	return &limitedListener{Listener: l, sem: make(chan struct{}, n), done: make(chan struct{})}
}

type limitedListener struct {
	net.Listener
	sem       chan struct{}
	closeOnce sync.Once
	done      chan struct{}
}

func (l *limitedListener) Accept() (net.Conn, error) {
	select {
	case l.sem <- struct{}{}:
	case <-l.done:
		return nil, net.ErrClosed
	}
	c, err := l.Listener.Accept()
	if err != nil {
		<-l.sem
		return nil, err
	}
	return &limitedConn{Conn: c, release: func() { <-l.sem }}, nil
}

func (l *limitedListener) Close() error {
	err := l.Listener.Close()
	l.closeOnce.Do(func() { close(l.done) })
	return err
}

type limitedConn struct {
	net.Conn
	once    sync.Once
	release func()
}

func (c *limitedConn) Close() error {
	err := c.Conn.Close()
	c.once.Do(c.release)
	return err
}

// The public signature endpoint faces strangers (the Go trust audit's E4-GT2): the cap above is per process, and a slot
// lasts as long as its connection, so one stranger holding maxConns keep-alive connections — one cheap GET on each a
// minute, inside the idle time — held every slot, and the relayers' fetches waited in the kernel's backlog until they
// timed out: no Ethereum → gno.land credit for as long as he kept it up. So the public endpoint serves one request a
// connection over HTTP/1 only (no keep-alive, and no HTTP/2, whose idle connections Go's server holds to the idle time
// whatever keep-alive says: a slot lasts at most the read and write timeouts); it holds at most perClient connections
// of one client at once — a client is its IP address, an IPv6 one by its /48, the block one subscriber is handed — and
// at most maxConns less publicReserve of everyone but the configured relayers (-relayers), whose own publicReserve
// slots no stranger can take; a connection past its pool is closed as it is accepted, so Accept never waits and a
// relayer is never queued behind strangers holding addresses by the thousand. Behind a reverse proxy (-proxy: nginx
// terminating TLS on the machine), every connection comes from the proxy: its own are not capped per client at the
// listener, and each request it forwards counts against the client X-Forwarded-For's last entry names — the one the
// proxy appended, read from the configured proxy alone (anyone else's header is a stranger's word) — at most perClient
// in flight, the next answered 429, the relayers exempt. The loopback status listeners keep their keep-alives.

// perClient bounds the connections (and, behind a proxy, the requests in flight) of one client at the public endpoint.
var perClient = 16

// publicReserve is how many of maxConns only the configured relayers may hold.
var publicReserve = 64

// publicIdle bounds a public connection's idle time (keep-alives off: it is served one request).
const publicIdle = 5 * time.Second

// servePublic serves the public signature endpoint within the limits above (HTTPS when a certificate is given); proxy is
// the reverse proxy's IP in front of it ("" for none), relayers the addresses (IPs or CIDR blocks) whose fetches have
// slots of their own.
func servePublic(addr string, h http.Handler, cert, key, proxy string, relayers []*net.IPNet) func() {
	if proxy != "" {
		h = clientCap(h, proxy, perClient, relayers)
	}
	return serveWith(addr, h, cert, key, func(srv *http.Server, l net.Listener) net.Listener {
		publicServer(srv)
		return newPublicListener(l, perClient, proxy, relayers, maxConns, publicReserve)
	})
}

// publicServer is a server's settings for the public endpoint: one request a connection, HTTP/1 only (an empty
// TLSNextProto turns Go's HTTP/2 off), a short idle time.
func publicServer(srv *http.Server) {
	srv.SetKeepAlivesEnabled(false)
	srv.TLSNextProto = map[string]func(*http.Server, *tls.Conn, http.Handler){}
	srv.IdleTimeout = publicIdle
}

// clientKey is a client's address as the caps count it: an IPv4 address, an IPv6 one's /48 ("" when it does not read).
func clientKey(host string) string {
	ip := net.ParseIP(strings.TrimSpace(host))
	if ip == nil {
		return ""
	}
	if v4 := ip.To4(); v4 != nil {
		return v4.String()
	}
	return ip.Mask(net.CIDRMask(48, 128)).String() + "/48"
}

// hostOf is a remote address's host part.
func hostOf(addr string) string {
	if host, _, err := net.SplitHostPort(addr); err == nil {
		return host
	}
	return addr
}

// ParseNets reads -relayers: IP addresses or CIDR blocks, comma separated.
func ParseNets(s string) ([]*net.IPNet, error) {
	var out []*net.IPNet
	for _, f := range strings.Split(s, ",") {
		f = strings.TrimSpace(f)
		if f == "" {
			continue
		}
		if !strings.Contains(f, "/") {
			ip := net.ParseIP(f)
			if ip == nil {
				return nil, fmt.Errorf("%q is not an IP address or a CIDR block", f)
			}
			bits := 128
			if ip.To4() != nil {
				ip, bits = ip.To4(), 32
			}
			f = ip.String() + "/" + strconv.Itoa(bits)
		}
		_, n, err := net.ParseCIDR(f)
		if err != nil {
			return nil, fmt.Errorf("%q is not an IP address or a CIDR block", f)
		}
		out = append(out, n)
	}
	return out, nil
}

// inNets says whether a host is in one of the blocks.
func inNets(host string, nets []*net.IPNet) bool {
	ip := net.ParseIP(strings.TrimSpace(host))
	if ip == nil {
		return false
	}
	for _, n := range nets {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

// newPublicListener holds the public endpoint's connections in two pools: the relayers' reserve, then the general
// pool, which every other client shares at most perClient connections each (the proxy's own connections uncounted per
// client); a connection past its pool is closed as accepted.
func newPublicListener(l net.Listener, n int, proxy string, relayers []*net.IPNet, total, reserve int) net.Listener {
	reserve = min(reserve, total)
	return &publicListener{Listener: l, n: n, proxy: clientKey(proxy), relayers: relayers, maxGeneral: total - reserve, maxReserved: reserve, conns: map[string]int{}}
}

type publicListener struct {
	net.Listener
	n                       int
	proxy                   string
	relayers                []*net.IPNet
	maxGeneral, maxReserved int
	mu                      sync.Mutex
	general, reserved       int
	conns                   map[string]int
}

func (l *publicListener) Accept() (net.Conn, error) {
	for {
		c, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}
		host := hostOf(c.RemoteAddr().String())
		if release, ok := l.admit(host); ok {
			return &limitedConn{Conn: c, release: release}, nil
		}
		_ = c.Close() // past its pool: closed as accepted, never a slot, never a wait
	}
}

// admit takes a slot for a connection from a host, and answers how to give it back.
func (l *publicListener) admit(host string) (func(), bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if inNets(host, l.relayers) {
		switch {
		case l.reserved < l.maxReserved:
			l.reserved++
			return func() { l.mu.Lock(); l.reserved--; l.mu.Unlock() }, true
		case l.general < l.maxGeneral:
			l.general++
			return func() { l.mu.Lock(); l.general--; l.mu.Unlock() }, true
		}
		return nil, false
	}
	key := clientKey(host)
	if l.general >= l.maxGeneral || (key != l.proxy || key == "") && l.conns[key] >= l.n {
		return nil, false
	}
	l.general++
	l.conns[key]++
	return func() {
		l.mu.Lock()
		l.general--
		if l.conns[key]--; l.conns[key] <= 0 {
			delete(l.conns, key)
		}
		l.mu.Unlock()
	}, true
}

// clientCap bounds the requests in flight of one client behind the proxy: a request from the proxy counts against the
// client its X-Forwarded-For's last entry names (the proxy appended it), a relayer's uncounted; a request from anyone
// else is the listener's to cap, its header ignored.
func clientCap(h http.Handler, proxy string, n int, relayers []*net.IPNet) http.Handler {
	proxyKey := clientKey(proxy)
	var mu sync.Mutex
	inflight := map[string]int{}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if clientKey(hostOf(r.RemoteAddr)) != proxyKey {
			h.ServeHTTP(w, r)
			return
		}
		client := "(the proxy names no client)"
		if vs := r.Header.Values("X-Forwarded-For"); len(vs) > 0 {
			parts := strings.Split(vs[len(vs)-1], ",")
			last := parts[len(parts)-1]
			if inNets(last, relayers) {
				h.ServeHTTP(w, r)
				return
			}
			if k := clientKey(last); k != "" {
				client = k
			}
		}
		mu.Lock()
		if inflight[client] >= n {
			mu.Unlock()
			http.Error(w, "too many requests in flight from this address", http.StatusTooManyRequests)
			return
		}
		inflight[client]++
		mu.Unlock()
		defer func() {
			mu.Lock()
			if inflight[client]--; inflight[client] <= 0 {
				delete(inflight, client)
			}
			mu.Unlock()
		}()
		h.ServeHTTP(w, r)
	})
}
