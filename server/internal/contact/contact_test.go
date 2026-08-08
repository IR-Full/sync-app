package contact

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/synapse-chat/synapse/internal/model"
	"github.com/synapse-chat/synapse/internal/store"
	"github.com/synapse-chat/synapse/internal/store/memory"
)

// fakeUsers is a fixed user directory.
type fakeUsers struct{ byID map[string]*model.User }

func (f fakeUsers) GetUser(_ context.Context, id string) (*model.User, error) {
	u, ok := f.byID[id]
	if !ok {
		return nil, store.ErrNotFound
	}
	return u, nil
}
func (f fakeUsers) GetUserByUsername(_ context.Context, name string) (*model.User, error) {
	for _, u := range f.byID {
		if u.Username == name {
			return u, nil
		}
	}
	return nil, store.ErrNotFound
}

func newSvc() *Service {
	users := fakeUsers{byID: map[string]*model.User{
		"1": {ID: "1", Username: "alice"},
		"2": {ID: "2", Username: "bob"},
		"3": {ID: "3", Username: "carol"},
	}}
	return New(memory.New().Stores().Contacts, users)
}

func TestContactAddAndSync(t *testing.T) {
	s := newSvc()
	ctx := context.Background()

	if _, err := s.Add(ctx, "1", "@bob", "Bobby", 100); err != nil {
		t.Fatal(err)
	}
	list, cursor, err := s.Sync(ctx, "1", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].UserID != "2" || list[0].Name != "Bobby" {
		t.Fatalf("full sync: %+v", list)
	}
	if cursor != 100 {
		t.Fatalf("cursor = %d, want 100", cursor)
	}

	// Incremental sync from the cursor returns nothing new…
	list, _, _ = s.Sync(ctx, "1", cursor)
	if len(list) != 0 {
		t.Fatalf("incremental sync returned %d unchanged rows", len(list))
	}
	// …until something changes.
	if _, err := s.Add(ctx, "1", "@carol", "Caz", 200); err != nil {
		t.Fatal(err)
	}
	list, cursor, _ = s.Sync(ctx, "1", cursor)
	if len(list) != 1 || list[0].UserID != "3" {
		t.Fatalf("incremental sync: %+v", list)
	}
	if cursor != 200 {
		t.Fatalf("cursor = %d, want 200", cursor)
	}
}

func TestContactsArePrivatePerOwner(t *testing.T) {
	s := newSvc()
	ctx := context.Background()
	if _, err := s.Add(ctx, "1", "@bob", "Bobby", 100); err != nil {
		t.Fatal(err)
	}
	// Bob must not see Alice's address book (the local name is Alice's data).
	list, _, err := s.Sync(ctx, "2", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 0 {
		t.Fatalf("owner leak: bob sees %+v", list)
	}
}

func TestBlockIsBidirectionalForDelivery(t *testing.T) {
	s := newSvc()
	ctx := context.Background()
	// Alice blocks Bob.
	if err := s.SetBlocked(ctx, "1", "@bob", true, 100); err != nil {
		t.Fatal(err)
	}
	// The gate must fire whichever direction it is asked about, otherwise Bob
	// could still reach Alice by initiating.
	if blocked, _ := s.BlocksBetween(ctx, "1", "2"); !blocked {
		t.Fatal("alice→bob not blocked")
	}
	if blocked, _ := s.BlocksBetween(ctx, "2", "1"); !blocked {
		t.Fatal("bob→alice not blocked (block must stop BOTH directions)")
	}
	// An unrelated pair is unaffected.
	if blocked, _ := s.BlocksBetween(ctx, "2", "3"); blocked {
		t.Fatal("unrelated pair reported as blocked")
	}
}

func TestBlockWorksOnStrangers(t *testing.T) {
	s := newSvc()
	ctx := context.Background()
	// No prior contact — blocking must still work (that is the point).
	if err := s.SetBlocked(ctx, "1", "@carol", true, 100); err != nil {
		t.Fatal(err)
	}
	if blocked, _ := s.IsBlocked(ctx, "1", "3"); !blocked {
		t.Fatal("blocking a stranger did not take effect")
	}
}

func TestRemoveKeepsBlock(t *testing.T) {
	s := newSvc()
	ctx := context.Background()
	if _, err := s.Add(ctx, "1", "@bob", "Bobby", 100); err != nil {
		t.Fatal(err)
	}
	if err := s.SetBlocked(ctx, "1", "@bob", true, 150); err != nil {
		t.Fatal(err)
	}
	// Deleting the contact must NOT silently unblock them.
	if err := s.Remove(ctx, "1", "@bob"); err != nil {
		t.Fatal(err)
	}
	if blocked, _ := s.IsBlocked(ctx, "1", "2"); !blocked {
		t.Fatal("removing a contact cleared the block")
	}
}

func TestUnblock(t *testing.T) {
	s := newSvc()
	ctx := context.Background()
	if err := s.SetBlocked(ctx, "1", "@bob", true, 100); err != nil {
		t.Fatal(err)
	}
	if err := s.SetBlocked(ctx, "1", "@bob", false, 200); err != nil {
		t.Fatal(err)
	}
	if blocked, _ := s.BlocksBetween(ctx, "1", "2"); blocked {
		t.Fatal("unblock did not take effect")
	}
}

func TestCannotTargetSelf(t *testing.T) {
	s := newSvc()
	ctx := context.Background()
	if _, err := s.Add(ctx, "1", "@alice", "me", 1); !errors.Is(err, ErrSelf) {
		t.Fatalf("self-add: got %v, want ErrSelf", err)
	}
	if err := s.SetBlocked(ctx, "1", "@alice", true, 1); !errors.Is(err, ErrSelf) {
		t.Fatalf("self-block: got %v, want ErrSelf", err)
	}
}

func TestUnknownTargetRejected(t *testing.T) {
	s := newSvc()
	if _, err := s.Add(context.Background(), "1", "@nobody", "x", 1); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("unknown user: got %v, want ErrNotFound", err)
	}
}

