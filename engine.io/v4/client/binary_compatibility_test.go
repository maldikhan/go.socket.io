package engineio_v4_client

import (
	"github.com/stretchr/testify/require"
	"testing"
)

type legacyTransport = Transport
type textOnlyTransport struct{ legacyTransport }

func TestSendBinaryRejectsTextOnlyTransport(t *testing.T) {
	c, _, _, _ := newBinaryTestClient(t)
	c.transport = textOnlyTransport{c.transport}
	require.ErrorContains(t, c.SendBinary([]byte{1}), "transport does not support binary")
}
