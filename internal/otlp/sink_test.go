// SPDX-License-Identifier: Apache-2.0

package otlp

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/inceptionstack/clawtello/internal/status"
	"github.com/stretchr/testify/require"
)

func TestSinkSuccessPath(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	store := status.New(t.TempDir() + "/status.json")
	sink := NewSink(NewExporter(server.URL, "token", nil, server.Client()), slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil)), store, "testmode")
	sink.Counter("pack.agent.turn", map[string]string{"outcome": "success", "model.family": "openai"})
	sink.Counter("pack.tool.call", map[string]string{"outcome": "success", "tool.class": "file"})

	result, err := sink.Flush(context.Background())
	require.NoError(t, err)
	require.Equal(t, 2, result.MetricCount)
	require.Equal(t, 0, result.DroppedTotal)
}

func TestSinkAuthFailure(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()

	sink := NewSink(NewExporter(server.URL, "token", nil, server.Client()), slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil)), status.New(t.TempDir()+"/status.json"), "testmode")
	sink.Counter("pack.agent.turn", map[string]string{"outcome": "success", "model.family": "openai"})

	result, err := sink.Flush(context.Background())
	require.Error(t, err)
	require.True(t, result.AuthFailure)
	require.True(t, result.Dropped)
	require.Equal(t, 1, result.DroppedTotal)
}

func TestSinkServerFailure(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	sink := NewSink(NewExporter(server.URL, "token", nil, server.Client()), slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil)), status.New(t.TempDir()+"/status.json"), "testmode")
	sink.Counter("pack.agent.turn", map[string]string{"outcome": "success", "model.family": "openai"})

	result, err := sink.Flush(context.Background())
	require.Error(t, err)
	require.False(t, result.AuthFailure)
	require.True(t, result.Dropped)
	require.Equal(t, 1, result.DroppedTotal)
}

func TestSinkTransportFailure(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	client := server.Client()
	server.Close()

	sink := NewSink(NewExporter(server.URL, "token", nil, client), slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil)), status.New(t.TempDir()+"/status.json"), "testmode")
	sink.Counter("pack.agent.turn", map[string]string{"outcome": "success", "model.family": "openai"})

	result, err := sink.Flush(context.Background())
	require.Error(t, err)
	require.Equal(t, 0, result.HTTPStatus)
	require.True(t, result.Dropped)
	require.Equal(t, 1, result.DroppedTotal)
}
