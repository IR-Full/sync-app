// Command client is an interactive terminal client that speaks the Synapse
// binary protocol. It demonstrates the full handshake → auth → send/receive
// lifecycle over raw TCP (default) or WebSocket.
//
// Usage:
//
//	client -addr localhost:7000 -user alice -pass secret123
//	client -ws ws://localhost:8080/ws -user bob -pass secret123
//
// The full command set is printed on connect, grouped by area. The core ones:
//
//	/to @bob            set the current target to a direct chat with @bob
//	/to <chatID>        set the current target to an existing chat id
//	<any text>          send text to the current target
//	/hist [n]           fetch the last n messages of the current chat
//	/read <seq>         mark the current chat read up to <seq>
//	/typing             send a typing indicator to the current chat
//	/quit               disconnect and exit
//
// Most commands act on the current target, so they read as "do this here". The
// exceptions are /join (joining is how you GET a target) and the membership
// commands, which need a concrete chat id rather than an @user direct chat.
package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"flag"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"github.com/quic-go/quic-go"
	"github.com/synapse-chat/synapse/pkg/wire"
)

func main() {
	addr := flag.String("addr", "localhost:7000", "gateway TCP address")
	wsURL := flag.String("ws", "", "gateway WebSocket URL (overrides -addr, e.g. ws://localhost:8080/ws)")
	user := flag.String("user", "", "username")
	pass := flag.String("pass", "", "password")
	register := flag.Bool("register", false, "create the account instead of logging in")
	deviceID := flag.String("device", "", "device id (blank = server-assigned)")
	useTLS := flag.Bool("tls", false, "connect over TLS (raw-TCP path)")
	useQUIC := flag.Bool("quic", false, "connect over QUIC (implies TLS; use -insecure for self-signed)")
	insecure := flag.Bool("insecure", false, "skip TLS certificate verification (self-signed dev servers)")
	flag.Parse()

	if *user == "" || *pass == "" {
		fmt.Println("usage: client -user <name> -pass <pass> [-addr host:port | -ws ws://...]")
		os.Exit(2)
	}

	cl, err := dial(*wsURL, *addr, *useTLS, *insecure, *useQUIC)
	if err != nil {
		fmt.Println("connect:", err)
		os.Exit(1)
	}
	defer cl.conn.Close()

	if err := cl.handshake(*deviceID); err != nil {
		fmt.Println("handshake:", err)
		os.Exit(1)
	}
	if err := cl.authenticate(*user, *pass, *register); err != nil {
		fmt.Println("auth:", err)
		os.Exit(1)
	}
	fmt.Printf("connected as %s (user %s)\n", *user, cl.userID)
	fmt.Println("commands:")
	fmt.Println("  chat        /to @user|<chatID> | <text> | /hist [n] | /read <seq> | /typing | /search <q>")
	fmt.Println("  message     /react <msgID> <emoji> | /thread <msgID> | /forward <msgID> <chat> | /upload <path>")
	fmt.Println("  timed       /ttl <seconds> | /schedule <+2h|RFC3339> <text> | /scheduled | /unschedule <id>")
	fmt.Println("  chat state  /pin <msgID> | /unpin <msgID> | /pins | /draft [text] | /drafts")
	fmt.Println("  polls       /poll Q|A|B | /vote <pollID> <n>")
	fmt.Println("  people      /contact @user [name] | /contacts | /block @user | /unblock @user")
	fmt.Println("  membership  /group <title> [@user...] | /channel <title> [@user...]")
	fmt.Println("              /handle <name|-> | /invite [uses] [+24h] | /invites | /revoke <code>")
	fmt.Println("              /join <code|@handle> | /role <userID> <member|admin|owner>")
	fmt.Println("  calls       /call [audio|video] | /accept | /decline | /hangup")
	fmt.Println("  identity    /chats [cursor] | /me | /who @user|<userID> | /name <display name>")
	fmt.Println("  other       /export <chatID> | /quit")

	cl.activeCall.Store("")
	cl.contactCursor.Store(int64(0))
	cl.draftCursor.Store(int64(0))
	go cl.readLoop()
	cl.inputLoop()
}

type client struct {
	conn          *wire.Conn
	userID        string
	seq           atomic.Uint64
	reqID         atomic.Uint64
	target        atomic.Value          // current chat target (string)
	pendingUpload atomic.Value          // path of a file awaiting a MediaTicket
	exportMsgs    []wire.NewMessageBody // accumulates streamed export pages
	contactCursor atomic.Value          // incremental contact-sync high-water mark
	draftCursor   atomic.Value          // incremental draft-sync high-water mark
	activeCall    atomic.Value          // current call id ("" when none)
	ttl           atomic.Int32          // self-destruct seconds applied to sends (0 = off)
	cancelReq     atomic.Uint64         // request id of the last /unschedule, to read its reply
}

func dial(wsURL, tcpAddr string, useTLS, insecure, useQUIC bool) (*client, error) {
	if useQUIC {
		qconn, err := quic.DialAddr(context.Background(), tcpAddr,
			// #nosec G402 -- InsecureSkipVerify is gated behind the client's explicit
			// opt-in -insecure flag, for connecting to dev self-signed servers only.
			&tls.Config{InsecureSkipVerify: insecure, NextProtos: []string{wire.QUICALPN}, MinVersion: tls.VersionTLS13},
			&quic.Config{KeepAlivePeriod: 15 * time.Second})
		if err != nil {
			return nil, err
		}
		stream, err := qconn.OpenStreamSync(context.Background())
		if err != nil {
			return nil, err
		}
		return &client{conn: wire.NewConn(wire.NewStreamTransport(stream), true)}, nil
	}
	if wsURL != "" {
		u, err := url.Parse(wsURL)
		if err != nil {
			return nil, err
		}
		dialer := *websocket.DefaultDialer
		if insecure {
			// #nosec G402 -- opt-in -insecure flag, dev self-signed servers only.
			dialer.TLSClientConfig = &tls.Config{InsecureSkipVerify: true, MinVersion: tls.VersionTLS13}
		}
		wc, _, err := dialer.Dial(u.String(), nil)
		if err != nil {
			return nil, err
		}
		return &client{conn: wire.NewConn(wire.NewWSTransport(wc), true)}, nil
	}
	var (
		c   net.Conn
		err error
	)
	if useTLS {
		// #nosec G402 -- opt-in -insecure flag, dev self-signed servers only.
		c, err = tls.DialWithDialer(&net.Dialer{Timeout: 5 * time.Second}, "tcp", tcpAddr,
			&tls.Config{InsecureSkipVerify: insecure, MinVersion: tls.VersionTLS13})
	} else {
		c, err = net.DialTimeout("tcp", tcpAddr, 5*time.Second)
	}
	if err != nil {
		return nil, err
	}
	return &client{conn: wire.NewConn(wire.NewTCPTransport(c), true)}, nil
}

