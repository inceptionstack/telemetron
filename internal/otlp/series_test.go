package otlp

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSeriesKeyIsDeterministic(t *testing.T) {
	t.Parallel()

	left := seriesKey("pack.tool.call", map[string]string{
		"tool.class": "file",
		"outcome":    "success",
	})
	right := seriesKey("pack.tool.call", map[string]string{
		"outcome":    "success",
		"tool.class": "file",
	})
	require.Equal(t, left, right)
}
