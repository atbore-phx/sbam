package mqtt

import "context"

func NewNoop() *Noop
func (n *Noop) Connect(ctx context.Context) error
func (n *Noop) Disconnect(ctx context.Context) error
func (n *Noop) Publish(ctx context.Context, topic string, qos byte, retained bool, payload []byte) error
func (n *Noop) Subscribe(ctx context.Context, topic string, qos byte, handler MessageHandler) error
func (n *Noop) IsConnected() bool