// TestSyncPagesLargeAddressBook pins that one sync answers with a bounded page
// and that repeating the request with the returned cursor walks the whole book.
// Before paging, a large address book came back as a single frame — which the
// protocol's size cap turns into a total failure rather than a slow sync.
func TestSyncPagesLargeAddressBook(t *testing.T) {
	ctx := context.Background()
	st := memory.New()
	svc := New(st.Stores().Contacts, fakeUsers{byID: map[string]*model.User{}})

	const owner = "1"
	total := SyncPageSize + 42
	for i := 0; i < total; i++ {
		if err := st.UpsertContact(ctx, &model.Contact{
			OwnerID: owner, UserID: fmt.Sprintf("%d", 1000+i), Name: "c",
			CreatedAt: int64(i + 1), UpdatedAt: int64(i + 1),
		}); err != nil {
			t.Fatal(err)
		}
	}

	page, cursor, err := svc.Sync(ctx, owner, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(page) != SyncPageSize {
		t.Fatalf("first page has %d rows, want the page size %d", len(page), SyncPageSize)
	}

	seen := map[string]bool{}
	for _, c := range page {
		seen[c.UserID] = true
	}
	for i := 0; i < 10 && len(seen) < total; i++ {
		page, cursor, err = svc.Sync(ctx, owner, cursor)
		if err != nil {
			t.Fatal(err)
		}
		if len(page) == 0 {
			break
		}
		for _, c := range page {
			seen[c.UserID] = true
		}
	}
	if len(seen) != total {
		t.Fatalf("paging reached %d of %d contacts", len(seen), total)
	}
}

// TestSyncNeverSplitsATimestampGroup pins the subtle half of paging. The cursor
// is a timestamp, so if a page ends in the middle of a group of rows written in
// the same millisecond, the next request ("> that millisecond") would skip the
// rest of the group forever. Here eleven contacts share one timestamp exactly
// across the page boundary; every one of them must still arrive.
func TestSyncNeverSplitsATimestampGroup(t *testing.T) {
	ctx := context.Background()
	st := memory.New()
	svc := New(st.Stores().Contacts, fakeUsers{byID: map[string]*model.User{}})

	const owner = "1"
	const shared = 11
	total := SyncPageSize - 1 + shared
	for i := 0; i < total; i++ {
		at := int64(i + 1)
		if i >= SyncPageSize-1 {
			at = int64(SyncPageSize) // the whole tail lands in one millisecond
		}
		if err := st.UpsertContact(ctx, &model.Contact{
			OwnerID: owner, UserID: fmt.Sprintf("%d", 1000+i), CreatedAt: at, UpdatedAt: at,
		}); err != nil {
			t.Fatal(err)
		}
	}

	seen := map[string]bool{}
	cursor := int64(0)
	for i := 0; i < 5; i++ {
		page, next, err := svc.Sync(ctx, owner, cursor)
		if err != nil {
			t.Fatal(err)
		}
		if len(page) == 0 {
			break
		}
		if len(page) > SyncPageSize {
			t.Fatalf("page of %d exceeds the page size", len(page))
		}
		for _, c := range page {
			if seen[c.UserID] {
				t.Fatalf("contact %s delivered twice", c.UserID)
			}
			seen[c.UserID] = true
		}
		if next == cursor {
			t.Fatal("cursor did not advance; the sync would loop forever")
		}
		cursor = next
	}
	if len(seen) != total {
		t.Fatalf("paging reached %d of %d contacts — a timestamp group was split", len(seen), total)
	}
}
