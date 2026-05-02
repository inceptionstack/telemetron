// SPDX-License-Identifier: Apache-2.0

package otlp

import (
	"sort"
	"strings"
)

func seriesKey(name string, attrs map[string]string) string {
	keys := make([]string, 0, len(attrs))
	for key := range attrs {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	parts := []string{name}
	for _, key := range keys {
		parts = append(parts, key+"="+attrs[key])
	}
	return strings.Join(parts, "|")
}
