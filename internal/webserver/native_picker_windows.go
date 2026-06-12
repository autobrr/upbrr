//go:build windows

// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package webserver

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/autobrr/upbrr/internal/filesystem"
)

type powershellNativePicker struct{}

func newNativePicker() nativePicker {
	return powershellNativePicker{}
}

func (powershellNativePicker) BrowseFile() (string, error) {
	filterPattern := videoFilePickerFilterPattern()
	return runPickerScript(fmt.Sprintf(`
Add-Type -AssemblyName System.Windows.Forms
$dialog = New-Object System.Windows.Forms.OpenFileDialog
$dialog.Title = 'Select a file'
$dialog.Filter = 'Video files (%[1]s)|%[1]s'
if ($dialog.ShowDialog() -eq [System.Windows.Forms.DialogResult]::OK) {
  [Console]::Out.Write($dialog.FileName)
}
`, filterPattern))
}

func (powershellNativePicker) BrowseImageFiles() ([]string, error) {
	output, err := runPickerScript(`
Add-Type -AssemblyName System.Windows.Forms
$dialog = New-Object System.Windows.Forms.OpenFileDialog
$dialog.Title = 'Select images'
$dialog.Filter = 'Image files (*.png;*.jpg;*.jpeg;*.webp)|*.png;*.jpg;*.jpeg;*.webp|All files (*.*)|*.*'
$dialog.Multiselect = $true
if ($dialog.ShowDialog() -eq [System.Windows.Forms.DialogResult]::OK) {
  [Console]::Out.Write(($dialog.FileNames -join [Environment]::NewLine))
}
`)
	if err != nil {
		return nil, err
	}
	var paths []string
	for line := range strings.SplitSeq(output, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" {
			paths = append(paths, trimmed)
		}
	}
	return paths, nil
}

func (powershellNativePicker) BrowseFolder() (string, error) {
	return runPickerScript(`
Add-Type -AssemblyName System.Windows.Forms
$dialog = New-Object System.Windows.Forms.FolderBrowserDialog
$dialog.Description = 'Select a folder'
$dialog.ShowNewFolderButton = $false
if ($dialog.ShowDialog() -eq [System.Windows.Forms.DialogResult]::OK) {
  [Console]::Out.Write($dialog.SelectedPath)
}
`)
}

func runPickerScript(script string) (string, error) {
	wrapped := strings.TrimSpace(`
$OutputEncoding = [Console]::OutputEncoding = [System.Text.UTF8Encoding]::new($false)
` + "\n" + script)

	cmd := exec.CommandContext(
		context.Background(),
		"powershell",
		"-NoLogo",
		"-NoProfile",
		"-NonInteractive",
		"-STA",
		"-Command",
		wrapped,
	)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		message := strings.TrimSpace(stderr.String())
		if message == "" {
			message = err.Error()
		}
		return "", fmt.Errorf("native browse failed: %s", message)
	}
	return decodePickerOutput(stdout.Bytes()), nil
}

func videoFilePickerFilterPattern() string {
	extensions := filesystem.SupportedVideoExtensions()
	patterns := make([]string, 0, len(extensions))
	for _, ext := range extensions {
		patterns = append(patterns, "*"+ext)
	}
	return strings.Join(patterns, ";")
}

func decodePickerOutput(raw []byte) string {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return ""
	}

	if utf8.Valid(trimmed) {
		return strings.TrimSpace(string(trimmed))
	}

	if len(trimmed) >= 2 {
		if trimmed[0] == 0xFF && trimmed[1] == 0xFE {
			if decoded, ok := decodeUTF16(trimmed[2:], true); ok {
				return decoded
			}
		}
		if trimmed[0] == 0xFE && trimmed[1] == 0xFF {
			if decoded, ok := decodeUTF16(trimmed[2:], false); ok {
				return decoded
			}
		}
	}

	if decoded, ok := decodeUTF16(trimmed, true); ok {
		return decoded
	}
	if decoded, ok := decodeUTF16(trimmed, false); ok {
		return decoded
	}

	return strings.TrimSpace(string(trimmed))
}

func decodeUTF16(raw []byte, littleEndian bool) (string, bool) {
	if len(raw) < 2 || len(raw)%2 != 0 {
		return "", false
	}

	u16 := make([]uint16, 0, len(raw)/2)
	for i := 0; i < len(raw); i += 2 {
		if littleEndian {
			u16 = append(u16, uint16(raw[i])|uint16(raw[i+1])<<8)
		} else {
			u16 = append(u16, uint16(raw[i])<<8|uint16(raw[i+1]))
		}
	}

	decoded := strings.TrimSpace(string(utf16.Decode(u16)))
	if decoded == "" {
		return "", false
	}
	if strings.ContainsRune(decoded, '\uFFFD') {
		return "", false
	}
	return decoded, true
}
