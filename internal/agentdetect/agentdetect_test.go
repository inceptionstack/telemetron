// SPDX-License-Identifier: Apache-2.0

package agentdetect

import (
	"os"
	"os/user"
	"path/filepath"
	"testing"
)

func TestDetectOpenClaw(t *testing.T) {
	t.Run("no agents found", func(t *testing.T) {
		tmp := t.TempDir()
		u, _ := user.Current()
		d, err := DetectOpenClaw(Options{
			User:            u.Username,
			HomeDirOverride: tmp,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if d.Mode != "" || d.SessionDir != "" || len(d.Ambiguous) != 0 {
			t.Fatalf("expected empty detection, got %+v", d)
		}
	})

	t.Run("single agent", func(t *testing.T) {
		tmp := t.TempDir()
		sess := filepath.Join(tmp, ".openclaw", "agents", "main", "sessions")
		mustMkdir(t, sess)
		u, _ := user.Current()
		d, err := DetectOpenClaw(Options{
			User:            u.Username,
			HomeDirOverride: tmp,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if d.Mode != "openclaw" {
			t.Errorf("mode: want openclaw, got %q", d.Mode)
		}
		if d.SessionDir != sess {
			t.Errorf("session_dir: want %q, got %q", sess, d.SessionDir)
		}
		if d.AgentName != "main" {
			t.Errorf("agent_name: want main, got %q", d.AgentName)
		}
	})

	t.Run("main preferred when multiple", func(t *testing.T) {
		tmp := t.TempDir()
		mustMkdir(t, filepath.Join(tmp, ".openclaw", "agents", "other", "sessions"))
		mustMkdir(t, filepath.Join(tmp, ".openclaw", "agents", "main", "sessions"))
		u, _ := user.Current()
		d, err := DetectOpenClaw(Options{
			User:            u.Username,
			HomeDirOverride: tmp,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if d.AgentName != "main" {
			t.Errorf("want main preferred, got %q", d.AgentName)
		}
		if len(d.Ambiguous) != 0 {
			t.Errorf("ambiguous should be empty when main exists")
		}
	})

	t.Run("multiple agents without main is ambiguous", func(t *testing.T) {
		tmp := t.TempDir()
		mustMkdir(t, filepath.Join(tmp, ".openclaw", "agents", "alpha", "sessions"))
		mustMkdir(t, filepath.Join(tmp, ".openclaw", "agents", "beta", "sessions"))
		u, _ := user.Current()
		d, err := DetectOpenClaw(Options{
			User:            u.Username,
			HomeDirOverride: tmp,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if d.Mode != "" {
			t.Errorf("mode must be empty when ambiguous, got %q", d.Mode)
		}
		if len(d.Ambiguous) != 2 {
			t.Fatalf("want 2 candidates, got %d", len(d.Ambiguous))
		}
		if d.Ambiguous[0].AgentName != "alpha" || d.Ambiguous[1].AgentName != "beta" {
			t.Errorf("candidates not sorted: %+v", d.Ambiguous)
		}
	})

	t.Run("agents dir without sessions subdir is skipped", func(t *testing.T) {
		tmp := t.TempDir()
		mustMkdir(t, filepath.Join(tmp, ".openclaw", "agents", "half"))
		u, _ := user.Current()
		d, err := DetectOpenClaw(Options{
			User:            u.Username,
			HomeDirOverride: tmp,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if d.Mode != "" {
			t.Errorf("should not match when sessions subdir missing")
		}
	})
}

func TestFindOpenClawMainCandidates(t *testing.T) {
	t.Run("linux homes and root are discovered", func(t *testing.T) {
		tmp := t.TempDir()
		mustMkdir(t, filepath.Join(tmp, "home", "alice", ".openclaw", "agents", "main", "sessions"))
		mustMkdir(t, filepath.Join(tmp, "root", ".openclaw", "agents", "main", "sessions"))
		mustMkdir(t, filepath.Join(tmp, "home", "bob", ".openclaw", "agents", "other", "sessions"))

		candidates, err := FindOpenClawMainCandidates("linux", tmp)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(candidates) != 2 {
			t.Fatalf("want 2 candidates, got %d", len(candidates))
		}
		if candidates[0].RunAsUser != "alice" || candidates[0].SessionDir != "/home/alice/.openclaw/agents/main/sessions" {
			t.Fatalf("unexpected first candidate: %+v", candidates[0])
		}
		if candidates[1].RunAsUser != "root" || candidates[1].SessionDir != "/root/.openclaw/agents/main/sessions" {
			t.Fatalf("unexpected second candidate: %+v", candidates[1])
		}
	})

	t.Run("darwin users are discovered", func(t *testing.T) {
		tmp := t.TempDir()
		mustMkdir(t, filepath.Join(tmp, "Users", "roy", ".openclaw", "agents", "main", "sessions"))

		candidates, err := FindOpenClawMainCandidates("darwin", tmp)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(candidates) != 1 {
			t.Fatalf("want 1 candidate, got %d", len(candidates))
		}
		if candidates[0].RunAsUser != "roy" {
			t.Fatalf("want run-as roy, got %+v", candidates[0])
		}
	})
}

func mustMkdir(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
}
