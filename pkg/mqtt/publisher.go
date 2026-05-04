package mqtt

import "context"

func PublishState(ctx context.Context, client Client, prefix string, payload StatePayload)
func PublishError(ctx context.Context, client Client, prefix string, payload ErrorPayload)
func PublishAvailability(ctx context.Context, client Client, prefix string, online bool)
