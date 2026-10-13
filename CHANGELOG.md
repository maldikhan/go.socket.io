# Changelog

## v0.2.0 — 2026-10-13

### Connection reliability

- Automatic Engine.IO reconnection uses fresh sessions, bounded exponential
  backoff and cancellation-aware lifecycle events. The configured Socket.IO
  namespace is joined again after reconnection.
- `WithConnectTimeout` bounds dialing, handshake and upgrade independently of
  session lifetime. Caller cancellation still ends the session.
- Engine.IO heartbeats detect silent peers; invalid heartbeat values fail
  without panicking. Close releases pending emits and transport waits.
- WebSocket sessions are isolated across reconnects and shared connection access
  is synchronized. User lifecycle handlers may call Close.

### Binary events and WebSocket backend

- The default parser supports binary events and acknowledgements on WebSocket
  and HTTP long-polling, including multiple buffers and nested exported fields.
- Attachment writes are serialized, reconstruction validates packet ordering,
  and retained attachment data is limited to 64 MiB by default. See
  [binary behavior and custom parser capabilities](docs/binary.md).
- The optional `websocket/coder` package supplies compression, close handshakes,
  custom HTTP clients and configurable read limits. Both backends use a 4 MiB
  incoming WebSocket message limit by default.

### Migration from v0.1.1

- Go 1.26.0 is the minimum version. Use the latest security patch in your Go
  release; dependencies include `golang.org/x/net v0.61.0` and
  `github.com/coder/websocket v1.8.15`.
- The default parser constructor now returns an error:

  ```go
  parser, err := defaultparser.NewParser(options...)
  if err != nil {
      return err
  }
  ```

- Custom Engine.IO transports must change their `Run` receive channel from
  `chan<- []byte` to `chan<- engineio.Frame`. Set `Data` for text frames and
  `IsBinary: true` for raw attachment frames. Binary send methods on custom
  parsers, Engine.IO clients and WebSocket adapters remain optional capabilities.
- Each constructed client accepts one public `Connect` call. Repeated or
  concurrent calls return `ErrConnectAlreadyStarted`; Close is terminal and
  calls afterward return `ErrClientClosed`. Construct another client for a new
  manual session. Automatic reconnection is unaffected.
- Reconnection is enabled by default. Use `engineio.WithReconnect(false)` to
  disable it. Delivery and outstanding acknowledgements are not replayed after
  a lost session.
- `WithConnectTimeout` waits for Engine.IO readiness; application namespace
  readiness remains the Socket.IO `connect` event.

### Verification

The CI matrix builds, tests and vets supported Go releases. Race-enabled unit
coverage and real Socket.IO server integration run for pull requests and master.
Integration dependencies are pinned with a committed npm lockfile. Security
checks include govulncheck, gosec and CodeQL.
