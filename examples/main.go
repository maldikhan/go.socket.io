package main

import (
	"context"
	"crypto/rand"
	"fmt"
	"math/big"
	"os"
	"os/signal"
	"time"

	socketio_v5_client "maldikhan/go.socket.io/socket.io/v5/client"
	"maldikhan/go.socket.io/utils"
)

func main() {

	onExit := make(chan os.Signal, 1)
	signal.Notify(onExit)
	ctx, contextClose := context.WithCancel(context.Background())
	defer contextClose()
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	type Result struct {
		Operation string
		Result    int
	}

	results := make(chan *Result, 1)

	client, err := socketio_v5_client.NewClient(
		socketio_v5_client.WithRawURL("http://127.0.0.1:3001"),
		socketio_v5_client.WithLogger(&utils.DefaultLogger{Level: utils.WARN}),
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

	client.EmitWathAck(
		func(hiResponse string) {
			fmt.Println("Server said:", hiResponse)
		},
		"hi",
	)

	client.EmitWathAckTimeout(
		500*time.Millisecond,
		func(delayResponse string) {
			fmt.Println("This one should not be called", delayResponse)
		},
		func() {
			fmt.Println("Expected timeout")
		},
		"delay", 1000,
	)

	client.EmitWathAckTimeout(
		1500*time.Millisecond,
		func(delayResponse string) {
			fmt.Println("This one is Ack from emit with timeout:", delayResponse)
		},
		func() {
			fmt.Println("Not expected timeout")
		},
		"delay", 1000,
	)

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
				client.Emit("get_square", num.Int64())
			case <-ticker2.C:
				num, _ := rand.Int(rand.Reader, big.NewInt(10))
				numbers = append(numbers, num.Int64())
				if len(numbers) > 10 {
					numbers = numbers[1:]
				}
				fmt.Println("request get_sum", numbers)
				client.Emit("get_sum", numbers...)
			}
		}
	}(ctx)

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