func (c *client) nextSeq() uint64   { return c.seq.Add(1) }
func (c *client) nextReqID() uint64 { return c.reqID.Add(1) }

func (c *client) handshake(deviceID string) error {
	if err := c.conn.Send(wire.MsgHello, c.nextSeq(), 0, c.nextReqID(), wire.HelloBody{
		ClientVersion: "cli/0.1",
		DeviceID:      deviceID,
		Platform:      "cli",
		Caps:          wire.CapCompression | wire.CapZstd | wire.CapResume | wire.CapTypingSignals,
	}); err != nil {
		return err
	}
	e, err := c.conn.ReadEnvelope()
	if err != nil {
		return err
	}
	if e.Type != wire.MsgWelcome {
		return fmt.Errorf("expected WELCOME, got %s", e.Type)
	}
	var w wire.WelcomeBody
	_ = wire.Unmarshal(e.Body, &w)
	if w.Caps&wire.CapZstd != 0 {
		c.conn.SetZstd(true)
	} else if w.Caps&wire.CapCompression != 0 {
		c.conn.SetCompression(true)
	}
	return nil
}

func (c *client) authenticate(user, pass string, register bool) error {
	if err := c.conn.Send(wire.MsgAuth, c.nextSeq(), 0, c.nextReqID(), wire.AuthBody{
		Username: user, Password: pass, Register: register,
	}); err != nil {
		return err
	}
	e, err := c.conn.ReadEnvelope()
	if err != nil {
		return err
	}
	if e.Type == wire.MsgError || e.Type == wire.MsgAuthErr {
		var eb wire.ErrorBody
		_ = wire.Unmarshal(e.Body, &eb)
		return fmt.Errorf("rejected: %s", eb.Message)
	}
	if e.Type != wire.MsgAuthOK {
		return fmt.Errorf("expected AUTH_OK, got %s", e.Type)
	}
	var ok wire.AuthOKBody
	_ = wire.Unmarshal(e.Body, &ok)
	c.userID = ok.UserID
	return nil
}

// readLoop prints server pushes and answers pings.
func (c *client) readLoop() {
	for {
		e, err := c.conn.ReadEnvelope()
		if err != nil {
			fmt.Println("\n[disconnected]", err)
			os.Exit(0)
		}
		switch e.Type {
		case wire.MsgPing:
			_ = c.conn.Send(wire.MsgPong, c.nextSeq(), e.Seq, e.RequestID, nil)
		case wire.MsgNew:
			var m wire.NewMessageBody
			_ = wire.Unmarshal(e.Body, &m)
			printMessage(m)
		case wire.MsgSendAck:
			var a wire.SendAckBody
			_ = wire.Unmarshal(e.Body, &a)
			dup := ""
			if a.Duplicate {
				dup = " (dup)"
			}
			fmt.Printf("  ✓ sent id=%s seq=%d%s\n", a.MessageID, a.ChatSeq, dup)
		case wire.MsgReadUpd:
			var r wire.ReadUpdateBody
			_ = wire.Unmarshal(e.Body, &r)
			fmt.Printf("  ✓✓ %s read up to seq %d in %s\n", r.UserID, r.UpToChatSeq, r.ChatID)
		case wire.MsgTyping:
			var t wire.TypingBody
			_ = wire.Unmarshal(e.Body, &t)
			if t.Active {
				fmt.Printf("  … %s is typing in %s\n", t.UserID, t.ChatID)
			}
		case wire.MsgContactList:
			var l wire.ContactListBody
			_ = wire.Unmarshal(e.Body, &l)
			c.printContacts(l)
		case wire.MsgPollState:
			var p wire.PollStateBody
			_ = wire.Unmarshal(e.Body, &p)
			printPoll(p)
		case wire.MsgCallState:
			var s wire.CallStateBody
			_ = wire.Unmarshal(e.Body, &s)
			c.printCallState(s)
		case wire.MsgCallSignal:
			var sg wire.CallSignalBody
			_ = wire.Unmarshal(e.Body, &sg)
			fmt.Printf("  ☎ signal %s from %s (%d bytes) — hand to WebRTC\n", sg.SignalType, sg.FromUserID, len(sg.Payload))
		case wire.MsgReactUpd:
			var r wire.ReactUpdateBody
			_ = wire.Unmarshal(e.Body, &r)
			verb := "removed"
			if r.Added {
				verb = "reacted"
			}
			fmt.Printf("  %s %s %s to %s %s\n", r.Emoji, r.UserID, verb, r.MessageID, formatCounts(r.Counts))
		case wire.MsgPresence:
			var p wire.PresenceBody
			_ = wire.Unmarshal(e.Body, &p)
			state := "offline"
			if p.Online {
				state = "online"
			}
			fmt.Printf("  • %s is %s\n", p.UserID, state)
		case wire.MsgScheduled:
			var s wire.ScheduledBody
			_ = wire.Unmarshal(e.Body, &s)
			c.printScheduled(s, e.RequestID)
		case wire.MsgPinned:
			var p wire.PinnedBody
			_ = wire.Unmarshal(e.Body, &p)
			printPinned(p)
		case wire.MsgDrafts:
			var d wire.DraftsBody
			_ = wire.Unmarshal(e.Body, &d)
			c.printDrafts(d)
		case wire.MsgChatInfo:
			var ci wire.ChatInfoBody
			_ = wire.Unmarshal(e.Body, &ci)
			c.target.Store(ci.ChatID)
			fmt.Printf("  ✨ created %s %q as %s (target set)\n", ci.Type, ci.Title, ci.ChatID)
		case wire.MsgInvites:
			var inv wire.InvitesBody
			_ = wire.Unmarshal(e.Body, &inv)
			c.printInvites(inv)
		case wire.MsgChats:
			var ch wire.ChatsBody
			_ = wire.Unmarshal(e.Body, &ch)
			printChats(ch)
		case wire.MsgProfile:
			var p wire.ProfileBody
			_ = wire.Unmarshal(e.Body, &p)
			printProfile(p)
		case wire.MsgThreadOK:
			fmt.Println("  --- end of thread ---")
		case wire.MsgHistoryOK:
			fmt.Println("  --- end of history ---")
		case wire.MsgSearchResults:
			var r wire.SearchResultsBody
			_ = wire.Unmarshal(e.Body, &r)
			fmt.Printf("  search %q: %d hit(s)\n", r.Query, len(r.Hits))
			for _, h := range r.Hits {
				fmt.Printf("    %s#%d %s: %s\n", h.ChatID, h.Seq, h.SenderID, h.Text)
			}
		case wire.MsgChatExportResult:
			var x wire.ChatExportResultBody
			_ = wire.Unmarshal(e.Body, &x)
			c.handleExportPage(x)
		case wire.MsgMediaTicket:
			var mt wire.MediaTicketBody
			_ = wire.Unmarshal(e.Body, &mt)
			c.finishUpload(mt)
		case wire.MsgMediaURL:
			var mu wire.MediaURLBody
			_ = wire.Unmarshal(e.Body, &mu)
			fmt.Printf("  download %s: %s\n", mu.MediaRef, mu.DownloadURL)
		case wire.MsgSecretRecv:
			var sm wire.SecretMsgBody
			_ = wire.Unmarshal(e.Body, &sm)
			fmt.Printf("  🔒 secret ciphertext from %s/%s (%d bytes, server can't read)\n",
				sm.FromUserID, sm.FromDeviceID, len(sm.Ciphertext))
		case wire.MsgError:
			var eb wire.ErrorBody
			_ = wire.Unmarshal(e.Body, &eb)
			fmt.Printf("  ! error %d: %s\n", eb.Code, eb.Message)
		}
	}
}

