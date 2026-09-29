// Command e2epeer is a secret-chat peer built on the server's own pkg/e2e, for
// checking a client's X3DH + Double Ratchet against the Go implementation over a
// real gateway. It speaks the same envelope the clients do: SecretMsg's
// ratchet_header is JSON {"ik","ek","rh"} on a session's first message and {"rh"}
// after, where rh is the base64 of the ratchet header's wire bytes.
//
// Responder (default): publish a bundle with one-time prekeys, print
// "READY <user-id>:<device-id>", then decrypt every SECRET_RECV, print
// "PEER_DECRYPTED <plaintext>" and answer with -reply.
//
// Initiator (-initiate-to user:device): fetch that device's bundle, start a
// session, send -text, print "SENT" and exit.
//
// client/scripts/e2e-secret*.mts are the other half; server/scripts/secret-interop.sh
// runs both directions.
package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"strings"
	"time"

	"github.com/IR-Full/sync-app/server/pkg/e2e"
	"github.com/IR-Full/sync-app/server/pkg/wire"
)

const oneTimePreKeys = 8

// envelope is the X3DH bootstrap the clients put in SecretMsg.ratchet_header.
type envelope struct {
	IK string `json:"ik,omitempty"`
	EK string `json:"ek,omitempty"`
	RH string `json:"rh"`
}

type peer struct {
	conn     *wire.Conn
	seq, req uint64
	userID   string
	deviceID string
}

func main() {
	addr := flag.String("addr", "localhost:7000", "gateway TCP address")
	user := flag.String("user", "gopeer", "username to register (a timestamp is appended)")
	device := flag.String("device", "go-peer-device", "device id")
	initiateTo := flag.String("initiate-to", "", "user-id:device-id to start a session with (initiator mode)")
	text := flag.String("text", "initiated from Go", "plaintext to send in initiator mode")
	reply := flag.String("reply", "hello back from Go", "plaintext to answer with in responder mode")
	timeout := flag.Duration("timeout", 60*time.Second, "give up after this long")
	flag.Parse()
	log.SetFlags(0)

	p, err := connect(*addr, fmt.Sprintf("%s%d", *user, time.Now().UnixNano()), *device)
	if err != nil {
		log.Fatalf("connect: %v", err)
	}
	deadline := time.Now().Add(*timeout)
	if *initiateTo != "" {
		err = p.initiate(*initiateTo, *text, deadline)
	} else {
		err = p.respond(*reply, deadline)
	}
	if err != nil {
		log.Fatalf("e2epeer: %v", err)
	}
}

func connect(addr, username, deviceID string) (*peer, error) {
	nc, err := net.Dial("tcp", addr)
	if err != nil {
		return nil, err
	}
	p := &peer{conn: wire.NewConn(wire.NewTCPTransport(nc), false)}
	if _, err := p.call(wire.MsgHello, wire.HelloBody{ClientVersion: "e2epeer", Platform: "cli", DeviceID: deviceID}, wire.MsgWelcome, time.Now().Add(10*time.Second)); err != nil {
		return nil, err
	}
	e, err := p.call(wire.MsgAuth, wire.AuthBody{Username: username, Password: "correct horse battery", Register: true}, wire.MsgAuthOK, time.Now().Add(10*time.Second))
	if err != nil {
		return nil, err
	}
	var ok wire.AuthOKBody
	if err := wire.Unmarshal(e.Body, &ok); err != nil {
		return nil, err
	}
	p.userID, p.deviceID = ok.UserID, ok.DeviceID
	return p, nil
}

func (p *peer) send(typ wire.MsgType, body any) (uint64, error) {
	p.seq++
	p.req++
	return p.req, p.conn.Send(typ, p.seq, 0, p.req, body)
}

