package mqtt

import (
	"context"
	"errors"
	"fmt"
	u "sbam/src/utils"
	"time"
)

const (
	defaultOpTimeout = 5 * time.Second
)

var (
	newClientFactory = New
	newNoopFactory   = func() Client { return NewNoop() }
)

// InitWithCleanup creates an MQTT client and initiates a non-blocking
// connection attempt. If client setup fails (e.g. bad TLS config) a noop
// client is returned instead. The returned cleanup function attempts a
// graceful disconnect and should be deferred by the caller.
//
// The initial connection is handled by Paho's ConnectRetry mechanism in a
// background goroutine, so this function returns immediately without waiting
// for the broker to become available. OnConnectHandler in paho.go handles
// subscriptions and discovery once the connection succeeds.
func InitWithCleanup(cfg Config, version string) (Client, func(), error) {
	client, newErr := newClientFactory(cfg, version)
	var accErr error
	if newErr != nil {
		accErr = errors.Join(accErr, fmt.Errorf("mqtt client setup failed: %w", newErr))
		u.Log.Warnw("mqtt client setup failed, using noop", "error", newErr)
		client = newNoopFactory()
	}

	if cfg.Enabled {
		go func() {
			u.Log.Infow("mqtt starting background connect", "broker", cfg.Broker)
			if err := client.Connect(context.Background()); err != nil {
				u.Log.Errorw("mqtt background connect failed", "error", err)
			}
		}()
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