func printMessage(m wire.NewMessageBody) {
	ts := time.UnixMilli(m.Timestamp).Format("15:04:05")
	switch {
	case m.Deleted:
		fmt.Printf("[%s] %s#%d %s: (deleted)\n", ts, m.ChatID, m.ChatSeq, m.SenderID)
	case m.Edited:
		fmt.Printf("[%s] %s#%d %s: %s (edited)%s\n", ts, m.ChatID, m.ChatSeq, m.SenderID, m.Text, expiryNote(m))
	default:
		fmt.Printf("[%s] %s#%d %s: %s%s%s\n", ts, m.ChatID, m.ChatSeq, m.SenderID, m.Text, forwardNote(m), expiryNote(m))
	}
}

// forwardNote renders a forwarded message's provenance. It is a snapshot taken
// at forward time, not a live reference — so it is shown as origin, not a link.
func forwardNote(m wire.NewMessageBody) string {
	if m.Forward == nil {
		return ""
	}
	return fmt.Sprintf(" ↪ from %s in %s", m.Forward.SenderID, m.Forward.ChatID)
}

// expiryNote shows the self-destruct deadline of a message that has one.
func expiryNote(m wire.NewMessageBody) string {
	if m.ExpiresAt == 0 {
		return ""
	}
	return fmt.Sprintf(" ⏱ expires %s", time.UnixMilli(m.ExpiresAt).Format("15:04:05"))
}

