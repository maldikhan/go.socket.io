# Socket.IO V5 Default Event Payload Parser with Jsoniter

This package provides a high-performance JSON parser for Socket.IO V5 protocol event payload using the [jsoniter](https://github.com/json-iterator/go) library. It's designed to be a drop-in replacement for the default JSON parser, offering significant performance improvements.

## Features

- High-performance JSON parsing and serialization
- Compatible with Socket.IO V5 default protocol parser
- Easy integration with the default parser

## Installation

To use this parser, first ensure you have the main Socket.IO client library installed:

```sh
go get github.com/maldikhan/go.socket.io
```

Then, install this parser:

```sh
go get github.com/maldikhan/go.socket.io/socket.io/v5/parser/default/jsoniter
```

## Usage

To use this parser with the Socket.IO client, you need to create a new parser instance and pass it to the client constructor:

```go
package main

import (
    "github.com/maldikhan/go.socket.io"
    socketio_v5_parser_default "github.com/maldikhan/go.socket.io/socket.io/v5/parser/default"
    socketio_v5_parser_default_jsoniter "github.com/maldikhan/go.socket.io/socket.io/v5/parser/default/jsoniter"
    "github.com/maldikhan/go.socket.io/utils"
)

func main() {
    client, err := socketio.NewClient(
        socketio.WithRawURL("http://localhost:3000"),
        socketio.WithParser(socketio_v5_parser_default.NewParser(
            socketio_v5_parser_default.WithLogger(&utils.DefaultLogger{Level: utils.WARN}),
            socketio_v5_parser_default.WithPayloadParser(socketio_v5_parser_default_jsoniter.NewPayloadParser(
                socketio_v5_parser_default_jsoniter.WithLogger(&utils.DefaultLogger{Level: utils.WARN}),
            )),
        )),
    )
    if err != nil {
        // Handle error
    }
    
    // Use the client as normal
}
```

## Performance

This parser can provide 2-4x faster parsing of payloads compared to the standard library's `encoding/json`. The exact performance gain may vary depending on your specific use case and payload structure.

## Compatibility

This parser is designed to be fully compatible with the Socket.IO V5 protocol. It can be used as a drop-in replacement for the default parser without any changes to your existing code.

## Contributing

Contributions to improve the parser are welcome. Please feel free to submit issues or pull requests.

## License

This parser is licensed under the MIT License. See the [LICENSE](../../../../../LICENSE) file in the root of the repository for the full license text.