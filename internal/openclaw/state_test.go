package openclaw

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/inceptionstack/loki-otl/internal/config"
	"github.com/inceptionstack/loki-otl/internal/otlp"
	"github.com/inceptionstack/loki-otl/internal/status"
	"github.com/stretchr/testify/require"
)

type recordingSink struct {
	points []otlp.Point
}

func (s *recordingSink) Counter(name string, attrs map[string]string) {
	s.points = append(s.points, otlp.Point{Name: name, Attrs: attrs, Count: 1})
}

func TestScanFileResumesFromSavedOffset(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	sessionDir := filepath.Join(dir, "sessions")
	require.NoError(t, os.MkdirAll(sessionDir, 0o755))
	stateFile := filepath.Join(dir, "openclaw.state.json")
	statusFile := filepath.Join(dir, "status.json")
	sessionPath := filepath.Join(sessionDir, "sample.jsonl")

	firstLine := `{"type":"session","cwd":"/home/ec2-user/.openclaw/workspace"}` + "\n"
	secondLine := `{"type":"message","message":{"role":"assistant","provider":"amazon-bedrock","model":"us.anthropic.claude-opus-4-6-v1","stopReason":"stop","content":[{"type":"toolCall","name":"read"}]}}` + "\n"
	require.NoError(t, os.WriteFile(sessionPath, []byte(firstLine+secondLine), 0o644))

	collector, err := NewCollector(config.OpenClawConfig{
		SessionDir:    sessionDir,
		FlushInterval: time.Second,
		ScanInterval:  time.Second,
		StateFile:     stateFile,
		StatusFile:    statusFile,
	}, status.New(statusFile))
	require.NoError(t, err)

	sink := &recordingSink{}
	require.NoError(t, collector.scan(sink))
	require.Len(t, sink.points, 3)

	state, err := LoadState(stateFile)
	require.NoError(t, err)
	require.NotZero(t, state.Files[sessionPath].Offset)

	thirdLine := `{"type":"custom","customType":"openclaw:prompt-error"}` + "\n"
	fh, err := os.OpenFile(sessionPath, os.O_APPEND|os.O_WRONLY, 0)
	require.NoError(t, err)
	_, err = fh.WriteString(thirdLine)
	require.NoError(t, err)
	require.NoError(t, fh.Close())

	collector2, err := NewCollector(config.OpenClawConfig{
		SessionDir:    sessionDir,
		FlushInterval: time.Second,
		ScanInterval:  time.Second,
		StateFile:     stateFile,
		StatusFile:    statusFile,
	}, status.New(statusFile))
	require.NoError(t, err)

	sink2 := &recordingSink{}
	require.NoError(t, collector2.scan(sink2))
	require.Len(t, sink2.points, 1)
	require.Equal(t, "pack.error", sink2.points[0].Name)
	require.Equal(t, "prompt", sink2.points[0].Attrs["error.type"])
}
