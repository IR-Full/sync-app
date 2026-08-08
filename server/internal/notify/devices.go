package notify

import (
	"context"
	"log/slog"
)

// ListDevices returns every registered device of a user with its push token.
func (d StoreDevices) ListDevices(ctx context.Context, userID string) ([]DeviceToken, error) {
	devs, err := d.Users.ListDevices(ctx, userID)
	if err != nil {
		return nil, err
	}
	out := make([]DeviceToken, 0, len(devs))
	for _, dev := range devs {
		out = append(out, DeviceToken{DeviceID: dev.ID, Platform: dev.Platform, Token: dev.PushToken})
	}
	return out, nil
}

// InvalidateToken clears a token the provider reported as dead. The device row
// stays: the device still exists and may register a fresh token on next launch —
// it is the token that is gone, not the device.
func (d StoreDevices) InvalidateToken(ctx context.Context, deviceID string) error {
	dev, err := d.Users.GetDevice(ctx, deviceID)
	if err != nil {
		return err
	}
	return d.Users.SetPushToken(ctx, dev.UserID, deviceID, "")
}

// ProviderFor builds the push provider described by configuration: an HTTP
// endpoint when one is set, the logging stand-in otherwise. Defaulting to the
// logger keeps `go run ./cmd/server` working with no credentials while making
// the real path one environment variable away.
func ProviderFor(endpoint, authKey string, log *slog.Logger) Provider {
	if endpoint == "" {
		return LogProvider{Log: log}
	}
	return NewHTTPProvider(endpoint, authKey, log)
}
