//go:build !linux && !darwin

package service

import "github.com/inceptionstack/loki-otl/internal/config"

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
