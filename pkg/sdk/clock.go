package sdk

import (
	"fmt"
	"strings"
	"time"
)

// Clock abstracts the current time so results are reproducible in tests.
type Clock interface {
	Now() time.Time
}

// SystemClock returns the real time in UTC.
type SystemClock struct{}

// Now implements Clock.
func (SystemClock) Now() time.Time { return time.Now().UTC() }

// FixedClock always returns the same instant.
type FixedClock struct{ T time.Time }

// Now implements Clock.
func (c FixedClock) Now() time.Time { return c.T }

// Duration is a time.Duration that unmarshals from strings such as "30s" or
// "5m" in configuration files.
type Duration time.Duration

// UnmarshalText implements encoding.TextUnmarshaler.
func (d *Duration) UnmarshalText(b []byte) error {
	v, err := time.ParseDuration(strings.TrimSpace(string(b)))
	if err != nil {
		return fmt.Errorf("invalid duration %q (examples: 30s, 5m, 1h)", string(b))
	}
	*d = Duration(v)
	return nil
}

// MarshalText implements encoding.TextMarshaler.
func (d Duration) MarshalText() ([]byte, error) { return []byte(time.Duration(d).String()), nil }

// Std returns the standard library duration.
func (d Duration) Std() time.Duration { return time.Duration(d) }

// Date is a calendar date (YYYY-MM-DD) used for expiry fields in configuration.
type Date struct{ time.Time }

// UnmarshalText implements encoding.TextUnmarshaler.
func (d *Date) UnmarshalText(b []byte) error {
	t, err := time.Parse(time.DateOnly, strings.TrimSpace(string(b)))
	if err != nil {
		return fmt.Errorf("invalid date %q (expected YYYY-MM-DD)", string(b))
	}
	d.Time = t
	return nil
}

// MarshalText implements encoding.TextMarshaler.
func (d Date) MarshalText() ([]byte, error) { return []byte(d.Format(time.DateOnly)), nil }
