// SPDX-License-Identifier: Apache-2.0

package machineid

import (
	"regexp"
	"testing"
)

func TestComputeFromFrozenVectors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		etcMachineID string
		hostname     string
		want         string
	}{
		{
			name:         "ascii",
			etcMachineID: "abc123",
			hostname:     "foo",
			want:         "sha256:250d65d166b80109d2656e9435fcd5d008027f507271321e3dcd6362ce141b81",
		},
		{
			name:         "empty machine id",
			etcMachineID: "",
			hostname:     "bar",
			want:         "sha256:3509765e2d0dc4e4eaca65417138d20c8ff46041cbfa0062dea151605bd23056",
		},
		{
			name:         "unicode hostname",
			etcMachineID: "abc123",
			hostname:     "föö",
			want:         "sha256:ffea51f860cb742657350e3c18d105443953c8833ae002b182891b6e2d428001",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := ComputeFrom(tt.etcMachineID, tt.hostname); got != tt.want {
				t.Fatalf("ComputeFrom(%q, %q) = %q, want %q", tt.etcMachineID, tt.hostname, got, tt.want)
			}
		})
	}
}

func TestComputeReturnsStableHashedMachineIDOnHost(t *testing.T) {
	first, err := Compute()
	if err != nil {
		t.Fatal(err)
	}
	second, err := Compute()
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("expected stable machine id, got %q and %q", first, second)
	}
	if !regexp.MustCompile(`^sha256:[0-9a-f]{64}$`).MatchString(first) {
		t.Fatalf("unexpected machine id format: %q", first)
	}
}
