// SPDX-License-Identifier: Apache-2.0

package telemetry

import (
	"path/filepath"
	"testing"
)

func TestIsDisabled(t *testing.T) {
	t.Parallel()

	type result struct {
		disabled bool
		source   string
	}

	fakeHome := "/fake/home"
	clawMarker := filepath.Join(fakeHome, ".clawtello/telemetry-off")
	lowkeyMarker := filepath.Join(fakeHome, ".lowkey/telemetry-off")

	cases := []struct {
		name     string
		env      map[string]string
		files    map[string]bool
		homeErr  bool
		expected result
	}{
		{"unset", nil, nil, false, result{false, ""}},

		// DO_NOT_TRACK — truthy wins.
		{"do_not_track=1", map[string]string{"DO_NOT_TRACK": "1"}, nil, false, result{true, "env:DO_NOT_TRACK"}},
		{"do_not_track=true", map[string]string{"DO_NOT_TRACK": "true"}, nil, false, result{true, "env:DO_NOT_TRACK"}},
		{"do_not_track=TRUE", map[string]string{"DO_NOT_TRACK": "TRUE"}, nil, false, result{true, "env:DO_NOT_TRACK"}},
		{"do_not_track=yes", map[string]string{"DO_NOT_TRACK": "yes"}, nil, false, result{true, "env:DO_NOT_TRACK"}},
		{"do_not_track=on", map[string]string{"DO_NOT_TRACK": "on"}, nil, false, result{true, "env:DO_NOT_TRACK"}},
		{"do_not_track=0_noop", map[string]string{"DO_NOT_TRACK": "0"}, nil, false, result{false, ""}},
		{"do_not_track=false_noop", map[string]string{"DO_NOT_TRACK": "false"}, nil, false, result{false, ""}},
		{"do_not_track=empty", map[string]string{"DO_NOT_TRACK": ""}, nil, false, result{false, ""}},
		{"do_not_track=whitespace", map[string]string{"DO_NOT_TRACK": "   "}, nil, false, result{false, ""}},

		// CLAWTELLO_TELEMETRY — falsy wins.
		{"clawtello_telemetry=0", map[string]string{"CLAWTELLO_TELEMETRY": "0"}, nil, false, result{true, "env:CLAWTELLO_TELEMETRY"}},
		{"clawtello_telemetry=false", map[string]string{"CLAWTELLO_TELEMETRY": "false"}, nil, false, result{true, "env:CLAWTELLO_TELEMETRY"}},
		{"clawtello_telemetry=OFF", map[string]string{"CLAWTELLO_TELEMETRY": "OFF"}, nil, false, result{true, "env:CLAWTELLO_TELEMETRY"}},
		{"clawtello_telemetry=1_noop", map[string]string{"CLAWTELLO_TELEMETRY": "1"}, nil, false, result{false, ""}},
		{"clawtello_telemetry=empty_noop", map[string]string{"CLAWTELLO_TELEMETRY": ""}, nil, false, result{false, ""}},

		// LOWKEY_TELEMETRY — inherited from lowkey installer.
		{"lowkey_telemetry=0", map[string]string{"LOWKEY_TELEMETRY": "0"}, nil, false, result{true, "env:LOWKEY_TELEMETRY"}},
		{"lowkey_telemetry=false", map[string]string{"LOWKEY_TELEMETRY": "false"}, nil, false, result{true, "env:LOWKEY_TELEMETRY"}},
		{"lowkey_telemetry=off", map[string]string{"LOWKEY_TELEMETRY": "off"}, nil, false, result{true, "env:LOWKEY_TELEMETRY"}},
		{"lowkey_telemetry=1_noop", map[string]string{"LOWKEY_TELEMETRY": "1"}, nil, false, result{false, ""}},
		{"lowkey_telemetry=empty_noop", map[string]string{"LOWKEY_TELEMETRY": ""}, nil, false, result{false, ""}},

		// CLAWTELLO_DISABLE is not honored anymore.
		{"clawtello_disable_ignored", map[string]string{"CLAWTELLO_DISABLE": "1"}, nil, false, result{false, ""}},

		// Precedence among env vars.
		{"dnt_wins_over_clawtello", map[string]string{"DO_NOT_TRACK": "1", "CLAWTELLO_TELEMETRY": "0"}, nil, false, result{true, "env:DO_NOT_TRACK"}},
		{"clawtello_wins_over_lowkey", map[string]string{"CLAWTELLO_TELEMETRY": "0", "LOWKEY_TELEMETRY": "0"}, nil, false, result{true, "env:CLAWTELLO_TELEMETRY"}},

		// Marker files.
		{"clawtello_marker_present", nil, map[string]bool{clawMarker: true}, false, result{true, "file:" + clawMarker}},
		{"lowkey_marker_present", nil, map[string]bool{lowkeyMarker: true}, false, result{true, "file:" + lowkeyMarker}},
		{"clawtello_marker_wins_over_lowkey", nil, map[string]bool{clawMarker: true, lowkeyMarker: true}, false, result{true, "file:" + clawMarker}},
		{"no_marker", nil, map[string]bool{clawMarker: false, lowkeyMarker: false}, false, result{false, ""}},
		{"marker_ignored_when_home_errs", nil, map[string]bool{clawMarker: true}, true, result{false, ""}},

		// Env wins over markers.
		{"env_wins_over_markers", map[string]string{"DO_NOT_TRACK": "1"}, map[string]bool{clawMarker: true, lowkeyMarker: true}, false, result{true, "env:DO_NOT_TRACK"}},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			getenv := func(k string) string { return tc.env[k] }
			exists := func(p string) bool { return tc.files[p] }
			userHome := func() (string, error) {
				if tc.homeErr {
					return "", errTestHome
				}
				return fakeHome, nil
			}
			got, source, _ := isDisabled(getenv, exists, userHome)
			if got != tc.expected.disabled {
				t.Fatalf("disabled got %v want %v (env=%v files=%v)", got, tc.expected.disabled, tc.env, tc.files)
			}
			if source != tc.expected.source {
				t.Fatalf("source got %q want %q", source, tc.expected.source)
			}
		})
	}
}

type testErr string

func (e testErr) Error() string { return string(e) }

var errTestHome testErr = "test: no home"
