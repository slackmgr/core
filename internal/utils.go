package internal

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
)

func TrySend[T any](ctx context.Context, msg T, sinkCh chan<- T) error {
	select {
	case sinkCh <- msg:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func IsCtxCanceledErr(err error) bool {
	return errors.Is(err, context.Canceled)
}

func Hash(input ...string) string {
	h := sha256.New()

	for _, s := range input {
		h.Write([]byte(s))
	}

	bs := h.Sum(nil)

	return base64.URLEncoding.EncodeToString(bs)
}

func HashBytes(b []byte) []byte {
	h := sha256.New()
	h.Write(b)
	return h.Sum(nil)
}
