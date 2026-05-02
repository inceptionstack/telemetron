// SPDX-License-Identifier: Apache-2.0

package openclaw

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/inceptionstack/loki-otl/internal/collectorapi"
	"github.com/inceptionstack/loki-otl/internal/config"
	"github.com/inceptionstack/loki-otl/internal/contract"
	"github.com/inceptionstack/loki-otl/internal/jsonx"
	"github.com/inceptionstack/loki-otl/internal/status"
	"github.com/mitchellh/mapstructure"
)

type Collector struct {
	cfg   Config
	store *status.Store
	state State
}

type Config struct {
	SessionDir    string        `mapstructure:"session_dir" yaml:"session_dir"`
	FlushInterval time.Duration `mapstructure:"flush_interval" yaml:"flush_interval"`
	ScanInterval  time.Duration `mapstructure:"scan_interval" yaml:"scan_interval"`
	StateFile     string        `mapstructure:"state_file" yaml:"state_file"`
}

func init() {
	collectorapi.Register(collectorapi.Spec{
		Name:          Mode,
		Description:   "OpenClaw agent session tailer",
		Factory:       newCollector,
		DecodeFn:      decodeConfig,
		DefaultConfig: defaultConfig,
	})
}

func newCollector(rawConfig any, store *status.Store, _ config.Config) (collectorapi.Collector, error) {
	decoded, err := decodeConfig(rawConfig)
	if err != nil {
		return nil, err
	}
	return NewCollector(decoded.(Config), store)
}

func NewCollector(cfg Config, store *status.Store) (*Collector, error) {
	state, err := LoadState(cfg.StateFile)
	if err != nil {
		return nil, err
	}
	return &Collector{cfg: cfg, store: store, state: state}, nil
}

func (c *Collector) Name() string {
	return Mode
}

func (c *Collector) Start(ctx context.Context, sink collectorapi.MetricSink) error {
	scanTicker := time.NewTicker(c.cfg.ScanInterval)
	defer scanTicker.Stop()

	nextFlushDelay := c.cfg.FlushInterval
	flushTimer := time.NewTimer(nextFlushDelay)
	defer flushTimer.Stop()

	if err := c.scan(sink); err != nil {
		return err
	}

	for {
		select {
		case <-ctx.Done():
			_ = SaveState(c.cfg.StateFile, c.state)
			flushCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			sink.Counter(contract.MetricEmitterHeartbeat, nil)
			_, _ = sink.Flush(flushCtx)
			cancel()
			return nil
		case <-scanTicker.C:
			if err := c.scan(sink); err != nil {
				return err
			}
		case <-flushTimer.C:
			sink.Counter(contract.MetricEmitterHeartbeat, nil)
			result, err := sink.Flush(ctx)
			_ = SaveState(c.cfg.StateFile, c.state)
			nextFlushDelay = c.cfg.FlushInterval
			if result.AuthFailure {
				nextFlushDelay = c.cfg.FlushInterval * 6
			}
			flushTimer.Reset(nextFlushDelay)
			if err != nil && result.AuthFailure {
				continue
			}
		}
	}
}

func (c *Collector) ReportStatus(_ context.Context) []collectorapi.StatusLine {
	files, _ := os.ReadDir(c.cfg.SessionDir)
	state, _ := LoadState(c.cfg.StateFile)
	return []collectorapi.StatusLine{
		{Label: "session_dir", Value: fmt.Sprintf("%s (%d files)", c.cfg.SessionDir, len(files))},
		{Label: "state file", Value: fmt.Sprintf("%s (%d sessions tracked)", c.cfg.StateFile, len(state.Files))},
	}
}

func (c *Collector) scan(sink collectorapi.MetricSink) error {
	entries, err := filepath.Glob(filepath.Join(c.cfg.SessionDir, "*.jsonl"))
	if err != nil {
		return err
	}
	sort.Strings(entries)
	for _, path := range entries {
		if err := c.scanFile(path, sink); err != nil {
			return err
		}
	}
	return SaveState(c.cfg.StateFile, c.state)
}

func (c *Collector) scanFile(path string, sink collectorapi.MetricSink) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	fileState := c.state.Files[path]
	if info.Size() < fileState.Offset {
		fileState.Offset = 0
		fileState.SessionStarted = false
		fileState.SessionType = ""
		fileState.LastModelFamily = ""
	}

	fh, err := os.Open(path)
	if err != nil {
		return err
	}
	defer fh.Close()

	if _, err := fh.Seek(fileState.Offset, 0); err != nil {
		return err
	}
	reader := bufio.NewReader(fh)
	offset := fileState.Offset

	for {
		raw, err := reader.ReadBytes('\n')
		if len(raw) == 0 && (err == nil || err == io.EOF) {
			break
		}
		if len(raw) == 0 {
			break
		}
		if raw[len(raw)-1] != '\n' {
			break
		}

		var event map[string]any
		if err := json.Unmarshal(raw, &event); err != nil {
			offset += int64(len(raw))
			fileState.Offset = offset
			continue
		}

		c.handleEvent(path, event, &fileState, sink)
		offset += int64(len(raw))
		fileState.Offset = offset
		if err == io.EOF {
			break
		}
	}

	c.state.Files[path] = fileState
	return nil
}

