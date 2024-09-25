package utils

import (
	"bytes"
	"log"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDefaultLogger(t *testing.T) {

	tests := []struct {
		name          string
		level         int
		logFunc       func(*DefaultLogger, string, ...any)
		message       string
		expectedLog   string
		shouldContain bool
	}{
		{"Debug log at DEBUG level", DEBUG, (*DefaultLogger).Debugf, "Debug message", "Debug message", true},
		{"Info log at DEBUG level", DEBUG, (*DefaultLogger).Infof, "Info message", "Info message", true},
		{"Warn log at DEBUG level", DEBUG, (*DefaultLogger).Warnf, "Warn message", "Warn message", true},
		{"Error log at DEBUG level", DEBUG, (*DefaultLogger).Errorf, "Error message", "Error message", true},

		{"Debug log at INFO level", INFO, (*DefaultLogger).Debugf, "Debug message", "Debug message", false},
		{"Info log at INFO level", INFO, (*DefaultLogger).Infof, "Info message", "Info message", true},
		{"Warn log at INFO level", INFO, (*DefaultLogger).Warnf, "Warn message", "Warn message", true},
		{"Error log at INFO level", INFO, (*DefaultLogger).Errorf, "Error message", "Error message", true},

		{"Debug log at WARN level", WARN, (*DefaultLogger).Debugf, "Debug message", "Debug message", false},
		{"Info log at WARN level", WARN, (*DefaultLogger).Infof, "Info message", "Info message", false},
		{"Warn log at WARN level", WARN, (*DefaultLogger).Warnf, "Warn message", "Warn message", true},
		{"Error log at WARN level", WARN, (*DefaultLogger).Errorf, "Error message", "Error message", true},

		{"Debug log at ERROR level", ERROR, (*DefaultLogger).Debugf, "Debug message", "Debug message", false},
		{"Info log at ERROR level", ERROR, (*DefaultLogger).Infof, "Info message", "Info message", false},
		{"Warn log at ERROR level", ERROR, (*DefaultLogger).Warnf, "Warn message", "Warn message", false},
		{"Error log at ERROR level", ERROR, (*DefaultLogger).Errorf, "Error message", "Error message", true},

		{"Debug log at NONE level", NONE, (*DefaultLogger).Debugf, "Debug message", "Debug message", false},
		{"Info log at NONE level", NONE, (*DefaultLogger).Infof, "Info message", "Info message", false},
		{"Warn log at NONE level", NONE, (*DefaultLogger).Warnf, "Warn message", "Warn message", false},
		{"Error log at NONE level", NONE, (*DefaultLogger).Errorf, "Error message", "Error message", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {

			var buf bytes.Buffer
			log.SetOutput(&buf)
			defer log.SetOutput(os.Stderr)

			logger := &DefaultLogger{Level: tt.level}
			tt.logFunc(logger, tt.message)

			logOutput := buf.String()
			if tt.shouldContain {
				assert.Contains(t, logOutput, tt.expectedLog)
			} else {
				assert.NotContains(t, logOutput, tt.expectedLog)
			}
		})
	}
}

func TestDefaultLoggerFormatting(t *testing.T) {
	var buf bytes.Buffer
	log.SetOutput(&buf)
	defer log.SetOutput(os.Stderr)

	logger := &DefaultLogger{Level: DEBUG}
	logger.Debugf("Test %s %d", "message", 123)

	logOutput := buf.String()
	assert.True(t, strings.Contains(logOutput, "Test message 123"))
}
