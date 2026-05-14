package mqtt

import (
	"context"
	"errors"
	"fmt"
	u "sbam/src/utils"
	"strings"
	"time"
)

const (
	defaultOpTimeout = 5 * time.Second
)

var (
	newClientFactory = New
	newNoopFactory   = func() Client { return NewNoop() }
)

// connectWithRetries performs a small number of Connect attempts with
// exponential backoff and returns the last error if all attempts fail.
// It uses a short per-attempt timeout governed by defaultOpTimeout.
func connectWithRetries(client Client, maxAttempts int, baseBackoff time.Duration) error {
	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		attemptCtx, attemptCancel := context.WithTimeout(context.Background(), defaultOpTimeout)
		err := client.Connect(attemptCtx)
		attemptCancel()
		if err == nil {
			return nil
		}
		lastErr = err
		u.Log.Warnw("mqtt connect attempt failed", "attempt", attempt, "error", err)

		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			u.Log.Warnw("mqtt connect aborted, disconnecting client due to context timeout", "error", err)
			dctx, dcancel := context.WithTimeout(context.Background(), defaultOpTimeout)
			_ = client.Disconnect(dctx)
			dcancel()
		}

		if attempt < maxAttempts {
			sleep := time.Duration(1<<uint(attempt-1)) * baseBackoff
			time.Sleep(sleep)
		}
	}
	return lastErr
}

// InitWithCleanup encapsulates client creation, initial connect (with
// retries) and returns a cleanup function that attempts a graceful
// disconnect. On Home Assistant discovery it subscribes to the status
// topic and publishes discovery when HA comes online. Optional handlers
// are invoked after discovery publish in the same callback.
func InitWithCleanup(cfg Config, version string, maxAttempts int, baseBackoff time.Duration) (Client, func(), error) {
	client, newErr := newClientFactory(cfg, version)
	var accErr error
	if newErr != nil {
		accErr = errors.Join(accErr, fmt.Errorf("mqtt client setup failed: %w", newErr))
		u.Log.Warnw("mqtt client setup failed, using noop", "error", newErr)
		client = newNoopFactory()
	}

	if cfg.Enabled {
		if connErr := connectWithRetries(client, maxAttempts, baseBackoff); connErr != nil {
			accErr = errors.Join(accErr, fmt.Errorf("mqtt connect failed after retries: %w", connErr))
			u.Log.Warnw("mqtt connect failed after retries, using noop", "error", connErr)
			client = newNoopFactory()
		} else if client.IsConnected() && cfg.HADiscovery {
			subCtx, subCancel := context.WithTimeout(context.Background(), defaultOpTimeout)
			subErr := client.Subscribe(subCtx, haStatusTopic(), byte(1), func(topic string, payload []byte) {
				_ = topic
				payloadCopy := append([]byte(nil), payload...)
				if strings.TrimSpace(string(payloadCopy)) != "online" {
					return
				}

				ctx, cancel := context.WithTimeout(context.Background(), defaultOpTimeout)
				PublishDiscovery(ctx, client, cfg, version)
				cancel()
			})
			subCancel()
			if subErr != nil {
				accErr = errors.Join(accErr, fmt.Errorf("mqtt subscribe homeassistant/status failed: %w", subErr))
			}
		}
	}

	cleanup := func() {}
	if cfg.Enabled {
		cleanup = func() {
			disconnectCtx, disconnectCancel := context.WithTimeout(context.Background(), defaultOpTimeout)
			_ = client.Disconnect(disconnectCtx)
			disconnectCancel()
		}
	}

	return client, cleanup, accErr
}
