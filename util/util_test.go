package util

import (
	"testing"
	"time"

	"go.uber.org/zap"
)

func TestBackoff(t *testing.T) {
	logger := zap.NewNop()

	for i := 0; i < 3; i++ {
		start := time.Now()
		Backoff(0, logger)
		wait := time.Since(start).Nanoseconds() / int64(time.Millisecond)

		if !(wait >= 0 && wait < int64((i+1)*100)) {
			t.Error("Expected sleep for [0 --", (i+1)*100, ") got", wait)
		}
	}
}

func Test_getCaller(t *testing.T) {
	logger := zap.NewNop()

	expected := "testing.tRunner"
	caller := getCaller(logger)
	if caller != expected {
		t.Error("Failed to get caller. Expected", expected, ", got", caller)
	}
}
