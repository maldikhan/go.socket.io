package utils

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestDefaultTimer_After(t *testing.T) {
	timer := &DefaultTimer{}

	// Базовый тест
	t.Run("Basic test", func(t *testing.T) {
		t.Parallel()
		duration := 10 * time.Millisecond
		ch := timer.After(duration)

		start := time.Now()
		<-ch
		elapsed := time.Since(start)

		assert.GreaterOrEqual(t, elapsed, duration)
	})

	// Тест с коротким таймаутом
	t.Run("Short timeout test", func(t *testing.T) {
		t.Parallel()
		duration := 50 * time.Millisecond
		ch := timer.After(duration)

		select {
		case <-ch:
			t.Error("Channel should not have fired yet")
		case <-time.After(10 * time.Millisecond):
			// Этот случай должен сработать
		}
	})
}
