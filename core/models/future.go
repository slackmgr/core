package models

import "context"

type Future interface {
	Wait(context.Context) error
}
