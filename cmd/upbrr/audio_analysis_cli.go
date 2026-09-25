// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"golang.org/x/term"

	"github.com/autobrr/upbrr/internal/services/audioanalysis"
	"github.com/autobrr/upbrr/pkg/api"
)

func runAudioAnalysisOnly(ctx context.Context, opts cliOptions, visited map[string]bool, paths []string, streams cliIO) error {
	for _, flag := range slices.Sorted(maps.Keys(visited)) {
		switch flag {
		case "audio-analysis-only", "audio-output", "audio-tracks", "audio-images":
		default:
			return exitError(2, fmt.Errorf("--%s cannot be used with --audio-analysis-only", flag))
		}
	}
	if len(paths) != 1 {
		return exitError(2, errors.New("--audio-analysis-only requires exactly one media file"))
	}
	if strings.TrimSpace(opts.AudioOutput) == "" {
		return exitError(2, errors.New("--audio-analysis-only requires --audio-output <directory>"))
	}
	input, err := filepath.Abs(paths[0])
	if err != nil {
		return fmt.Errorf("resolve audio analysis input: %w", err)
	}
	info, err := os.Stat(input)
	if err != nil {
		return fmt.Errorf("open audio analysis input: %w", err)
	}
	if !info.Mode().IsRegular() {
		return errors.New("audio analysis input must be a regular media file")
	}
	output, err := filepath.Abs(opts.AudioOutput)
	if err != nil {
		return fmt.Errorf("resolve audio analysis output: %w", err)
	}
	selection, ordinals, err := parseCLIAudioTrackSelection(opts.AudioTracks)
	if err != nil {
		return exitError(2, err)
	}
	variants, err := parseCLIAudioVariants(opts.AudioImages)
	if err != nil {
		return exitError(2, err)
	}
	variants = append(variants, api.AudioAnalysisStats)
	fmt.Fprintf(streams.errOut, "Analyzing audio in %s\n", formatPathLabel(input))
	progress := newCLIAudioProgress(streams.errOut)
	ctx = api.WithWorkflowProgressReporter(ctx, progress.update)
	results, err := audioanalysis.NewService(nil).AnalyzeFile(ctx, input, selection, ordinals, variants, output)
	success := err == nil && len(results) > 0
	for _, result := range results {
		if result.Public.Status != api.StageStatusCompleted {
			success = false
		}
	}
	progress.finish(results, success)
	reportErr := reportAudioAnalysisResults(results, streams.out)
	if err != nil {
		return errors.Join(fmt.Errorf("analyze audio: %w", err), reportErr)
	}
	return reportErr
}

type cliAudioTrackProgress struct {
	label   string
	percent int
}

type cliAudioProgress struct {
	mu         sync.Mutex
	output     io.Writer
	terminal   bool
	terminalFD int
	width      int
	tracks     map[string]cliAudioTrackProgress
	order      []string
	lastWidth  int
	lastBucket int
}

func newCLIAudioProgress(output io.Writer) *cliAudioProgress {
	progress := &cliAudioProgress{
		output:     output,
		terminalFD: -1,
		tracks:     make(map[string]cliAudioTrackProgress),
		lastBucket: -1,
	}
	if file, ok := output.(*os.File); ok {
		if fd, valid := terminalFileDescriptor(file); valid {
			progress.terminal = term.IsTerminal(fd)
			if progress.terminal {
				progress.terminalFD = fd
				progress.width, _, _ = term.GetSize(fd)
			}
		}
	}
	return progress
}

