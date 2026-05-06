// SPDX-License-Identifier: Apache-2.0

package roundhouse

import (
	"path/filepath"
	"testing"

	"github.com/inceptionstack/telemetron/internal/collectorapi"
	"github.com/inceptionstack/telemetron/internal/openclaw"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRoundhouseRegistered(t *testing.T) {
	spec, ok := collectorapi.Lookup(Mode)
	require.True(t, ok, "roundhouse mode should be registered")
	assert.Equal(t, "roundhouse", spec.Name)
	assert.NotNil(t, spec.Factory)
}

func TestRoundhouseCollectorName(t *testing.T) {
	// Verify the wrapper returns "roundhouse", not "openclaw"
	tmpDir := t.TempDir()
	cfg := openclaw.Config{
		SessionDir:    tmpDir,
		FlushInterval: openclaw.DefaultFlushInterval,
		ScanInterval:  openclaw.DefaultScanInterval,
		StateFile:     filepath.Join(tmpDir, "state.json"),
	}
	oc, err := openclaw.NewCollector(cfg, nil)
	require.NoError(t, err)
	c := &collector{Collector: oc}
	assert.Equal(t, "roundhouse", c.Name())
}
