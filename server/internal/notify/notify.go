// Package notify is the Notification Service (Section 6). It consumes push jobs
// that fanout enqueues for offline recipients and hands them to a push Provider
// (APNs/FCM in production). The Provider interface keeps the transport pluggable;
// the MVP ships a LogProvider that "delivers" by logging, so the offline path is
// observable end-to-end without real push credentials.
package notify

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"

	"github.com/synapse-chat/synapse/internal/metrics"

	"github.com/synapse-chat/synapse/pkg/eventbus"
)

// New builds the notification service.
func New(bus eventbus.Bus, provider Provider, log *slog.Logger) *Service {
	return &Service{bus: bus, provider: provider, log: log}
}

// WithDevices enables the real push path: one delivery per registered device,
// and removal of tokens the provider reports as dead. Without it the service
// keeps the old behaviour of handing the job to the provider as-is, which is
// all the LogProvider needs.
func (s *Service) WithDevices(d Devices) *Service { s.devices = d; return s }

// Start subscribes to the push topic in the "notify" queue group (so, across
// replicas, each job is delivered once).
func (s *Service) Start() error {
	return s.bus.Subscribe(eventbus.SubjNotifyPush, "notify", s.onPush)
}

func (s *Service) onPush(ctx context.Context, e eventbus.Event) error {
	var job PushJob
	if err := json.Unmarshal(e.Data, &job); err != nil {
		return err
	}
	if s.devices == nil {
		if err := s.provider.Send(ctx, job); err != nil {
			s.log.Warn("push send failed", "provider", s.provider.Name(), "user", job.UserID, "err", err)
			return err
		}
		return nil
	}
	return s.fanOutToDevices(ctx, job)
}

// fanOutToDevices delivers one job to every registered device of the recipient.
// A user is a SET of devices with independent tokens; "send to a user" is not an
// operation any provider offers.
func (s *Service) fanOutToDevices(ctx context.Context, job PushJob) error {
	devs, err := s.devices.ListDevices(ctx, job.UserID)
	if err != nil {
		return err
	}
	sender, canTarget := s.provider.(deviceSender)
	var firstErr error
	for _, d := range devs {
		if d.Token == "" {
			continue // registered device, no push token yet (web, or not granted)
		}
		perDevice := job
		perDevice.Token, perDevice.Platform = d.Token, d.Platform

		var err error
		if canTarget {
			err = sender.SendToDevice(ctx, d, perDevice)
		} else {
			err = s.provider.Send(ctx, perDevice)
		}
		switch {
		case err == nil:
		case errors.Is(err, errTokenDead):
			// The app is gone from that device. Keeping the token means paying for
			// an impossible delivery on every future message, forever.
			if err := s.devices.InvalidateToken(ctx, d.DeviceID); err != nil {
				s.log.Warn("could not drop dead push token", "device", d.DeviceID, "err", err)
			} else {
				metrics.PushTokensInvalidated.Inc()
				s.log.Info("dropped dead push token", "device", d.DeviceID, "user", job.UserID)
			}
		default:
			// One device failing must not cost the others their notification.
			s.log.Warn("push send failed", "provider", s.provider.Name(),
				"user", job.UserID, "device", d.DeviceID, "err", err)
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
}

// Send logs the notification.
func (p LogProvider) Send(_ context.Context, job PushJob) error {
	p.Log.Info("PUSH", "to", job.UserID, "chat", job.ChatID, "from", job.SenderID, "preview", job.Preview)
	return nil
}

// Name identifies the provider.
func (p LogProvider) Name() string { return "log" }
