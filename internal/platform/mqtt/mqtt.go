package mqtt

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	paho "github.com/eclipse/paho.mqtt.golang"
)

type Options struct {
	Broker       string
	ClientID     string
	Username     string
	Password     string
	KeepAlive    time.Duration
	MaxReconnect time.Duration
}

type Client struct {
	client    paho.Client
	log       *slog.Logger
	mu        sync.RWMutex
	onConnect func()
}

func New(opts Options, log *slog.Logger) *Client {
	if opts.KeepAlive <= 0 {
		opts.KeepAlive = 30 * time.Second
	}
	if opts.MaxReconnect <= 0 {
		opts.MaxReconnect = 30 * time.Second
	}

	c := &Client{log: log}

	pahoOpts := paho.NewClientOptions().
		AddBroker(opts.Broker).
		SetClientID(opts.ClientID).
		SetUsername(opts.Username).
		SetPassword(opts.Password).
		SetKeepAlive(opts.KeepAlive).
		SetPingTimeout(10 * time.Second).
		SetConnectTimeout(10 * time.Second).
		SetAutoReconnect(true).
		SetConnectRetry(false).
		SetMaxReconnectInterval(opts.MaxReconnect).
		SetCleanSession(false).
		SetOrderMatters(false).
		SetOnConnectHandler(func(paho.Client) {
			c.log.Info("mqtt connected", "broker", opts.Broker, "client_id", opts.ClientID)
			c.mu.RLock()
			cb := c.onConnect
			c.mu.RUnlock()
			if cb != nil {
				cb()
			}
		}).
		SetConnectionLostHandler(func(_ paho.Client, err error) {
			c.log.Warn("mqtt connection lost", "error", err)
		}).
		SetReconnectingHandler(func(paho.Client, *paho.ClientOptions) {
			c.log.Info("mqtt reconnecting")
		})

	c.client = paho.NewClient(pahoOpts)
	return c
}

func (c *Client) SetOnConnect(cb func()) {
	c.mu.Lock()
	c.onConnect = cb
	c.mu.Unlock()
}

func (c *Client) Connect(ctx context.Context) error {
	backoff := 2 * time.Second
	for {
		token := c.client.Connect()
		if token.WaitTimeout(10*time.Second) && token.Error() == nil {
			return nil
		}
		err := token.Error()
		if err == nil {
			err = errors.New("mqtt connect timeout")
		}
		c.log.Warn("mqtt connect failed, retrying", "error", err, "retry_in", backoff)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
		}
		if backoff < 30*time.Second {
			backoff *= 2
		}
	}
}

func (c *Client) Subscribe(topic string, qos byte, handler paho.MessageHandler) error {
	if !c.client.IsConnectionOpen() {
		return fmt.Errorf("mqtt not connected")
	}
	token := c.client.Subscribe(topic, qos, handler)
	if !token.WaitTimeout(10 * time.Second) {
		return fmt.Errorf("subscribe %s: timeout", topic)
	}
	if err := token.Error(); err != nil {
		return fmt.Errorf("subscribe %s: %w", topic, err)
	}
	c.log.Info("mqtt subscribed", "topic", topic, "qos", qos)
	return nil
}

func (c *Client) Publish(ctx context.Context, topic string, qos byte, payload []byte) error {
	if !c.client.IsConnectionOpen() {
		return fmt.Errorf("mqtt not connected")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	token := c.client.Publish(topic, qos, false, payload)
	if !token.WaitTimeout(5 * time.Second) {
		return fmt.Errorf("publish %s: timeout", topic)
	}
	return token.Error()
}

func (c *Client) IsConnected() bool { return c.client.IsConnectionOpen() }

func (c *Client) Disconnect() {
	c.client.Disconnect(250)
}
