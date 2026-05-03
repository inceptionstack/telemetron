//go:build linux

// SPDX-License-Identifier: Apache-2.0

package service

import (
	"os"
	"os/user"
	"path/filepath"
	"testing"

	"github.com/inceptionstack/telemetron/internal/config"
	"github.com/stretchr/testify/require"
)

type fakeFS struct {
	data     map[string][]byte
	walks    map[string][]string
	chowns   []string
	created  []string
	written  []string
	removed  []string
	chmodded []string
}

func (f *fakeFS) MkdirAll(path string, perm os.FileMode) error {
	f.created = append(f.created, path)
	return nil
}

func (f *fakeFS) ReadFile(path string) ([]byte, error) {
	data, ok := f.data[path]
	if !ok {
		return nil, os.ErrNotExist
	}
	return data, nil
}

func (f *fakeFS) WriteFile(path string, data []byte, perm os.FileMode) error {
	f.written = append(f.written, path)
	if f.data == nil {
		f.data = map[string][]byte{}
	}
	f.data[path] = data
	return nil
}

func (f *fakeFS) Remove(path string) error {
	f.removed = append(f.removed, path)
	return nil
}

func (f *fakeFS) Chown(path string, uid, gid int) error {
	f.chowns = append(f.chowns, path)
	return nil
}

func (f *fakeFS) Chmod(path string, mode os.FileMode) error {
	f.chmodded = append(f.chmodded, path)
	return nil
}

func (f *fakeFS) WalkDir(root string, fn filepath.WalkFunc) error {
	for _, path := range f.walks[root] {
		if err := fn(path, nil, nil); err != nil {
			return err
		}
	}
	return nil
}

func TestRenderUnitIncludesRequiredLines(t *testing.T) {
	unit := renderUnit("/etc/telemetron/config.yaml", "ec2-user", "ec2-user")
	require.Contains(t, unit, "User=ec2-user")
	require.Contains(t, unit, "Group=ec2-user")
	require.Contains(t, unit, "ExecStart=/usr/local/bin/telemetron start --config /etc/telemetron/config.yaml")
	require.Contains(t, unit, "ReadWritePaths=/var/lib/telemetron")
	require.Contains(t, unit, "ProtectSystem=strict")
}

func TestResolveRunAsUserPrefersExplicit(t *testing.T) {
	s := &linuxService{}
	t.Setenv("SUDO_USER", "roy")
	u, err := s.resolveRunAsUser("alice")
	require.NoError(t, err)
	require.Equal(t, "alice", u)
}

func TestResolveRunAsUserFallsBackToSudoUser(t *testing.T) {
	s := &linuxService{}
	t.Setenv("SUDO_USER", "roy")
	u, err := s.resolveRunAsUser("")
	require.NoError(t, err)
	require.Equal(t, "roy", u)
}

func TestResolveRunAsUserFallsBackToSystemUser(t *testing.T) {
	s := &linuxService{}
	t.Setenv("SUDO_USER", "")
	u, err := s.resolveRunAsUser("")
	require.NoError(t, err)
	require.Equal(t, systemUser, u)
}

func TestResolveRunAsUserIgnoresSudoUserRoot(t *testing.T) {
	s := &linuxService{}
	t.Setenv("SUDO_USER", "root")
	u, err := s.resolveRunAsUser("")
	require.NoError(t, err)
	require.Equal(t, systemUser, u)
}

func TestInstallChownsTokenAndStateDir(t *testing.T) {
	t.Setenv("SUDO_USER", "") // force fallback to system "telemetron" user
	fs := &fakeFS{
		data: map[string][]byte{
			"/tmp/telemetron": []byte("bin"),
		},
		walks: map[string][]string{
			"/var/lib/telemetron": {
				"/var/lib/telemetron",
				"/var/lib/telemetron/existing.json",
			},
		},
	}
	svc := &linuxService{
		fs: fs,
		run: func(name string, args ...string) error {
			return nil
		},
		runOutput: func(name string, args ...string) ([]byte, error) {
			return []byte("telemetron:x:1001:1001::/var/lib/telemetron:/usr/sbin/nologin"), nil
		},
		lookupUser: func(username string) (*user.User, error) {
			return &user.User{Uid: "1001", Gid: "1001", Username: username}, nil
		},
		lookupGroup: func(gid string) (*user.Group, error) {
			return &user.Group{Gid: gid, Name: "telemetron"}, nil
		},
		executable: func() (string, error) { return "/tmp/telemetron", nil },
		uid:        func() int { return 0 },
	}
	cfg := config.Config{
		Mode:      "testmode",
		Endpoint:  "https://example.test/v1/metrics",
		TokenFile: "/etc/telemetron/token",
		FilePath:  "/etc/telemetron/config.yaml",
		Paths: config.Paths{
			StateDir: "/var/lib/telemetron",
		},
		Collectors: map[string]any{
			"testmode": map[string]any{"session_dir": "/tmp/sessions"},
		},
	}

	err := svc.Install(cfg, "secret")
	require.NoError(t, err)
	require.Contains(t, fs.chowns, "/etc/telemetron/token")
	require.Contains(t, fs.chowns, "/var/lib/telemetron")
	require.Contains(t, fs.chowns, "/var/lib/telemetron/existing.json")
	// Unit must be pinned to the system user when SUDO_USER is unset.
	unit := string(fs.data["/etc/systemd/system/telemetron.service"])
	require.Contains(t, unit, "User=telemetron")
}

