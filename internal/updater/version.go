// SPDX-License-Identifier: Apache-2.0

package updater

import "strings"

// IsNewerVersion returns true if latest is strictly newer than current.
// Both must be valid semver with "v" prefix (e.g. "v0.3.7").
// Returns false for dev/snapshot builds or unparseable versions.
func IsNewerVersion(current, latest string) bool {
	if ShouldSkip(current) {
		return false
	}
	if ShouldSkip(latest) {
		return false
	}
	cv := parseSemver(current)
	lv := parseSemver(latest)
	if cv == nil || lv == nil {
		return false
	}
	return compareSemver(lv, cv) > 0
}

// ShouldSkip returns true if the version string indicates a dev/snapshot build.
func ShouldSkip(version string) bool {
	if version == "" || version == "dev" {
		return true
	}
	if strings.Contains(version, "snapshot") {
		return true
	}
	return false
}

type semver struct {
	major, minor, patch int
}

func parseSemver(v string) *semver {
	v = strings.TrimPrefix(v, "v")
	parts := strings.SplitN(v, ".", 3)
	if len(parts) != 3 {
		return nil
	}
	// Strip pre-release suffix from patch
	patchStr := parts[2]
	if idx := strings.IndexByte(patchStr, '-'); idx >= 0 {
		patchStr = patchStr[:idx]
	}
	sv := &semver{}
	for i, s := range []string{parts[0], parts[1], patchStr} {
		if s == "" {
			return nil
		}
		n := 0
		for _, c := range s {
			if c < '0' || c > '9' {
				return nil
			}
			n = n*10 + int(c-'0')
		}
		switch i {
		case 0:
			sv.major = n
		case 1:
			sv.minor = n
		case 2:
			sv.patch = n
		}
	}
	return sv
}

func compareSemver(a, b *semver) int {
	if a.major != b.major {
		return a.major - b.major
	}
	if a.minor != b.minor {
		return a.minor - b.minor
	}
	return a.patch - b.patch
}
