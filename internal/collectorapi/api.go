package collectorapi

import (
	"context"

	"github.com/inceptionstack/loki-otl/internal/otlp"
)

type MetricSink interface {
	Counter(name string, attrs map[string]string)
	Flush(ctx context.Context) (otlp.FlushResult, error)
}

type Collector interface {
	Name() string
	Start(ctx context.Context, sink MetricSink) error
}
