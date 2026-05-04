package mqtt

import "context"

func NewPaho(cfg Config) (*Paho, error)
func (p *Paho) Connect(ctx context.Context) error
func (p *Paho) Disconnect(ctx context.Context) error
func (p *Paho) Publish(ctx context.Context, topic string, qos byte, retained bool, payload []byte) error
func (p *Paho) Subscribe(ctx context.Context, topic string, qos byte, handler MessageHandler) error
func (p *Paho) IsConnected() bool
