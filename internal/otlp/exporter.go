// SPDX-License-Identifier: Apache-2.0

package otlp

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	colmetricpb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	metricpb "go.opentelemetry.io/proto/otlp/metrics/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
	"google.golang.org/protobuf/proto"
)

type Point struct {
	Name  string
	Attrs map[string]string
	Count uint64
}

type Exporter struct {
	endpoint string
	token    string
	client   *http.Client
	declared map[string]string
}

type Response struct {
	StatusCode int
	Body       string
	Bytes      int
}

func NewExporter(endpoint, token string, declared map[string]string, client *http.Client) *Exporter {
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	// Defense in depth: bearer tokens must not contain leading/trailing
	// whitespace or CR/LF. Trim here so an accidentally-miswritten token
	// file does not produce a corrupt Authorization header that strict
	// authorizers reject with 403 (we've seen this in production).
	return &Exporter{
		endpoint: endpoint,
		token:    strings.Trim(token, " \t\r\n"),
		client:   client,
		declared: declared,
	}
}

func (e *Exporter) Export(ctx context.Context, points []Point) (Response, error) {
	payload, err := e.Marshal(points)
	if err != nil {
		return Response{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.endpoint, bytes.NewReader(payload))
	if err != nil {
		return Response{}, err
	}
	req.Header.Set("Content-Type", "application/x-protobuf")
	req.Header.Set("Authorization", "Bearer "+e.token)
	resp, err := e.client.Do(req)
	if err != nil {
		return Response{}, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	return Response{
		StatusCode: resp.StatusCode,
		Body:       string(body),
		Bytes:      len(payload),
	}, nil
}

func (e *Exporter) Marshal(points []Point) ([]byte, error) {
	now := uint64(time.Now().UnixNano())
	metrics := make([]*metricpb.Metric, 0, len(points))
	for _, point := range points {
		attrs := make([]*commonpb.KeyValue, 0, len(point.Attrs))
		for key, value := range point.Attrs {
			attrs = append(attrs, &commonpb.KeyValue{
				Key: key,
				Value: &commonpb.AnyValue{
					Value: &commonpb.AnyValue_StringValue{StringValue: value},
				},
			})
		}
		metrics = append(metrics, &metricpb.Metric{
			Name: point.Name,
			Data: &metricpb.Metric_Sum{
				Sum: &metricpb.Sum{
					AggregationTemporality: metricpb.AggregationTemporality_AGGREGATION_TEMPORALITY_DELTA,
					IsMonotonic:            true,
					DataPoints: []*metricpb.NumberDataPoint{{
						TimeUnixNano: now,
						Value:        &metricpb.NumberDataPoint_AsInt{AsInt: int64(point.Count)},
						Attributes:   attrs,
					}},
				},
			},
		})
	}

	resourceAttrs := make([]*commonpb.KeyValue, 0, len(e.declared))
	for key, value := range e.declared {
		if value == "" {
			continue
		}
		resourceAttrs = append(resourceAttrs, &commonpb.KeyValue{
			Key: key,
			Value: &commonpb.AnyValue{
				Value: &commonpb.AnyValue_StringValue{StringValue: value},
			},
		})
	}

	req := &colmetricpb.ExportMetricsServiceRequest{
		ResourceMetrics: []*metricpb.ResourceMetrics{{
			Resource: &resourcepb.Resource{Attributes: resourceAttrs},
			ScopeMetrics: []*metricpb.ScopeMetrics{{
				Metrics: metrics,
			}},
		}},
	}
	data, err := proto.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal otlp payload: %w", err)
	}
	return data, nil
}
