// SPDX-License-Identifier: Apache-2.0

package updater

import "testing"

func TestIsNewerVersion(t *testing.T) {
	tests := []struct {
		current, latest string
		want            bool
	}{
		{"v0.3.6", "v0.3.7", true},
		{"v0.3.7", "v0.3.7", false},
		{"v0.3.7", "v0.3.6", false},
		{"v0.3.6", "v0.4.0", true},
		{"v0.3.6", "v1.0.0", true},
		{"v1.0.0", "v0.9.9", false},
		{"dev", "v0.3.7", false},
		{"v0.3.6-snapshot", "v0.3.7", false},
		{"v0.3.6", "dev", false},
		{"", "v0.3.7", false},
		{"v0.3.6", "", false},
	}
	for _, tt := range tests {
		got := IsNewerVersion(tt.current, tt.latest)
		if got != tt.want {
			t.Errorf("IsNewerVersion(%q, %q) = %v, want %v", tt.current, tt.latest, got, tt.want)
		}
	}
}

func TestShouldSkip(t *testing.T) {
	tests := []struct {
		version string
		want    bool
	}{
		{"dev", true},
		{"", true},
		{"0.3.7-snapshot", true},
		{"v0.3.7", false},
		{"0.3.7", false},
	}
	for _, tt := range tests {
		if got := ShouldSkip(tt.version); got != tt.want {
			t.Errorf("ShouldSkip(%q) = %v, want %v", tt.version, got, tt.want)
		}
	}
}

func TestAssetName(t *testing.T) {
	name := AssetName("v0.3.7")
	// Should strip the v prefix
	if name == "" || name[0] == 'v' {
		t.Errorf("AssetName should strip v prefix, got %q", name)
	}
	// Should contain version without v
	if !containsStr(name, "0.3.7") {
		t.Errorf("AssetName should contain 0.3.7, got %q", name)
	}
}

func containsStr(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(s) > 0 && containsSubstr(s, sub))
}

func containsSubstr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
