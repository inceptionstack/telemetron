// SPDX-License-Identifier: Apache-2.0

package otlp

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/inceptionstack/loki-otl/internal/contract"
	"github.com/inceptionstack/loki-otl/internal/status"
)

type FlushResult struct {
	MetricCount  int
	Bytes        int
	HTTPStatus   int
	Dropped      bool
	AuthFailure  bool
	DroppedTotal int
}

type Sink struct {
	mu      sync.Mutex
	buffer  map[string]*Point
	export  *Exporter
	logger  *slog.Logger
	status  *status.Store
	mode    string
	snap    status.Snapshot
	dropped int
}

func NewSink(exporter *Exporter, logger *slog.Logger, store *status.Store, mode string) *Sink {
	if logger == nil {
		logger = slog.Default()
	}
	return &Sink{
		buffer: make(map[string]*Point),
		export: exporter,
		logger: logger,
		status: store,
		mode:   mode,
	}
}

func (s *Sink) Counter(name string, attrs map[string]string) {
	normalized, ok := contract.NormalizeMetric(name, attrs)
	if !ok {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	key := seriesKey(name, normalized)
	point, ok := s.buffer[key]
	if !ok {
		point = &Point{Name: name, Attrs: normalized}
		s.buffer[key] = point
	}
	point.Count++
	if name == contract.MetricEmitterHeartbeat {
		s.snap.LastHeartbeatAt = time.Now().UTC()
		_ = s.status.Write(s.snap)
	}
}

func (s *Sink) Flush(ctx context.Context) (FlushResult, error) {
	s.mu.Lock()
	points := make([]Point, 0, len(s.buffer))
	for _, point := range s.buffer {
		points = append(points, *point)
	}
	s.buffer = make(map[string]*Point)
	s.mu.Unlock()

	if len(points) == 0 {
		return FlushResult{DroppedTotal: s.dropped}, nil
	}

	start := time.Now()
	resp, err := s.export.Export(ctx, points)
	if err != nil {
		s.logger.Warn("export failed", slog.String("error", err.Error()))
		s.recordDrop(resp.StatusCode)
		return FlushResult{MetricCount: len(points), Dropped: true, DroppedTotal: s.dropped}, err
	}

	result := FlushResult{
		MetricCount:  len(points),
		Bytes:        resp.Bytes,
		HTTPStatus:   resp.StatusCode,
		DroppedTotal: s.dropped,
	}

	if resp.StatusCode == httpStatusUnauthorized || resp.StatusCode == httpStatusForbidden {
		s.recordDrop(resp.StatusCode)
		result.AuthFailure = true
		result.Dropped = true
		result.DroppedTotal = s.dropped
		s.logger.Error("auth export failure", slog.Int("status", resp.StatusCode), slog.String("body", resp.Body))
		return result, fmt.Errorf("export rejected: %d", resp.StatusCode)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		s.recordDrop(resp.StatusCode)
		result.Dropped = true
		result.DroppedTotal = s.dropped
		s.logger.Warn("http export failure", slog.Int("status", resp.StatusCode), slog.String("body", resp.Body))
		return result, fmt.Errorf("export rejected: %d", resp.StatusCode)
	}

	s.snap.LastFlushAt = time.Now().UTC()
	s.snap.LastFlushMetric = len(points)
	s.snap.LastFlushBytes = resp.Bytes
	s.snap.LastHTTPStatus = resp.StatusCode
	_ = s.status.Write(s.snap)
	s.logger.Info("flush",
		slog.String("event", "flush"),
		slog.String("mode", s.mode),
		slog.Int("batch_metrics", len(points)),
		slog.Int("bytes", resp.Bytes),
		slog.Int("http_status", resp.StatusCode),
		slog.Int64("took_ms", time.Since(start).Milliseconds()),
	)
	return result, nil
}

func (s *Sink) recordDrop(statusCode int) {
	s.dropped++
	s.snap.DroppedBatches = s.dropped
	s.snap.LastHTTPStatus = statusCode
	_ = s.status.Write(s.snap)
}

const (
	httpStatusUnauthorized = 401
	httpStatusForbidden    = 403
)