// inputLoop reads stdin and turns lines into protocol messages.
func (c *client) inputLoop() {
	sc := bufio.NewScanner(os.Stdin)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		switch {
		case line == "/quit":
			return
		case strings.HasPrefix(line, "/to "):
			c.target.Store(strings.TrimSpace(line[4:]))
			fmt.Println("  target set:", c.target.Load())
		case strings.HasPrefix(line, "/hist"):
			c.doHistory(line)
		case strings.HasPrefix(line, "/read "):
			c.doRead(line)
		case strings.HasPrefix(line, "/search "):
			c.doSearch(strings.TrimSpace(line[8:]))
		case strings.HasPrefix(line, "/export "):
			c.doExport(strings.TrimSpace(line[8:]))
		case strings.HasPrefix(line, "/upload "):
			c.doUpload(strings.TrimSpace(line[8:]))
		case strings.HasPrefix(line, "/react "):
			c.doReact(strings.TrimSpace(line[7:]))
		case strings.HasPrefix(line, "/contact "):
			c.doContact(strings.TrimSpace(line[9:]))
		case line == "/contacts":
			c.doContacts()
		case strings.HasPrefix(line, "/block "):
			c.doBlock(strings.TrimSpace(line[7:]), true)
		case strings.HasPrefix(line, "/unblock "):
			c.doBlock(strings.TrimSpace(line[9:]), false)
		case strings.HasPrefix(line, "/poll "):
			c.doPoll(strings.TrimSpace(line[6:]))
		case strings.HasPrefix(line, "/vote "):
			c.doVote(strings.TrimSpace(line[6:]))
		case strings.HasPrefix(line, "/thread "):
			c.doThread(strings.TrimSpace(line[8:]))
		case strings.HasPrefix(line, "/forward "):
			c.doForward(strings.TrimSpace(line[9:]))
		case strings.HasPrefix(line, "/ttl"):
			c.doTTL(strings.TrimSpace(strings.TrimPrefix(line, "/ttl")))
		case strings.HasPrefix(line, "/schedule "):
			c.doSchedule(strings.TrimSpace(line[10:]))
		case line == "/scheduled":
			c.doScheduled()
		case strings.HasPrefix(line, "/unschedule "):
			c.doUnschedule(strings.TrimSpace(line[12:]))
		case strings.HasPrefix(line, "/pin "):
			c.doPin(strings.TrimSpace(line[5:]), false)
		case strings.HasPrefix(line, "/unpin "):
			c.doPin(strings.TrimSpace(line[7:]), true)
		case line == "/pins":
			c.doPins()
		case strings.HasPrefix(line, "/draft "):
			c.doDraft(strings.TrimSpace(line[7:]))
		case line == "/draft":
			c.doDraft("")
		case line == "/drafts":
			c.doDrafts()
		case strings.HasPrefix(line, "/group "):
			c.doCreateChat("group", strings.TrimSpace(line[7:]))
		case strings.HasPrefix(line, "/channel "):
			c.doCreateChat("channel", strings.TrimSpace(line[9:]))
		case strings.HasPrefix(line, "/handle "):
			c.doHandle(strings.TrimSpace(line[8:]))
		case line == "/invites":
			c.doInvites()
		case strings.HasPrefix(line, "/invite"):
			c.doInvite(strings.TrimSpace(strings.TrimPrefix(line, "/invite")))
		case strings.HasPrefix(line, "/revoke "):
			c.doRevoke(strings.TrimSpace(line[8:]))
		case strings.HasPrefix(line, "/join "):
			c.doJoin(strings.TrimSpace(line[6:]))
		case strings.HasPrefix(line, "/role "):
			c.doRole(strings.TrimSpace(line[6:]))
		case strings.HasPrefix(line, "/call"):
			c.doCall(strings.TrimSpace(strings.TrimPrefix(line, "/call")))
		case line == "/accept":
			c.doCallAction(wire.MsgCallAccept, "")
		case line == "/decline":
			c.doCallAction(wire.MsgCallDecline, "")
		case line == "/hangup":
			c.doCallAction(wire.MsgCallHangup, "")
		case line == "/typing":
			c.doTyping()
		case strings.HasPrefix(line, "/chats"):
			c.doChats(strings.TrimSpace(strings.TrimPrefix(line, "/chats")))
		case strings.HasPrefix(line, "/who "):
			c.doProfileGet(strings.TrimSpace(strings.TrimPrefix(line, "/who ")))
		case line == "/me":
			c.doProfileGet("")
		case strings.HasPrefix(line, "/name "):
			c.doProfileSet(strings.TrimSpace(strings.TrimPrefix(line, "/name ")))
		case strings.HasPrefix(line, "/"):
			fmt.Println("  unknown command")
		default:
			c.doSend(line)
		}
	}
}

func (c *client) currentTarget() (string, bool) {
	v := c.target.Load()
	if v == nil {
		fmt.Println("  no target — use /to @user or /to <chatID>")
		return "", false
	}
	return v.(string), true
}

func (c *client) doSend(text string) {
	target, ok := c.currentTarget()
	if !ok {
		return
	}
	_ = c.conn.Send(wire.MsgSend, c.nextSeq(), 0, c.nextReqID(), wire.SendBody{
		ChatID:     target,
		DedupKey:   randKey(),
		Text:       text,
		TTLSeconds: c.ttl.Load(),
	})
}

func (c *client) doHistory(line string) {
	target, ok := c.currentTarget()
	if !ok {
		return
	}
	limit := 20
	if f := strings.Fields(line); len(f) > 1 {
		if n, err := strconv.Atoi(f[1]); err == nil {
			limit = n
		}
	}
	_ = c.conn.Send(wire.MsgHistory, c.nextSeq(), 0, c.nextReqID(), wire.HistoryBody{
		ChatID: target, Limit: limit,
	})
}

func (c *client) doRead(line string) {
	target, ok := c.currentTarget()
	if !ok {
		return
	}
	f := strings.Fields(line)
	if len(f) < 2 {
		fmt.Println("  usage: /read <seq>")
		return
	}
	seq, _ := strconv.ParseUint(f[1], 10, 64)
	_ = c.conn.Send(wire.MsgRead, c.nextSeq(), 0, c.nextReqID(), wire.ReadBody{
		ChatID: target, UpToChatSeq: seq,
	})
}

func (c *client) doTyping() {
	target, ok := c.currentTarget()
	if !ok {
		return
	}
	_ = c.conn.Send(wire.MsgTyping, c.nextSeq(), 0, 0, wire.TypingBody{ChatID: target, Active: true})
}

// doExport requests a streamed dump of a chat (owner/admin only).
func (c *client) doExport(chatID string) {
	if chatID == "" {
		fmt.Println("  usage: /export <chatID>")
		return
	}
	_ = c.conn.Send(wire.MsgChatExport, c.nextSeq(), 0, c.nextReqID(), wire.ChatExportBody{ChatID: chatID})
}

// handleExportPage accumulates streamed export pages, printing the full sorted
// dump once the final (Done) frame arrives.
func (c *client) handleExportPage(x wire.ChatExportResultBody) {
	if len(x.Members) > 0 || (len(x.Messages) == 0 && !x.Done) {
		fmt.Printf("  === export of chat %s (%s) \"%s\" owner=%s ===\n", x.ChatID, x.Type, x.Title, x.OwnerID)
		for _, m := range x.Members {
			fmt.Printf("    member: %s (%s)\n", m.UserID, m.Role)
		}
	}
	c.exportMsgs = append(c.exportMsgs, x.Messages...)
	if x.Done {
		sort.Slice(c.exportMsgs, func(i, j int) bool { return c.exportMsgs[i].ChatSeq < c.exportMsgs[j].ChatSeq })
		fmt.Printf("  messages (%d):\n", len(c.exportMsgs))
		for _, m := range c.exportMsgs {
			fmt.Printf("    #%d %s: %s\n", m.ChatSeq, m.SenderID, m.Text)
		}
		fmt.Println("  === end of export ===")
		c.exportMsgs = nil
	}
}

func (c *client) doSearch(query string) {
	if query == "" {
		fmt.Println("  usage: /search <text>")
		return
	}
	_ = c.conn.Send(wire.MsgSearch, c.nextSeq(), 0, c.nextReqID(), wire.SearchBody{Query: query, Limit: 20})
}

