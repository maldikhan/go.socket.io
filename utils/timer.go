package utils

import "time"

type DefaultTimer struct{}

func (t *DefaultTimer) After(d time.Duration) <-chan time.Time {
	return time.After(d)
}
