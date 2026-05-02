//go:build linux

// SPDX-License-Identifier: Apache-2.0

package service

import (
	"bytes"
	"fmt"
	iofs "io/fs"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/inceptionstack/loki-otl/internal/config"
)

const (
	binaryPath = "/usr/local/bin/lokiotel"
	unitPath   = "/etc/systemd/system/lokiotel.service"
	configDir  = "/etc/lokiotel"
)

type filesystem interface {
	MkdirAll(path string, perm os.FileMode) error
	ReadFile(path string) ([]byte, error)
	WriteFile(path string, data []byte, perm os.FileMode) error
	Remove(path string) error
	Chown(path string, uid, gid int) error
	Chmod(path string, mode os.FileMode) error
	WalkDir(root string, fn filepath.WalkFunc) error
}

type osFS struct{}

func (osFS) MkdirAll(path string, perm os.FileMode) error { return os.MkdirAll(path, perm) }
func (osFS) ReadFile(path string) ([]byte, error)         { return os.ReadFile(path) }
func (osFS) WriteFile(path string, data []byte, perm os.FileMode) error {
	return os.WriteFile(path, data, perm)
}
func (osFS) Remove(path string) error                        { return os.Remove(path) }
func (osFS) Chown(path string, uid, gid int) error           { return os.Chown(path, uid, gid) }
func (osFS) Chmod(path string, mode os.FileMode) error       { return os.Chmod(path, mode) }
func (osFS) WalkDir(root string, fn filepath.WalkFunc) error { return filepath.Walk(root, fn) }

type linuxService struct {
	fs         filesystem
	run        func(name string, args ...string) error
	runOutput  func(name string, args ...string) ([]byte, error)
	lookupUser func(username string) (*user.User, error)
	executable func() (string, error)
	uid        func() int
}

func newService() Service {
	return &linuxService{
		fs: osFS{},
		run: func(name string, args ...string) error {
			cmd := exec.Command(name, args...)
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
			return cmd.Run()
		},
		runOutput: func(name string, args ...string) ([]byte, error) {
			return exec.Command(name, args...).Output()
		},
		lookupUser: user.Lookup,
		executable: os.Executable,
		uid:        os.Geteuid,
	}
}

func (s *linuxService) Install(cfg config.Config, token string) error {
	if s.uid() != 0 {
		return fmt.Errorf("this command must run as root")
	}
	if err := s.ensureUser(); err != nil {
		return err
	}
	account, err := s.lookupUser("lokiotel")
	if err != nil {
		return err
	}
	uid, err := strconv.Atoi(account.Uid)
	if err != nil {
		return err
	}
	gid, err := strconv.Atoi(account.Gid)
	if err != nil {
		return err
	}

	if err := s.copySelf(); err != nil {
		return err
	}
	if err := s.fs.MkdirAll(configDir, 0o755); err != nil {
		return err
	}
	if err := s.fs.MkdirAll(filepath.Dir(cfg.FilePath), 0o755); err != nil {
		return err
	}
	if err := s.fs.MkdirAll(cfg.Paths.StateDir, 0o755); err != nil {
		return err
	}
	if err := s.fs.MkdirAll(filepath.Dir(cfg.TokenFile), 0o755); err != nil {
		return err
	}

	data, err := cfg.Marshal()
	if err != nil {
		return err
	}
	if err := s.fs.WriteFile(cfg.FilePath, data, 0o644); err != nil {
		return err
	}
	if err := s.fs.WriteFile(cfg.TokenFile, []byte(token+"\n"), 0o400); err != nil {
		return err
	}
	if err := s.fs.Chown(cfg.TokenFile, uid, gid); err != nil {
		return err
	}
	if err := s.fs.Chmod(cfg.TokenFile, 0o400); err != nil {
		return err
	}
	if err := s.fs.WriteFile(unitPath, []byte(renderUnit(cfg.FilePath)), 0o644); err != nil {
		return err
	}
	return s.chownRecursive(cfg.Paths.StateDir, uid, gid)
}

func (s *linuxService) EnableAndStart() error {
	if err := s.run("systemctl", "daemon-reload"); err != nil {
		return err
	}
	return s.run("systemctl", "enable", "--now", "lokiotel.service")
}

func (s *linuxService) Uninstall() error {
	if s.uid() != 0 {
		return fmt.Errorf("this command must run as root")
	}
	_ = s.run("systemctl", "disable", "--now", "lokiotel.service")
	if err := s.fs.Remove(unitPath); err != nil && !errorsIsNotExist(err) {
		return err
	}
	return s.run("systemctl", "daemon-reload")
}

func (s *linuxService) ProbeStatus() (Status, error) {
	out, err := s.runOutput("systemctl", "show", "lokiotel.service", "--property=LoadState,ActiveState,SubState,ActiveEnterTimestamp", "--value")
	if err != nil {
		return Status{Detail: "unknown"}, nil
	}
	parts := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(parts) < 4 {
		return Status{Detail: strings.TrimSpace(string(out))}, nil
	}
	installed := parts[0] != "not-found"
	active := parts[1] == "active"
	detail := "not installed"
	if installed {
		detail = fmt.Sprintf("%s (%s)", parts[1], parts[2])
		if parts[3] != "" {
			detail += " since " + parts[3]
		}
	}
	return Status{Installed: installed, Active: active, Detail: detail}, nil
}

func renderUnit(configPath string) string {
	return fmt.Sprintf(`[Unit]
Description=Loki OTel sidecar
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=lokiotel
Group=lokiotel
ExecStart=/usr/local/bin/lokiotel start --config %s
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
`, configPath)
}

func (s *linuxService) copySelf() error {
	src, err := s.executable()
	if err != nil {
		return err
	}
	srcData, err := s.fs.ReadFile(src)
	if err != nil {
		return err
	}
	if dstData, err := s.fs.ReadFile(binaryPath); err == nil && bytes.Equal(srcData, dstData) {
		return nil
	}
	return s.fs.WriteFile(binaryPath, srcData, 0o755)
}

func (s *linuxService) ensureUser() error {
	if _, err := s.runOutput("getent", "passwd", "lokiotel"); err == nil {
		return nil
	}
	if _, err := exec.LookPath("useradd"); err != nil {
		return fmt.Errorf("useradd is required to create the lokiotel system user")
	}
	shells := []string{"/usr/sbin/nologin", "/sbin/nologin", "/bin/false"}
	for _, shell := range shells {
		if err := s.run("useradd", "--system", "--home-dir", "/var/lib/lokiotel", "--shell", shell, "lokiotel"); err == nil {
			return nil
		}
	}
	return fmt.Errorf("failed to create lokiotel system user with useradd")
}

func (s *linuxService) chownRecursive(root string, uid, gid int) error {
	return s.fs.WalkDir(root, func(path string, _ iofs.FileInfo, err error) error {
		if err != nil {
			return err
		}
		return s.fs.Chown(path, uid, gid)
	})
}

func errorsIsNotExist(err error) bool {
	return err != nil && os.IsNotExist(err)
}
