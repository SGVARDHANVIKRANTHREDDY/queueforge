package chaos_test

import (
	"io"
	"net"
	"net/url"
	"sync"
	"testing"
	"time"
)

// faultProxy is a tiny TCP proxy that sits between a client and PostgreSQL and can inject the
// network faults that real deployments see: added latency, abruptly reset connections, and a
// "black hole" where packets are silently dropped (the nastiest case: connections look open but
// nothing ever comes back, so naive code waits forever).
type faultProxy struct {
	ln     net.Listener
	target string

	mu        sync.Mutex
	conns     map[net.Conn]struct{}
	delay     time.Duration
	blackhole bool
	closed    bool
}

func newFaultProxy(t *testing.T, target string) *faultProxy {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	p := &faultProxy{ln: ln, target: target, conns: map[net.Conn]struct{}{}}
	go p.accept()
	t.Cleanup(p.Close)
	return p
}

func (p *faultProxy) Addr() string { return p.ln.Addr().String() }

// RewriteURL points a postgres:// URL at the proxy instead of the real server.
func (p *faultProxy) RewriteURL(dbURL string) string {
	u, err := url.Parse(dbURL)
	if err != nil {
		panic(err)
	}
	u.Host = p.Addr()
	return u.String()
}

func (p *faultProxy) SetDelay(d time.Duration) { p.mu.Lock(); p.delay = d; p.mu.Unlock() }
func (p *faultProxy) SetBlackhole(b bool)      { p.mu.Lock(); p.blackhole = b; p.mu.Unlock() }

// ResetAll abruptly closes every open connection (like a failover or a NAT timeout).
func (p *faultProxy) ResetAll() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for c := range p.conns {
		if tc, ok := c.(*net.TCPConn); ok {
			_ = tc.SetLinger(0) // send RST rather than FIN
		}
		_ = c.Close()
	}
	p.conns = map[net.Conn]struct{}{}
}

func (p *faultProxy) Close() {
	p.mu.Lock()
	p.closed = true
	p.mu.Unlock()
	_ = p.ln.Close()
	p.ResetAll()
}

func (p *faultProxy) accept() {
	for {
		c, err := p.ln.Accept()
		if err != nil {
			return
		}
		go p.handle(c)
	}
}

func (p *faultProxy) handle(client net.Conn) {
	server, err := net.DialTimeout("tcp", p.target, 2*time.Second)
	if err != nil {
		_ = client.Close()
		return
	}
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		_ = client.Close()
		_ = server.Close()
		return
	}
	p.conns[client], p.conns[server] = struct{}{}, struct{}{}
	p.mu.Unlock()

	pipe := func(dst, src net.Conn) {
		buf := make([]byte, 32*1024)
		for {
			n, err := src.Read(buf)
			if n > 0 {
				p.mu.Lock()
				d, hole := p.delay, p.blackhole
				p.mu.Unlock()
				if hole {
					continue // swallow the bytes: the connection stays open, nothing is delivered
				}
				if d > 0 {
					time.Sleep(d)
				}
				if _, werr := dst.Write(buf[:n]); werr != nil {
					break
				}
			}
			if err != nil {
				if err != io.EOF {
					_ = err
				}
				break
			}
		}
		_ = dst.Close()
		_ = src.Close()
	}
	go pipe(server, client)
	go pipe(client, server)
}

func urlParse(dbURL string) (string, error) {
	u, err := url.Parse(dbURL)
	if err != nil {
		return "", err
	}
	return u.Host, nil
}
