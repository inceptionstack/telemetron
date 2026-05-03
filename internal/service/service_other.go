//go:build !linux && !darwin

// SPDX-License-Identifier: Apache-2.0

package service

import "github.com/inceptionstack/telemetron/internal/config"

type unsupportedService struct{}

func newService() Service {
	return unsupportedService{}
}

func (unsupportedService) Install(config.Config, string) error { return ErrUnsupported }
func (unsupportedService) Uninstall() error                    { return ErrUnsupported }
func (unsupportedService) EnableAndStart() error               { return ErrUnsupported }
func (unsupportedService) ProbeStatus() (Status, error) {
	return Status{Detail: ErrUnsupported.Error()}, nil
}
