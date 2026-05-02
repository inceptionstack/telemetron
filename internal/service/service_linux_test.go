//go:build linux

package service

import (
	"os"
	"os/user"
	"path/filepath"
	"testing"

	"github.com/inceptionstack/loki-otl/internal/config"
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
	unit := renderUnit("/etc/lokiotel/config.yaml")
	require.Contains(t, unit, "User=lokiotel")
	require.Contains(t, unit, "ExecStart=/usr/local/bin/lokiotel start --config /etc/lokiotel/config.yaml")
	require.Contains(t, unit, "ReadWritePaths=/var/lib/lokiotel")
	require.Contains(t, unit, "ProtectSystem=strict")
}

func TestInstallChownsTokenAndStateDir(t *testing.T) {
	fs := &fakeFS{
		data: map[string][]byte{
			"/tmp/lokiotel": []byte("bin"),
		},
		walks: map[string][]string{
			"/var/lib/lokiotel": {
				"/var/lib/lokiotel",
				"/var/lib/lokiotel/existing.json",
			},
		},
	}
	svc := &linuxService{
		fs: fs,
		run: func(name string, args ...string) error {
			return nil
		},
		runOutput: func(name string, args ...string) ([]byte, error) {
			return []byte("lokiotel:x:1001:1001::/var/lib/lokiotel:/usr/sbin/nologin"), nil
		},
		lookupUser: func(username string) (*user.User, error) {
			return &user.User{Uid: "1001", Gid: "1001"}, nil
		},
		executable: func() (string, error) { return "/tmp/lokiotel", nil },
		uid:        func() int { return 0 },
	}
	cfg := config.Config{
		Mode:      "testmode",
		Endpoint:  "https://example.test/v1/metrics",
		TokenFile: "/etc/lokiotel/token",
		FilePath:  "/etc/lokiotel/config.yaml",
		Paths: config.Paths{
			StateDir: "/var/lib/lokiotel",
		},
		Collectors: map[string]any{
			"testmode": map[string]any{"session_dir": "/tmp/sessions"},
		},
	}

	err := svc.Install(cfg, "secret")
	require.NoError(t, err)
	require.Contains(t, fs.chowns, "/etc/lokiotel/token")
	require.Contains(t, fs.chowns, "/var/lib/lokiotel")
	require.Contains(t, fs.chowns, "/var/lib/lokiotel/existing.json")
}