func (p *cliAudioProgress) update(update api.WorkflowProgressUpdate) {
	if update.Phase != "audio_analysis_decode" || update.Status != api.StageStatusRunning || update.Total <= 0 {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	track, known := p.tracks[update.ItemID]
	if !known {
		track.label = "T" + strings.TrimPrefix(update.ItemID, "audio-")
		p.order = append(p.order, update.ItemID)
	}
	track.percent = min(99, max(0, update.Completed*100/update.Total))
	p.tracks[update.ItemID] = track
	if update.Completed > 0 {
		p.render()
	}
}

func (p *cliAudioProgress) finish(results []audioanalysis.TrackResult, success bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, result := range results {
		if result.Public.Status == api.StageStatusCompleted {
			track := p.tracks[result.Public.TrackID]
			track.percent = 100
			p.tracks[result.Public.TrackID] = track
		}
	}
	if len(p.order) > 0 {
		p.renderFinal(success)
	}
}

func (p *cliAudioProgress) line(status string) (string, int) {
	var builder strings.Builder
	total := 0
	for _, id := range p.order {
		total += p.tracks[id].percent
	}
	percent := total / len(p.order)
	fmt.Fprintf(&builder, "Audio analysis%s %d%%", status, percent)
	for _, id := range p.order {
		track := p.tracks[id]
		fmt.Fprintf(&builder, " | %s %d%%", track.label, track.percent)
	}
	return builder.String(), percent
}

func (p *cliAudioProgress) terminalLine(status string) string {
	line, percent := p.line(status)
	if p.width <= 0 || len(line) < p.width {
		return line
	}
	var builder strings.Builder
	label := "Audio"
	switch status {
	case " complete":
		label = "Done"
	case " incomplete":
		label = "Partial"
	}
	fmt.Fprintf(&builder, "%s %d%% |", label, percent)
	shown := 0
	for index, id := range p.order {
		track := p.tracks[id]
		part := fmt.Sprintf(" %s:%d%%", strings.TrimPrefix(track.label, "T"), track.percent)
		remaining := len(p.order) - index - 1
		suffix := ""
		if remaining > 0 {
			suffix = fmt.Sprintf(" +%d", remaining)
		}
		if builder.Len()+len(part)+len(suffix) >= p.width {
			break
		}
		builder.WriteString(part)
		shown++
	}
	if hidden := len(p.order) - shown; hidden > 0 {
		fmt.Fprintf(&builder, " +%d", hidden)
	}
	compact := builder.String()
	if len(compact) >= p.width {
		return compact[:max(0, p.width-1)]
	}
	return compact
}

func (p *cliAudioProgress) refreshTerminalWidth() {
	if p.terminalFD < 0 {
		return
	}
	if width, _, err := term.GetSize(p.terminalFD); err == nil && width > 0 {
		p.width = width
	}
}

func (p *cliAudioProgress) terminalPadding(line string) string {
	width := p.lastWidth
	if p.width > 0 {
		width = min(width, p.width-1)
	}
	return strings.Repeat(" ", max(0, width-len(line)))
}

func (p *cliAudioProgress) render() {
	line, percent := p.line("")
	if p.terminal {
		p.refreshTerminalWidth()
		line = p.terminalLine("")
		fmt.Fprintf(p.output, "\r%s%s", line, p.terminalPadding(line))
		p.lastWidth = len(line)
		return
	}
	bucket := percent / 10
	if bucket != p.lastBucket {
		fmt.Fprintln(p.output, line)
		p.lastBucket = bucket
	}
}

func (p *cliAudioProgress) renderFinal(success bool) {
	status := " incomplete"
	if success {
		status = " complete"
	}
	line, _ := p.line(status)
	if p.terminal {
		p.refreshTerminalWidth()
		line = p.terminalLine(status)
		fmt.Fprintf(p.output, "\r%s%s\n", line, p.terminalPadding(line))
		return
	}
	fmt.Fprintln(p.output, line)
}

func reportAudioAnalysisResults(results []audioanalysis.TrackResult, output io.Writer) error {
	var failures []string
	for _, result := range results {
		for _, artifact := range result.Artifacts {
			fmt.Fprintf(output, "Audio track %d %s: %s\n", result.Public.Ordinal, artifact.Public.Variant, artifact.Path)
		}
		failed := false
		if result.Public.Failure != nil {
			failures = append(failures, fmt.Sprintf("track %d: %s", result.Public.Ordinal, result.Public.Failure.Message))
			failed = true
		}
		for _, artifact := range result.Public.Artifacts {
			if artifact.Failure != nil {
				failures = append(failures, fmt.Sprintf("track %d %s: %s", result.Public.Ordinal, artifact.Variant, artifact.Failure.Message))
				failed = true
			}
		}
		if result.Public.Status != api.StageStatusCompleted && !failed {
			failures = append(failures, fmt.Sprintf("track %d: incomplete analysis", result.Public.Ordinal))
		}
	}
	if len(failures) > 0 {
		return fmt.Errorf("audio analysis incomplete: %s", strings.Join(failures, "; "))
	}
	return nil
}
