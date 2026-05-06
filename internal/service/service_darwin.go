//go:build darwin

// SPDX-License-Identifier: Apache-2.0

package service

import "github.com/inceptionstack/telemetron/internal/config"

type darwinService struct{}

func newServiceWithInstance(_ string) Service {
	return darwinService{}
}

func SetupPrecondition() error {
	return ErrUnsupported
}

func (darwinService) Install(config.Config, string) error {
	return ErrUnsupported
}

func (darwinService) InstallAs(config.Config, string, string) error {
	return ErrUnsupported
}

func (darwinService) Uninstall() error {
	return ErrUnsupported
}

func (darwinService) EnableAndStart() error {
	return ErrUnsupported
}

func (darwinService) ProbeStatus() (Status, error) {
	return Status{Detail: "daemon install unsupported on macOS"}, nil
}
