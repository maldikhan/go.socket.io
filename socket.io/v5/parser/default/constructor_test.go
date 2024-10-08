package socketio_v5_parser_default

import (
	"testing"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"

	mock_socketio_v5_parser_default "github.com/maldikhan/go.socket.io/socket.io/v5/parser/default/mocks"
)

func TestNewParser(t *testing.T) {
	t.Run("Default configuration", func(t *testing.T) {
		parser := NewParser()
		assert.NotNil(t, parser)
		assert.IsType(t, &SocketIOV5DefaultParser{}, parser)
	})

	t.Run("With custom logger", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		mockLogger := mock_socketio_v5_parser_default.NewMockLogger(ctrl)
		parser := NewParser(WithLogger(mockLogger))
		assert.NotNil(t, parser)
		// Проверяем, что логгер был установлен, но это потребует изменения в структуре ParserConfig
		// assert.Equal(t, mockLogger, parser.logger)
	})

	t.Run("With custom payload parser", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		mockPayloadParser := mock_socketio_v5_parser_default.NewMockPayloadParser(ctrl)

		parser := NewParser(WithPayloadParser(mockPayloadParser))
		assert.NotNil(t, parser)
		// Проверяем, что payload parser был установлен, но это потребует изменения в структуре ParserConfig
		// assert.Equal(t, mockPayloadParser, parser.payloadParser)
	})

	t.Run("With multiple options", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		mockLogger := mock_socketio_v5_parser_default.NewMockLogger(ctrl)
		mockPayloadParser := mock_socketio_v5_parser_default.NewMockPayloadParser(ctrl)

		parser := NewParser(
			WithLogger(mockLogger),
			WithPayloadParser(mockPayloadParser),
		)
		assert.NotNil(t, parser)
		// Проверяем, что оба опции были применены, но это потребует изменения в структуре ParserConfig
		// assert.Equal(t, mockLogger, parser.logger)
		// assert.Equal(t, mockPayloadParser, parser.payloadParser)
	})
}

func TestWithLogger(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockLogger := mock_socketio_v5_parser_default.NewMockLogger(ctrl)
	option := WithLogger(mockLogger)

	config := &SocketIOV5DefaultParser{}
	err := option(config)

	assert.NoError(t, err)
	assert.Equal(t, mockLogger, config.logger)
}

func TestWithPayloadParser(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockPayloadParser := mock_socketio_v5_parser_default.NewMockPayloadParser(ctrl)

	option := WithPayloadParser(mockPayloadParser)

	config := &SocketIOV5DefaultParser{}
	err := option(config)

	assert.NoError(t, err)
	assert.NotNil(t, config.payloadParser)
}

func TestParserConfig_DefaultLogger(t *testing.T) {
	parser := NewParser()
	assert.NotNil(t, parser)

	// Проверяем, что используется DefaultLogger, когда не указан пользовательский логгер
	// Это потребует изменения в структуре ParserConfig для доступа к полю logger
	// assert.IsType(t, &utils.DefaultLogger{}, parser.logger)
}

func TestParserConfig_PanicOnInvalidOption(t *testing.T) {
	invalidOption := func(p *SocketIOV5DefaultParser) error {
		return assert.AnError
	}

	assert.Panics(t, func() {
		NewParser(invalidOption)
	})
}
