package collectors

import (
	"fmt"

	"github.com/inceptionstack/loki-otl/internal/collectorapi"
	"github.com/inceptionstack/loki-otl/internal/config"
	"github.com/inceptionstack/loki-otl/internal/openclaw"
	"github.com/inceptionstack/loki-otl/internal/status"
)

type Constructor func(cfg config.Config, store *status.Store) (collectorapi.Collector, error)

var registry = map[string]Constructor{
	openclaw.Mode: func(cfg config.Config, store *status.Store) (collectorapi.Collector, error) {
		return openclaw.NewCollector(cfg.OpenClaw, store)
	},
}

func New(cfg config.Config, store *status.Store) (collectorapi.Collector, error) {
	constructor, ok := registry[cfg.Mode]
	if !ok {
		return nil, fmt.Errorf("unsupported mode %q", cfg.Mode)
	}
	return constructor(cfg, store)
}