// doUpload begins a media upload: request a ticket, remember the file, and let
// the read loop complete the HTTP PUT when the ticket arrives.
func (c *client) doUpload(path string) {
	fi, err := os.Stat(path)
	if err != nil {
		fmt.Println("  cannot stat file:", err)
		return
	}
	if _, ok := c.currentTarget(); !ok {
		return
	}
	c.pendingUpload.Store(path)
	_ = c.conn.Send(wire.MsgMediaInit, c.nextSeq(), 0, c.nextReqID(), wire.MediaInitBody{
		Filename: filepath.Base(path), ContentType: "application/octet-stream", Size: fi.Size(),
	})
}

// finishUpload PUTs the pending file to the signed URL, then posts a message
// referencing it to the current target.
func (c *client) finishUpload(mt wire.MediaTicketBody) {
	v := c.pendingUpload.Load()
	if v == nil {
		return
	}
	path := v.(string)
	c.pendingUpload.Store("")
	data, err := os.ReadFile(path) // #nosec G304 -- CLI user chose this upload path themselves
	if err != nil {
		fmt.Println("  read file:", err)
		return
	}
	req, _ := http.NewRequest(http.MethodPut, mt.UploadURL, bytes.NewReader(data))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fmt.Println("  upload failed:", err)
		return
	}
	_ = resp.Body.Close()
	if resp.StatusCode >= 300 {
		fmt.Println("  upload rejected:", resp.Status)
		return
	}
	fmt.Printf("  ↑ uploaded %s as %s\n", filepath.Base(path), mt.MediaRef)
	if target, ok := c.currentTarget(); ok {
		_ = c.conn.Send(wire.MsgSend, c.nextSeq(), 0, c.nextReqID(), wire.SendBody{
			ChatID: target, DedupKey: randKey(), Text: "[file] " + filepath.Base(path), MediaRef: mt.MediaRef,
		})
	}
}

// randKey is a client-side idempotency key for a send.
func randKey() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// doReact toggles an emoji reaction on a message: /react <messageID> <emoji>
func (c *client) doReact(arg string) {
	target, ok := c.currentTarget()
	if !ok {
		return
	}
	parts := strings.Fields(arg)
	if len(parts) != 2 {
		fmt.Println("  usage: /react <messageID> <emoji>")
		return
	}
	_ = c.conn.Send(wire.MsgReact, c.nextSeq(), 0, 0, wire.ReactBody{
		ChatID: target, MessageID: parts[0], Emoji: parts[1],
	})
}

// formatCounts renders the per-emoji tally, e.g. "[👍×2 🎉×1]".
func formatCounts(counts map[string]int) string {
	if len(counts) == 0 {
		return ""
	}
	keys := make([]string, 0, len(counts))
	for k := range counts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteString("[")
	for i, k := range keys {
		if i > 0 {
			b.WriteString(" ")
		}
		fmt.Fprintf(&b, "%s×%d", k, counts[k])
	}
	b.WriteString("]")
	return b.String()
}

// doCall starts a call in the current chat: /call [audio|video]
func (c *client) doCall(kind string) {
	target, ok := c.currentTarget()
	if !ok {
		return
	}
	if kind == "" {
		kind = "audio"
	}
	_ = c.conn.Send(wire.MsgCallInvite, c.nextSeq(), 0, c.nextReqID(), wire.CallInviteBody{ChatID: target, Kind: kind})
}

// doCallAction accepts/declines/hangs up the current call.
func (c *client) doCallAction(t wire.MsgType, callID string) {
	if callID == "" {
		callID = c.activeCall.Load().(string)
	}
	if callID == "" {
		fmt.Println("  no active call")
		return
	}
	_ = c.conn.Send(t, c.nextSeq(), 0, c.nextReqID(), wire.CallActionBody{CallID: callID})
}

// printCallState renders a call room update and tracks the active call id so the
// user can /accept or /hangup without typing it.
func (c *client) printCallState(s wire.CallStateBody) {
	if s.State == "ended" {
		c.activeCall.Store("")
	} else {
		c.activeCall.Store(s.CallID)
	}
	var roster []string
	for _, p := range s.Participants {
		roster = append(roster, fmt.Sprintf("%s:%s", p.UserID, p.State))
	}
	fmt.Printf("  ☎ call %s [%s %s] %s\n", s.CallID, s.Kind, s.State, strings.Join(roster, " "))
	if s.State == "ringing" {
		fmt.Println("    /accept | /decline")
	}
}

// doThread fetches a message's reply branch: /thread <rootMessageID>
func (c *client) doThread(rootID string) {
	target, ok := c.currentTarget()
	if !ok {
		return
	}
	if rootID == "" {
		fmt.Println("  usage: /thread <messageID>")
		return
	}
	_ = c.conn.Send(wire.MsgThread, c.nextSeq(), 0, c.nextReqID(),
		wire.ThreadBody{ChatID: target, RootID: rootID, Limit: 50})
}

// doForward copies a message out of the current chat into another one:
// /forward <messageID> <chatID|@user>. The source is the current target, so a
// forward reads as "take this, put it there" from where the user already is.
func (c *client) doForward(arg string) {
	from, ok := c.currentTarget()
	if !ok {
		return
	}
	f := strings.Fields(arg)
	if len(f) != 2 {
		fmt.Println("  usage: /forward <messageID> <chatID|@user>")
		return
	}
	_ = c.conn.Send(wire.MsgForward, c.nextSeq(), 0, c.nextReqID(), wire.ForwardBody{
		FromChatID: from, MessageID: f[0], ToChatID: f[1], DedupKey: randKey(),
	})
}

// doTTL sets the self-destruct lifetime stamped on everything this client sends
// from now on (0 = never). It is a session mode rather than a per-message flag
// because that is how disappearing messages actually work: you turn them on for
// a conversation, not for one line. It also applies to /schedule, so a deferred
// message can expire after it lands.
func (c *client) doTTL(arg string) {
	if arg == "" {
		if n := c.ttl.Load(); n > 0 {
			fmt.Printf("  self-destruct is on: %ds\n", n)
		} else {
			fmt.Println("  self-destruct is off")
		}
		return
	}
	n, err := strconv.ParseInt(arg, 10, 32)
	if err != nil || n < 0 {
		fmt.Println("  usage: /ttl <seconds>  (0 = off)")
		return
	}
	c.ttl.Store(int32(n))
	if n == 0 {
		fmt.Println("  self-destruct off")
		return
	}
	fmt.Printf("  self-destruct on: sends expire %ds after they land\n", n)
}

