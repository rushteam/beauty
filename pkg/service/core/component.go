package core

import "context"

type Component interface {
	Name() string
	Init() context.CancelFunc
}