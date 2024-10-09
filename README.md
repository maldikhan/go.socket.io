# Go Socket.IO Client

A Go implementation of the Socket.IO v5 client, compatible with Socket.IO v4 servers.

[![License](https://img.shields.io/badge/license-MIT-blue.svg)](https://github.com/maldikhan/go.socket.io/blob/main/LICENSE)
[![Go Report Card](https://goreportcard.com/badge/github.com/maldikhan/go.socket.io#latest)](https://goreportcard.com/report/github.com/maldikhan/go.socket.io)
[![codecov](https://codecov.io/github/maldikhan/go.socket.io/graph/badge.svg?token=HQTEDRMY81)](https://codecov.io/github/maldikhan/go.socket.io)
[![Go Reference](https://pkg.go.dev/badge/github.com/maldikhan/go.socket.io.svg)](https://pkg.go.dev/github.com/maldikhan/go.socket.io)  
[![Test](https://github.com/maldikhan/go.socket.io/actions/workflows/test.yaml/badge.svg)](https://github.com/maldikhan/go.socket.io/actions/workflows/test.yaml)
[![Lint](https://github.com/maldikhan/go.socket.io/actions/workflows/lint.yaml/badge.svg)](https://github.com/maldikhan/go.socket.io/actions/workflows/lint.yaml)
[![Sec](https://github.com/maldikhan/go.socket.io/actions/workflows/security.yaml/badge.svg)](https://github.com/maldikhan/go.socket.io/actions/workflows/security.yaml)
[![CodeQL](https://github.com/maldikhan/go.socket.io/actions/workflows/github-code-scanning/codeql/badge.svg)](https://github.com/maldikhan/go.socket.io/actions/workflows/github-code-scanning/codeql)

This library implements a zero side dependency client compatible with the Socket.IO v5 protocol and Engine.IO v4 protocol. It supports WebSocket and HTTP long-polling transports, as well as the authorization mechanism introduced in Socket.IO v5.

**Note: This is a preview version implementing the core functionality of the protocols. It's in an early stage of testing and may not be suitable for production use.**

## Table of Contents

- [Quick Start](#quick-start)
- [Features](#features)
- [Installation](#installation)
- [Usage](#usage)
  - [Creating a client](#creating-a-client)
  - [Connecting to a server](#connecting-to-a-server)
  - [Event handling](#event-handling)
  - [Emitting events](#emitting-events)
  - [Closing the connection](#closing-the-connection)
- [Advanced Configuration](#advanced-configuration)
- [Limitations](#limitations)
- [Contributing](#contributing)
- [License](#license)
- [Acknowledgments](#acknowledgments)

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

```bash
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

Basic connection:

```go
ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
defer cancel()

err := client.Connect(ctx)
if err != nil {
    log.Fatal(err)
}
```

You can also pass one or more callback functions as additional parameters to `client.Connect`. These callbacks will be executed upon successful connection. This is effectively an alias for `client.On("connect", func(){...})`. Here's an example:

```go
ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
defer cancel()

err := client.Connect(ctx, 
    func() {
        fmt.Println("Connected to the server!")
    },
    func() {
        fmt.Println("This is another on-connect callback")
    },
)
if err != nil {
    log.Fatal(err)
}
```

This approach allows you to set up connection handlers inline with the connection call, which can be more convenient in some cases. It's particularly useful when you want to perform specific actions immediately after a successful connection, such as joining rooms or emitting initial events.

Remember that these callbacks will be called every time a connection is established, including after automatic reconnects if your client is configured to use them.

### Event handling

The library allows you to handle events in two ways:

#### Automatically deserialize event parameters (similar to the original client)

```go
client.On("result", func(operation string, result int) {
 // Process event ["result", "operationtype", 123]
 fmt.Printf("Operation: %s, Result: %d\n", operation, result)
})
```

#### Work with raw events

```go
client.On("result", func(args []interface{}) {
 // Extract raw JSON data
 if arg0, ok := args[0].(json.RawMessage); ok {
  // Process raw JSON
 }
})
```

#### Internal events

The library allows you to handle socket.io internal events:

```go
client.On("connect", func() {
 // ... do anything on namespace connected
 // you should emit events to namespace only after NS connection
})

client.On("disconnect", func() {
 // ... do anything on disconnected
})

client.On("error", func() {
 // ... do anything on connect error
})
```

### Emitting events

**Important Note:** Emitting events is only possible after a connection to the namespace has been established (i.e., after receiving the 'connect' event). When calling `Emit` for a namespace that hasn't established a connection yet, the `Emit` method will block until the 'connect' event is received from that namespace.

#### Simple event emission

```go
err := client.Emit("eventName", "eventData")
if err != nil {
    log.Println("Error emitting event:", err)
}
```

#### Emitting events with acknowledgement and timeout

```go
err = client.Emit("delay", 1000,
    emit.WithAck(func(delayResponse string) {
        fmt.Println("Ack received:", delayResponse)
    }),
    emit.WithTimeout(500*time.Millisecond, func() {
        fmt.Println("Ack timeout occurred")
    }),
)
if err != nil {
    log.Println("Error emitting event:", err)
}
```

To ensure proper sequencing, you can use the following pattern:

```go
client.On("connect", func() {
    fmt.Println("Connected to namespace")
    // Now it's safe to emit events
    err := client.Emit("eventName", "eventData")
    if err != nil {
        fmt.Println("Error emitting event:", err)
    }
})

err := client.Connect(ctx)
if err != nil {
    fmt.Println("Error connecting:", err)
    return
}
```

This approach guarantees that you'll start emitting events only after the connection to the namespace has been established, preventing event loss and potential errors related to premature emission attempts.

### Closing the connection

```go
err := client.Close()
```

## Advanced Configuration

### Socket.IO Client Options

- `WithURL(*url.URL)`: Set the server URL
- `WithRawURL(string)`: Set the server URL as a string
- `WithEngineIOClient(EngineIOClient)`: Use a custom Engine.IO client
- `WithDefaultNamespace(string)`: Set the default namespace
- `WithLogger(Logger)`: Use a custom logger
- `WithTimer(Timer)`: Use a custom timer
- `WithParser(Parser)`: Use a custom parser (see [jsoniter fast default event parser implementation](./socket.io/v5/parser/default/jsoniter/))

### Engine.IO Client Options

- `WithURL(*url.URL)`: Set the server URL
- `WithRawURL(string)`: Set the server URL as a string
- `WithLogger(Logger)`: Use a custom logger
- `WithTransport(Transport)`: Use a specific transport
- `WithSupportedTransports([]Transport)`: Set supported transports
- `WithParser(Parser)`: Use a custom parser
- `WithReconnectAttempts(int)`: Set the number of reconnect attempts
- `WithReconnectWait(time.Duration)`: Set the wait time between reconnect attempts

## Limitations

- Binary packets are not currently supported
- Reconnection functionality is not yet implemented (TBD)
- Full namespace support is not yet implemented (TBD)
- Syntetic events like reconnect/reconnect_error are not implemented yet (TBD)
- Contract is stable but may be extended in future releases, please follow socket.io limitations for event naming

## Contributing

Contributions are welcome! Please feel free to submit a Pull Request.

## License

This project is licensed under the MIT License - see the [LICENSE](LICENSE) file for details.

## Acknowledgments

- [Socket.IO](https://socket.io/) for the original protocol and implementation
- [jsoniter](https://github.com/json-iterator/go) for fast JSON parsing
