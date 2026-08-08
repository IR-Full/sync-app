package rpc

import (
	"errors"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/synapse-chat/synapse/internal/message"
	"github.com/synapse-chat/synapse/internal/store"
)

// gRPC transports error MESSAGES but not Go error IDENTITY: an errors.Is check on
// the client would always fail against a plain gRPC error. The gateway relies on
// sentinels (store.ErrNotFound to fall through FindDirect→EnsureDirect,
// message.ErrForbidden/ErrEmptyMessage/… to map to protocol error codes), so the
// server maps sentinels to gRPC status codes and the client maps them back. This
// keeps the split behaviorally identical to the monolith.

// toStatus maps a domain sentinel to a gRPC status error (server side).
func toStatus(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, store.ErrNotFound):
		return status.Error(codes.NotFound, err.Error())
	case errors.Is(err, message.ErrForbidden):
		return status.Error(codes.PermissionDenied, err.Error())
	case errors.Is(err, message.ErrEmptyMessage):
		return status.Error(codes.InvalidArgument, "empty")
	case errors.Is(err, message.ErrTooLong):
		return status.Error(codes.InvalidArgument, "too_long")
	case errors.Is(err, message.ErrBadCommand):
		return status.Error(codes.InvalidArgument, "bad_command")
	default:
		return err // unknown → grpc reports codes.Unknown, message preserved
	}
}

// fromStatus maps a gRPC status error back to the domain sentinel (client side),
// so downstream errors.Is checks in the gateway behave as in-process.
func fromStatus(err error) error {
	if err == nil {
		return nil
	}
	st, ok := status.FromError(err)
	if !ok {
		return err
	}
	switch st.Code() {
	case codes.NotFound:
		return store.ErrNotFound
	case codes.PermissionDenied:
		return message.ErrForbidden
	case codes.InvalidArgument:
		switch st.Message() {
		case "empty":
			return message.ErrEmptyMessage
		case "too_long":
			return message.ErrTooLong
		case "bad_command":
			return message.ErrBadCommand
		}
		return err
	default:
		return err
	}
}
