package main

import (
	"log"
	"net"
	"net/http"
	"sync"
	"time"
)

// The machines' HTTP servers within limits (the Ethereum port's third audit round, E3-GT5, found on this code as on
// its copy). The signer's signature endpoint is public: a server with ReadHeaderTimeout alone waits for a keep-alive
// connection's next request for ever, and a stranger held 3,000 idle connections open on its settings (≈20 KiB each,
// every one a descriptor the loop's provider and gno reads need). So every server here — the signer's public
// endpoint and every machine's loopback status — closes an idle connection after a minute, bounds a request's
// reading and its answer's writing (a signature is a few hundred bytes, a status a few kilobytes), refuses headers
// past 8 KiB, and holds at most maxConns connections at once: the rest wait in the kernel's backlog, never in the
// process.
var (
	readHeaderTimeout = 5 * time.Second
	readTimeout       = 10 * time.Second
	writeTimeout      = 10 * time.Second
	idleTimeout       = 60 * time.Second
)

// maxHeaderBytes bounds a request's headers (a GET by nonce needs a few hundred bytes).
const maxHeaderBytes = 8 << 10

// maxConns bounds the connections a server holds at once, so the loop's own sockets — its providers', its node's —
// are never starved of descriptors.
var maxConns = 1024

// newServer is a server for a handler within the limits above.
func newServer(h http.Handler) *http.Server {
	return &http.Server{Handler: h, ReadHeaderTimeout: readHeaderTimeout, ReadTimeout: readTimeout, WriteTimeout: writeTimeout, IdleTimeout: idleTimeout, MaxHeaderBytes: maxHeaderBytes}
}

// serveOn serves a handler on an address (HTTPS when a certificate is given) within the limits above, until the
// returned func is called. It listens before it returns, so an address that cannot be bound fails at start.
func serveOn(addr string, h http.Handler, cert, key string) func() {
	if addr == "" {
		return func() {}
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatal(err)
	}
	srv := newServer(h)
	l := limitListener(ln, maxConns)
	go func() {
		var err error
		if cert != "" {
			err = srv.ServeTLS(l, cert, key)
		} else {
			err = srv.Serve(l)
		}
		if err != nil && err != http.ErrServerClosed {
			log.Fatal(err)
		}
	}()
	return func() { _ = srv.Close() }
}

// limitListener accepts at most n connections at once (golang.org/x/net/netutil's LimitListener, without the
// module): Accept waits for a slot, and a connection's Close frees it once.
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
