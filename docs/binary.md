# Binary events and acknowledgements

The default parser sends non-nil `[]byte` values as Socket.IO binary attachments.
It supports multiple buffers, nested arrays/maps, exported struct fields and
exported fields promoted from unexported embedded value structs. A non-nil empty
slice is an empty attachment; a nil byte slice remains JSON `null`.

JSON field tags and custom JSON/text marshalers keep their ordinary encoding.
`json.RawMessage` remains inline JSON. Marshalers own their representation, so a
marshaler that encodes bytes itself does not create Socket.IO attachments.

Typed callbacks can receive `[]byte`, maps containing byte slices, or structs
with byte-slice fields. Numbers reconstructed in dynamic binary payloads use
`json.Number` to preserve integer precision. Typed integer callback parameters
remain exact, including values above 2^53.

A binary-bearing unexported embedded **pointer** cannot be safely detached with
reflection. Emitting that shape returns `ErrParseBinary`. Export the embedded
type or pass its exported fields explicitly. This avoids mutating the caller's
object or silently replacing a binary buffer with base64 JSON.

## Limits and packet ordering

Inbound binary events and acknowledgements retain at most 64 MiB of aggregate
attachment data by default. `socketio.WithMaxBinarySize(bytes)` changes this
limit; its argument must be positive. Headers with more than 65,536 attachments
are rejected. An oversized packet releases all its accumulated attachments.

The client serializes the header and every attachment of an outbound binary
packet as one contiguous sequence, including across concurrent emits and
namespaces. A Socket.IO text packet that interrupts an inbound attachment
sequence drops the incomplete packet and the intervening text packet; subsequent
orphan attachments are discarded. Close and a new Engine.IO connection clear any
incomplete attachment state.

These aggregate limits supplement transport frame/body limits. A peer cannot
raise the client aggregate limit through its handshake.

## Custom implementations

`socketio.Parser`, `socketio.EngineIOClient` and the WebSocket dependency keep
binary methods optional. A custom parser can continue to use its own `Serialize`
wire format. A parser using separate attachments implements
`socketio.BinaryParser`; an Engine.IO client implements
`socketio.BinaryEngineIOClient`; an Engine.IO transport implements
`engineio.BinaryTransport`; a WebSocket adapter implements
`websocket.BinaryWebSocket`. Missing outbound attachment capabilities return an
explicit error.

Existing `WithPayloadParser` integrations, including text-only parsers that
represent raw JSON with `[]byte`, retain their previous text/base64 send behavior.
They reject inbound binary reconstruction explicitly. To opt in, a custom payload
parser implements `defaultparser.BinaryPayloadParser.ReconstructBinary` and a
matching `WrapCallback` capable of reading its reconstructed buffer values.
