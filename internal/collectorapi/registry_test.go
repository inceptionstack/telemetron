// SPDX-License-Identifier: Apache-2.0

package collectorapi

import (
	"testing"

	"github.com/inceptionstack/clawtello/internal/config"
	"github.com/inceptionstack/clawtello/internal/status"
	"github.com/stretchr/testify/require"
)

func TestRegisterPanicsOnDuplicateName(t *testing.T) {
	name := "registry-duplicate"
	spec := Spec{
		Name: name,
		Factory: func(rawConfig any, store *status.Store, base config.Config) (Collector, error) {
			return nil, nil
		},
	}
	Register(spec)
	require.Panics(t, func() {
		Register(spec)
	})
}

func TestAllReturnsSortedList(t *testing.T) {
	Register(Spec{
		Name: "registry-zeta",
		Factory: func(rawConfig any, store *status.Store, base config.Config) (Collector, error) {
			return nil, nil
		},
	})
	Register(Spec{
		Name: "registry-alpha",
		Factory: func(rawConfig any, store *status.Store, base config.Config) (Collector, error) {
			return nil, nil
		},
	})

	all := All()
	names := make([]string, 0, len(all))
	for _, spec := range all {
		names = append(names, spec.Name)
	}
	require.Subset(t, names, []string{"registry-alpha", "registry-zeta"})
	for i := 1; i < len(names); i++ {
		require.LessOrEqual(t, names[i-1], names[i])
	}
}

func TestLookupMissingReturnsFalse(t *testing.T) {
	_, ok := Lookup("missing")
	require.False(t, ok)
}
