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

func TestSupportsBinaryRejectsTextOnlyTransport(t *testing.T) {
	c, _, _, _ := newBinaryTestClient(t)
	require.True(t, c.SupportsBinary())
	c.transport = textOnlyTransport{c.transport}
	require.False(t, c.SupportsBinary())
	c.transport = nil
	require.False(t, c.SupportsBinary())
}

type binaryCapabilityTransport struct {
	legacyTransport
	enabled bool
}

func (t binaryCapabilityTransport) SendBinary([]byte) error { return nil }
func (t binaryCapabilityTransport) SupportsBinary() bool    { return t.enabled }

func TestSupportsBinaryPropagatesTransportCapability(t *testing.T) {
	c, _, _, _ := newBinaryTestClient(t)
	underlying := c.transport
	c.transport = binaryCapabilityTransport{legacyTransport: underlying, enabled: false}
	require.False(t, c.SupportsBinary())
	c.transport = binaryCapabilityTransport{legacyTransport: underlying, enabled: true}
	require.True(t, c.SupportsBinary())
}
