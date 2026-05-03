# macOS

`telemetron install` and `telemetron uninstall` are not supported on macOS in v0.2.

Use:

```bash
telemetron start --config ~/.config/telemetron/config.yaml
telemetron status
```

Default macOS paths:

- config: `~/.config/telemetron/config.yaml`
- token: `~/.config/telemetron/token`
- state: `~/.local/share/telemetron/`
- status: `~/.local/share/telemetron/status.json`
