// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package mediainfo

import (
	"math"
	"regexp"
	"strconv"
	"strings"
)

var durationLinePattern = regexp.MustCompile(`(?im)^\s*duration(?:\s*/\s*string[123]?)?\s*:\s*(.+)$`)

var durationTokenPattern = regexp.MustCompile(
	`(?i)(\d+(?:\.\d+)?)\s*(milliseconds?|msecs?|ms|hours?|hrs?|hr|h|minutes?|mins?|min|mn|m|seconds?|secs?|sec|s)\b`,
)

var isoDurationPattern = regexp.MustCompile(`(?i)^pt(?:(\d+(?:\.\d+)?)h)?(?:(\d+(?:\.\d+)?)m)?(?:(\d+(?:\.\d+)?)s)?$`)

// DurationMinutes returns rounded minutes from the first parseable MediaInfo
// Duration or Duration/String[1-3] line.
func DurationMinutes(text string) int {
	for _, matches := range durationLinePattern.FindAllStringSubmatch(text, -1) {
		if len(matches) != 2 {
			continue
		}
		if minutes := durationValueMinutes(matches[1]); minutes > 0 {
			return minutes
		}
	}
	return 0
}

func durationValueMinutes(value string) int {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return 0
	}
	if matches := isoDurationPattern.FindStringSubmatch(trimmed); len(matches) == 4 {
		return durationSecondsToMinutes(durationComponentSeconds(matches[1], matches[2], matches[3], ""))
	}
	if strings.Contains(trimmed, ":") {
		return durationSecondsToMinutes(durationColonSeconds(trimmed))
	}
	if seconds := durationTokenSeconds(trimmed); seconds > 0 {
		return durationSecondsToMinutes(seconds)
	}
	if fields := strings.Fields(trimmed); len(fields) > 0 {
		if ms, err := strconv.ParseFloat(strings.ReplaceAll(fields[0], ",", ""), 64); err == nil && ms > 10000 {
			return int(math.Round(ms / 60000.0))
		}
	}
	return 0
}

func durationTokenSeconds(value string) float64 {
	var total float64
	for _, matches := range durationTokenPattern.FindAllStringSubmatch(value, -1) {
		if len(matches) != 3 {
			continue
		}
		amount, err := strconv.ParseFloat(strings.ReplaceAll(matches[1], ",", ""), 64)
		if err != nil || amount <= 0 {
			continue
		}
		switch strings.ToLower(matches[2]) {
		case "h", "hr", "hrs", "hour", "hours":
			total += amount * 3600
		case "m", "mn", "min", "mins", "minute", "minutes":
			total += amount * 60
		case "s", "sec", "secs", "second", "seconds":
			total += amount
		case "ms", "msec", "msecs", "millisecond", "milliseconds":
			total += amount / 1000
		}
	}
	return total
}

func durationColonSeconds(value string) float64 {
	parts := strings.Split(strings.TrimSpace(value), ":")
	if len(parts) < 2 {
		return 0
	}
	var total float64
	multiplier := 1.0
	for idx := len(parts) - 1; idx >= 0; idx-- {
		part := strings.TrimSpace(parts[idx])
		if part == "" {
			continue
		}
		amount, err := strconv.ParseFloat(strings.ReplaceAll(part, ",", ""), 64)
		if err != nil || amount < 0 {
			return 0
		}
		total += amount * multiplier
		multiplier *= 60
	}
	return total
}

func durationComponentSeconds(hours string, minutes string, seconds string, milliseconds string) float64 {
	totalSeconds := durationComponent(hours) * 3600
	totalSeconds += durationComponent(minutes) * 60
	totalSeconds += durationComponent(seconds)
	totalSeconds += durationComponent(milliseconds) / 1000
	return totalSeconds
}

func durationComponent(value string) float64 {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return 0
	}
	parsed, err := strconv.ParseFloat(trimmed, 64)
	if err != nil || parsed < 0 {
		return 0
	}
	return parsed
}

func durationSecondsToMinutes(seconds float64) int {
	if seconds <= 0 {
		return 0
	}
	return int(math.Round(seconds / 60.0))
}
