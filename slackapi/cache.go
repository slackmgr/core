package slackapi

import (
	"context"
	"time"
)

type Cache interface {
	Get(ctx context.Context, key string) (string, bool, error)
	GetInt(ctx context.Context, key string) (int, bool, error)
	GetBool(ctx context.Context, key string) (bool, bool, error)
	Set(ctx context.Context, key string, value interface{}, expiration time.Duration) error
	SetWithRandomExpiration(ctx context.Context, key string, value any, minExpiration time.Duration, variation time.Duration) error
}
