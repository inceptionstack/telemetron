// SPDX-License-Identifier: Apache-2.0

package telemetry

import "testing"

func TestIsDisabled(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		env     map[string]string
		want    bool
		wantVar string
	}{
		{"unset", map[string]string{}, false, ""},
		{"do_not_track=1", map[string]string{"DO_NOT_TRACK": "1"}, true, "DO_NOT_TRACK"},
		{"do_not_track=true", map[string]string{"DO_NOT_TRACK": "true"}, true, "DO_NOT_TRACK"},
		{"do_not_track=TRUE", map[string]string{"DO_NOT_TRACK": "TRUE"}, true, "DO_NOT_TRACK"},
		{"do_not_track=yes", map[string]string{"DO_NOT_TRACK": "yes"}, true, "DO_NOT_TRACK"},
		{"do_not_track=on", map[string]string{"DO_NOT_TRACK": "on"}, true, "DO_NOT_TRACK"},
		{"do_not_track=0", map[string]string{"DO_NOT_TRACK": "0"}, false, ""},
		{"do_not_track=false", map[string]string{"DO_NOT_TRACK": "false"}, false, ""},
		{"do_not_track=empty", map[string]string{"DO_NOT_TRACK": ""}, false, ""},
		{"do_not_track=whitespace", map[string]string{"DO_NOT_TRACK": "   "}, false, ""},
		{"clawtello_disable=1", map[string]string{"CLAWTELLO_DISABLE": "1"}, true, "CLAWTELLO_DISABLE"},
		{"dnt_wins_over_clawtello", map[string]string{"DO_NOT_TRACK": "1", "CLAWTELLO_DISABLE": "1"}, true, "DO_NOT_TRACK"},
		{"only_clawtello_set", map[string]string{"DO_NOT_TRACK": "", "CLAWTELLO_DISABLE": "yes"}, true, "CLAWTELLO_DISABLE"},
		{"padded_truthy", map[string]string{"DO_NOT_TRACK": "  1  "}, true, "DO_NOT_TRACK"},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			lookup := func(k string) string { return tc.env[k] }
			got, gotVar, _ := isDisabled(lookup)
			if got != tc.want {
				t.Fatalf("got disabled=%v, want %v (env=%v)", got, tc.want, tc.env)
			}
			if gotVar != tc.wantVar {
				t.Fatalf("got var=%q, want %q", gotVar, tc.wantVar)
			}
		})
	}
}