// doSchedule defers a send: /schedule <when> <text>, where <when> is "+90s" /
// "+2h" or an RFC3339 instant.
func (c *client) doSchedule(arg string) {
	target, ok := c.currentTarget()
	if !ok {
		return
	}
	when, text, found := strings.Cut(arg, " ")
	text = strings.TrimSpace(text)
	if !found || text == "" {
		fmt.Println("  usage: /schedule <+90s|+2h|RFC3339> <text>")
		return
	}
	at, err := parseWhen(when)
	if err != nil {
		fmt.Println("  bad time:", err)
		return
	}
	_ = c.conn.Send(wire.MsgSchedule, c.nextSeq(), 0, c.nextReqID(), wire.ScheduleBody{
		ChatID: target, Text: text, TTLSeconds: c.ttl.Load(), SendAt: at,
	})
}

// parseWhen accepts a relative "+<duration>" or an absolute RFC3339 instant and
// returns unix millis. Absolute times are what a real "send at 9am" uses;
// relative ones keep the demo typable.
func parseWhen(s string) (int64, error) {
	if rel, ok := strings.CutPrefix(s, "+"); ok {
		d, err := time.ParseDuration(rel)
		if err != nil {
			return 0, err
		}
		return time.Now().Add(d).UnixMilli(), nil
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return 0, err
	}
	return t.UnixMilli(), nil
}

// doScheduled lists this user's pending sends in the current chat.
func (c *client) doScheduled() {
	target, ok := c.currentTarget()
	if !ok {
		return
	}
	_ = c.conn.Send(wire.MsgScheduleList, c.nextSeq(), 0, c.nextReqID(),
		wire.ScheduleListBody{ChatID: target})
}

// doUnschedule cancels one pending send by id.
func (c *client) doUnschedule(id string) {
	if id == "" {
		fmt.Println("  usage: /unschedule <id>")
		return
	}
	reqID := c.nextReqID()
	c.cancelReq.Store(reqID)
	_ = c.conn.Send(wire.MsgScheduleCancel, c.nextSeq(), 0, reqID, wire.ScheduleCancelBody{ID: id})
}

// printScheduled renders pending sends. An empty list is ambiguous on its own —
// the server answers both "list" and "cancel" with MsgScheduled — so a reply
// carrying the last cancel's request id is reported as the cancellation it is.
func (c *client) printScheduled(s wire.ScheduledBody, reqID uint64) {
	if reqID != 0 && reqID == c.cancelReq.Load() {
		c.cancelReq.Store(0)
		fmt.Println("  ⏳ cancelled")
		return
	}
	if len(s.Items) == 0 {
		fmt.Println("  (no pending sends)")
		return
	}
	for _, it := range s.Items {
		fmt.Printf("  ⏳ %s → %s at %s: %s\n",
			it.ID, it.ChatID, time.UnixMilli(it.SendAt).Format(time.RFC3339), it.Text)
	}
}

// doPin pins or unpins a message in the current chat: /pin <messageID>. Both
// answer with the chat's FULL pin set, so the client never merges deltas.
func (c *client) doPin(msgID string, unpin bool) {
	target, ok := c.currentTarget()
	if !ok {
		return
	}
	if msgID == "" {
		fmt.Println("  usage: /pin <messageID> | /unpin <messageID>")
		return
	}
	t := wire.MsgPin
	if unpin {
		t = wire.MsgUnpin
	}
	_ = c.conn.Send(t, c.nextSeq(), 0, c.nextReqID(), wire.PinBody{ChatID: target, MessageID: msgID})
}

// doPins lists the current chat's pins.
// doChats asks for a page of the account's chat list. With no argument it starts
// at the beginning; the cursor printed by the previous page continues it.
func (c *client) doChats(after string) {
	_ = c.conn.Send(wire.MsgChatList, c.nextSeq(), 0, c.nextReqID(), wire.ChatListBody{After: after})
}

func (c *client) doProfileGet(target string) {
	_ = c.conn.Send(wire.MsgProfileGet, c.nextSeq(), 0, c.nextReqID(), wire.ProfileGetBody{Target: target})
}

// doProfileSet changes the display name. "-" clears the avatar, which is the one
// thing an empty field cannot express.
func (c *client) doProfileSet(name string) {
	body := wire.ProfileSetBody{DisplayName: name}
	if name == "-" {
		body = wire.ProfileSetBody{ClearAvatar: true}
	}
	_ = c.conn.Send(wire.MsgProfileSet, c.nextSeq(), 0, c.nextReqID(), body)
}

func printChats(ch wire.ChatsBody) {
	if len(ch.Chats) == 0 {
		fmt.Println("  💬 (no chats)")
		return
	}
	for _, s := range ch.Chats {
		name := s.Title
		if name == "" {
			name = "→ " + s.PeerID // a direct chat is named by the other person
		}
		fmt.Printf("  💬 %s  %-8s %-24s seq=%d %s\n", s.ChatID, s.Type, name, s.LastSeq, s.MyRole)
	}
	if !ch.Done {
		fmt.Printf("  … more: /chats %s\n", ch.NextAfter)
	}
}

func printProfile(p wire.ProfileBody) {
	line := fmt.Sprintf("  👤 %s  @%s  %q", p.UserID, p.Username, p.DisplayName)
	if p.AvatarRef != "" {
		line += "  avatar=" + p.AvatarRef
	}
	fmt.Println(line)
}

func (c *client) doPins() {
	target, ok := c.currentTarget()
	if !ok {
		return
	}
	_ = c.conn.Send(wire.MsgPinList, c.nextSeq(), 0, c.nextReqID(), wire.PinBody{ChatID: target})
}

