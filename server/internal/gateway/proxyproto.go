package gateway

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"sync"
	"time"
)

// PROXY protocol v2 lets a TCP load balancer pass the client's address to the
// gateway in a binary header ahead of the stream. Raw TCP has no
// X-Forwarded-For, so without it the per-IP guard behind a balancer charges every
// client to the balancer's address.
//
// The header is read only from a trusted proxy (Config.TrustedProxies); from
// any other peer the bytes are the client's own and pass through untouched, so
// a client cannot claim an address by sending one.

// proxyV2Signature opens every PROXY protocol v2 header.
var proxyV2Signature = []byte("\r\n\r\n\x00\r\nQUIT\n")

// proxyHeaderTimeout bounds how long a trusted proxy may take to send the
// header. A balancer writes it before any client byte, so this only has to
// cover the network.
const proxyHeaderTimeout = 5 * time.Second

// errProxyHeader is returned for a trusted peer whose stream does not start
// with a valid PROXY v2 header.
var errProxyHeader = errors.New("gateway: missing or malformed PROXY protocol header")

// NewProxyProtocolListener wraps a raw TCP listener so connections from a
// trusted proxy report the client address carried in their PROXY v2 header.
// Wrap below TLS: the header precedes the TLS handshake on the wire.
func NewProxyProtocolListener(ln net.Listener, trustedProxies []string) net.Listener {
	trusted, _ := parseTrustedProxies(trustedProxies)
	return &proxyListener{Listener: ln, trusted: trusted}
}

type proxyListener struct {
	net.Listener
	trusted []*net.IPNet
}

func (l *proxyListener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	if !isTrustedProxy(hostOf(c.RemoteAddr().String()), l.trusted) {
		return c, nil
	}
	return &proxyConn{Conn: c}, nil
}

// proxyConn reads the header lazily, on the first RemoteAddr or Read, so Accept
// never blocks on a slow peer.
type proxyConn struct {
	net.Conn
	once   sync.Once
	remote net.Addr // nil for a LOCAL connection (a balancer health check)
	err    error
}

func (p *proxyConn) resolve() {
	p.once.Do(func() {
		_ = p.SetReadDeadline(time.Now().Add(proxyHeaderTimeout))
		p.remote, p.err = readProxyV2(p.Conn)
		_ = p.SetReadDeadline(time.Time{})
	})
}

// RemoteAddr is the client address from the header, or the proxy's own for a
// LOCAL connection or a failed header (which Read then reports).
func (p *proxyConn) RemoteAddr() net.Addr {
	p.resolve()
	if p.remote != nil {
		return p.remote
	}
	return p.Conn.RemoteAddr()
}

func (p *proxyConn) Read(b []byte) (int, error) {
	p.resolve()
	if p.err != nil {
		return 0, p.err
	}
	return p.Conn.Read(b)
}

// behindProxy reports whether c came through a trusted proxy's PROXY header,
// looking through a TLS wrapper.
func behindProxy(c net.Conn) bool {
	return unwrapProxy(c) != nil
}

// proxyHeaderErr reports a missing or malformed header on a connection from a
// trusted proxy. It reads the header if that has not happened yet.
func proxyHeaderErr(c net.Conn) error {
	p := unwrapProxy(c)
	if p == nil {
		return nil
	}
	p.resolve()
	return p.err
}

func unwrapProxy(c net.Conn) *proxyConn {
	for {
		switch v := c.(type) {
		case *proxyConn:
			return v
		case interface{ NetConn() net.Conn }:
			c = v.NetConn()
		default:
			return nil
		}
	}
}

// readProxyV2 reads one PROXY v2 header and returns the source address, or nil
// for the LOCAL command. TLVs after the addresses are skipped.
func readProxyV2(r io.Reader) (net.Addr, error) {
	var hdr [16]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return nil, errProxyHeader
	}
	if !bytes.Equal(hdr[:12], proxyV2Signature) || hdr[12]>>4 != 2 {
		return nil, errProxyHeader
	}
	body := make([]byte, binary.BigEndian.Uint16(hdr[14:16]))
	if _, err := io.ReadFull(r, body); err != nil {
		return nil, errProxyHeader
	}
	switch hdr[12] & 0x0f {
	case 0x0: // LOCAL: the proxy's own connection, e.g. a health check
		return nil, nil
	case 0x1: // PROXY
	default:
		return nil, errProxyHeader
	}
	switch hdr[13] {
	case 0x11: // TCP over IPv4
		if len(body) < 12 {
			return nil, errProxyHeader
		}
		return &net.TCPAddr{IP: net.IP(body[0:4]), Port: int(binary.BigEndian.Uint16(body[8:10]))}, nil
	case 0x21: // TCP over IPv6
		if len(body) < 36 {
			return nil, errProxyHeader
		}
		return &net.TCPAddr{IP: net.IP(body[0:16]), Port: int(binary.BigEndian.Uint16(body[32:34]))}, nil
	default:
		// UNSPEC or a non-TCP family: keep the proxy's address.
		return nil, nil
	}
}
