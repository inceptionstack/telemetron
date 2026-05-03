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
	marker := filepath.Join(fakeHome, markerFileRel)

	cases := []struct {
		name     string
		env      map[string]string
		files    map[string]bool // path -> exists
		homeErr  bool
		expected result
	}{
		{"unset", nil, nil, false, result{false, ""}},
		{"do_not_track=1", map[string]string{"DO_NOT_TRACK": "1"}, nil, false, result{true, "env:DO_NOT_TRACK"}},
		{"do_not_track=true", map[string]string{"DO_NOT_TRACK": "true"}, nil, false, result{true, "env:DO_NOT_TRACK"}},
		{"do_not_track=TRUE", map[string]string{"DO_NOT_TRACK": "TRUE"}, nil, false, result{true, "env:DO_NOT_TRACK"}},
		{"do_not_track=yes", map[string]string{"DO_NOT_TRACK": "yes"}, nil, false, result{true, "env:DO_NOT_TRACK"}},
		{"do_not_track=on", map[string]string{"DO_NOT_TRACK": "on"}, nil, false, result{true, "env:DO_NOT_TRACK"}},
		{"do_not_track=0_noop", map[string]string{"DO_NOT_TRACK": "0"}, nil, false, result{false, ""}},
		{"do_not_track=false_noop", map[string]string{"DO_NOT_TRACK": "false"}, nil, false, result{false, ""}},
		{"do_not_track=empty", map[string]string{"DO_NOT_TRACK": ""}, nil, false, result{false, ""}},
		{"do_not_track=whitespace", map[string]string{"DO_NOT_TRACK": "   "}, nil, false, result{false, ""}},

		// LOWKEY-style: CLAWTELLO_TELEMETRY=0 opts out.
		{"clawtello_telemetry=0", map[string]string{"CLAWTELLO_TELEMETRY": "0"}, nil, false, result{true, "env:CLAWTELLO_TELEMETRY"}},
		{"clawtello_telemetry=false", map[string]string{"CLAWTELLO_TELEMETRY": "false"}, nil, false, result{true, "env:CLAWTELLO_TELEMETRY"}},
		{"clawtello_telemetry=OFF", map[string]string{"CLAWTELLO_TELEMETRY": "OFF"}, nil, false, result{true, "env:CLAWTELLO_TELEMETRY"}},
		{"clawtello_telemetry=1_noop", map[string]string{"CLAWTELLO_TELEMETRY": "1"}, nil, false, result{false, ""}},
		{"clawtello_telemetry=empty_noop", map[string]string{"CLAWTELLO_TELEMETRY": ""}, nil, false, result{false, ""}},
		{"clawtello_telemetry=unset_noop", nil, nil, false, result{false, ""}},

		// CLAWTELLO_DISABLE is no longer honored (single tool-specific var).
		{"clawtello_disable_ignored", map[string]string{"CLAWTELLO_DISABLE": "1"}, nil, false, result{false, ""}},

		// Precedence.
		{"dnt_wins_over_telemetry", map[string]string{"DO_NOT_TRACK": "1", "CLAWTELLO_TELEMETRY": "0"}, nil, false, result{true, "env:DO_NOT_TRACK"}},

		// Marker file.
		{"marker_file_present", nil, map[string]bool{marker: true}, false, result{true, "file:" + marker}},
		{"marker_file_absent", nil, map[string]bool{marker: false}, false, result{false, ""}},
		{"marker_file_home_error", nil, map[string]bool{marker: true}, true, result{false, ""}},

		// Env wins over marker.
		{"env_wins_over_marker", map[string]string{"DO_NOT_TRACK": "1"}, map[string]bool{marker: true}, false, result{true, "env:DO_NOT_TRACK"}},
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
