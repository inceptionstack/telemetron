// SPDX-License-Identifier: Apache-2.0

package service

import (
	"errors"

	"github.com/inceptionstack/telemetron/internal/config"
)

var ErrUnsupported = errors.New("platform does not support daemon install")

type Status struct {
	Installed bool
	Active    bool
	Detail    string
}

type Service interface {
	Install(cfg config.Config, token string) error
	// InstallAs installs the service and runs the systemd unit as runAsUser.
	// When runAsUser is empty, platform implementations pick a sensible
	// default (Linux: $SUDO_USER when set, else the system "telemetron"
	// user). Non-Linux platforms may return ErrUnsupported.
	InstallAs(cfg config.Config, token, runAsUser string) error
	Uninstall() error
	EnableAndStart() error
	ProbeStatus() (Status, error)
}

func New() Service {
	return NewForInstance("")
}

// NewForInstance creates a service manager for a named instance.
// Empty instance = primary.
func NewForInstance(instance string) Service {
	return newServiceWithInstance(instance)
}
