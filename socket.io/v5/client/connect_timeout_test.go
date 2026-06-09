package socketio_v5_client

import (
	"testing"
	"time"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	mocks "github.com/maldikhan/go.socket.io/socket.io/v5/client/mocks"
)

func TestWithConnectTimeout(t *testing.T) {
	t.Parallel()

	t.Run("valid timeout with internal engine.io client", func(t *testing.T) {
		client, err := NewClient(
			WithRawURL("http://localhost"),
			WithConnectTimeout(3*time.Second),
		)
		require.NoError(t, err)
		assert.NotNil(t, client)
	})

	t.Run("non-positive timeout", func(t *testing.T) {
		for _, timeout := range []time.Duration{0, -time.Second} {
			client, err := NewClient(
				WithRawURL("http://localhost"),
				WithConnectTimeout(timeout),
			)
			assert.Nil(t, client)
			assert.ErrorContains(t, err, "connect timeout must be positive")
		}
	})

	t.Run("conflicts with custom engine.io client", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		mockEngineIOClient := mocks.NewMockEngineIOClient(ctrl)

		client, err := NewClient(
			WithEngineIOClient(mockEngineIOClient),
			WithConnectTimeout(3*time.Second),
		)
		assert.Nil(t, client)
		assert.ErrorContains(t, err, "WithConnectTimeout can't be combined with WithEngineIOClient")
	})
}
