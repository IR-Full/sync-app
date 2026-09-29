// Command roles lists, grants and revokes platform roles (admin, moderator) in
// the database the deployment uses (SYNCAPP_PG_DSN). Every change is written to
// the audit log. A grant or revocation reaches running gateways within their
// role cache TTL (30 s).
//
//	roles list
//	roles grant  <user-id | @username> admin|moderator [-by <who>]
//	roles revoke <user-id | @username>                 [-by <who>]
//
// Users listed in SYNCAPP_ADMIN_USERS / SYNCAPP_MODERATOR_USERS hold their role
// from the configuration regardless; revoking one means editing that list.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/user"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/IR-Full/sync-app/server/internal/audit"
	"github.com/IR-Full/sync-app/server/internal/envcfg"
	"github.com/IR-Full/sync-app/server/internal/model"
	"github.com/IR-Full/sync-app/server/internal/store"
	"github.com/IR-Full/sync-app/server/internal/store/postgres"
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	dsn := envcfg.Get("SYNCAPP_PG_DSN")
	if dsn == "" {
		fmt.Fprintln(os.Stderr, "roles: SYNCAPP_PG_DSN is not set; roles live in the deployment's Postgres")
		os.Exit(2)
	}
	ctx := context.Background()
	pg, err := postgres.Connect(ctx, dsn)
	if err != nil {
		fmt.Fprintln(os.Stderr, "roles:", err)
		os.Exit(1)
	}
	defer pg.Close()
	if err := pg.Migrate(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "roles:", err)
		os.Exit(1)
	}
	if err := run(ctx, os.Args[1:], pg.Stores(), audit.NewLogSink(log), os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "roles:", err)
		os.Exit(1)
	}
}

var errUsage = errors.New("usage: roles list | roles grant <user> admin|moderator [-by who] | roles revoke <user> [-by who]")

// run executes one command against the stores.
func run(ctx context.Context, args []string, st store.Stores, sink audit.Sink, out io.Writer) error {
	if len(args) == 0 {
		return errUsage
	}
	cmd, rest := args[0], args[1:]
	fs := flag.NewFlagSet(cmd, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	by := fs.String("by", defaultActor(), "who is making the change, for the audit log")
	positional, err := parseInterleaved(fs, rest)
	if err != nil {
		return err
	}

	switch cmd {
	case "list":
		grants, err := st.Roles.ListPlatformRoles(ctx)
		if err != nil {
			return err
		}
		w := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
		if _, err := fmt.Fprintln(w, "USER\tUSERNAME\tROLE\tGRANTED BY\tGRANTED AT"); err != nil {
			return err
		}
		for _, g := range grants {
			name := ""
			if u, err := st.Users.GetUser(ctx, g.UserID); err == nil {
				name = "@" + u.Username
			}
			at := time.UnixMilli(g.GrantedAt).UTC().Format(time.RFC3339)
			if _, err := fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", g.UserID, name, g.Role, g.GrantedBy, at); err != nil {
				return err
			}
		}
		return w.Flush()

	case "grant":
		if len(positional) != 2 {
			return errUsage
		}
		role := model.PlatformRole(positional[1])
		if !role.Valid() {
			return fmt.Errorf("unknown role %q: want admin or moderator", positional[1])
		}
		u, err := resolveUser(ctx, st, positional[0])
		if err != nil {
			return err
		}
		if err := st.Roles.SetPlatformRole(ctx, model.PlatformRoleGrant{
			UserID: u.ID, Role: role, GrantedBy: *by, GrantedAt: time.Now().UnixMilli(),
		}); err != nil {
			return err
		}
		sink.Record(ctx, audit.Event{Action: "platform.role.grant", Actor: *by, Target: u.ID, Detail: string(role)})
		_, err = fmt.Fprintf(out, "granted %s to %s (@%s)\n", role, u.ID, u.Username)
		return err

	case "revoke":
		if len(positional) != 1 {
			return errUsage
		}
		u, err := resolveUser(ctx, st, positional[0])
		if err != nil {
			return err
		}
		if err := st.Roles.RemovePlatformRole(ctx, u.ID); err != nil {
			return err
		}
		sink.Record(ctx, audit.Event{Action: "platform.role.revoke", Actor: *by, Target: u.ID})
		_, err = fmt.Fprintf(out, "revoked the platform role of %s (@%s)\n", u.ID, u.Username)
		return err
	}
	return errUsage
}

// parseInterleaved lets flags come before or after the positional arguments.
func parseInterleaved(fs *flag.FlagSet, args []string) ([]string, error) {
	var positional []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		args = fs.Args()
		if len(args) == 0 {
			return positional, nil
		}
		positional = append(positional, args[0])
		args = args[1:]
	}
}

func resolveUser(ctx context.Context, st store.Stores, ref string) (*model.User, error) {
	var (
		u   *model.User
		err error
	)
	if name, ok := strings.CutPrefix(ref, "@"); ok {
		u, err = st.Users.GetUserByUsername(ctx, strings.ToLower(name))
	} else {
		u, err = st.Users.GetUser(ctx, ref)
	}
	if errors.Is(err, store.ErrNotFound) {
		return nil, fmt.Errorf("no user %s", ref)
	}
	return u, err
}

func defaultActor() string {
	if u, err := user.Current(); err == nil {
		return "cli:" + u.Username
	}
	return "cli"
}
