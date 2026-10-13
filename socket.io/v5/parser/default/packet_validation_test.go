package socketio_v5_parser_default

import (
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRejectMalformedSocketIOPackets(t *testing.T) {
	t.Parallel()
	p := newHardeningParser()
	for _, data := range []string{"2", "3", "4", "7", "z", `2[null]`, `2["event"]garbage`, `51-["event",{"_placeholder":true,"num":0}]["extra"]`, `61-1[]null`} {
		t.Run(data, func(t *testing.T) {
			_, err := p.Parse([]byte(data))
			require.Error(t, err)
		})
	}
	if strconv.IntSize == 32 {
		_, err := p.Parse([]byte(`54294967296-["event"]`))
		require.ErrorIs(t, err, ErrParsePackage)
		_, err = p.Parse([]byte(`24294967296["event"]`))
		require.ErrorIs(t, err, ErrParsePackage)
	}
}
