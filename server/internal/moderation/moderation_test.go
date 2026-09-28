package moderation

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/SyncApp-chat/SyncApp/pkg/eventbus"
	"github.com/SyncApp-chat/SyncApp/pkg/wire"
)

func newSvc(t *testing.T, terms ...string) *Service {
	t.Helper()
	return New(eventbus.NewMemory(), terms, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

// feed pushes one message through the rules exactly as the bus would.
func feed(t *testing.T, s *Service, text string) {
	t.Helper()
	body := wire.NewMessageBody{
		MessageID: "1", ChatID: "c1", SenderID: "u1", Text: text,
	}
	if err := s.onMessage(context.Background(), eventbus.Event{Data: wire.Marshal(body)}); err != nil {
		t.Fatalf("onMessage: %v", err)
	}
}

func bannedHits(s *Service) int {
	n := 0
	for _, e := range s.Events() {
		if e.Rule == "banned_term" {
			n++
		}
	}
	return n
}

func TestBannedTermMatchesPlainText(t *testing.T) {
	s := newSvc(t, "badword")
	feed(t, s, "this is a badword here")
	if bannedHits(s) != 1 {
		t.Fatalf("plain term not flagged: %+v", s.Events())
	}
}

// Each of these is the same word to a reader, and each defeated the old
// lower-cased Contains match.
func TestBannedTermSurvivesEvasion(t *testing.T) {
	evasions := map[string]string{
		"case":            "BadWord",
		"full-width":      "ｂａｄｗｏｒｄ",
		"accents":         "bádwórd",
		"zero-width":      "bad\u200bword",
		"soft hyphen":     "bad\u00adword",
		"punctuation":     "b.a.d.w.o.r.d",
		"spaces":          "b a d w o r d",
		"leet digits":     "b4dw0rd",
		"leet symbols":    "b@dword",
		"cyrillic o":      "badwоrd", // Cyrillic 'о'
		"cyrillic a":      "bаdword", // Cyrillic 'а'
		"mixed":           "BаD‑w0rd",
		"inside sentence": "hey b_a_d_w_o_r_d ok",
	}
	for name, text := range evasions {
		t.Run(name, func(t *testing.T) {
			s := newSvc(t, "badword")
			feed(t, s, text)
			if bannedHits(s) != 1 {
				t.Fatalf("evasion %q (%q) not flagged", name, text)
			}
		})
	}
}

func TestCleanTextIsNotFlagged(t *testing.T) {
	s := newSvc(t, "badword")
	for _, text := range []string{"hello there", "a perfectly ordinary message", ""} {
		feed(t, s, text)
	}
	if bannedHits(s) != 0 {
		t.Fatalf("clean text flagged: %+v", s.Events())
	}
}

// Russian is a first-class language here, so the homoglyph map must not mangle
// ordinary Cyrillic into accidental matches.
func TestOrdinaryCyrillicIsNotFlagged(t *testing.T) {
	s := newSvc(t, "badword", "spam")
	feed(t, s, "привет, как дела? всё хорошо")
	if bannedHits(s) != 0 {
		t.Fatalf("ordinary Russian flagged: %+v", s.Events())
	}
}

// A punctuation-only term normalizes to the empty string, which as a substring
// matches every message — so it must be dropped at construction.
func TestPunctuationOnlyTermIsDropped(t *testing.T) {
	s := newSvc(t, "...", "")
	feed(t, s, "a completely innocent message")
	if bannedHits(s) != 0 {
		t.Fatalf("empty term flagged everything: %+v", s.Events())
	}
}

func TestSpamVelocityFlagsBurst(t *testing.T) {
	s := newSvc(t)
	// Limiter is 5/s with burst 15; well past that in one go.
	for i := 0; i < 40; i++ {
		feed(t, s, "hi")
	}
	flagged := false
	for _, e := range s.Events() {
		if e.Rule == "spam_velocity" {
			flagged = true
		}
	}
	if !flagged {
		t.Fatal("sustained burst not flagged as spam_velocity")
	}
}

func TestEventRingIsBounded(t *testing.T) {
	s := newSvc(t, "badword")
	for i := 0; i < 1200; i++ {
		feed(t, s, "badword")
	}
	if got := len(s.Events()); got > 1000 {
		t.Fatalf("event ring grew to %d, want <= 1000", got)
	}
}
