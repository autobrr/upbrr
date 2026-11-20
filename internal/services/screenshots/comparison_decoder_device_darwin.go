// Copyright (c) 2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package screenshots

import (
	"context"
	"fmt"
	"runtime"

	"golang.org/x/sys/unix"

	"github.com/autobrr/upbrr/pkg/api"
)

func comparisonDeviceIdentity(ctx context.Context) (comparisonDeviceGeneration, error) {
	if ctx.Err() != nil {
		return comparisonDeviceGeneration{}, fmt.Errorf("identify comparison display device: %w", context.Cause(ctx))
	}
	// Apple silicon's integrated decoder cannot be replaced during this
	// process lifetime. Intel/eGPU systems need positive device identity first.
	if runtime.GOARCH != "arm64" {
		return comparisonDeviceGeneration{}, nil
	}
	model, err := unix.Sysctl("hw.model")
	if err != nil || model == "" {
		return comparisonDeviceGeneration{}, nil
	}
	build, err := unix.Sysctl("kern.osversion")
	if err != nil || build == "" {
		return comparisonDeviceGeneration{}, nil
	}
	fingerprint, err := api.CanonicalWorkflowFingerprint([]string{model, build})
	if err != nil {
		return comparisonDeviceGeneration{}, fmt.Errorf("fingerprint comparison display device: %w", err)
	}
	return comparisonDeviceGeneration{fingerprint: string(fingerprint)}, nil
}
