package emit

import (
	"time"
)

type EmitOption func(*EmitOptions)

type EmitOptions struct {
	timeout         *time.Duration
	timeoutCallback func()
	ackCallback     interface{}
}

func (o *EmitOptions) Timeout() *time.Duration {
	return o.timeout
}
func (o *EmitOptions) TimeoutCallback() func() {
	return o.timeoutCallback
}

func (o *EmitOptions) AckCallback() interface{} {
	return o.ackCallback
}

func WithTimeout(timeout time.Duration, callback func()) EmitOption {
	return func(o *EmitOptions) {
		o.timeout = &timeout
		o.timeoutCallback = callback
	}
}

func WithAck(callback interface{}) EmitOption {
	return func(o *EmitOptions) {
		o.ackCallback = callback
	}
}
