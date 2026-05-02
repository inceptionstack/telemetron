package systemd

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"

	"github.com/inceptionstack/loki-otl/internal/config"
)

const (
	BinaryPath = "/usr/local/bin/lokiotel"
	UnitPath   = "/etc/systemd/system/lokiotel.service"
	ConfigDir  = "/etc/lokiotel"
)

func EnsureRoot() error {
	if os.Geteuid() != 0 {
		return fmt.Errorf("this command must run as root")
	}
	return nil
}

func Install(cfg config.Config, token string) error {
	if err := EnsureRoot(); err != nil {
		return err
	}
	if err := ensureUser(); err != nil {
		return err
	}
	if err := copySelf(); err != nil {
		return err
	}
	if err := os.MkdirAll(ConfigDir, 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(cfg.OpenClaw.StateFile), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(ConfigDir, "config.yaml"), []byte(cfg.ToYAML()), 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(cfg.TokenFile, []byte(token+"\n"), 0o600); err != nil {
		return err
	}
	if err := os.WriteFile(UnitPath, []byte(renderUnit()), 0o644); err != nil {
		return err
	}
	return nil
}

func EnableAndStart() error {
	if err := run("systemctl", "daemon-reload"); err != nil {
		return err
	}
	return run("systemctl", "enable", "--now", "lokiotel.service")
}

func Uninstall() error {
	if err := EnsureRoot(); err != nil {
		return err
	}
	_ = run("systemctl", "disable", "--now", "lokiotel.service")
	if err := os.Remove(UnitPath); err != nil && !os.IsNotExist(err) {
		return err
	}
	return run("systemctl", "daemon-reload")
}

func renderUnit() string {
	return `[Unit]
Description=Loki OTel sidecar
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=lokiotel
Group=lokiotel
ExecStart=/usr/local/bin/lokiotel start --config /etc/lokiotel/config.yaml
Restart=on-failure
RestartSec=10
LimitCORE=0
ProtectSystem=strict
ProtectHome=read-only
PrivateTmp=true
NoNewPrivileges=true
ReadWritePaths=/var/lib/lokiotel
StandardOutput=journal
StandardError=journal

[Install]
WantedBy=multi-user.target
`
}

func copySelf() error {
	src, err := os.Executable()
	if err != nil {
		return err
	}
	srcData, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	if dstData, err := os.ReadFile(BinaryPath); err == nil && bytes.Equal(srcData, dstData) {
		return nil
	}
	return os.WriteFile(BinaryPath, srcData, 0o755)
}

func ensureUser() error {
	if _, err := user.Lookup("lokiotel"); err == nil {
		return nil
	}
	if err := run("useradd", "--system", "--home-dir", "/var/lib/lokiotel", "--shell", "/usr/sbin/nologin", "lokiotel"); err == nil {
		return nil
	}
	return run("useradd", "--system", "--home-dir", "/var/lib/lokiotel", "--shell", "/sbin/nologin", "lokiotel")
}

func run(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
