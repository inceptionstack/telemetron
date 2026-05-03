// SPDX-License-Identifier: Apache-2.0

package otlp

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	colmetricpb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	"google.golang.org/protobuf/proto"

	"github.com/stretchr/testify/require"
)

func TestExporterPayloadShape(t *testing.T) {
	t.Parallel()

	var gotReq *http.Request
	var gotBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotReq = r
		var err error
		gotBody, err = io.ReadAll(r.Body)
		require.NoError(t, err)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	exporter := NewExporter(server.URL, "test-token", map[string]string{"deployment_id": "mvp-002"}, server.Client())
	_, err := exporter.Export(context.Background(), []Point{
		{Name: "pack.tool.call", Attrs: map[string]string{"outcome": "success", "tool.class": "file"}, Count: 2},
	})
	require.NoError(t, err)
	require.Equal(t, "application/x-protobuf", gotReq.Header.Get("Content-Type"))
	require.Equal(t, "Bearer test-token", gotReq.Header.Get("Authorization"))

	var payload colmetricpb.ExportMetricsServiceRequest
	require.NoError(t, proto.Unmarshal(gotBody, &payload))
	require.Len(t, payload.ResourceMetrics, 1)
	require.Len(t, payload.ResourceMetrics[0].ScopeMetrics, 1)
	require.Len(t, payload.ResourceMetrics[0].ScopeMetrics[0].Metrics, 1)
	metric := payload.ResourceMetrics[0].ScopeMetrics[0].Metrics[0]
	require.Equal(t, "pack.tool.call", metric.Name)
	sum := metric.GetSum()
	require.NotNil(t, sum)
	require.Len(t, sum.DataPoints, 1)
	require.EqualValues(t, 2, sum.DataPoints[0].GetAsInt())
}

// TestExporterTrimsTokenWhitespace guards against the production
// incident where /etc/telemetron/token contained a trailing \n
// (legitimate POSIX text-file convention) but the authorizer
// regex required an exact bearer value. Tokens written by any
// path — installer tee, editor save, `echo "$tok" >`, etc. —
// must not produce a 403.
func TestExporterTrimsTokenWhitespace(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		raw  string
	}{
		{"trailing LF", "lpk_live_abc\n"},
		{"trailing CRLF", "lpk_live_abc\r\n"},
		{"leading space", " lpk_live_abc"},
		{"trailing space", "lpk_live_abc "},
		{"leading tab trailing LF", "\tlpk_live_abc\n"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var gotAuth string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotAuth = r.Header.Get("Authorization")
				w.WriteHeader(http.StatusOK)
			}))
			defer server.Close()

			exporter := NewExporter(server.URL, tc.raw, nil, server.Client())
			_, err := exporter.Export(context.Background(), []Point{
				{Name: "pack.tool.call", Count: 1},
			})
			require.NoError(t, err)
			require.Equal(t, "Bearer lpk_live_abc", gotAuth,
				"exporter must strip surrounding whitespace/CRLF from bearer token")
		})
	}
}
