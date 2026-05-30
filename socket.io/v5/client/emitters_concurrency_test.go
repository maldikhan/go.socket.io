package socketio_v5_client

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	socketio_v5 "github.com/maldikhan/go.socket.io/socket.io/v5"
)

// recordingEngineIO records the global order in which transport frames are
// written. Each recorded entry is tagged so the test can verify that the frames
// of one logical packet are emitted contiguously, with no foreign frame
// interleaved by a concurrent emit.
type recordingEngineIO struct {
	mu     sync.Mutex
	frames []string
}

func (e *recordingEngineIO) record(tag string) {
	e.mu.Lock()
	e.frames = append(e.frames, tag)
	e.mu.Unlock()
}

func (e *recordingEngineIO) Connect(_ context.Context) error { return nil }

func (e *recordingEngineIO) Send(message []byte) error {
	// A text frame: either a binary packet header ("H:<id>") or a plain text
	// event frame ("T").
	e.record(string(message))
	return nil
}

func (e *recordingEngineIO) SendBinary(message []byte) error {
	e.record(string(message))
	return nil
}

func (e *recordingEngineIO) On(_ string, _ func([]byte)) {}
func (e *recordingEngineIO) Close() error                { return nil }

// recordingParser produces deterministic, identifiable frames. Binary events
// (whose payload list contains a []byte) yield a header tagged "H:<id>" and a
// fixed number of attachment frames tagged "A:<id>:<n>". Text events yield a
// single "T" frame.
type recordingParser struct {
	attachments int
}

func (p *recordingParser) WrapCallback(_ interface{}) func(in []interface{}) { return nil }
func (p *recordingParser) Parse(_ []byte) (*socketio_v5.Message, error)      { return nil, nil }
func (p *recordingParser) ReconstructBinary(_ *socketio_v5.Message, _ [][]byte) error {
	return nil
}

func (p *recordingParser) HasBinary(event *socketio_v5.Event) bool {
	for _, payload := range event.Payloads {
		if _, ok := payload.([]byte); ok {
			return true
		}
	}
	return false
}

func (p *recordingParser) Serialize(msg *socketio_v5.Message) ([]byte, error) {
	return []byte("T"), nil
}

func (p *recordingParser) SerializeBinary(msg *socketio_v5.Message) ([]byte, [][]byte, error) {
	id := msg.Event.Name
	header := []byte("H:" + id)
	attachments := make([][]byte, p.attachments)
	for i := range attachments {
		attachments[i] = []byte("A:" + id + ":" + itoa(i))
	}
	return header, attachments, nil
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b [20]byte
	pos := len(b)
	for i > 0 {
		pos--
		b[pos] = byte('0' + i%10)
		i /= 10
	}
	return string(b[pos:])
}

// TestSendPacketBinaryFramesAreContiguousUnderConcurrency drives many concurrent
// binary and text emits through a single Client and asserts that every binary
// header frame is immediately followed by exactly its own attachment frames,
// with no frame from another logical packet interleaved. Without the send mutex
// around the whole multi-frame write, concurrent emits interleave and this
// assertion fails (and -race reports a data race on the transport ordering).
func TestSendPacketBinaryFramesAreContiguousUnderConcurrency(t *testing.T) {
	const attachments = 3
	engine := &recordingEngineIO{}
	parser := &recordingParser{attachments: attachments}

	client := &Client{
		engineio:     engine,
		parser:       parser,
		logger:       &concNopLogger{},
		timer:        concNopTimer{},
		ctx:          context.Background(),
		namespaces:   map[string]*namespace{},
		ackCallbacks: map[int]func([]interface{}){},
	}

	const goroutines = 50
	const iterations = 20

	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for it := 0; it < iterations; it++ {
				if (g+it)%2 == 0 {
					// Binary emit: event with a []byte payload. The event name
					// is the unique id used to tag header/attachment frames.
					id := itoa(g) + "-" + itoa(it)
					_ = client.sendPacket(&socketio_v5.Message{
						Type: socketio_v5.PacketBinaryEvent,
						Event: &socketio_v5.Event{
							Name:     id,
							Payloads: []interface{}{[]byte("blob")},
						},
					})
				} else {
					// Text emit: single text frame, no attachments.
					_ = client.sendPacket(&socketio_v5.Message{
						Type: socketio_v5.PacketEvent,
						Event: &socketio_v5.Event{
							Name:     "text",
							Payloads: []interface{}{"hello"},
						},
					})
				}
			}
		}(g)
	}
	wg.Wait()

	frames := engine.frames
	headers := 0
	for i := 0; i < len(frames); i++ {
		f := frames[i]
		switch {
		case strings.HasPrefix(f, "H:"):
			headers++
			id := strings.TrimPrefix(f, "H:")
			// The next `attachments` frames must be exactly this header's
			// attachments, in order, with nothing foreign in between.
			for n := 0; n < attachments; n++ {
				idx := i + 1 + n
				if idx >= len(frames) {
					t.Fatalf("binary header %q at %d is missing attachment %d (only %d frames)", f, i, n, len(frames))
				}
				want := "A:" + id + ":" + itoa(n)
				if frames[idx] != want {
					t.Fatalf("binary header %q at %d: frame %d = %q, want %q (foreign frame interleaved)", f, i, idx, frames[idx], want)
				}
			}
			i += attachments
		case strings.HasPrefix(f, "A:"):
			t.Fatalf("orphan attachment frame %q at %d (not preceded by its header)", f, i)
		case f == "T":
			// Standalone text frame is fine anywhere.
		default:
			t.Fatalf("unexpected frame %q at %d", f, i)
		}
	}

	// Sanity: we actually exercised the binary path.
	if headers == 0 {
		t.Fatal("expected at least one binary header to be emitted")
	}
	expectedFrames := headers*(1+attachments) + (len(frames) - headers*(1+attachments))
	if len(frames) != expectedFrames {
		t.Fatalf("frame accounting mismatch: got %d frames", len(frames))
	}
}

type concNopLogger struct{}

func (concNopLogger) Debugf(string, ...any) {}
func (concNopLogger) Infof(string, ...any)  {}
func (concNopLogger) Warnf(string, ...any)  {}
func (concNopLogger) Errorf(string, ...any) {}

type concNopTimer struct{}

func (concNopTimer) After(d time.Duration) <-chan time.Time {
	return make(chan time.Time)
}
