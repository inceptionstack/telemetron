// SPDX-License-Identifier: Apache-2.0

// Package agentdetect resolves the agent-specific inputs that `telemetron
// setup` needs (mode, session directory, run-as user, tier) from the host
// filesystem and environment.
//
// Detectors never read configuration, never contact the network, and never
// make destructive changes. They return a Detection and let the caller
// decide how to act on ambiguity.
package agentdetect

import (
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

// Detection describes what a detector resolved about the host.
type Detection struct {
	// Mode is the collector mode (e.g. "openclaw") that matched.
	Mode string
	// SessionDir is the absolute path that the collector should scan.
	SessionDir string
	// RunAsUser is the OS user whose session files we should be able to
	// read. Empty when the caller should fall back to $SUDO_USER.
	RunAsUser string
	// AgentName is the directory-level agent slot that matched (e.g.
	// "main"). Used to label the default deployment id.
	AgentName string
	// Ambiguous is populated when more than one candidate matched and the
	// caller must either prompt or error.
	Ambiguous []Candidate
}

// Candidate is one possible resolution when detection is ambiguous.
type Candidate struct {
	AgentName  string
	SessionDir string
}

// HomeCandidate is a user-home-based candidate discovered from the
// standard OpenClaw layout.
type HomeCandidate struct {
	RunAsUser  string
	AgentName  string
	SessionDir string
}

// Options controls detection. A zero Options uses $SUDO_USER / current
// user and the standard filesystem layout.
type Options struct {
	// User forces detection to consider this user's $HOME. Empty means:
	// use $SUDO_USER if set (and not "root"), else the current user.
	User string
	// HomeDirOverride short-circuits home-dir lookup. Used by tests.
	HomeDirOverride string
	// FSRoot is prepended to detection paths. Used by tests.
	FSRoot string
}

// DetectOpenClaw looks for $HOME/.openclaw/agents/*/sessions on disk.
//
// Resolution:
//   - exactly one agent: return that.
//   - an agent named "main" exists: prefer "main".
//   - multiple agents and no "main": return Detection with Ambiguous
//     populated (Mode stays empty). Caller decides whether to prompt or
//     error.
//   - no agents found: return (Detection{}, nil). Caller decides whether
//     to fall back to a different detector or fail.
func DetectOpenClaw(opts Options) (Detection, error) {
	username, err := resolveUser(opts.User)
	if err != nil {
		return Detection{}, err
	}

	home := opts.HomeDirOverride
	if home == "" {
		home, err = lookupHomeDir(username)
		if err != nil {
			return Detection{}, err
		}
	}
	if home == "" {
		return Detection{}, nil
	}

	agentsDir := filepath.Join(opts.FSRoot, home, ".openclaw", "agents")
	entries, err := os.ReadDir(agentsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return Detection{}, nil
		}
		return Detection{}, fmt.Errorf("read %s: %w", agentsDir, err)
	}

	candidates := make([]Candidate, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		sessionDir := filepath.Join(agentsDir, entry.Name(), "sessions")
		if info, err := os.Stat(sessionDir); err == nil && info.IsDir() {
			candidates = append(candidates, Candidate{
				AgentName:  entry.Name(),
				SessionDir: sessionDir,
			})
		}
	}
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].AgentName < candidates[j].AgentName
	})

	if len(candidates) == 0 {
		return Detection{}, nil
	}

	// Prefer "main" if present.
	for _, c := range candidates {
		if c.AgentName == "main" {
			return Detection{
				Mode:       "openclaw",
				SessionDir: c.SessionDir,
				RunAsUser:  username,
				AgentName:  c.AgentName,
			}, nil
		}
	}

	if len(candidates) == 1 {
		return Detection{
			Mode:       "openclaw",
			SessionDir: candidates[0].SessionDir,
			RunAsUser:  username,
			AgentName:  candidates[0].AgentName,
		}, nil
	}

	// Ambiguous. Let the caller decide.
	return Detection{
		RunAsUser: username,
		Ambiguous: candidates,
	}, nil
}

func resolveUser(explicit string) (string, error) {
	if explicit != "" {
		return explicit, nil
	}
	if sudoUser := strings.TrimSpace(os.Getenv("SUDO_USER")); sudoUser != "" && sudoUser != "root" {
		return sudoUser, nil
	}
	u, err := user.Current()
	if err != nil {
		return "", err
	}
	return u.Username, nil
}

func lookupHomeDir(username string) (string, error) {
	u, err := user.Lookup(username)
	if err != nil {
		return "", fmt.Errorf("lookup user %q: %w", username, err)
	}
	return u.HomeDir, nil
}

// FindOpenClawMainCandidates scans plausible home directories for a
// single-agent OpenClaw layout rooted at `.openclaw/agents/main/sessions`.
// It is used by root-first installers that do not have $SUDO_USER.
func FindOpenClawMainCandidates(platform, fsRoot string) ([]HomeCandidate, error) {
	if platform == "" {
		platform = runtime.GOOS
	}

	homeDirs, err := plausibleHomeDirs(platform, fsRoot)
	if err != nil {
		return nil, err
	}

	candidates := make([]HomeCandidate, 0, len(homeDirs))
	for _, homeDir := range homeDirs {
		runAsUser, ok := runAsUserForHome(platform, homeDir)
		if !ok {
			continue
		}
		sessionDir := filepath.Join(homeDir, ".openclaw", "agents", "main", "sessions")
		info, err := os.Stat(filepath.Join(fsRoot, sessionDir))
		if err != nil || !info.IsDir() {
			continue
		}
		candidates = append(candidates, HomeCandidate{
			RunAsUser:  runAsUser,
			AgentName:  "main",
			SessionDir: sessionDir,
		})
	}

	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].RunAsUser == candidates[j].RunAsUser {
			return candidates[i].SessionDir < candidates[j].SessionDir
		}
		return candidates[i].RunAsUser < candidates[j].RunAsUser
	})
	return candidates, nil
}

func plausibleHomeDirs(platform, fsRoot string) ([]string, error) {
	var roots []string
	switch platform {
	case "darwin":
		roots = []string{"/Users"}
	default:
		roots = []string{"/home"}
	}

	homeDirs := make([]string, 0, 8)
	for _, root := range roots {
		entries, err := os.ReadDir(filepath.Join(fsRoot, root))
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, fmt.Errorf("read %s: %w", root, err)
		}
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			homeDirs = append(homeDirs, filepath.Join(root, entry.Name()))
		}
	}
	if platform != "darwin" {
		homeDirs = append(homeDirs, "/root")
	}
	sort.Strings(homeDirs)
	return homeDirs, nil
}

func runAsUserForHome(platform, homeDir string) (string, bool) {
	trimmed := strings.Trim(filepath.Clean(homeDir), string(filepath.Separator))
	if trimmed == "" {
		return "", false
	}
	parts := strings.Split(trimmed, string(filepath.Separator))
	if len(parts) == 1 && parts[0] == "root" {
		return "root", true
	}
	if platform == "darwin" && len(parts) == 2 && parts[0] == "Users" {
		return parts[1], true
	}
	if platform != "darwin" && len(parts) == 2 && parts[0] == "home" {
		return parts[1], true
	}
	return "", false
}
