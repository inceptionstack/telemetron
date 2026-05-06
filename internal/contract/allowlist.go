// SPDX-License-Identifier: Apache-2.0

package contract

import "sort"

const (
	MetricSessionStart     = "pack.session.start"
	MetricAgentTurn        = "pack.agent.turn"
	MetricToolCall         = "pack.tool.call"
	MetricError            = "pack.error"
	MetricEmitterHeartbeat = "pack.emitter.heartbeat"
)

var allowedMetricNames = map[string]struct{}{
	MetricSessionStart:     {},
	MetricAgentTurn:        {},
	MetricToolCall:         {},
	MetricError:            {},
	MetricEmitterHeartbeat: {},
}

var allowedMetricAttrs = map[string]map[string]struct{}{
	MetricSessionStart: {
		"outcome":      {},
		"model.family": {},
		"session.type": {},
	},
	MetricAgentTurn: {
		"outcome":      {},
		"model.family": {},
		"session.type": {},
	},
	MetricToolCall: {
		"outcome":    {},
		"tool.class": {},
		"tool.name":  {},
	},
	MetricError: {
		"error.type": {},
	},
	MetricEmitterHeartbeat: {},
}

func AllowedMetric(name string) bool {
	_, ok := allowedMetricNames[name]
	return ok
}

func AllowedAttrs(name string) []string {
	keys := make([]string, 0, len(allowedMetricAttrs[name]))
	for key := range allowedMetricAttrs[name] {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
