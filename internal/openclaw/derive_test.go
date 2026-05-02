// SPDX-License-Identifier: Apache-2.0

package openclaw

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDeriveModelFamily(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		provider string
		model    string
		expected string
	}{
		{"bedrock wins", "amazon-bedrock", "claude-3", "bedrock"},
		{"openai model", "", "gpt-4.1", "openai"},
		{"anthropic model", "", "claude-opus-4", "anthropic"},
		{"gemini provider", "gemini", "", "gemini"},
		{"unknown", "", "", "unknown"},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.expected, DeriveModelFamily(tt.provider, tt.model))
		})
	}
}

func TestDeriveToolClass(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		tool     string
		expected string
	}{
		{"mapped file", "read", "file"},
		{"mapped agent", "sessions_spawn", "agent"},
		{"search heuristic", "semantic_search", "search"},
		{"http heuristic", "web_fetch_page", "http"},
		{"fallback", "mystery_tool", "other"},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.expected, DeriveToolClass(tt.tool))
		})
	}
}

func TestDeriveSessionType(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		path      string
		cwd       string
		firstText string
		expected  string
	}{
		{"main path", "/home/tester/.openclaw/agents/main/sessions/a.jsonl", "", "", "main"},
		{"subagent text", "/tmp/s.jsonl", "", "[Subagent Context] do work", "subagent"},
		{"heartbeat path", "/tmp/heartbeat-session.jsonl", "", "", "heartbeat"},
		{"cron path", "/tmp/cron-run.jsonl", "", "", "cron"},
		{"ephemeral cwd", "/tmp/s.jsonl", "/tmp/workspace", "", "ephemeral"},
		{"unknown", "/tmp/s.jsonl", "/var/data", "", "unknown"},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.expected, DeriveSessionType(tt.path, tt.cwd, tt.firstText))
		})
	}
}

func TestDeriveErrorType(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"prompt", "openclaw:prompt-error", "prompt"},
		{"auth", "AuthFailure", "auth"},
		{"quota", "rate-limit-error", "quota"},
		{"config", "config-error", "config"},
		{"transient", "transient-db-error", "transient"},
		{"permanent", "permanent-error", "permanent"},
		{"unknown", "mystery", "unknown"},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.expected, DeriveErrorType(tt.input))
		})
	}
}
