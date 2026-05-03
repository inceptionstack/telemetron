// SPDX-License-Identifier: Apache-2.0

package collectorapi

import (
	"context"
	"fmt"
	"sort"
	"sync"

	"github.com/inceptionstack/clawtello/internal/config"
	"github.com/inceptionstack/clawtello/internal/otlp"
	"github.com/inceptionstack/clawtello/internal/status"
)

type MetricSink interface {
	Counter(name string, attrs map[string]string)
	Flush(ctx context.Context) (otlp.FlushResult, error)
}

type Collector interface {
	Name() string
	Start(ctx context.Context, sink MetricSink) error
}

type StatusLine struct {
	Label string
	Value string
}

type StatusReporter interface {
	Collector
	ReportStatus(ctx context.Context) []StatusLine
}

type Spec struct {
	Name          string
	Description   string
	Factory       func(rawConfig any, store *status.Store, base config.Config) (Collector, error)
	DecodeFn      func(raw any) (validated any, err error)
	DefaultConfig func(paths config.Paths) any
}

var (
	registryMu sync.RWMutex
	registry   = map[string]Spec{}
)

func Register(spec Spec) {
	if spec.Name == "" {
		panic("collectorapi: collector name is required")
	}
	if spec.Factory == nil {
		panic(fmt.Sprintf("collectorapi: collector %q factory is required", spec.Name))
	}

	registryMu.Lock()
	defer registryMu.Unlock()

	if _, exists := registry[spec.Name]; exists {
		panic(fmt.Sprintf("collectorapi: duplicate collector %q", spec.Name))
	}
	registry[spec.Name] = spec
	config.RegisterMode(spec.Name, spec.DecodeFn, spec.DefaultConfig)
}

func Lookup(name string) (Spec, bool) {
	registryMu.RLock()
	defer registryMu.RUnlock()

	spec, ok := registry[name]
	return spec, ok
}

func All() []Spec {
	registryMu.RLock()
	defer registryMu.RUnlock()

	names := make([]string, 0, len(registry))
	for name := range registry {
		names = append(names, name)
	}
	sort.Strings(names)

	specs := make([]Spec, 0, len(names))
	for _, name := range names {
		specs = append(specs, registry[name])
	}
	return specs
}

func New(rawConfig any, store *status.Store, base config.Config) (Collector, error) {
	spec, ok := Lookup(base.Mode)
	if !ok {
		return nil, fmt.Errorf("unsupported mode %q", base.Mode)
	}
	return spec.Factory(rawConfig, store, base)
}