func (c *Collector) handleEvent(path string, event map[string]any, fileState *FileState, sink collectorapi.MetricSink) {
	switch event["type"] {
	case "session":
		cwd, _ := event["cwd"].(string)
		sessionType := DeriveSessionType(path, cwd, "")
		if !fileState.SessionStarted {
			if sessionType == "" {
				sessionType = "unknown"
			}
			sink.Counter(contract.MetricSessionStart, map[string]string{
				"outcome":      "success",
				"model.family": normalizeModel(fileState.LastModelFamily),
				"session.type": sessionType,
			})
			fileState.SessionStarted = true
			fileState.SessionType = sessionType
		}
	case "model_change":
		fileState.LastModelFamily = DeriveModelFamily(jsonx.AsString(event["provider"]), jsonx.AsString(event["modelId"]))
	case "message":
		msg, ok := event["message"].(map[string]any)
		if !ok {
			return
		}
		c.handleMessage(path, msg, fileState, sink)
	case "custom":
		customType := jsonx.AsString(event["customType"])
		if customType != "" && isErrorEvent(customType) {
			sink.Counter(contract.MetricError, map[string]string{
				"error.type": DeriveErrorType(customType),
			})
		}
	}
}

func (c *Collector) handleMessage(path string, msg map[string]any, fileState *FileState, sink collectorapi.MetricSink) {
	role := jsonx.AsString(msg["role"])
	if role == "user" && fileState.SessionType == "" {
		fileState.SessionType = DeriveSessionType(path, "", jsonx.FirstText(msg["content"]))
	}
	if role != "assistant" {
		return
	}

	modelFamily := DeriveModelFamily(jsonx.AsString(msg["provider"]), jsonx.AsString(msg["model"]))
	if modelFamily == "unknown" && fileState.LastModelFamily != "" {
		modelFamily = fileState.LastModelFamily
	}

	outcome := "success"
	if jsonx.AsString(msg["stopReason"]) == "aborted" {
		outcome = "aborted"
	}

	sink.Counter(contract.MetricAgentTurn, map[string]string{
		"outcome":      outcome,
		"model.family": modelFamily,
	})

	content, _ := msg["content"].([]any)
	for _, item := range content {
		block, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if jsonx.AsString(block["type"]) != "toolCall" {
			continue
		}
		sink.Counter(contract.MetricToolCall, map[string]string{
			"outcome":    outcome,
			"tool.class": DeriveToolClass(jsonx.AsString(block["name"])),
		})
	}
}

func decodeConfig(raw any) (any, error) {
	cfg := Config{}
	decoder, err := mapstructure.NewDecoder(&mapstructure.DecoderConfig{
		DecodeHook:       mapstructure.StringToTimeDurationHookFunc(),
		ErrorUnused:      true,
		Result:           &cfg,
		TagName:          "mapstructure",
		WeaklyTypedInput: true,
	})
	if err != nil {
		return nil, err
	}
	if err := decoder.Decode(raw); err != nil {
		return nil, err
	}
	if strings.TrimSpace(cfg.SessionDir) == "" {
		return nil, fmt.Errorf("openclaw.session_dir is required")
	}
	if cfg.FlushInterval <= 0 {
		return nil, fmt.Errorf("openclaw.flush_interval must be > 0")
	}
	if cfg.ScanInterval <= 0 {
		return nil, fmt.Errorf("openclaw.scan_interval must be > 0")
	}
	if strings.TrimSpace(cfg.StateFile) == "" {
		return nil, fmt.Errorf("openclaw.state_file is required")
	}
	return cfg, nil
}

func defaultConfig(paths config.Paths) any {
	return Config{
		SessionDir:    defaultSessionDir(),
		FlushInterval: 15 * time.Second,
		ScanInterval:  15 * time.Second,
		StateFile:     filepath.Join(paths.StateDir, "openclaw.state.json"),
	}
}

func defaultSessionDir() string {
	return resolveSessionDir(runtime.GOOS, os.Getenv("HOME"))
}

func resolveSessionDir(goos, home string) string {
	home = strings.TrimSpace(home)
	switch goos {
	case "linux":
		if home != "" {
			return filepath.Join(home, ".openclaw/agents/main/sessions")
		}
		return ""
	case "darwin":
		if home != "" {
			return filepath.Join(home, ".openclaw/agents/main/sessions")
		}
		return ""
	default:
		return ""
	}
}

func normalizeModel(value string) string {
	if value == "" {
		return "unknown"
	}
	return value
}

func isErrorEvent(value string) bool {
	return strings.Contains(strings.ToLower(value), "error")
}
