# Go Socket.IO Client

A Go implementation of the Socket.IO v5 client, compatible with Socket.IO v4 servers.

[![Go Reference](https://pkg.go.dev/badge/github.com/maldikhan/go.socket.io.svg)](https://pkg.go.dev/github.com/maldikhan/go.socket.io)
[![Go Report Card](https://goreportcard.com/badge/github.com/maldikhan/go.socket.io)](https://goreportcard.com/report/github.com/maldikhan/go.socket.io)
[![Go Test and Lint](https://github.com/maldikhan/go.socket.io/actions/workflows/run-test.yaml/badge.svg)](https://github.com/maldikhan/go.socket.io/actions/workflows/run-test.yaml)
[![codecov](https://codecov.io/github/maldikhan/go.socket.io/graph/badge.svg?token=HQTEDRMY81)](https://codecov.io/github/maldikhan/go.socket.io)
[![CodeQL](https://github.com/maldikhan/go.socket.io/actions/workflows/github-code-scanning/codeql/badge.svg)](https://github.com/maldikhan/go.socket.io/actions/workflows/github-code-scanning/codeql)
[![License](https://img.shields.io/badge/license-MIT-blue.svg)](https://github.com/maldikhan/go.socket.io/blob/main/LICENSE)

This library implements a zero side dependency client compatible with the Socket.IO v5 protocol and Engine.IO v4 protocol. It supports WebSocket and HTTP long-polling transports, as well as the authorization mechanism introduced in Socket.IO v5.

**Note: This is a preview version implementing the core functionality of the protocols. It's in an early stage of testing and may not be suitable for production use.**

## Quick Start

```go
package main

import (
	"context"
	"fmt"
	"log"
	"time"

	socketio "github.com/maldikhan/go.socket.io"
)

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	client, err := socketio.NewClient(
		socketio.WithRawURL("http://localhost:3000"),
	)
	if err != nil {
		log.Fatalf("Error creating client: %v", err)
	}


	client.On("eventname", func(data []byte) {
		fmt.Printf("Received message: %s\n", string(data))
	})

	if err := client.Connect(ctx); err != nil {
		log.Fatalf("Error connecting to server: %v", err)
	}

	if err := client.Emit("hello", "world"); err != nil {
		log.Fatalf("Error sending message: %v", err)
	}

	<-ctx.Done()
}
```

## Features

- Socket.IO v5 protocol support
- Engine.IO v4 protocol support
- WebSocket and HTTP long-polling transports
- Authorization support
- Namespaces support (limited)
- Modular design for easy component replacement
- Fast JSON parsing with jsoniter

## Installation

```
go get github.com/maldikhan/go.socket.io
```

## Usage

### Creating a client

```go
client, err := socketio.NewClient(
	socketio.WithRawURL("http://localhost:3000"),
	socketio.WithLogger(customLogger),
)
```

### Connecting to a server

```go
ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
defer cancel()

err := client.Connect(ctx)
```

### Event handling

The library allows you to handle events in two ways:

1. Automatically deserialize event parameters (similar to the original client):

```go
client.On("result", func(operation string, result int) {
	// Process event ["result", "operationtype", 123]
	fmt.Printf("Operation: %s, Result: %d\n", operation, result)
})
```

2. Work with raw events:

```go
client.On("result", func(args []interface{}) {
	// Extract raw JSON data
	if arg0, ok := args[0].(json.RawMessage); ok {
		// Process raw JSON
	}
})
```

### Emitting events

Simple event emission:

```go
err := client.Emit("eventName", "eventData")
```

Emitting events with acknowledgement and timeout:

```go
err = client.Emit("delay", 1000,
	emit.WithAck(func(delayResponse string) {
		fmt.Println("Ack received:", delayResponse)
	}),
	emit.WithTimeout(500*time.Millisecond, func() {
		fmt.Println("Ack timeout occurred")
	}),
)
```

### Closing the connection

```go
err := client.Close()
```

## Advanced Configuration

<details>
<summary>Socket.IO Client Options</summary>

- `WithURL(*url.URL)`: Set the server URL
- `WithRawURL(string)`: Set the server URL as a string
- `WithEngineIOClient(EngineIOClient)`: Use a custom Engine.IO client
- `WithDefaultNamespace(string)`: Set the default namespace
- `WithLogger(Logger)`: Use a custom logger
- `WithTimer(Timer)`: Use a custom timer
- `WithParser(Parser)`: Use a custom parser (see [jsoniter fast default event parser implementation](./socket.io/v5/parser/default/jsoniter/))

</details>

<details>
<summary>Engine.IO Client Options</summary>

- `WithURL(*url.URL)`: Set the server URL
- `WithRawURL(string)`: Set the server URL as a string
- `WithLogger(Logger)`: Use a custom logger
- `WithTransport(Transport)`: Use a specific transport
- `WithSupportedTransports([]Transport)`: Set supported transports
- `WithParser(Parser)`: Use a custom parser
- `WithReconnectAttempts(int)`: Set the number of reconnect attempts
- `WithReconnectWait(time.Duration)`: Set the wait time between reconnect attempts

</details>

## Limitations

- Binary packets are not currently supported
- Reconnection functionality is not yet implemented (TBD)
- Full namespace support is not yet implemented (TBD)
- Syntetic events like connect/disconnect are not implemented yet (TBD)
- Contract is stable but may be extended in future releases, please follow socket.io limitations for event naming

## Contributing

Contributions are welcome! Please feel free to submit a Pull Request.

## License

This project is licensed under the MIT License - see the [LICENSE](LICENSE) file for details.

## Acknowledgments

- [Socket.IO](https://socket.io/) for the original protocol and implementation
- [jsoniter](https://github.com/json-iterator/go) for fast JSON parsing
