//go:build demo

package demo

import (
	"context"
	"time"

	"github.com/inceptionstack/loki-otl/internal/collectorapi"
	"github.com/inceptionstack/loki-otl/internal/config"
	"github.com/inceptionstack/loki-otl/internal/contract"
	"github.com/inceptionstack/loki-otl/internal/status"
)

func init() {
	collectorapi.Register(collectorapi.Spec{
		Name:          "demo",
		Description:   "Demo heartbeat collector",
		Factory:       newCollector,
		DecodeFn:      decodeConfig,
		DefaultConfig: defaultConfig,
	})
}

type collector struct{}

func newCollector(rawConfig any, store *status.Store, base config.Config) (collectorapi.Collector, error) {
	return collector{}, nil
}

func (collector) Name() string {
	return "demo"
}

func (collector) Start(ctx context.Context, sink collectorapi.MetricSink) error {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			sink.Counter(contract.MetricEmitterHeartbeat, nil)
		}
	}
}

func decodeConfig(raw any) (any, error) {
	return raw, nil
}

func defaultConfig(paths config.Paths) any {
	return map[string]any{}
}