// printPinned renders a chat's pin set. It arrives both as a reply to /pins and
// as a broadcast when any member pins something — pins are chat-wide state.
func printPinned(p wire.PinnedBody) {
	if len(p.Pins) == 0 {
		fmt.Printf("  📌 %s: (no pins)\n", p.ChatID)
		return
	}
	fmt.Printf("  📌 %s: %d pinned\n", p.ChatID, len(p.Pins))
	for _, pin := range p.Pins {
		fmt.Printf("    %s by %s at %s\n",
			pin.MessageID, pin.PinnedBy, time.UnixMilli(pin.PinnedAt).Format("15:04:05"))
	}
}

// doDraft saves what the user is composing in the current chat; an empty text
// clears it. Drafts are private to the user but shared across their devices, so
// the server echoes this back to every device — including this one.
func (c *client) doDraft(text string) {
	target, ok := c.currentTarget()
	if !ok {
		return
	}
	_ = c.conn.Send(wire.MsgDraftSet, c.nextSeq(), 0, c.nextReqID(),
		wire.DraftBody{ChatID: target, Text: text})
	if text == "" {
		fmt.Println("  draft cleared")
	}
}

// doDrafts pulls drafts changed since the stored cursor (0 = all).
func (c *client) doDrafts() {
	since, _ := c.draftCursor.Load().(int64)
	_ = c.conn.Send(wire.MsgDraftSync, c.nextSeq(), 0, c.nextReqID(), wire.DraftSyncBody{Since: since})
}

// printDrafts renders a draft-sync page and advances the client's cursor.
func (c *client) printDrafts(d wire.DraftsBody) {
	if d.Cursor > 0 {
		c.draftCursor.Store(d.Cursor)
	}
	if len(d.Drafts) == 0 {
		fmt.Println("  (no draft changes)")
		return
	}
	for _, dr := range d.Drafts {
		if dr.Text == "" {
			fmt.Printf("  ✎ %s: (cleared)\n", dr.ChatID)
			continue
		}
		fmt.Printf("  ✎ %s: %s\n", dr.ChatID, dr.Text)
	}
}

// doCreateChat makes a group or channel: /group <title> [@user ...]. The reply
// carries the new chat id, which becomes the target — creating a chat and then
// having to look up where it went would be a strange way to start one.
func (c *client) doCreateChat(kind, arg string) {
	f := strings.Fields(arg)
	if len(f) == 0 {
		fmt.Printf("  usage: /%s <title> [@user ...]\n", kind)
		return
	}
	var title []string
	var members []string
	for _, tok := range f {
		if strings.HasPrefix(tok, "@") {
			members = append(members, tok)
			continue
		}
		title = append(title, tok)
	}
	if len(title) == 0 {
		fmt.Printf("  usage: /%s <title> [@user ...]\n", kind)
		return
	}
	_ = c.conn.Send(wire.MsgChatCreate, c.nextSeq(), 0, c.nextReqID(), wire.ChatCreateBody{
		Type: kind, Title: strings.Join(title, " "), Members: members,
	})
}

// currentChatID returns the current target only when it is a concrete chat id.
// The membership commands below address a CHAT — a handle names a chat, not a
// peer — and the server resolves neither "@user" nor a handle for them, so a
// direct-chat target is caught here rather than sent as a malformed chat id.
func (c *client) currentChatID() (string, bool) {
	target, ok := c.currentTarget()
	if !ok {
		return "", false
	}
	if strings.HasPrefix(target, "@") {
		fmt.Println("  this command needs a chat id — use /to <chatID>, not /to @user")
		return "", false
	}
	return target, true
}

// doHandle claims the current chat's public handle, or clears it with "-".
// Owner-only: the handle is the chat's public identity.
func (c *client) doHandle(name string) {
	chatID, ok := c.currentChatID()
	if !ok {
		return
	}
	if name == "-" {
		name = ""
	}
	_ = c.conn.Send(wire.MsgSetUsername, c.nextSeq(), 0, c.nextReqID(),
		wire.SetUsernameBody{ChatID: chatID, Username: strings.TrimPrefix(name, "@")})
}

// doInvite mints an invite link for the current chat: /invite [maxUses]
// [+duration], e.g. "/invite 5 +24h". Both bounds are optional, and leaving
// them out means an unlimited link — allowed, but chosen explicitly.
func (c *client) doInvite(arg string) {
	chatID, ok := c.currentChatID()
	if !ok {
		return
	}
	var body = wire.InviteCreateBody{ChatID: chatID}
	for _, f := range strings.Fields(arg) {
		if strings.HasPrefix(f, "+") {
			at, err := parseWhen(f)
			if err != nil {
				fmt.Println("  bad expiry:", err)
				return
			}
			body.ExpiresAt = at
			continue
		}
		n, err := strconv.ParseInt(f, 10, 32)
		if err != nil || n < 0 {
			fmt.Println("  usage: /invite [maxUses] [+24h]")
			return
		}
		body.MaxUses = int32(n)
	}
	_ = c.conn.Send(wire.MsgInviteCreate, c.nextSeq(), 0, c.nextReqID(), body)
}

// doInvites lists the current chat's live links (admin/owner).
func (c *client) doInvites() {
	chatID, ok := c.currentChatID()
	if !ok {
		return
	}
	_ = c.conn.Send(wire.MsgInviteList, c.nextSeq(), 0, c.nextReqID(), wire.InviteListBody{ChatID: chatID})
}

// doRevoke kills one link of the current chat.
func (c *client) doRevoke(code string) {
	chatID, ok := c.currentChatID()
	if !ok {
		return
	}
	if code == "" {
		fmt.Println("  usage: /revoke <code>")
		return
	}
	_ = c.conn.Send(wire.MsgInviteRevoke, c.nextSeq(), 0, c.nextReqID(),
		wire.InviteRevokeBody{ChatID: chatID, Code: code})
}

