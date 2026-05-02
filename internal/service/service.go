// SPDX-License-Identifier: Apache-2.0

package service

import (
	"errors"

	"github.com/inceptionstack/loki-otl/internal/config"
)

var ErrUnsupported = errors.New("platform does not support daemon install")

type Status struct {
	Installed bool
	Active    bool
	Detail    string
}

type Service interface {
	Install(cfg config.Config, token string) error
	Uninstall() error
	EnableAndStart() error
	ProbeStatus() (Status, error)
}

func New() Service {
	return newService()
}
