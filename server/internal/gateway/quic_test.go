package gateway_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"io"
	"log/slog"
	"math/big"
	"net"
	"testing"
	"time"

	"github.com/quic-go/quic-go"
	"github.com/synapse-chat/synapse/internal/auth"
	"github.com/synapse-chat/synapse/internal/chat"
	"github.com/synapse-chat/synapse/internal/delivery"
	"github.com/synapse-chat/synapse/internal/fanout"
	"github.com/synapse-chat/synapse/internal/gateway"
	"github.com/synapse-chat/synapse/internal/keydir"
	"github.com/synapse-chat/synapse/internal/message"
	"github.com/synapse-chat/synapse/internal/outbox"
	"github.com/synapse-chat/synapse/internal/presence"
	"github.com/synapse-chat/synapse/internal/router"
	"github.com/synapse-chat/synapse/internal/store/memory"
	"github.com/synapse-chat/synapse/pkg/eventbus"
	"github.com/synapse-chat/synapse/pkg/id"
	"github.com/synapse-chat/synapse/pkg/wire"
)

// TestQUICEndToEnd proves the binary protocol works over QUIC: a client dials via
// QUIC, opens one stream, and completes handshake → auth → send → ack.
func TestQUICEndToEnd(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	ids, _ := id.NewGenerator(1)
	st := memory.New().Stores()
	bus := eventbus.NewMemory()
	rtr := router.NewMemory()

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go outbox.New(st.Outbox, bus, log).Run(ctx)

	chatSvc := chat.New(st.Chats, ids)
	msgSvc := message.New(st.Messages, st.Reads, chatSvc, bus, ids)
	fan := fanout.New(bus, chatSvc, rtr, log)
	if err := fan.Start(); err != nil {
		t.Fatal(err)
	}
	cfg := gateway.DefaultConfig()
	cfg.Heartbeat = time.Hour
	cfg.NodeID = "1"
	gw := gateway.New(gateway.Services{
		Auth: auth.New(st.Users, st.Sessions, ids), Chat: chatSvc, Msg: msgSvc,
		Broker: message.NewBroker(msgSvc, log), Presence: presence.New(presence.NewMemoryBackend(), bus, time.Minute),
		Users: st.Users, Hub: delivery.NewHub(), Bus: bus, Router: rtr, KeyDir: keydir.NewMemory(),
	}, cfg, log)
	if err := gw.StartDelivery(); err != nil {
		t.Fatal(err)
	}

	// QUIC listener on an ephemeral UDP port.
	ln, err := gateway.ListenQUIC("127.0.0.1:0", selfSignedTLS(t), cfg)
	if err != nil {
		t.Fatalf("listen quic: %v", err)
	}
	go gw.ServeQUICListener(ctx, ln)
	addr := ln.Addr().String()

	// QUIC client: dial, open a stream, run the protocol over it.
	dialCtx, dialCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer dialCancel()
	qconn, err := quic.DialAddr(dialCtx, addr,
		&tls.Config{InsecureSkipVerify: true, NextProtos: []string{gateway.QUICALPN}},
		&quic.Config{KeepAlivePeriod: time.Second})
	if err != nil {
		t.Fatalf("quic dial: %v", err)
	}
	stream, err := qconn.OpenStreamSync(dialCtx)
	if err != nil {
		t.Fatalf("open stream: %v", err)
	}
	conn := wire.NewConn(wire.NewStreamTransport(stream), false)

	var seq uint64
	send := func(typ wire.MsgType, body any) {
		seq++
		if err := conn.Send(typ, seq, 0, seq, body); err != nil {
			t.Fatal(err)
		}
	}
	send(wire.MsgHello, wire.HelloBody{ClientVersion: "quic-test", Platform: "cli"})
	if e, _ := conn.ReadEnvelope(); e.Type != wire.MsgWelcome {
		t.Fatalf("want WELCOME got %s", e.Type)
	}
	send(wire.MsgAuth, wire.AuthBody{Username: "quicuser", Password: "secret123", Register: true})
	if e, _ := conn.ReadEnvelope(); e.Type != wire.MsgAuthOK {
		t.Fatalf("want AUTH_OK got %s", e.Type)
	}
	// Send to self-chat and expect an ack over QUIC.
	send(wire.MsgSend, wire.SendBody{ChatID: "@quicuser", DedupKey: "q1", Text: "over quic"})
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		e, err := conn.ReadEnvelope()
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		if e.Type == wire.MsgSendAck {
			var ack wire.SendAckBody
			_ = wire.Unmarshal(e.Body, &ack)
			if ack.MessageID == "" {
				t.Fatal("empty ack")
			}
			return // success
		}
	}
	t.Fatal("no SEND_ACK over QUIC")
}

// selfSignedTLS returns a server TLS config with an ephemeral cert for the test.
func selfSignedTLS(t *testing.T) *tls.Config {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &priv.PublicKey, priv)
	if err != nil {
		t.Fatal(err)
	}
	return &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: priv}}}
}
