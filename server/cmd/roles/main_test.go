package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/IR-Full/sync-app/server/internal/audit"
	"github.com/IR-Full/sync-app/server/internal/model"
	"github.com/IR-Full/sync-app/server/internal/store/memory"
)

type recordingSink struct{ events []audit.Event }

func (r *recordingSink) Record(_ context.Context, e audit.Event) { r.events = append(r.events, e) }

func TestGrantListRevoke(t *testing.T) {
	ctx := context.Background()
	st := memory.New().Stores()
	if err := st.Users.CreateUser(ctx, &model.User{ID: "42", Username: "alice"}); err != nil {
		t.Fatal(err)
	}
	sink := &recordingSink{}
	var out bytes.Buffer

	if err := run(ctx, []string{"grant", "@alice", "moderator", "-by", "ops"}, st, sink, &out); err != nil {
		t.Fatal(err)
	}
	if role, _ := st.Roles.PlatformRole(ctx, "42"); role != model.PlatformModerator {
		t.Fatalf("role after grant: %q", role)
	}
	out.Reset()
	if err := run(ctx, []string{"list"}, st, sink, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "@alice") || !strings.Contains(out.String(), "moderator") || !strings.Contains(out.String(), "ops") {
		t.Fatalf("list output: %q", out.String())
	}
	if err := run(ctx, []string{"revoke", "42", "-by", "ops"}, st, sink, &out); err != nil {
		t.Fatal(err)
	}
	if role, _ := st.Roles.PlatformRole(ctx, "42"); role != "" {
		t.Fatalf("role after revoke: %q", role)
	}
	if len(sink.events) != 2 || sink.events[0].Action != "platform.role.grant" || sink.events[1].Action != "platform.role.revoke" || sink.events[0].Actor != "ops" {
		t.Fatalf("audit events: %+v", sink.events)
	}
}

func TestRefusals(t *testing.T) {
	ctx := context.Background()
	st := memory.New().Stores()
	_ = st.Users.CreateUser(ctx, &model.User{ID: "42", Username: "alice"})
	for name, args := range map[string][]string{
		"no command":   nil,
		"unknown role": {"grant", "42", "owner"},
		"no such user": {"grant", "@bob", "admin"},
		"missing role": {"grant", "42"},
		"unknown verb": {"promote", "42"},
	} {
		t.Run(name, func(t *testing.T) {
			if err := run(ctx, args, st, &recordingSink{}, &bytes.Buffer{}); err == nil {
				t.Fatal("want an error")
			}
		})
	}
}
