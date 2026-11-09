package realtime

import (
	"context"
	"encoding/json"
	"time"

	"github.com/redis/go-redis/v9"
)

const Channel = "ff:realtime"

type Envelope struct {
	Type      string    `json:"type"`
	DeviceID  string    `json:"device_id,omitempty"`
	Data      any       `json:"data,omitempty"`
	Timestamp time.Time `json:"ts"`
}

func Publish(ctx context.Context, rdb *redis.Client, env Envelope) error {
	if env.Timestamp.IsZero() {
		env.Timestamp = time.Now().UTC()
	}
	raw, err := json.Marshal(env)
	if err != nil {
		return err
	}
	return rdb.Publish(ctx, Channel, raw).Err()
}