// doJoin joins by invite code, or by "@handle" for a public chat. Unlike the
// other commands here it needs no target: joining is how you get one.
func (c *client) doJoin(arg string) {
	if arg == "" {
		fmt.Println("  usage: /join <code> | /join @handle")
		return
	}
	body := wire.JoinBody{Code: arg}
	if handle, ok := strings.CutPrefix(arg, "@"); ok {
		body = wire.JoinBody{Handle: handle}
	}
	_ = c.conn.Send(wire.MsgJoin, c.nextSeq(), 0, c.nextReqID(), body)
}

// doRole promotes or demotes a member: /role <userID> <member|admin|owner>.
// The id is the numeric user id shown next to every message, not a @name — the
// server takes a member id here, and guessing at a name would be worse than
// asking for the id the user can see.
func (c *client) doRole(arg string) {
	chatID, ok := c.currentChatID()
	if !ok {
		return
	}
	f := strings.Fields(arg)
	if len(f) != 2 {
		fmt.Println("  usage: /role <userID> <member|admin|owner>")
		return
	}
	if strings.HasPrefix(f[0], "@") {
		fmt.Println("  /role takes the numeric user id shown next to messages, not @name")
		return
	}
	_ = c.conn.Send(wire.MsgSetRole, c.nextSeq(), 0, c.nextReqID(),
		wire.SetRoleBody{ChatID: chatID, UserID: f[0], Role: f[1]})
}

// printInvites renders the invite reply. The server answers handle/revoke/role
// changes with an EMPTY body, a join with the chat joined, and a list with the
// links — so the three cases are told apart here rather than guessed at.
func (c *client) printInvites(inv wire.InvitesBody) {
	if inv.JoinedChat != "" {
		c.target.Store(inv.JoinedChat)
		fmt.Printf("  ➜ joined %s (target set)\n", inv.JoinedChat)
		return
	}
	if len(inv.Links) == 0 {
		fmt.Println("  ✓ ok")
		return
	}
	for _, l := range inv.Links {
		uses := fmt.Sprintf("%d used", l.Uses)
		if l.MaxUses > 0 {
			uses = fmt.Sprintf("%d/%d used", l.Uses, l.MaxUses)
		}
		expiry := "no expiry"
		if l.ExpiresAt > 0 {
			expiry = "until " + time.UnixMilli(l.ExpiresAt).Format(time.RFC3339)
		}
		fmt.Printf("  🔗 %s (%s, %s)\n", l.Code, uses, expiry)
	}
}

// doPoll posts a poll: /poll Question? | Option A | Option B
func (c *client) doPoll(arg string) {
	target, ok := c.currentTarget()
	if !ok {
		return
	}
	parts := strings.Split(arg, "|")
	if len(parts) < 3 {
		fmt.Println("  usage: /poll Question? | Option A | Option B")
		return
	}
	opts := make([]string, 0, len(parts)-1)
	for _, o := range parts[1:] {
		opts = append(opts, strings.TrimSpace(o))
	}
	_ = c.conn.Send(wire.MsgPollCreate, c.nextSeq(), 0, c.nextReqID(), wire.PollCreateBody{
		ChatID: target, Question: strings.TrimSpace(parts[0]), Options: opts,
	})
}

// doVote votes in a poll: /vote <pollID> <optionIndex>
func (c *client) doVote(arg string) {
	f := strings.Fields(arg)
	if len(f) != 2 {
		fmt.Println("  usage: /vote <pollID> <optionIndex>")
		return
	}
	// ParseInt with an explicit 32-bit width: an out-of-range index is rejected
	// here rather than silently wrapping into a valid-looking option number.
	idx, err := strconv.ParseInt(f[1], 10, 32)
	if err != nil || idx < 0 {
		fmt.Println("  option index must be a non-negative number")
		return
	}
	_ = c.conn.Send(wire.MsgPollVote, c.nextSeq(), 0, c.nextReqID(),
		wire.PollVoteBody{PollID: f[0], Option: int32(idx)})
}

// printPoll renders a poll with a simple bar per option.
func printPoll(p wire.PollStateBody) {
	status := ""
	if p.Closed {
		status = " (closed)"
	}
	fmt.Printf("  ▣ poll %s%s — %s  [%d votes]\n", p.PollID, status, p.Question, p.TotalVotes)
	mine := map[int32]bool{}
	for _, v := range p.MyVotes {
		mine[v] = true
	}
	for _, o := range p.Options {
		mark := " "
		if mine[o.Index] {
			mark = "•"
		}
		bar := strings.Repeat("█", int(o.Votes))
		fmt.Printf("    %s %d) %-20s %s %d\n", mark, o.Index, o.Text, bar, o.Votes)
	}
}

// doContact adds a contact: /contact @user [local name]
func (c *client) doContact(arg string) {
	f := strings.Fields(arg)
	if len(f) == 0 {
		fmt.Println("  usage: /contact @user [name]")
		return
	}
	_ = c.conn.Send(wire.MsgContactAdd, c.nextSeq(), 0, c.nextReqID(),
		wire.ContactAddBody{Target: f[0], Name: strings.Join(f[1:], " ")})
}

// doContacts requests an incremental sync from the stored cursor.
func (c *client) doContacts() {
	since, _ := c.contactCursor.Load().(int64)
	_ = c.conn.Send(wire.MsgContactSync, c.nextSeq(), 0, c.nextReqID(),
		wire.ContactSyncBody{Since: since})
}

// doBlock blocks or unblocks: /block @user | /unblock @user
func (c *client) doBlock(target string, blocked bool) {
	if target == "" {
		fmt.Println("  usage: /block @user")
		return
	}
	_ = c.conn.Send(wire.MsgBlock, c.nextSeq(), 0, c.nextReqID(),
		wire.BlockBody{Target: target, Blocked: blocked})
}

// printContacts renders a sync page and advances the client's cursor.
func (c *client) printContacts(l wire.ContactListBody) {
	if l.Cursor > 0 {
		c.contactCursor.Store(l.Cursor)
	}
	if len(l.Contacts) == 0 {
		fmt.Println("  (no contact changes)")
		return
	}
	for _, ct := range l.Contacts {
		flag := ""
		if ct.Blocked {
			flag = " [blocked]"
		}
		fmt.Printf("  ● %s %s%s\n", ct.UserID, ct.Name, flag)
	}
}
