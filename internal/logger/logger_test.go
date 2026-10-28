package logger

import (
	"testing"

	"go.uber.org/zap"
)

func TestNewHonorsLevelAndFormat(t *testing.T) {
	debugLogger, err := New("debug", "text")
	if err != nil {
		t.Fatalf("New(debug, text) error = %v", err)
	}

	if !debugLogger.Core().Enabled(zap.DebugLevel) {
		t.Error("debug level should be enabled")
	}

	errorLogger, err := New("error", "json")
	if err != nil {
		t.Fatalf("New(error, json) error = %v", err)
	}

	if errorLogger.Core().Enabled(zap.InfoLevel) {
		t.Error("info level should be disabled when level=error")
	}
}
