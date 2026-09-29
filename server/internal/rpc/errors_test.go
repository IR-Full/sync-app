package rpc

import (
	"errors"
	"fmt"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/IR-Full/sync-app/server/internal/message"
	"github.com/IR-Full/sync-app/server/internal/store"
)

// gRPC transports error MESSAGES but not error IDENTITY, so an errors.Is check
// on the client would always fail against a plain gRPC error. The gateway
// depends on that identity in ways that are not obviously error handling at all
// — store.ErrNotFound is how FindDirect falls through to EnsureDirect, and the
// message sentinels are what pick the protocol error code a client receives.
//
// A break here therefore does not surface as a crash: it surfaces as the
// gateway creating a duplicate chat, or as every rejection arriving at the
// client as a generic INTERNAL. Only the split deployment is affected, so the
// monolith's tests stay green throughout.

func TestSentinelsSurviveTheRoundTrip(t *testing.T) {
	for _, sentinel := range []error{
		store.ErrNotFound,
		message.ErrForbidden,
		message.ErrEmptyMessage,
		message.ErrTooLong,
		message.ErrBadCommand,
	} {
		t.Run(sentinel.Error(), func(t *testing.T) {
			if got := fromStatus(toStatus(sentinel)); !errors.Is(got, sentinel) {
				t.Errorf("sentinel did not survive: want %v, got %v", sentinel, got)
			}
		})
	}
}

func TestWrappedSentinelsSurviveTheRoundTrip(t *testing.T) {
	// Services wrap before returning ("submit: %w"), so the mapping has to match
	// on identity rather than on equality — otherwise every real error from a
	// real call site falls through to the default branch.
	for _, sentinel := range []error{
		store.ErrNotFound,
		message.ErrForbidden,
		message.ErrEmptyMessage,
		message.ErrTooLong,
		message.ErrBadCommand,
	} {
		wrapped := fmt.Errorf("service: %w", sentinel)
		if got := fromStatus(toStatus(wrapped)); !errors.Is(got, sentinel) {
			t.Errorf("wrapped %v did not survive: got %v", sentinel, got)
		}
	}
}

func TestSentinelsMapToDistinctCodes(t *testing.T) {
	// The three InvalidArgument sentinels are told apart by their message, so
	// they legitimately share a code — but NotFound and PermissionDenied must
	// not collide with anything, or the client cannot tell "no such chat" from
	// "not a member".
	notFound := status.Code(toStatus(store.ErrNotFound))
	forbidden := status.Code(toStatus(message.ErrForbidden))

	if notFound != codes.NotFound {
		t.Errorf("ErrNotFound mapped to %v", notFound)
	}
	if forbidden != codes.PermissionDenied {
		t.Errorf("ErrForbidden mapped to %v", forbidden)
	}
	if notFound == forbidden {
		t.Error("not-found and forbidden collapsed to one code")
	}
}

func TestInvalidArgumentSentinelsAreToldApartByMessage(t *testing.T) {
	// They share codes.InvalidArgument, so the message is the only thing
	// separating them. A changed string would silently merge two distinct
	// rejections into one.
	cases := map[string]error{
		"empty":       message.ErrEmptyMessage,
		"too_long":    message.ErrTooLong,
		"bad_command": message.ErrBadCommand,
	}
	for want, sentinel := range cases {
		st, _ := status.FromError(toStatus(sentinel))
		if st.Code() != codes.InvalidArgument {
			t.Errorf("%v mapped to %v, want InvalidArgument", sentinel, st.Code())
		}
		if st.Message() != want {
			t.Errorf("%v carried message %q, want %q", sentinel, st.Message(), want)
		}
	}
}

func TestNilStaysNil(t *testing.T) {
	// Success is the overwhelmingly common path; inventing an error here would
	// fail every call.
	if got := toStatus(nil); got != nil {
		t.Errorf("toStatus(nil) = %v", got)
	}
	if got := fromStatus(nil); got != nil {
		t.Errorf("fromStatus(nil) = %v", got)
	}
}

func TestUnknownErrorKeepsItsMessage(t *testing.T) {
	// An unmapped error becomes codes.Unknown, which is fine — but the text has
	// to survive or an operator reading the gateway's log sees nothing usable.
	original := errors.New("database connection refused")

	mapped := toStatus(original)

	if mapped == nil {
		t.Fatal("unknown error was swallowed")
	}
	if got := status.Convert(mapped).Message(); got != original.Error() {
		t.Errorf("message lost: got %q, want %q", got, original.Error())
	}
}

func TestUnknownStatusIsPassedThrough(t *testing.T) {
	// A transport-level failure (Unavailable, DeadlineExceeded) is not a domain
	// sentinel and must not be rewritten into one — mapping it to ErrNotFound
	// would make the gateway create a duplicate chat on every blip.
	for _, code := range []codes.Code{
		codes.Unavailable, codes.DeadlineExceeded, codes.Internal, codes.Unauthenticated,
	} {
		original := status.Error(code, "transport")
		got := fromStatus(original)

		if errors.Is(got, store.ErrNotFound) || errors.Is(got, message.ErrForbidden) {
			t.Errorf("%v was rewritten into a domain sentinel: %v", code, got)
		}
		if status.Code(got) != code {
			t.Errorf("%v became %v", code, status.Code(got))
		}
	}
}

func TestUnrecognisedInvalidArgumentIsPassedThrough(t *testing.T) {
	// A newer server may reject with a reason this client has never heard of.
	// Collapsing it into ErrEmptyMessage would report the wrong cause to the user.
	original := status.Error(codes.InvalidArgument, "some_future_reason")

	got := fromStatus(original)

	for _, sentinel := range []error{
		message.ErrEmptyMessage, message.ErrTooLong, message.ErrBadCommand,
	} {
		if errors.Is(got, sentinel) {
			t.Errorf("unknown reason was mapped onto %v", sentinel)
		}
	}
	if status.Convert(got).Message() != "some_future_reason" {
		t.Errorf("reason lost: %v", got)
	}
}

func TestNonStatusErrorIsPassedThrough(t *testing.T) {
	// fromStatus also runs on errors that never crossed gRPC at all (a dial
	// failure, a context cancellation constructed locally).
	original := errors.New("dial tcp: connection refused")

	if got := fromStatus(original); got.Error() != original.Error() {
		t.Errorf("local error was rewritten: %v", got)
	}
}

func TestClientSeesNotFoundForAMissingChat(t *testing.T) {
	// Spelled out because this exact identity is what FindDirect→EnsureDirect
	// fall-through depends on: a gateway that stopped recognising it would
	// create a second direct chat between the same two users.
	wrapped := fmt.Errorf("chats.FindDirect: %w", store.ErrNotFound)

	got := fromStatus(toStatus(wrapped))

	if !errors.Is(got, store.ErrNotFound) {
		t.Fatalf("FindDirect would no longer fall through to EnsureDirect: %v", got)
	}
}
