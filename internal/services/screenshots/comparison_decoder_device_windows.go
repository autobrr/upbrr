// Copyright (c) 2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package screenshots

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"

	"golang.org/x/sys/windows/registry"

	"github.com/autobrr/upbrr/pkg/api"
)

// Bind process-local receipts to the OS display-device and installed-driver
// generation. No vendor utility or device-enumeration executable is required.
func comparisonDeviceIdentity(ctx context.Context) (comparisonDeviceGeneration, error) {
	key, err := registry.OpenKey(registry.LOCAL_MACHINE, `SYSTEM\CurrentControlSet\Control\Video`, registry.READ)
	if err != nil {
		return comparisonDeviceGeneration{}, nil
	}
	defer key.Close()
	names, err := key.ReadSubKeyNames(128)
	if err != nil && !errors.Is(err, io.EOF) || len(names) == 0 || len(names) >= 128 {
		return comparisonDeviceGeneration{}, nil
	}
	slices.Sort(names)
	var devices []string
	preferredVendor := ""
	for _, name := range names {
		if ctx.Err() != nil {
			return comparisonDeviceGeneration{}, fmt.Errorf("identify comparison display devices: %w", context.Cause(ctx))
		}
		device, err := registry.OpenKey(key, name+`\0000`, registry.QUERY_VALUE)
		if err != nil {
			continue
		}
		matching, _, _ := device.GetStringValue("MatchingDeviceId")
		version, _, _ := device.GetStringValue("DriverVersion")
		description, _, _ := device.GetStringValue("DriverDesc")
		guid, _, _ := device.GetStringValue("UserModeDriverGUID")
		_ = device.Close()
		if matching != "" && version != "" {
			devices = append(devices, strings.Join([]string{name, matching, version, description, guid}, "/"))
			matching = strings.ToUpper(matching)
			switch {
			case strings.Contains(matching, "VEN_8086"):
				preferredVendor = "0x8086"
			case strings.Contains(matching, "VEN_1002") && preferredVendor != "0x8086":
				preferredVendor = "0x1002"
			case strings.Contains(matching, "VEN_10DE") && preferredVendor == "":
				preferredVendor = "0x10de"
			}
		}
	}
	if mapping, err := registry.OpenKey(registry.LOCAL_MACHINE, `HARDWARE\DEVICEMAP\VIDEO`, registry.READ); err == nil {
		names, readErr := mapping.ReadValueNames(128)
		if readErr == nil || errors.Is(readErr, io.EOF) {
			slices.Sort(names)
			for _, name := range names {
				if value, _, err := mapping.GetStringValue(name); err == nil {
					devices = append(devices, name+"/"+value)
				}
			}
		}
		_ = mapping.Close()
	}
	if len(devices) == 0 {
		return comparisonDeviceGeneration{}, nil
	}
	fingerprint, err := api.CanonicalWorkflowFingerprint(devices)
	if err != nil {
		return comparisonDeviceGeneration{}, fmt.Errorf("fingerprint comparison display devices: %w", err)
	}
	return comparisonDeviceGeneration{fingerprint: string(fingerprint), d3dVendor: preferredVendor}, nil
}
