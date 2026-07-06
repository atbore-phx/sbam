package mqtt

import "context"

type Noop struct{}

func NewNoop() *Noop {
	return &Noop{}
}

func (n *Noop) Connect(ctx context.Context) error {
	return nil
}

func (n *Noop) Disconnect(ctx context.Context) error {
	return nil
}

func (n *Noop) Publish(ctx context.Context, topic string, qos byte, retained bool, payload []byte) error {
	return nil
}

func (n *Noop) Subscribe(ctx context.Context, topic string, qos byte, handler MessageHandler) error {
	return nil
}

func (n *Noop) IsConnected() bool {
	return false
}

func (n *Noop) OnConnect(cb func()) {}
