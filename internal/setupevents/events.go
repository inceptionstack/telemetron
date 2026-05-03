// SPDX-License-Identifier: Apache-2.0

// Package setupevents defines the stable JSON event schema emitted by
// `telemetron setup --json`.
//
// Installers (notably the Loki bundle installer) treat this schema as a
// contract: adding new fields is allowed, but renaming, removing, or
// repurposing existing fields requires a schema version bump.
package setupevents

// SchemaVersion is the stable tag attached to every emitted event.
const SchemaVersion = "telemetron.setup.v1"

// Event names. One event per lifecycle phase, plus a final completion or
// failure envelope.
const (
	EventConfigResolved    = "config.resolved"
	EventAgentDetected     = "agent.detected"
	EventTokenLoaded       = "token.loaded"
	EventTokenWritten      = "token.written"
	EventServiceInstalled  = "service.installed"
	EventServiceStarted    = "service.started"
	EventHealthcheckPassed = "healthcheck.passed"
	EventSetupCompleted    = "setup.completed"
	EventSetupFailed       = "setup.failed"
)

// Error codes used by EventSetupFailed. Keep this list stable; installers
// switch on them.
const (
	ErrMissingRequiredInput = "missing_required_input"
	ErrAmbiguousAgent       = "ambiguous_agent"
	ErrTokenReadFailed      = "token_read_failed"
	ErrSystemdInstallFailed = "systemd_install_failed"
	ErrServiceStartFailed   = "service_start_failed"
	ErrHealthCheckFailed    = "health_check_failed"
	ErrPreconditionFailed   = "precondition_failed"
	ErrDetectionFailed      = "detection_failed"
	ErrInvalidConfig        = "invalid_config"
)

// ActionTaken values for EventSetupCompleted.
const (
	ActionInstalled = "installed"
	ActionUpdated   = "updated"
	ActionUnchanged = "unchanged"
)

// Event is the common envelope. Concrete events carry additional fields
// via EmitEvent / the writer helpers in setup.go.
type Event struct {
	Schema string `json:"schema"`
	Event  string `json:"event"`
	// Free-form fields merged by the emitter. Kept as a generic map so the
	// schema stays open for additive evolution.
	Fields map[string]any `json:"-"`
}
