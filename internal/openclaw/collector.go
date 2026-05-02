package openclaw

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/inceptionstack/loki-otl/internal/collectorapi"
	"github.com/inceptionstack/loki-otl/internal/config"
	"github.com/inceptionstack/loki-otl/internal/contract"
	"github.com/inceptionstack/loki-otl/internal/status"
)

type Collector struct {
	cfg   config.OpenClawConfig
	store *status.Store
	state State
}

func NewCollector(cfg config.OpenClawConfig, store *status.Store) (*Collector, error) {
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
			defer cancel()
			sink.Counter(contract.MetricEmitterHeartbeat, nil)
			_, _ = sink.Flush(flushCtx)
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

func (c *Collector) scan(sink interface {
	Counter(string, map[string]string)
}) error {
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

func (c *Collector) scanFile(path string, sink interface {
	Counter(string, map[string]string)
}) error {
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
		fileState.LastTopRole = ""
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
		if len(raw) == 0 && errorsIsEOF(err) {
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

func (c *Collector) handleEvent(path string, event map[string]any, fileState *FileState, sink interface {
	Counter(string, map[string]string)
}) {
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
		fileState.LastModelFamily = DeriveModelFamily(asString(event["provider"]), asString(event["modelId"]))
	case "message":
		msg, ok := event["message"].(map[string]any)
		if !ok {
			return
		}
		c.handleMessage(path, msg, fileState, sink)
	case "custom":
		customType := asString(event["customType"])
		if customType != "" && strings.Contains(strings.ToLower(customType), "error") {
			sink.Counter(contract.MetricError, map[string]string{
				"error.type": DeriveErrorType(customType),
			})
		}
	}
}

func (c *Collector) handleMessage(path string, msg map[string]any, fileState *FileState, sink interface {
	Counter(string, map[string]string)
}) {
	role := asString(msg["role"])
	if role == "user" && fileState.SessionType == "" {
		fileState.SessionType = DeriveSessionType(path, "", firstText(msg["content"]))
	}
	if role == "user" || role == "assistant" {
		if fileState.LastTopRole == role {
			return
		}
		fileState.LastTopRole = role
	}
	if role != "assistant" {
		return
	}
	modelFamily := DeriveModelFamily(asString(msg["provider"]), asString(msg["model"]))
	if modelFamily == "unknown" && fileState.LastModelFamily != "" {
		modelFamily = fileState.LastModelFamily
	}
	outcome := "success"
	if asString(msg["stopReason"]) == "aborted" {
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
		if asString(block["type"]) != "toolCall" {
			continue
		}
		sink.Counter(contract.MetricToolCall, map[string]string{
			"outcome":    outcome,
			"tool.class": DeriveToolClass(asString(block["name"])),
		})
	}
}

func asString(value any) string {
	s, _ := value.(string)
	return s
}

func firstText(value any) string {
	items, _ := value.([]any)
	for _, item := range items {
		block, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if text, ok := block["text"].(string); ok {
			return text
		}
	}
	return ""
}

func errorsIsEOF(err error) bool {
	return err == nil || err == io.EOF
}

func normalizeModel(value string) string {
	if value == "" {
		return "unknown"
	}
	return value
}