// read returns the next envelope, skipping heartbeats, or fails at deadline.
func (p *peer) read(deadline time.Time) (wire.Envelope, error) {
	for {
		_ = p.conn.SetReadDeadline(deadline)
		e, err := p.conn.ReadEnvelope()
		if err != nil {
			return e, err
		}
		if e.Type == wire.MsgPing {
			p.seq++
			_ = p.conn.Send(wire.MsgPong, p.seq, 0, e.RequestID, nil)
			continue
		}
		return e, nil
	}
}

// call sends a request and reads until its reply, failing on an ERROR for it.
func (p *peer) call(typ wire.MsgType, body any, want wire.MsgType, deadline time.Time) (wire.Envelope, error) {
	id, err := p.send(typ, body)
	if err != nil {
		return wire.Envelope{}, err
	}
	for {
		e, err := p.read(deadline)
		if err != nil {
			return e, fmt.Errorf("waiting for %s: %w", want, err)
		}
		if e.RequestID != id {
			continue
		}
		if e.Type == wire.MsgError {
			var eb wire.ErrorBody
			_ = wire.Unmarshal(e.Body, &eb)
			return e, fmt.Errorf("%s: server error %v: %s", typ, eb.Code, eb.Message)
		}
		if e.Type == want {
			return e, nil
		}
	}
}

func b64(b []byte) string { return base64.StdEncoding.EncodeToString(b) }

// identity is the responder's published key material.
type identity struct {
	ik, spk *e2e.KeyPair
	opks    []*e2e.KeyPair
}

func (p *peer) respond(reply string, deadline time.Time) error {
	id := identity{}
	var err error
	if id.ik, err = e2e.GenerateKeyPair(); err != nil {
		return err
	}
	if id.spk, err = e2e.GenerateKeyPair(); err != nil {
		return err
	}
	signPub, signPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	pub := wire.KeyPublishBody{
		IdentityKey: b64(id.ik.PublicBytes()), SigningKey: b64(signPub),
		SignedPreKey: b64(id.spk.PublicBytes()), SignedPreKeySig: b64(e2e.SignPreKey(signPriv, id.spk.PublicBytes())),
	}
	for i := 0; i < oneTimePreKeys; i++ {
		kp, err := e2e.GenerateKeyPair()
		if err != nil {
			return err
		}
		id.opks = append(id.opks, kp)
		pub.PreKeys = append(pub.PreKeys, b64(kp.PublicBytes()))
	}
	if _, err := p.send(wire.MsgKeyPublish, pub); err != nil {
		return err
	}
	fmt.Printf("READY %s:%s\n", p.userID, p.deviceID)

	sessions := map[string]*e2e.Session{}
	for {
		e, err := p.read(deadline)
		if err != nil {
			return err
		}
		if e.Type != wire.MsgSecretRecv {
			continue
		}
		var msg wire.SecretMsgBody
		if err := wire.Unmarshal(e.Body, &msg); err != nil {
			return err
		}
		headerJSON, cipher, ok := wire.SecretPayload(msg)
		if !ok {
			return errors.New("SECRET_RECV without a payload")
		}
		key := msg.FromUserID + "/" + msg.FromDeviceID
		plaintext, session, err := open(id, sessions[key], headerJSON, cipher)
		if err != nil {
			return fmt.Errorf("decrypt from %s: %w", key, err)
		}
		sessions[key] = session
		fmt.Printf("PEER_DECRYPTED %s\n", plaintext)

		hdr, ct, err := session.Encrypt([]byte(reply))
		if err != nil {
			return err
		}
		if err := p.sendSecret(msg.FromUserID, msg.FromDeviceID, envelope{RH: b64(e2e.MarshalHeader(hdr))}, ct); err != nil {
			return err
		}
	}
}

