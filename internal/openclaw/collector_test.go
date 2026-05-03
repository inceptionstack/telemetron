// SPDX-License-Identifier: Apache-2.0

package openclaw

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/inceptionstack/telemetron/internal/collectorapi"
	"github.com/inceptionstack/telemetron/internal/otlp"
	"github.com/inceptionstack/telemetron/internal/status"
	"github.com/stretchr/testify/require"
)

type recordingSink struct {
	points []otlp.Point
}

func (s *recordingSink) Counter(name string, attrs map[string]string) {
	s.points = append(s.points, otlp.Point{Name: name, Attrs: attrs, Count: 1})
}

func (s *recordingSink) Flush(context.Context) (otlp.FlushResult, error) {
	return otlp.FlushResult{}, nil
}

var _ collectorapi.MetricSink = (*recordingSink)(nil)

func TestCollectorScanEndToEnd(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	sessionDir := filepath.Join(dir, "sessions")
	require.NoError(t, os.MkdirAll(sessionDir, 0o755))
	stateFile := filepath.Join(dir, "state.json")
	statusFile := filepath.Join(dir, "status.json")
	sessionPath := filepath.Join(sessionDir, "sample.jsonl")

	transcript := "" +
		`{"type":"session","cwd":"/home/tester/.openclaw/workspace"}` + "\n" +
		`{"type":"message","message":{"role":"assistant","provider":"amazon-bedrock","model":"us.anthropic.claude-opus-4-6-v1","stopReason":"stop","content":[{"type":"toolCall","name":"read"}]}}` + "\n" +
		`{"type":"message","message":{"role":"assistant","provider":"openai","model":"gpt-4.1","stopReason":"stop","content":[{"type":"toolCall","name":"web_fetch"}]}}` + "\n" +
		`{"type":"message","message":{"role":"assistant","provider":"openai","model":"gpt-4.1","stopReason":"stop","content":[]}}` + "\n" +
		`{"type":"custom","customType":"openclaw:prompt-error"}` + "\n"
	require.NoError(t, os.WriteFile(sessionPath, []byte(transcript), 0o644))

	collector, err := NewCollector(Config{
		SessionDir:    sessionDir,
		FlushInterval: time.Second,
		ScanInterval:  time.Second,
		StateFile:     stateFile,
	}, status.New(statusFile))
	require.NoError(t, err)

	sink := &recordingSink{}
	require.NoError(t, collector.scan(sink))
	require.Len(t, sink.points, 7)

	agentTurns := 0
	toolCalls := 0
	errors := 0
	for _, point := range sink.points {
		switch point.Name {
		case "pack.agent.turn":
			agentTurns++
		case "pack.tool.call":
			toolCalls++
		case "pack.error":
			errors++
		}
	}
	require.Equal(t, 3, agentTurns)
	require.Equal(t, 2, toolCalls)
	require.Equal(t, 1, errors)
	require.Equal(t, "file", sink.points[2].Attrs["tool.class"])
	require.Equal(t, "http", sink.points[4].Attrs["tool.class"])

	fh, err := os.OpenFile(sessionPath, os.O_APPEND|os.O_WRONLY, 0)
	require.NoError(t, err)
	_, err = fh.WriteString(`{"type":"message","message":{"role":"assistant","provider":"openai","model":"gpt-4.1","stopReason":"stop","content":[]}}`)
	require.NoError(t, err)
	require.NoError(t, fh.Close())

	partialSink := &recordingSink{}
	require.NoError(t, collector.scan(partialSink))
	require.Empty(t, partialSink.points)

	fh, err = os.OpenFile(sessionPath, os.O_APPEND|os.O_WRONLY, 0)
	require.NoError(t, err)
	_, err = fh.WriteString("\n")
	require.NoError(t, err)
	require.NoError(t, fh.Close())

	completionSink := &recordingSink{}
	require.NoError(t, collector.scan(completionSink))
	require.Len(t, completionSink.points, 1)
	require.Equal(t, "pack.agent.turn", completionSink.points[0].Name)

	finalSink := &recordingSink{}
	require.NoError(t, collector.scan(finalSink))
	require.Empty(t, finalSink.points)
}

func TestResolveSessionDir(t *testing.T) {
	t.Parallel()

	home := filepath.Join(string(os.PathSeparator), "home", "tester")
	expected := filepath.Join(home, ".openclaw/agents/main/sessions")

	require.Equal(t, expected, resolveSessionDir("linux", home))
	require.Equal(t, "", resolveSessionDir("linux", ""))
	require.Equal(t, expected, resolveSessionDir("darwin", home))
	require.Equal(t, "", resolveSessionDir("darwin", ""))
}

func TestDefaultSessionDirUsesHome(t *testing.T) {
	t.Setenv("HOME", filepath.Join(string(os.PathSeparator), "tmp", "tester"))
	require.Equal(t,
		filepath.Join(string(os.PathSeparator), "tmp", "tester", ".openclaw/agents/main/sessions"),
		defaultSessionDir(),
	)
}
