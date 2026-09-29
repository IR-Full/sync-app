package gateway

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/binary"
	"io"
	"log/slog"
	"net"
	"testing"
	"time"

	"github.com/SyncApp-chat/SyncApp/internal/auth"
	"github.com/SyncApp-chat/SyncApp/internal/chat"
	"github.com/SyncApp-chat/SyncApp/internal/delivery"
	"github.com/SyncApp-chat/SyncApp/internal/message"
	"github.com/SyncApp-chat/SyncApp/internal/presence"
	"github.com/SyncApp-chat/SyncApp/internal/router"
	"github.com/SyncApp-chat/SyncApp/internal/store/memory"
	"github.com/SyncApp-chat/SyncApp/pkg/eventbus"
	"github.com/SyncApp-chat/SyncApp/pkg/id"
	"github.com/SyncApp-chat/SyncApp/pkg/wire"
)

// proxyV2Header builds a PROXY v2 header; src nil builds a LOCAL one.
func proxyV2Header(src *net.TCPAddr) []byte {
	var b bytes.Buffer
	b.Write(proxyV2Signature)
	if src == nil {
		b.Write([]byte{0x20, 0x00, 0x00, 0x00})
		return b.Bytes()
	}
	var body []byte
	fam := byte(0x11)
	if ip4 := src.IP.To4(); ip4 != nil {
		body = append(append([]byte{}, ip4...), 192, 0, 2, 1) // dst is ignored
		body = binary.BigEndian.AppendUint16(body, uint16(src.Port))
		body = binary.BigEndian.AppendUint16(body, 7000)
	} else {
		fam = 0x21
		body = append(append([]byte{}, src.IP.To16()...), net.IPv6loopback...)
		body = binary.BigEndian.AppendUint16(body, uint16(src.Port))
		body = binary.BigEndian.AppendUint16(body, 7000)
	}
	body = append(body, 0x04, 0x00, 0x01, 0xff) // a TLV, which must be skipped
	b.Write([]byte{0x21, fam})
	_ = binary.Write(&b, binary.BigEndian, uint16(len(body)))
	b.Write(body)
	return b.Bytes()
}

