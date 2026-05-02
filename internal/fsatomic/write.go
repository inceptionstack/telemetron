// SPDX-License-Identifier: Apache-2.0

package fsatomic

import (
	"os"
	"path/filepath"
)

type options struct {
	mode os.FileMode
	uid  int
	gid  int
}

type Option func(*options)

func WithMode(mode os.FileMode) Option {
	return func(opts *options) {
		opts.mode = mode
	}
}

func WithOwner(uid, gid int) Option {
	return func(opts *options) {
		opts.uid = uid
		opts.gid = gid
	}
}

func WriteFile(path string, data []byte, opts ...Option) error {
	options := options{
		mode: 0o644,
		uid:  -1,
		gid:  -1,
	}
	for _, opt := range opts {
		opt(&options)
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}

	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpPath, options.mode); err != nil {
		return err
	}
	if options.uid >= 0 || options.gid >= 0 {
		if err := os.Chown(tmpPath, options.uid, options.gid); err != nil {
			return err
		}
	}
	return os.Rename(tmpPath, path)
}
