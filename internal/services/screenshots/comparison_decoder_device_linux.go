// Copyright (c) 2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package screenshots

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/autobrr/upbrr/pkg/api"
)

func comparisonDeviceIdentity(ctx context.Context) (comparisonDeviceGeneration, error) {
	var devices []string
	vaapiDevice := ""
	bestScore := 3
	deviceRoot := "/dev/dri"
	for _, root := range []string{"/sys/class/drm", "/proc/driver/nvidia/gpus"} {
		entries, err := os.ReadDir(root)
		if err != nil || len(entries) > 128 {
			continue
		}
		for _, entry := range entries {
			if ctx.Err() != nil {
				return comparisonDeviceGeneration{}, fmt.Errorf("identify comparison display devices: %w", context.Cause(ctx))
			}
			if root == "/sys/class/drm" && !strings.HasPrefix(entry.Name(), "renderD") {
				continue
			}
			for _, suffix := range [][]string{{"device", "uevent"}, {"device", "driver", "module", "version"}, {"information"}} {
				path := filepath.Join(append([]string{root, entry.Name()}, suffix...)...)
				file, err := os.Open(path)
				if err != nil {
					continue
				}
				data, err := io.ReadAll(io.LimitReader(file, 8193))
				_ = file.Close()
				if err == nil && len(data) > 0 && len(data) <= 8192 {
					devices = append(devices, entry.Name()+"/"+strings.Join(suffix, "/")+"/"+string(data))
					if root == "/sys/class/drm" && len(suffix) == 2 && suffix[1] == "uevent" {
						score := 2
						if strings.Contains(string(data), "DRIVER=i915") || strings.Contains(string(data), "DRIVER=xe") {
							score = 0
						} else if strings.Contains(string(data), "DRIVER=amdgpu") {
							score = 1
						}
						if score < bestScore {
							bestScore = score
							vaapiDevice = filepath.Join(deviceRoot, entry.Name())
						}
					}
				}
			}
		}
	}
	if len(devices) == 0 {
		return comparisonDeviceGeneration{}, nil
	}
	if file, err := os.Open("/proc/driver/nvidia/version"); err == nil {
		data, readErr := io.ReadAll(io.LimitReader(file, 8193))
		_ = file.Close()
		if readErr == nil && len(data) <= 8192 {
			devices = append(devices, string(data))
		}
	}
	slices.Sort(devices)
	fingerprint, err := api.CanonicalWorkflowFingerprint(devices)
	if err != nil {
		return comparisonDeviceGeneration{}, fmt.Errorf("fingerprint comparison display devices: %w", err)
	}
	return comparisonDeviceGeneration{fingerprint: string(fingerprint), vaapiDevice: vaapiDevice}, nil
}
