package socketio_v5_parser_default_jsoniter

import (
	"reflect"
	"testing"

	"github.com/golang/mock/gomock"
	"github.com/maldikhan/go.socket.io/utils"

	mock_socketio_v5_parser_default_jsoniter "github.com/maldikhan/go.socket.io/socket.io/v5/parser/default/jsoniter/mocks"
)

func TestNewPayloadParser(t *testing.T) {

	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	logger := mock_socketio_v5_parser_default_jsoniter.NewMockLogger(ctrl)

	type args struct {
		opts []ParserOption
	}
	tests := []struct {
		name string
		args args
		want *SocketIOV5PayloadParser
	}{
		{
			name: "With default logger",
			args: args{
				opts: []ParserOption{},
			},
			want: &SocketIOV5PayloadParser{
				logger: &utils.DefaultLogger{},
			},
		},
		{
			name: "With custom logger",
			args: args{
				opts: []ParserOption{
					WithLogger(logger),
				},
			},
			want: &SocketIOV5PayloadParser{
				logger: logger,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NewPayloadParser(tt.args.opts...); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("NewPayloadParser() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestWithLogger(t *testing.T) {

	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	logger := mock_socketio_v5_parser_default_jsoniter.NewMockLogger(ctrl)

	type args struct {
		logger Logger
	}
	tests := []struct {
		name       string
		args       args
		wantLogger Logger
	}{
		{
			name:       "With mock logger",
			args:       args{logger: logger},
			wantLogger: logger,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parser := &SocketIOV5PayloadParser{}
			option := WithLogger(tt.args.logger)
			option(parser)
			if got := parser.logger; !reflect.DeepEqual(got, tt.wantLogger) {
				t.Errorf("WithLogger() = %v, want %v", got, tt.wantLogger)
			}
		})
	}
}
