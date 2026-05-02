# Extending `loki-otl`

Adding a new collector mode is intentionally additive. The existing `openclaw` collector is the reference implementation.

## Five-step workflow

1. Create `internal/<mode>/` with a type that satisfies `collectorapi.Collector`.
2. Implement a `DecodeFn`, `DefaultConfig`, and `Factory`.
3. Register the collector from `init()` with `collectorapi.Register()`.
4. Blank-import the package in `cmd/lokiotel/main.go`.
5. Add tests under `internal/<mode>/` and update the README platform notes if the new mode changes operator behavior.

## Example skeleton

This is a complete minimal skeleton for a hypothetical `claude-code` collector:

```go
package claudecode

import (
	"context"
	"fmt"
	"time"

	"github.com/inceptionstack/loki-otl/internal/collectorapi"
	"github.com/inceptionstack/loki-otl/internal/config"
	"github.com/inceptionstack/loki-otl/internal/contract"
	"github.com/inceptionstack/loki-otl/internal/status"
	"github.com/mitchellh/mapstructure"
)

type Config struct {
	SessionDir    string        `mapstructure:"session_dir"`
	FlushInterval time.Duration `mapstructure:"flush_interval"`
}

type Collector struct {
	cfg   Config
	store *status.Store
}

func init() {
	collectorapi.Register(collectorapi.Spec{
		Name: "claude-code",
		Description: "Claude Code session tailer",
		Factory: func(raw any, store *status.Store, base config.Config) (collectorapi.Collector, error) {
			decoded, err := decodeConfig(raw)
			if err != nil {
				return nil, err
			}
			return &Collector{cfg: decoded.(Config), store: store}, nil
		},
		DecodeFn:      decodeConfig,
		DefaultConfig: defaultConfig,
	})
}

func (c *Collector) Name() string { return "claude-code" }

func (c *Collector) Start(ctx context.Context, sink collectorapi.MetricSink) error {
	ticker := time.NewTicker(c.cfg.FlushInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			sink.Counter(contract.MetricEmitterHeartbeat, nil)
		}
	}
}

func decodeConfig(raw any) (any, error) {
	var cfg Config
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
	if cfg.SessionDir == "" {
		return nil, fmt.Errorf("claude-code.session_dir is required")
	}
	if cfg.FlushInterval <= 0 {
		return nil, fmt.Errorf("claude-code.flush_interval must be > 0")
	}
	return cfg, nil
}

func defaultConfig(paths config.Paths) any {
	return Config{
		SessionDir:    "",
		FlushInterval: 15 * time.Second,
	}
}
```

## Registration note

After adding the package, import it in `cmd/lokiotel/main.go`:

```go
import (
	_ "github.com/inceptionstack/loki-otl/internal/claudecode"
)
```

## Testing guidance

- Add decode and derivation tests under `internal/<mode>/`.
- Add an end-to-end collector test that verifies the emitted metrics stay inside the contract allowlist.
- Run `go test ./...` and `golangci-lint run` before opening a PR.
