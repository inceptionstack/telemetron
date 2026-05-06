// SPDX-License-Identifier: Apache-2.0

// Package roundhouse registers the "roundhouse" collector mode.
// Roundhouse uses the same JSONL session format as OpenClaw, so this
// package reuses the openclaw collector with a different mode name and
// default session directory.
package roundhouse

import (
	"os"
	"path/filepath"
	"runtime"

	"github.com/inceptionstack/telemetron/internal/collectorapi"
	"github.com/inceptionstack/telemetron/internal/config"
	"github.com/inceptionstack/telemetron/internal/openclaw"
	"github.com/inceptionstack/telemetron/internal/status"
)

const Mode = "roundhouse"

// collector wraps openclaw.Collector to report the correct Name().
type collector struct {
	*openclaw.Collector
}

func (c *collector) Name() string { return Mode }

func init() {
	collectorapi.Register(collectorapi.Spec{
		Name:          Mode,
		Description:   "Roundhouse agent session tailer",
		Factory:       newCollector,
		DecodeFn:      openclaw.DecodeConfig,
		DefaultConfig: defaultConfig,
	})
}

func newCollector(rawConfig any, store *status.Store, _ config.Config) (collectorapi.Collector, error) {
	decoded, err := openclaw.DecodeConfig(rawConfig)
	if err != nil {
		return nil, err
	}
	oc, err := openclaw.NewCollector(decoded.(openclaw.Config), store)
	if err != nil {
		return nil, err
	}
	return &collector{Collector: oc}, nil
}

func defaultConfig(paths config.Paths) any {
	return openclaw.Config{
		SessionDir:    defaultSessionDir(),
		FlushInterval: openclaw.DefaultFlushInterval,
		ScanInterval:  openclaw.DefaultScanInterval,
		StateFile:     filepath.Join(paths.StateDir, "roundhouse.state.json"),
	}
}

func defaultSessionDir() string {
	home := os.Getenv("HOME")
	if home == "" {
		return ""
	}
	if runtime.GOOS == "linux" || runtime.GOOS == "darwin" {
		return filepath.Join(home, ".roundhouse", "sessions", "main")
	}
	return ""
}
