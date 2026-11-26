package engine

import (
	"context"
	"errors"
	"fmt"
	"runtime/debug"
	"time"
)

// ErrPluginTimeout is returned when a plugin call exceeds its timeout.
var ErrPluginTimeout = errors.New("plugin call timed out")

// PanicError wraps a recovered panic.
type PanicError struct {
	Value any
	Stack string
}

func (e *PanicError) Error() string { return fmt.Sprintf("plugin panicked: %v", e.Value) }

// guard runs fn with a timeout and converts panics into errors. On timeout
// the call's context is cancelled and guard returns without waiting for fn.
func guard(ctx context.Context, timeout time.Duration, fn func(ctx context.Context) error) error {
	if timeout <= 0 {
		timeout = 30 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				done <- &PanicError{Value: r, Stack: string(debug.Stack())}
			}
		}()
		done <- fn(ctx)
	}()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return fmt.Errorf("%w after %s", ErrPluginTimeout, timeout)
		}
		return ctx.Err()
	}
}
