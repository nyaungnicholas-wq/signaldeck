package main

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/nyaungnicholas-wq/signaldeck/internal/workers"
)

func TestClassify(t *testing.T) {
	t.Run("nil error", func(t *testing.T) {
		msg, code := classify(nil)
		if msg != "OK: no collapse, no inversion." || code != 0 {
			t.Errorf("nil error: expected (OK: no collapse, no inversion., 0), got (%s, %d)", msg, code)
		}
	})

	t.Run("wrapped ErrDegraded", func(t *testing.T) {
		err := fmt.Errorf("wrapped: %w", workers.ErrDegraded)
		msg, code := classify(err)
		if !strings.HasPrefix(msg, "DEGRADED (EXPECTED, NOT A FAULT)") || code != 0 {
			t.Errorf("wrapped ErrDegraded: expected (DEGRADED (EXPECTED, NOT A FAULT)..., 0), got (%s, %d)", msg, code)
		}
	})

	t.Run("plain error", func(t *testing.T) {
		err := errors.New("plain error")
		msg, code := classify(err)
		if !strings.HasPrefix(msg, "FAIL") || code != 1 {
			t.Errorf("plain error: expected (FAIL..., 1), got (%s, %d)", msg, code)
		}
	})

	t.Run("nested wrapped ErrDegraded", func(t *testing.T) {
		err := fmt.Errorf("outer: %w", fmt.Errorf("inner: %w", workers.ErrDegraded))
		msg, code := classify(err)
		if !strings.HasPrefix(msg, "DEGRADED (EXPECTED, NOT A FAULT)") || code != 0 {
			t.Errorf("nested wrapped ErrDegraded: expected (DEGRADED (EXPECTED, NOT A FAULT)..., 0), got (%s, %d)", msg, code)
		}
	})
}