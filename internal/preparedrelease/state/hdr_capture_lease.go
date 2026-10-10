// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package preparationstate

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// HDRCaptureLease keeps provisional metadata owned until its preparation releases it.
// The operating system releases the lock after a crash; runtime replacement shares it.
type HDRCaptureLease struct {
	file *os.File
	once sync.Once
}

// NewHDRCaptureLease exclusively creates and locks owner.lock in an existing capture directory.
// ReleaseHDRCaptures closes the lease when preparation relinquishes its resources.
func NewHDRCaptureLease(directory string) (*HDRCaptureLease, error) {
	file, err := os.OpenFile(filepath.Join(directory, "owner.lock"), os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("create HDR capture lease: %w", err)
	}
	locked, err := lockHDRCapture(file)
	if err != nil || !locked {
		_ = file.Close()
		if err == nil {
			err = errors.New("new capture is already leased")
		}
		return nil, fmt.Errorf("lock new HDR capture lease: %w", err)
	}
	return &HDRCaptureLease{file: file}, nil
}

func (lease *HDRCaptureLease) release() {
	if lease != nil {
		lease.once.Do(func() { _ = lease.file.Close() })
	}
}