// open decrypts one inbound message, completing X3DH as responder on a
// session's first message. The directory hands out one of our one-time prekeys
// without saying which, so each is tried and the AEAD picks the right one.
func open(id identity, existing *e2e.Session, headerJSON, cipher []byte) ([]byte, *e2e.Session, error) {
	var env envelope
	if err := json.Unmarshal(headerJSON, &env); err != nil {
		return nil, nil, err
	}
	rh, err := base64.StdEncoding.DecodeString(env.RH)
	if err != nil {
		return nil, nil, err
	}
	hdr, err := e2e.UnmarshalHeader(rh)
	if err != nil {
		return nil, nil, err
	}
	if existing != nil {
		if pt, err := existing.Decrypt(hdr, cipher); err == nil {
			return pt, existing, nil
		}
	}
	ik, err1 := base64.StdEncoding.DecodeString(env.IK)
	ek, err2 := base64.StdEncoding.DecodeString(env.EK)
	if err1 != nil || err2 != nil || len(ik) == 0 {
		return nil, nil, errors.New("first message without an X3DH bootstrap")
	}
	candidates := append([]*e2e.KeyPair{nil}, id.opks...)
	for _, opk := range candidates {
		sk, err := e2e.X3DHResponder(e2e.ResponderKeys{Identity: id.ik, SignedPreKey: id.spk, OneTimePreKey: opk}, ik, ek, opk != nil)
		if err != nil {
			continue
		}
		session, err := e2e.NewResponderSession(sk, id.spk)
		if err != nil {
			return nil, nil, err
		}
		if pt, err := session.Decrypt(hdr, cipher); err == nil {
			return pt, session, nil
		}
	}
	return nil, nil, e2e.ErrDecrypt
}

func (p *peer) initiate(target, text string, deadline time.Time) error {
	userID, deviceID, ok := strings.Cut(target, ":")
	if !ok {
		return fmt.Errorf("-initiate-to %q: want user-id:device-id", target)
	}
	var bundle wire.KeyBundleBody
	for bundle.IdentityKey == "" {
		e, err := p.call(wire.MsgKeyFetch, wire.KeyFetchBody{UserID: userID, DeviceID: deviceID}, wire.MsgKeyBundle, deadline)
		if err != nil {
			return err
		}
		if err := wire.Unmarshal(e.Body, &bundle); err != nil {
			return err
		}
		if bundle.IdentityKey == "" {
			if time.Now().After(deadline) {
				return errors.New("the peer never published a bundle")
			}
			time.Sleep(250 * time.Millisecond)
		}
	}
	dec := func(s string) []byte { b, _ := base64.StdEncoding.DecodeString(s); return b }
	ik, err := e2e.GenerateKeyPair()
	if err != nil {
		return err
	}
	ek, err := e2e.GenerateKeyPair()
	if err != nil {
		return err
	}
	sk, ekPub, err := e2e.X3DHInitiator(e2e.InitiatorKeys{Identity: ik, Ephemeral: ek}, e2e.PreKeyBundle{
		IdentityKey: dec(bundle.IdentityKey), SigningKey: dec(bundle.SigningKey),
		SignedPreKey: dec(bundle.SignedPreKey), SignedPreKeySig: dec(bundle.SignedPreKeySig),
		OneTimePreKey: dec(bundle.OneTimePreKey),
	})
	if err != nil {
		return err
	}
	session, err := e2e.NewInitiatorSession(sk, dec(bundle.SignedPreKey))
	if err != nil {
		return err
	}
	hdr, ct, err := session.Encrypt([]byte(text))
	if err != nil {
		return err
	}
	env := envelope{IK: b64(ik.PublicBytes()), EK: b64(ekPub), RH: b64(e2e.MarshalHeader(hdr))}
	if err := p.sendSecret(userID, deviceID, env, ct); err != nil {
		return err
	}
	fmt.Println("SENT")
	// Give the relay a moment before closing the connection under it.
	time.Sleep(500 * time.Millisecond)
	return nil
}

func (p *peer) sendSecret(userID, deviceID string, env envelope, cipher []byte) error {
	header, err := json.Marshal(env)
	if err != nil {
		return err
	}
	msg := wire.SecretMsgBody{ToUserID: userID, ToDeviceID: deviceID}
	wire.SetSecretPayloadLegacy(&msg, header, cipher)
	_, err = p.send(wire.MsgSecretSend, msg)
	return err
}
