//go:build linux

// SPDX-License-Identifier: Apache-2.0

package service

import (
	"errors"
	"os"
	"os/user"
	"path/filepath"
	"testing"
	"time"

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
	renames  []string
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
	if f.data != nil {
		delete(f.data, path)
	}
	return nil
}

func (f *fakeFS) Rename(oldpath, newpath string) error {
	if f.data == nil {
		f.data = map[string][]byte{}
	}
	data, ok := f.data[oldpath]
	if !ok {
		return os.ErrNotExist
	}
	f.data[newpath] = data
	delete(f.data, oldpath)
	f.renames = append(f.renames, oldpath+" -> "+newpath)
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
	unit := renderUnit("/etc/telemetron/config.yaml", "ec2-user", "ec2-user", "/usr/local/bin/telemetron", "")
	require.Contains(t, unit, "User=ec2-user")
	require.Contains(t, unit, "Group=ec2-user")
	require.Contains(t, unit, "ExecStart=/usr/local/bin/telemetron start --config /etc/telemetron/config.yaml")
	require.Contains(t, unit, "ReadWritePaths=/var/lib/telemetron")
	require.Contains(t, unit, "ProtectSystem=strict")
}

func TestRenderUnitSecondaryIncludesPartOf(t *testing.T) {
	unit := renderUnit("/etc/telemetron/config-roundhouse.yaml", "ec2-user", "ec2-user", "/usr/local/bin/telemetron", "roundhouse")
	require.Contains(t, unit, "PartOf=telemetron.service")
	require.Contains(t, unit, "After=telemetron.service")
	require.Contains(t, unit, "Description=telemetron OTLP metrics sidecar (roundhouse)")
	require.Contains(t, unit, "ExecStart=/usr/local/bin/telemetron start --config /etc/telemetron/config-roundhouse.yaml")
}

func TestRenderUnitPrimaryNoPartOf(t *testing.T) {
	unit := renderUnit("/etc/telemetron/config.yaml", "ec2-user", "ec2-user", "/usr/local/bin/telemetron", "")
	require.NotContains(t, unit, "PartOf")
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
	// Token file must not gain a trailing newline — it is an HTTP
	// header value, not a POSIX text file. A trailing \n tripped a
	// production authorizer regex in 2026-05-03 (incident fix 64247a0).
	require.Equal(t, []byte("secret"), fs.data["/etc/telemetron/token"],
		"InstallAs must write the token byte-for-byte with no trailing newline")
	// Unit must be pinned to the system user when SUDO_USER is unset.
	unit := string(fs.data["/etc/systemd/system/telemetron.service"])
	require.Contains(t, unit, "User=telemetron")
}

// TestCopySelfUsesTempFileAndRename guards the ETXTBSY fix (2026-05-04).
// When a previous telemetron service is still running, the installer
// cannot WriteFile directly onto /usr/local/bin/telemetron — Linux
// returns "text file busy". The fix writes to a sibling temp file and
// atomically renames it into place, which is safe because the old
// process's mmap references the inode, not the directory entry.
//
// This test asserts the two invariants that make the fix correct:
//  1. The actual write target is NOT /usr/local/bin/telemetron
//  2. A Rename(tmp -> /usr/local/bin/telemetron) follows the write
func TestCopySelfUsesTempFileAndRename(t *testing.T) {
	t.Setenv("SUDO_USER", "")
	fs := &fakeFS{
		data: map[string][]byte{
			"/tmp/telemetron":           []byte("new-bin"),
			"/usr/local/bin/telemetron": []byte("old-bin"),
		},
		walks: map[string][]string{
			"/var/lib/telemetron": {"/var/lib/telemetron"},
		},
	}
	svc := &linuxService{
		fs:        fs,
		run:       func(string, ...string) error { return nil },
		runOutput: func(string, ...string) ([]byte, error) { return []byte("telemetron:x:1001:1001::/var/lib/telemetron:/usr/sbin/nologin"), nil },
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
		Mode:       "testmode",
		Endpoint:   "https://example.test/v1/metrics",
		TokenFile:  "/etc/telemetron/token",
		FilePath:   "/etc/telemetron/config.yaml",
		Paths:      config.Paths{StateDir: "/var/lib/telemetron"},
		Collectors: map[string]any{"testmode": map[string]any{"session_dir": "/tmp/sessions"}},
	}

	require.NoError(t, svc.Install(cfg, "secret"))

	// Invariant 1: the binary must have been written to a sibling
	// temp path, not directly to the final path. Writing to the
	// final path directly would trip ETXTBSY on a real host.
	require.NotContains(t, fs.written, "/usr/local/bin/telemetron",
		"copySelf must not WriteFile directly onto the running binary")
	var tmpWrite string
	for _, w := range fs.written {
		if filepath.Dir(w) == "/usr/local/bin" && w != "/usr/local/bin/telemetron" {
			tmpWrite = w
			break
		}
	}
	require.NotEmpty(t, tmpWrite, "expected a temp-file write under /usr/local/bin, got %v", fs.written)

	// Invariant 2: a Rename(tmpWrite -> /usr/local/bin/telemetron) must
	// have followed, so the swap is atomic on the same filesystem.
	require.Contains(t, fs.renames, tmpWrite+" -> /usr/local/bin/telemetron",
		"copySelf must Rename the temp binary into the final path")

	// And the final binary contents must match the source.
	require.Equal(t, []byte("new-bin"), fs.data["/usr/local/bin/telemetron"])
}