func TestInstallAsUsesSudoUserByDefault(t *testing.T) {
	t.Setenv("SUDO_USER", "roy")
	fs := &fakeFS{
		data:  map[string][]byte{"/tmp/telemetron": []byte("bin")},
		walks: map[string][]string{"/var/lib/telemetron": {"/var/lib/telemetron"}},
	}
	lookupCalls := []string{}
	svc := &linuxService{
		fs: fs,
		run: func(name string, args ...string) error {
			t.Fatalf("unexpected command when run-as user already exists: %s %v", name, args)
			return nil
		},
		runOutput: func(name string, args ...string) ([]byte, error) {
			return nil, nil
		},
		lookupUser: func(username string) (*user.User, error) {
			lookupCalls = append(lookupCalls, username)
			return &user.User{Uid: "1000", Gid: "1000", Username: username}, nil
		},
		lookupGroup: func(gid string) (*user.Group, error) {
			return &user.Group{Gid: gid, Name: "roy"}, nil
		},
		executable: func() (string, error) { return "/tmp/telemetron", nil },
		uid:        func() int { return 0 },
	}
	cfg := config.Config{
		Mode:       "testmode",
		Endpoint:   "https://example.test/v1/metrics",
		TokenFile:  "/etc/telemetron/token",
		FilePath:   "/etc/telemetron/config.yaml",
		Paths:      config.Paths{StateDir: "/var/lib/telemetron"},
		Collectors: map[string]any{"testmode": map[string]any{"session_dir": "/tmp/s"}},
	}

	require.NoError(t, svc.Install(cfg, "secret"))

	unit := string(fs.data["/etc/systemd/system/telemetron.service"])
	require.Contains(t, unit, "User=roy")
	require.Contains(t, unit, "Group=roy")
	require.Contains(t, lookupCalls, "roy")
}

func TestInstallAsExplicitOverride(t *testing.T) {
	t.Setenv("SUDO_USER", "roy")
	fs := &fakeFS{
		data:  map[string][]byte{"/tmp/telemetron": []byte("bin")},
		walks: map[string][]string{"/var/lib/telemetron": {"/var/lib/telemetron"}},
	}
	svc := &linuxService{
		fs:        fs,
		run:       func(name string, args ...string) error { return nil },
		runOutput: func(name string, args ...string) ([]byte, error) { return nil, nil },
		lookupUser: func(username string) (*user.User, error) {
			return &user.User{Uid: "1234", Gid: "1234", Username: username}, nil
		},
		lookupGroup: func(gid string) (*user.Group, error) {
			return &user.Group{Gid: gid, Name: "alice"}, nil
		},
		executable: func() (string, error) { return "/tmp/telemetron", nil },
		uid:        func() int { return 0 },
	}
	cfg := config.Config{
		Mode:       "testmode",
		Endpoint:   "https://example.test/v1/metrics",
		TokenFile:  "/etc/telemetron/token",
		FilePath:   "/etc/telemetron/config.yaml",
		Paths:      config.Paths{StateDir: "/var/lib/telemetron"},
		Collectors: map[string]any{"testmode": map[string]any{"session_dir": "/tmp/s"}},
	}

	require.NoError(t, svc.InstallAs(cfg, "secret", "alice"))

	unit := string(fs.data["/etc/systemd/system/telemetron.service"])
	require.Contains(t, unit, "User=alice")
	require.Contains(t, unit, "Group=alice")
}
