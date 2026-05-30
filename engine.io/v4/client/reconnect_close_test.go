package engineio_v4_client

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// TestReconnectBackoffWakesOnClose is the issue-B regression test. When Close()
// is called while the reconnect supervisor is parked in its backoff wait, the
// loop must wake immediately via closeCh instead of blocking for the full (here
// deliberately huge) backoff duration. We drive the wake signal exactly as
// Close() does — set the closing flag, then close closeCh — and assert
// reconnectLoop returns well under the configured backoff.
func TestReconnectBackoffWakesOnClose(t *testing.T) {
	client, _, _, _ := newReconnectClient(t)
	// A backoff far larger than the test deadline: if the loop waited it out the
	// test would time out, proving the wake came from closeCh and not the timer.
	client.reconnectWait = 30 * time.Second
	client.closeCh = make(chan struct{})

	done := make(chan struct{})
	start := time.Now()
	go func() {
		client.reconnectLoop(assertErr("dropped"))
		close(done)
	}()

	// Let the loop reach its backoff wait, then signal Close() exactly as the real
	// Close() does: mark closing first (so the post-wait stopRequested aborts) and
	// then close closeCh to wake the select.
	time.Sleep(20 * time.Millisecond)
	atomic.StoreUint32(&client.closing, 1)
	close(client.closeCh)

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("reconnectLoop did not wake promptly on closeCh during backoff")
	}

	assert.Less(t, time.Since(start), 5*time.Second,
		"reconnectLoop must return well under the 30s backoff once Close signals closeCh")
}

// TestCloseReturnsPromptlyDuringBackoff exercises the full Close() path while a
// reconnect backoff is in progress and asserts Close() itself returns promptly
// (well under the backoff). The supervisor owns transportClosed here, so Close()
// waits on supervisorDone; closing closeCh unblocks the backoff so the supervisor
// goroutine can exit and Close() can return.
func TestCloseReturnsPromptlyDuringBackoff(t *testing.T) {
	client, transport, _, _ := newReconnectClient(t)
	client.reconnectWait = 30 * time.Second
	client.closeCh = make(chan struct{})
	client.supervisorDone = make(chan struct{})

	// Close() stops the live transport once during teardown.
	transport.EXPECT().Stop().Return(nil)

	// Mark a supervisor as running and drive supervise() so that, on a non-nil
	// transport drop, it enters reconnectLoop and parks in the backoff wait.
	atomic.StoreUint32(&client.supervisorStarted, 1)
	closed := client.transportClosed
	done := client.supervisorDone
	go client.supervise(closed, done)

	// Report an unexpected drop so supervise() proceeds into reconnectLoop.
	closed <- assertErr("dropped")

	// Give the supervisor time to enter the backoff wait.
	time.Sleep(20 * time.Millisecond)

	// Close() must return promptly: it closes closeCh (waking the backoff), then
	// waits on supervisorDone, which the now-woken supervisor closes on exit.
	closeReturned := make(chan struct{})
	start := time.Now()
	go func() {
		_ = client.Close()
		close(closeReturned)
	}()

	select {
	case <-closeReturned:
	case <-time.After(2 * time.Second):
		t.Fatal("Close() did not return promptly while a reconnect backoff was in progress")
	}
	assert.Less(t, time.Since(start), 5*time.Second,
		"Close() must not block for the full backoff duration")
}