func TestCopySelfSkipsWhenUnchanged(t *testing.T) {
	// Re-installing the same binary must be a no-op: no temp write,
	// no rename. Otherwise every `telemetron setup` call would burn
	// a brief window where the binary is swapped underneath running
	// processes for no reason.
	t.Setenv("SUDO_USER", "")
	fs := &fakeFS{
		data: map[string][]byte{
			"/tmp/telemetron":           []byte("same-bin"),
			"/usr/local/bin/telemetron": []byte("same-bin"),
		},
		walks: map[string][]string{"/var/lib/telemetron": {"/var/lib/telemetron"}},
	}
	svc := &linuxService{
		fs:        fs,
		run:       func(string, ...string) error { return nil },
		runOutput: func(string, ...string) ([]byte, error) { return []byte("telemetron:x:1001:1001::/var/lib/telemetron:/usr/sbin/nologin"), nil },
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
		Mode:       "testmode",
		Endpoint:   "https://example.test/v1/metrics",
		TokenFile:  "/etc/telemetron/token",
		FilePath:   "/etc/telemetron/config.yaml",
		Paths:      config.Paths{StateDir: "/var/lib/telemetron"},
		Collectors: map[string]any{"testmode": map[string]any{"session_dir": "/tmp/sessions"}},
	}

	require.NoError(t, svc.Install(cfg, "secret"))
	for _, r := range fs.renames {
		require.NotContains(t, r, "/usr/local/bin/telemetron",
			"copySelf must skip the temp+rename dance when bytes already match")
	}
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

func TestSetupPreconditionDetectsSystemd(t *testing.T) {
	prevStat := systemdStat
	prevLookPath := systemdLookPath
	prevRunOutput := systemdRunOutput
	prevReadInit := readInitProcess
	t.Cleanup(func() {
		systemdStat = prevStat
		systemdLookPath = prevLookPath
		systemdRunOutput = prevRunOutput
		readInitProcess = prevReadInit
	})

	systemdStat = func(path string) (os.FileInfo, error) {
		return fakeFileInfo{dir: true}, nil
	}
	systemdLookPath = func(file string) (string, error) { return "", errors.New("unexpected") }
	systemdRunOutput = func(name string, args ...string) ([]byte, error) {
		return nil, errors.New("unexpected")
	}

	require.NoError(t, SetupPrecondition())
}

func TestSetupPreconditionRejectsNonSystemdLinux(t *testing.T) {
	prevStat := systemdStat
	prevLookPath := systemdLookPath
	prevRunOutput := systemdRunOutput
	prevReadInit := readInitProcess
	t.Cleanup(func() {
		systemdStat = prevStat
		systemdLookPath = prevLookPath
		systemdRunOutput = prevRunOutput
		readInitProcess = prevReadInit
	})

	systemdStat = func(path string) (os.FileInfo, error) { return nil, os.ErrNotExist }
	systemdLookPath = func(file string) (string, error) { return "", errors.New("missing") }
	systemdRunOutput = func(name string, args ...string) ([]byte, error) {
		return nil, errors.New("unexpected")
	}
	readInitProcess = func(path string) ([]byte, error) { return []byte("bash\n"), nil }

	err := SetupPrecondition()
	require.EqualError(t, err, "telemetron setup requires systemd; detected init: bash. Use 'telemetron install' + manual service management.")
}

type fakeFileInfo struct {
	dir bool
}

func (f fakeFileInfo) Name() string       { return "systemd" }
func (f fakeFileInfo) Size() int64        { return 0 }
func (f fakeFileInfo) Mode() os.FileMode  { return 0o755 }
func (f fakeFileInfo) ModTime() time.Time { return time.Time{} }
func (f fakeFileInfo) IsDir() bool        { return f.dir }
func (f fakeFileInfo) Sys() any           { return nil }
