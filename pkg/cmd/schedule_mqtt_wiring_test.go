package cmd

import (
	"context"
	"errors"
	"testing"
	"time"

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

func TestSubscribeScheduleCommands_SkipsWhenDisabledOrDisconnected(t *testing.T) {
	client := &recordingMQTTClient{connected: true}
	runner := newRunnerForTests(client)

	err := subscribeScheduleCommands(context.Background(), client, mqtt.Config{Enabled: false, TopicPrefix: "sbam"}, runner)
	require.NoError(t, err)
	assert.Equal(t, 0, client.subscribeCalls)

	client.connected = false
	err = subscribeScheduleCommands(context.Background(), client, mqtt.Config{Enabled: true, TopicPrefix: "sbam"}, runner)
	require.NoError(t, err)
	assert.Equal(t, 0, client.subscribeCalls)
}

func TestSubscribeScheduleCommands_RequiresRunner(t *testing.T) {
	client := &recordingMQTTClient{connected: true}
	err := subscribeScheduleCommands(context.Background(), client, mqtt.Config{Enabled: true, TopicPrefix: "sbam"}, nil)
	require.Error(t, err)
	assert.ErrorContains(t, err, "runner must not be nil")
}

func TestPublishLatestState_NoSnapshot(t *testing.T) {
	client := &recordingMQTTClient{connected: true}
	latest := newLatestStateCache()

	published := publishLatestState(context.Background(), client, mqtt.Config{Enabled: true, TopicPrefix: "sbam"}, latest)
	assert.False(t, published)
	assert.Empty(t, client.publishedTopics)
}

func TestPublishLatestState_PublishesCachedSnapshotCopy(t *testing.T) {
	client := &recordingMQTTClient{connected: true}
	latest := newLatestStateCache()

	now := time.Date(2026, time.May, 14, 12, 0, 0, 0, time.UTC)
	soc := 42.0
	capacity := 10000.0
	next := now.Add(30 * time.Minute)
	payload := mqtt.StatePayload{
		BatterySOCPct:      &soc,
		BatteryCapacityWh:  &capacity,
		LastDecision:       "idle",
		LastDecisionReason: "cached",
		NextRun:            &next,
		Timestamp:          now,
	}

	runner := NewRunner(RunnerConfig{
		StartHR:            "00:00",
		EndHR:              "23:59",
		BattReserveStartHR: "00:00",
		BattReserveEndHR:   "23:59",
		MQTT:               mqtt.Config{Enabled: true, TopicPrefix: "sbam"},
		LatestState:        latest,
		Now:                func() time.Time { return now },
	}, client)

	runner.publishState(payload)

	soc = 88.0
	capacity = 5000.0
	next = now.Add(2 * time.Hour)

	published := publishLatestState(context.Background(), client, mqtt.Config{Enabled: true, TopicPrefix: "sbam"}, latest)
	require.True(t, published)
	require.GreaterOrEqual(t, len(client.publishedBodies), 2)

	republished := decodeStatePayload(t, client.publishedBodies[len(client.publishedBodies)-1])
	require.NotNil(t, republished.BatterySOCPct)
	require.NotNil(t, republished.BatteryCapacityWh)
	require.NotNil(t, republished.NextRun)
	assert.Equal(t, 42.0, *republished.BatterySOCPct)
	assert.Equal(t, 10000.0, *republished.BatteryCapacityWh)
	assert.Equal(t, now.Add(30*time.Minute), republished.NextRun.UTC())
}
