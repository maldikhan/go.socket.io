package main

import (
	"context"
	"crypto/rand"
	"fmt"
	"math/big"
	"os"
	"os/signal"
	"time"

	socketio_v5 "github.com/maldikhan/go.socket.io/socket.io/v5"
	socketio_v5_client "github.com/maldikhan/go.socket.io/socket.io/v5/client"
	"github.com/maldikhan/go.socket.io/socket.io/v5/client/emit"
	socketio_v5_parser_default "github.com/maldikhan/go.socket.io/socket.io/v5/parser/default"
	socketio_v5_parser_default_jsoniter "github.com/maldikhan/go.socket.io/socket.io/v5/parser/default/jsoniter"
	"github.com/maldikhan/go.socket.io/utils"
)

func main() {

	onExit := make(chan os.Signal, 1)
	signal.Notify(onExit)
	ctx, contextClose := context.WithCancel(context.Background())
	defer contextClose()
	ctx, cancel := context.WithTimeout(ctx, 1500*time.Second)
	defer cancel()

	type Result struct {
		Operation string
		Result    int
	}

	results := make(chan *Result, 1)

	client, err := socketio_v5_client.NewClient(
		socketio_v5_client.WithRawURL("http://127.0.0.1:3001"),
		socketio_v5_client.WithLogger(&utils.DefaultLogger{Level: utils.WARN}),
		socketio_v5_client.WithParser(socketio_v5_parser_default.NewParser(
			socketio_v5_parser_default.WithLogger(&utils.DefaultLogger{Level: utils.WARN}),
			socketio_v5_parser_default.WithPayloadParser(socketio_v5_parser_default_jsoniter.NewPayloadParser(
				socketio_v5_parser_default_jsoniter.WithLogger(&utils.DefaultLogger{Level: utils.WARN}),
			)),
		)),
	)

	if err != nil {
		fmt.Println(err)
	}

	client.SetHandshakeData(map[string]interface{}{"userName": "Varvar"})

	client.On("result", func(operation string, result int) {
		results <- &Result{
			Operation: operation,
			Result:    result,
		}
	})

	err = client.Connect(ctx)
	if err != nil {
		fmt.Println(err)
	}

	err = client.Emit("hi",
		emit.WithAck(func(hiResponse string) {
			fmt.Println("Server said:", hiResponse)
		}),
	)
	if err != nil {
		fmt.Println(err)
	}

	err = client.Emit("delay", 1000,
		emit.WithAck(
			func(delayResponse string) {
				fmt.Println("This one should not be called", delayResponse)
			}),
		emit.WithTimeout(
			500*time.Millisecond,
			func() {
				fmt.Println("Expected timeout")
			},
		),
	)
	if err != nil {
		fmt.Println(err)
	}

	err = client.Emit("delay", 1000,
		emit.WithAck(func(delayResponse string) {
			fmt.Println("This one is Ack from emit with timeout:", delayResponse)
		}),
		emit.WithTimeout(
			1500*time.Millisecond,
			func() {
				fmt.Println("Not expected timeout")
			},
		),
	)
	if err != nil {
		panic(err)
	}

	// Main loop simulation
	go func(ctx context.Context) {
		ticker1 := time.NewTicker(4 * time.Second)
		ticker2 := time.NewTicker(5 * time.Second)
		numbers := []any{10}

		for {
			select {
			case <-ctx.Done():
				fmt.Println("context done, exit main loop")
				return
			case <-ticker1.C:
				num, _ := rand.Int(rand.Reader, big.NewInt(10))
				fmt.Println("request get_square", num.Int64())

				err := client.Emit("get_square", num.Int64())
				if err != nil {
					panic(err)
				}

				_ = client.Emit(socketio_v5.Event{
					Name:     "get_square",
					Payloads: []any{num.Int64() * 2},
				})

				_ = client.Emit(&socketio_v5.Event{
					Name:     "get_square",
					Payloads: []any{num.Int64() * 3},
				})
			case <-ticker2.C:
				num, _ := rand.Int(rand.Reader, big.NewInt(10))
				numbers = append(numbers, num.Int64())
				if len(numbers) > 10 {
					numbers = numbers[1:]
				}
				fmt.Println("request get_sum", numbers)

				_ = client.Emit("get_sum", numbers...)
			}
		}
	}(ctx)

	// Running application waiting for exit
	for {
		select {
		case <-ctx.Done():
			fmt.Println("context done, exit app")
			return
		case <-onExit:
			fmt.Println("sigterm, exit app")
			contextClose()
			return
		case result := <-results:
			fmt.Println(result.Operation, "=", result.Result)
		}
	}
}
