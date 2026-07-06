package cmd

import (
	"context"
	"errors"
	"testing"

	"sbam/pkg/mqtt"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type recordingMQTTClient struct {
	connected       bool
	subscribeErr    error
	subscribeCalls  int
	subscribeTopic  string
	subscribeHdlr   mqtt.MessageHandler
	publishedTopics []string
	publishedBodies [][]byte
	onConnectCB     func()
}

func (c *recordingMQTTClient) Connect(context.Context) error { return nil }

func (c *recordingMQTTClient) Disconnect(context.Context) error { return nil }

func (c *recordingMQTTClient) Publish(ctx context.Context, topic string, qos byte, retained bool, payload []byte) error {
	_ = ctx
	_ = qos
	_ = retained
	c.publishedTopics = append(c.publishedTopics, topic)
	c.publishedBodies = append(c.publishedBodies, append([]byte(nil), payload...))
	return nil
}

func (c *recordingMQTTClient) Subscribe(ctx context.Context, topic string, qos byte, handler mqtt.MessageHandler) error {
	_ = ctx
	_ = qos
	c.subscribeCalls++
	c.subscribeTopic = topic
	c.subscribeHdlr = handler
	return c.subscribeErr
}

func (c *recordingMQTTClient) IsConnected() bool {
	return c.connected
}

func (c *recordingMQTTClient) OnConnect(cb func()) {
	c.onConnectCB = cb
}

func TestSubscribeScheduleCommands_DefaultPrefixRoutesTriggerNow(t *testing.T) {
	client := &recordingMQTTClient{connected: true}
	runner := newRunnerForTests(client)

	err := subscribeScheduleCommands(context.Background(), client, mqtt.Config{Enabled: true, TopicPrefix: " "}, runner)
	require.NoError(t, err)
	require.Equal(t, 1, client.subscribeCalls)
	assert.Equal(t, "sbam/cmd/+", client.subscribeTopic)
	require.NotNil(t, client.subscribeHdlr)

	client.subscribeHdlr("sbam/cmd/trigger_now", nil)
	select {
	case intent := <-runner.intents:
		assert.Equal(t, mqtt.IntentTriggerNow, intent.Kind)
		assert.Equal(t, "sbam/cmd/trigger_now", intent.CommandTopic)
	default:
		t.Fatal("expected trigger_now intent to be enqueued")
	}
}

func TestSubscribeScheduleCommands_CustomPrefix(t *testing.T) {
	client := &recordingMQTTClient{connected: true}
	runner := newRunnerForTests(client)

	err := subscribeScheduleCommands(context.Background(), client, mqtt.Config{Enabled: true, TopicPrefix: "house/sbam"}, runner)
	require.NoError(t, err)
	assert.Equal(t, "house/sbam/cmd/+", client.subscribeTopic)
}

func TestSubscribeScheduleCommands_ParseFailurePublishesRejectedAck(t *testing.T) {
	client := &recordingMQTTClient{connected: true}
	runner := newRunnerForTests(client)

	err := subscribeScheduleCommands(context.Background(), client, mqtt.Config{Enabled: true, TopicPrefix: "sbam"}, runner)
	require.NoError(t, err)
	require.NotNil(t, client.subscribeHdlr)

	client.subscribeHdlr("sbam/cmd/force_charge", []byte(`{"target_pct":"bad"}`))

	select {
	case <-runner.intents:
		t.Fatal("invalid payload must not enqueue intents")
	default:
	}

	require.NotEmpty(t, client.publishedTopics)
	ackTopic := client.publishedTopics[len(client.publishedTopics)-1]
	ackBody := client.publishedBodies[len(client.publishedBodies)-1]
	assert.Equal(t, "sbam/cmd/force_charge/ack", ackTopic)
	ack := decodeAckPayload(t, ackBody)
	assert.False(t, ack.Accepted)
	assert.Equal(t, "force_charge", ack.Command)
	assert.Contains(t, ack.Error, mqtt.ErrInvalidPayload.Error())
}

func TestSubscribeScheduleCommands_SubscribeError(t *testing.T) {
	client := &recordingMQTTClient{connected: true, subscribeErr: errors.New("subscribe failed")}
	runner := newRunnerForTests(client)

	err := subscribeScheduleCommands(context.Background(), client, mqtt.Config{Enabled: true, TopicPrefix: "sbam"}, runner)
	require.Error(t, err)
	assert.ErrorContains(t, err, "subscribe failed")
}

func TestSubscribeScheduleCommands_SkipsWhenDisabled(t *testing.T) {
	client := &recordingMQTTClient{connected: true}
	runner := newRunnerForTests(client)

	err := subscribeScheduleCommands(context.Background(), client, mqtt.Config{Enabled: false, TopicPrefix: "sbam"}, runner)
	require.NoError(t, err)
	assert.Equal(t, 0, client.subscribeCalls)

	// When MQTT is enabled, subscription proceeds even if not connected
	// (the function is called from OnConnect callback where connection is guaranteed).
	err = subscribeScheduleCommands(context.Background(), client, mqtt.Config{Enabled: true, TopicPrefix: "sbam"}, runner)
	require.NoError(t, err)
	assert.Equal(t, 1, client.subscribeCalls)
}

func TestSubscribeScheduleCommands_RequiresRunner(t *testing.T) {
	client := &recordingMQTTClient{connected: true}
	err := subscribeScheduleCommands(context.Background(), client, mqtt.Config{Enabled: true, TopicPrefix: "sbam"}, nil)
	require.Error(t, err)
	assert.ErrorContains(t, err, "runner must not be nil")
}