func TestReadProxyV2(t *testing.T) {
	for name, tc := range map[string]struct {
		in   []byte
		want string
		err  bool
	}{
		"ipv4":      {in: proxyV2Header(&net.TCPAddr{IP: net.ParseIP("203.0.113.9"), Port: 5555}), want: "203.0.113.9:5555"},
		"ipv6":      {in: proxyV2Header(&net.TCPAddr{IP: net.ParseIP("2001:db8::7"), Port: 443}), want: "[2001:db8::7]:443"},
		"local":     {in: proxyV2Header(nil), want: ""},
		"v1 text":   {in: []byte("PROXY TCP4 1.2.3.4 5.6.7.8 1 2\r\n"), err: true},
		"truncated": {in: proxyV2Header(&net.TCPAddr{IP: net.ParseIP("203.0.113.9"), Port: 1})[:20], err: true},
		"empty":     {in: nil, err: true},
	} {
		t.Run(name, func(t *testing.T) {
			addr, err := readProxyV2(bytes.NewReader(tc.in))
			if tc.err {
				if err == nil {
					t.Fatalf("want an error, got %v", addr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			got := ""
			if addr != nil {
				got = addr.String()
			}
			if got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

// acceptOne dials ln, writes payload, and returns the accepted server side.
func acceptOne(t *testing.T, ln net.Listener, payload []byte) (server, client net.Conn) {
	t.Helper()
	accepted := make(chan net.Conn, 1)
	go func() {
		c, err := ln.Accept()
		if err == nil {
			accepted <- c
		}
	}()
	client, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	if _, err := client.Write(payload); err != nil {
		t.Fatal(err)
	}
	select {
	case server = <-accepted:
		t.Cleanup(func() { _ = server.Close() })
		return server, client
	case <-time.After(5 * time.Second):
		t.Fatal("accept timed out")
		return nil, nil
	}
}

func TestProxyListenerHonoursOnlyTrustedPeers(t *testing.T) {
	raw, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = raw.Close() }()
	src := &net.TCPAddr{IP: net.ParseIP("198.51.100.4"), Port: 4000}

	t.Run("trusted", func(t *testing.T) {
		ln := NewProxyProtocolListener(raw, []string{"127.0.0.1"})
		c, _ := acceptOne(t, ln, append(proxyV2Header(src), "hello"...))
		if got := c.RemoteAddr().String(); got != src.String() {
			t.Fatalf("RemoteAddr = %s, want %s", got, src)
		}
		// The header is consumed; the stream starts at the client's first byte.
		buf := make([]byte, 5)
		if _, err := io.ReadFull(c, buf); err != nil || string(buf) != "hello" {
			t.Fatalf("stream after header: %q %v", buf, err)
		}
	})

	t.Run("untrusted", func(t *testing.T) {
		ln := NewProxyProtocolListener(raw, []string{"10.0.0.0/8"})
		payload := proxyV2Header(src)
		c, _ := acceptOne(t, ln, payload)
		if behindProxy(c) {
			t.Fatal("a peer outside the trusted set must not be parsed")
		}
		// A client sending a header gets its own bytes back as data, not an address.
		buf := make([]byte, len(payload))
		if _, err := io.ReadFull(c, buf); err != nil || !bytes.Equal(buf, payload) {
			t.Fatalf("untrusted bytes were altered: %v", err)
		}
	})

	t.Run("trusted without header", func(t *testing.T) {
		ln := NewProxyProtocolListener(raw, []string{"127.0.0.1"})
		c, client := acceptOne(t, ln, []byte("no header here, just data"))
		_ = client.Close()
		if err := proxyHeaderErr(c); err == nil {
			t.Fatal("a trusted peer without a header must be refused")
		}
	})
}

// The header is read below TLS, and seen through the TLS wrapper.
func TestProxyHeaderIsSeenThroughTLS(t *testing.T) {
	raw, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = raw.Close() }()
	ln := tls.NewListener(NewProxyProtocolListener(raw, []string{"127.0.0.1"}), &tls.Config{MinVersion: tls.VersionTLS13})
	src := &net.TCPAddr{IP: net.ParseIP("198.51.100.8"), Port: 8}
	c, _ := acceptOne(t, ln, proxyV2Header(src))
	if !behindProxy(c) {
		t.Fatal("behindProxy must look through *tls.Conn")
	}
	if got := c.RemoteAddr().String(); got != src.String() {
		t.Fatalf("RemoteAddr through TLS = %s, want %s", got, src)
	}
}

// Behind a TCP balancer every client arrives from the balancer's address. With
// a cap of one connection per IP, only the PROXY header lets a second client in.
func TestPerIPCapBehindProxyChargesTheClient(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	ids, _ := id.NewGenerator(9)
	st := memory.New().Stores()
	bus := eventbus.NewMemory()
	chatSvc := chat.New(st.Chats, ids)
	msgSvc := message.New(st.Messages, st.Reads, chatSvc, bus, ids)
	cfg := DefaultConfig()
	cfg.Heartbeat = time.Hour
	cfg.NodeID = "1"
	cfg.MaxConnsPerIP = 1
	cfg.TrustedProxies = []string{"127.0.0.1"}
	gw := New(Services{
		Auth: auth.New(st.Users, st.Sessions, ids), Chat: chatSvc, Msg: msgSvc,
		Broker:   message.NewBroker(msgSvc, log),
		Presence: presence.New(presence.NewMemoryBackend(), bus, time.Minute),
		Users:    st.Users, Hub: delivery.NewHub(), Bus: bus, Router: router.NewMemory(),
	}, cfg, log)

	raw, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = gw.ServeTCP(ctx, NewProxyProtocolListener(raw, cfg.TrustedProxies)) }()

	welcomed := func(src string) bool {
		c, err := net.Dial("tcp", raw.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = c.Close() })
		_, _ = c.Write(proxyV2Header(&net.TCPAddr{IP: net.ParseIP(src), Port: 1234}))
		wc := wire.NewConn(wire.NewTCPTransport(c), false)
		if err := wc.Send(wire.MsgHello, 1, 0, 1, wire.HelloBody{ClientVersion: "t", Platform: "cli"}); err != nil {
			return false
		}
		_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
		e, err := wc.ReadEnvelope()
		return err == nil && e.Type == wire.MsgWelcome
	}
	if !welcomed("203.0.113.1") || !welcomed("203.0.113.2") {
		t.Fatal("two clients behind one proxy were charged to the proxy's address")
	}
	if welcomed("203.0.113.1") {
		t.Fatal("the cap must still apply to each client's own address")
	}
}
