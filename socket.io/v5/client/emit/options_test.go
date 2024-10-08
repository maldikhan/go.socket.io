package emit

import (
	"reflect"
	"testing"
	"time"
)

func TestEmitOptions(t *testing.T) {
	timeout := 5 * time.Second
	timeoutCallback := func() {}
	ackCallback := func(args ...interface{}) {}

	options := &EmitOptions{
		timeout:         &timeout,
		timeoutCallback: timeoutCallback,
		ackCallback:     ackCallback,
	}

	if !reflect.DeepEqual(options.Timeout(), &timeout) {
		t.Errorf("Expected timeout %v, got %v", timeout, options.Timeout())
	}

	if reflect.ValueOf(options.TimeoutCallback()).Pointer() != reflect.ValueOf(timeoutCallback).Pointer() {
		t.Error("TimeoutCallback does not match the set callback")
	}

	if reflect.ValueOf(options.AckCallback()).Pointer() != reflect.ValueOf(ackCallback).Pointer() {
		t.Error("AckCallback does not match the set callback")
	}
}

func TestWithTimeout(t *testing.T) {
	timeout := 3 * time.Second
	timeoutCallback := func() {}

	option := WithTimeout(timeout, timeoutCallback)
	options := &EmitOptions{}
	option(options)

	if !reflect.DeepEqual(options.Timeout(), &timeout) {
		t.Errorf("Expected timeout %v, got %v", timeout, options.Timeout())
	}

	if reflect.ValueOf(options.TimeoutCallback()).Pointer() != reflect.ValueOf(timeoutCallback).Pointer() {
		t.Error("TimeoutCallback does not match the set callback")
	}
}

func TestWithAck(t *testing.T) {
	ackCallback := func(args ...interface{}) {}

	option := WithAck(ackCallback)
	options := &EmitOptions{}
	option(options)

	if reflect.ValueOf(options.AckCallback()).Pointer() != reflect.ValueOf(ackCallback).Pointer() {
		t.Error("AckCallback does not match the set callback")
	}
}

func TestEmitOptionsCombined(t *testing.T) {
	timeout := 2 * time.Second
	timeoutCallback := func() {}
	ackCallback := func(args ...interface{}) {}

	options := &EmitOptions{}
	WithTimeout(timeout, timeoutCallback)(options)
	WithAck(ackCallback)(options)

	if !reflect.DeepEqual(options.Timeout(), &timeout) {
		t.Errorf("Expected timeout %v, got %v", timeout, options.Timeout())
	}

	if reflect.ValueOf(options.TimeoutCallback()).Pointer() != reflect.ValueOf(timeoutCallback).Pointer() {
		t.Error("TimeoutCallback does not match the set callback")
	}

	if reflect.ValueOf(options.AckCallback()).Pointer() != reflect.ValueOf(ackCallback).Pointer() {
		t.Error("AckCallback does not match the set callback")
	}
}
