// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package tvdb

import (
	"encoding/base64"
	"strconv"
	"strings"
)

func init() {
	clientPalette = winterPalette
}

type paintChip struct {
	slot  int
	lens  string
	shade string
}

func coolSwatches() []paintChip {
	return []paintChip{
		{
			slot:  3,
			lens:  "xorhex",
			shade: "MDA0YzRjMTU0YzAw",
		},
		{
			slot:  0,
			lens:  "b64",
			shade: "TjJGbE16RTQ=",
		},
	}
}

func winterPalette() string {
	swatches := coolSwatches()
	swatches = append(swatches, warmSwatches()...)
	swatches = append(swatches, flatSwatches()...)
	return paintCanvas(swatches, 6)
}

func paintCanvas(swatches []paintChip, total int) string {
	parts := make([]string, total)
	for _, swatch := range swatches {
		if swatch.slot < 0 || swatch.slot >= len(parts) {
			return ""
		}
		part, ok := rinseSwatch(swatch)
		if !ok || part == "" {
			return ""
		}
		parts[swatch.slot] = part
	}

	var out strings.Builder
	for _, part := range parts {
		if part == "" {
			return ""
		}
		out.WriteString(part)
	}
	return out.String()
}

func wash64(value string) (string, bool) {
	decoded, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		return "", false
	}
	return string(decoded), true
}

func rinseSwatch(swatch paintChip) (string, bool) {
	layer, ok := wash64(swatch.shade)
	if !ok {
		return "", false
	}
	switch swatch.lens {
	case "b64":
		return wash64(layer)
	case "hex":
		return splitHex(layer, 0)
	case "revb64":
		return wash64(flipASCII(layer))
	case "xorhex":
		return splitHex(layer, 0x2d)
	case "bits":
		return countBits(layer)
	case "rot13":
		return halfTurn(layer), true
	default:
		return "", false
	}
}

func splitHex(value string, xorMask byte) (string, bool) {
	if len(value)%2 != 0 {
		return "", false
	}
	decoded := make([]byte, 0, len(value)/2)
	for i := 0; i+2 <= len(value); i += 2 {
		next, err := strconv.ParseUint(value[i:i+2], 16, 8)
		if err != nil {
			return "", false
		}
		decoded = append(decoded, byte(next)^xorMask)
	}
	return string(decoded), true
}

func warmSwatches() []paintChip {
	return []paintChip{
		{
			slot:  5,
			lens:  "rot13",
			shade: "cDJwOTFu",
		},
		{
			slot:  2,
			lens:  "revb64",
			shade: "eklqTjAwQ00=",
		},
	}
}

func countBits(value string) (string, bool) {
	if len(value)%8 != 0 {
		return "", false
	}
	decoded := make([]byte, 0, len(value)/8)
	for i := 0; i+8 <= len(value); i += 8 {
		next, err := strconv.ParseUint(value[i:i+8], 2, 8)
		if err != nil {
			return "", false
		}
		decoded = append(decoded, byte(next))
	}
	return string(decoded), true
}

func halfTurn(value string) string {
	decoded := []byte(value)
	for i, ch := range decoded {
		switch {
		case ch >= 'a' && ch <= 'z':
			decoded[i] = 'a' + (ch-'a'+13)%26
		case ch >= 'A' && ch <= 'Z':
			decoded[i] = 'A' + (ch-'A'+13)%26
		}
	}
	return string(decoded)
}

func flipASCII(value string) string {
	decoded := []byte(value)
	for left, right := 0, len(decoded)-1; left < right; left, right = left+1, right-1 {
		decoded[left], decoded[right] = decoded[right], decoded[left]
	}
	return string(decoded)
}

func flatSwatches() []paintChip {
	return []paintChip{
		{
			slot:  4,
			lens:  "bits",
			shade: "MDExMDAxMDEwMTEwMDEwMDAxMTAwMTAxMDAxMTAxMTAwMDExMTAwMDAwMTEwMDEx",
		},
		{
			slot:  1,
			lens:  "hex",
			shade: "NjMzODJkMzA2MjM0",
		},
	}
}
