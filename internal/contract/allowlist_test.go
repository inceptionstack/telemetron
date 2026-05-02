// SPDX-License-Identifier: Apache-2.0

package contract

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNormalizeMetric(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		metric   string
		attrs    map[string]string
		allowed  bool
		expected map[string]string
	}{
		{
			name:    "reject unknown metric",
			metric:  "pack.unknown",
			attrs:   map[string]string{"outcome": "success"},
			allowed: false,
		},
		{
			name:   "drop unknown attrs and normalize invalid enum",
			metric: MetricToolCall,
			attrs: map[string]string{
				"tool.class": "not-a-class",
				"outcome":    "bad",
				"ignored":    "value",
			},
			allowed: true,
			expected: map[string]string{
				"tool.class": "unknown",
				"outcome":    "unknown",
			},
		},
		{
			name:   "preserve valid attrs",
			metric: MetricSessionStart,
			attrs: map[string]string{
				"outcome":      "success",
				"model.family": "bedrock",
				"session.type": "subagent",
			},
			allowed: true,
			expected: map[string]string{
				"outcome":      "success",
				"model.family": "bedrock",
				"session.type": "subagent",
			},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, ok := NormalizeMetric(tt.metric, tt.attrs)
			require.Equal(t, tt.allowed, ok)
			require.Equal(t, tt.expected, got)
		})
	}
}
