// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	tea "charm.land/bubbletea/v2"

	"github.com/autobrr/upbrr/pkg/api"
)

const (
	cliPendingLogLimit  = 1000
	cliRetainedLogLimit = 2000
	cliLogLineLimit     = 4096
	cliQueueResultLimit = 128
	cliQueueResultBytes = 4096
	cliArtifactLimit    = 128
	cliTelemetryLimit   = 256
)

type cliLogLine struct{ Time, Level, Text string }
type cliPromptRequest struct {
	prompt cliPrompt
	reply  chan cliAnswer
	done   <-chan struct{}
}
type cliCompletion struct {
	err      error
	canceled bool
}
type cliBridgeUpdate struct {
	view      *cliView
	logs      []cliLogLine
	dropped   uint64
	progress  *cliTelemetry
	telemetry []cliTelemetry
}

// cliTelemetry carries producer-specific counts, such as trackers or torrent
// pieces. ItemOnly keeps these counts separate from overall workflow progress.
type cliTelemetry struct {
	Item, Epoch                                   uint64
	WorkflowID, OperationID, Lane, Attempt, Phase string
	Completed, Total                              int
	ItemOnly                                      bool
}

// cliTUIBridge connects concurrent producers to the single model update loop.
// Snapshots, telemetry, and logs are bounded or coalesced; questions and completion
// use independent channels. Publish and Progress never wait for rendering.
type cliTUIBridge struct {
	mu                                         sync.Mutex
	wake                                       chan struct{}
	view                                       *cliView
	progress                                   *cliTelemetry
	telemetry                                  []cliTelemetry
	logs                                       []cliLogLine
	dropped                                    uint64
	questions                                  chan cliPromptRequest
	completion                                 chan cliCompletion
	ready                                      chan struct{}
	launch                                     chan cliLaunchRequest
	latest                                     cliView
	results                                    []string
	artifacts                                  []string
	artifactsDropped                           uint64
	resultsDropped, itemsFinished, itemsFailed uint64
	epoch                                      uint64
	dashboard                                  bool
}

func newCLITUIBridge() *cliTUIBridge {
	return &cliTUIBridge{
		wake:       make(chan struct{}, 1),
		questions:  make(chan cliPromptRequest),
		completion: make(chan cliCompletion, 1),
		ready:      make(chan struct{}),
		launch:     make(chan cliLaunchRequest, 1),
	}
}

func (b *cliTUIBridge) notify() {
	select {
	case b.wake <- struct{}{}:
	default:
	}
}

func (b *cliTUIBridge) Publish(view cliView) {
	view = safeCLIView(view)
	b.mu.Lock()
	view.Epoch = b.epoch
	if view.Item == b.latest.Item && view.WorkflowID == b.latest.WorkflowID && view.Revision < b.latest.Revision {
		b.mu.Unlock()
		return
	}
	if view.Item >= b.latest.Item {
		if view.Item != b.latest.Item && b.latest.Item != 0 {
			var item strings.Builder
			item.WriteString(b.latest.Result)
			item.WriteByte('\n')
			for _, lane := range b.latest.Lanes {
				fmt.Fprintf(&item, "%s: %s (%s)\n", lane.ID, lane.State, lane.Reason)
				if item.Len() >= cliQueueResultBytes {
					break
				}
			}
			text := item.String()
			if len(text) > cliQueueResultBytes {
				text = strings.ToValidUTF8(text[:cliQueueResultBytes-18], "") + " [details omitted]"
			}
			if len(b.results) == cliQueueResultLimit {
				copy(b.results, b.results[1:])
				b.results[len(b.results)-1] = text
				b.resultsDropped++
			} else {
				b.results = append(b.results, text)
			}
			if b.latest.Result != "" {
				b.itemsFinished++
			}
			if b.latest.ItemFailed {
				b.itemsFailed++
			}
		}
		b.latest = view
		b.view = &view
	}
	b.mu.Unlock()
	b.notify()
}

// Progress coalesces counts by lane and attempt, rejecting callbacks from older
// items or reporter epochs and mismatched workflow or operation identities.
func (b *cliTUIBridge) Progress(progress cliTelemetry) {
	progress.Phase = safeDiagnosticText(progress.Phase)
	progress.Lane, progress.Attempt = safeDiagnosticText(progress.Lane), safeDiagnosticText(progress.Attempt)
	b.mu.Lock()
	if progress.Epoch == b.epoch && progress.Item >= b.latest.Item && (progress.WorkflowID == "" || progress.WorkflowID == string(b.latest.WorkflowID)) &&
		(progress.OperationID == "" || progress.OperationID == string(b.latest.OperationID)) {
		if !progress.ItemOnly {
			b.progress = &progress
		}
		index := slices.IndexFunc(b.telemetry, func(previous cliTelemetry) bool {
			return previous.Lane == progress.Lane && previous.Attempt == progress.Attempt
		})
		if index >= 0 {
			b.telemetry[index] = progress
		} else {
			if len(b.telemetry) >= cliTelemetryLimit {
				b.telemetry = b.telemetry[1:]
			}
			b.telemetry = append(b.telemetry, progress)
		}
	}
	b.mu.Unlock()
	b.notify()
}

// Ask waits for a correlated model reply or context cancellation. Dashboard mode
// rejects questions immediately, so strict unattended runs never wait for input.
func (b *cliTUIBridge) Ask(ctx context.Context, prompt cliPrompt) (cliAnswer, error) {
	if b.dashboard {
		return cliAnswer{}, errors.New("unattended dashboard cannot request input")
	}
	request := cliPromptRequest{
		prompt: safeCLIPrompt(prompt),
		reply:  make(chan cliAnswer, 1),
		done:   ctx.Done(),
	}
	select {
	case b.questions <- request:
	case <-ctx.Done():
		return cliAnswer{}, fmt.Errorf("publish terminal question: %w", ctx.Err())
	}
	select {
	case answer := <-request.reply:
		return answer, nil
	case <-ctx.Done():
		return cliAnswer{}, fmt.Errorf("await terminal answer: %w", ctx.Err())
	}
}

func (b *cliTUIBridge) AppendLog(level, message string) {
	// Bound before splitting: a single huge write cannot allocate an unbounded
	// slice of lines or retained transcript. Prefixes are sanitized as a unit.
	for len(message) > 0 {
		line, rest, _ := strings.Cut(message, "\n")
		message = rest
		if len(line) > cliLogLineLimit {
			line = line[:cliLogLineLimit-12] + " [truncated]"
		}
		line = safeDiagnosticText(line)
		if len(line) > cliLogLineLimit {
			line = line[:cliLogLineLimit-12] + " [truncated]"
		}
		if line == "" {
			continue
		}
		b.mu.Lock()
		if len(b.logs) == cliPendingLogLimit {
			copy(b.logs, b.logs[1:])
			b.logs = b.logs[:cliPendingLogLimit-1]
			b.dropped++
		}
		b.logs = append(b.logs, cliLogLine{
			Time:  time.Now().Format("15:04:05"),
			Level: level,
			Text:  line,
		})
		b.mu.Unlock()
	}
	b.notify()
}

// RetainAudioArtifact saves bounded owner-authorized paths for post-restoration
// output, separate from diagnostic redaction and model state. Terminal controls
// are quoted; oversized paths are omitted rather than truncated into another name.
func (b *cliTUIBridge) RetainAudioArtifact(item uint64, ordinal int, variant api.AudioAnalysisVariant, pathValue string) {
	if strings.ContainsFunc(pathValue, func(r rune) bool { return unicode.IsControl(r) || unicode.Is(unicode.Cf, r) }) {
		pathValue = strconv.QuoteToASCII(pathValue)
	}
	line := fmt.Sprintf("Audio analysis resource 1 track %d %s: %s\n", ordinal, variant, pathValue)
	if item > 0 {
		line = fmt.Sprintf("Item %d: %s", item, line)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(line) > cliQueueResultBytes {
		b.artifactsDropped++
		return
	}
	if len(b.artifacts) == cliArtifactLimit {
		copy(b.artifacts, b.artifacts[1:])
		b.artifacts[len(b.artifacts)-1] = line
		b.artifactsDropped++
	} else {
		b.artifacts = append(b.artifacts, line)
	}
}

// receive yields one bridge event. The model schedules its successor so only
// one command consumes questions, completion, and wakeups at a time.
func (b *cliTUIBridge) receive(ctx context.Context) tea.Cmd {
	return func() tea.Msg {
		select {
		case completion := <-b.completion:
			return completion
		case request := <-b.questions:
			return request
		case <-ctx.Done():
			return cliCompletion{err: ctx.Err()}
		case <-b.wake:
			return b.drain()
		}
	}
}

func (b *cliTUIBridge) drain() cliBridgeUpdate {
	b.mu.Lock()
	defer b.mu.Unlock()
	update := cliBridgeUpdate{
		view:      b.view,
		logs:      b.logs,
		dropped:   b.dropped,
		progress:  b.progress,
		telemetry: slices.Clone(b.telemetry),
	}
	b.view, b.logs, b.progress = nil, nil, nil
	return update
}

func (b *cliTUIBridge) summary(err error) string {
	b.mu.Lock()
	defer b.mu.Unlock()
	var result strings.Builder
	if b.latest.Debug {
		result.WriteString("Debug workflow: tracker submission suppressed.\n")
	}
	if b.latest.ItemsTotal > 1 {
		finished, failed := b.itemsFinished, b.itemsFailed
		if b.latest.Result != "" {
			finished++
		}
		if b.latest.ItemFailed {
			failed++
		}
		fmt.Fprintf(&result, "Queue: %d/%d items finished; %d failed or interrupted.\n", finished, b.latest.ItemsTotal, failed)
	}
	if b.resultsDropped > 0 {
		fmt.Fprintf(&result, "%d earlier item results omitted; inspect retained workflow history for details.\n", b.resultsDropped)
	}
	if len(b.results) > 0 {
		for index, item := range b.results {
			result.WriteString("Item ")
			result.WriteString(strconv.FormatUint(b.resultsDropped+uint64(index)+1, 10))
			result.WriteString(":\n")
			result.WriteString(item)
			result.WriteByte('\n')
		}
	}
	result.WriteString(cliLaneSummary(b.latest.Lanes))
	if b.latest.Result != "" {
		result.WriteString(b.latest.Result)
		result.WriteByte('\n')
	}
	if result.Len() == 0 {
		if err == nil {
			result.WriteString("Completed.\n")
		} else {
			result.WriteString("Workflow stopped. Inspect retained state before retrying any remote submission.\n")
		}
	} else if err != nil {
		result.WriteString("Workflow stopped. Verify remote effects before retrying.\n")
	}
	if b.artifactsDropped > 0 {
		fmt.Fprintf(&result, "%d artifact paths omitted; inspect retained audio analysis results for details.\n", b.artifactsDropped)
	}
	for _, artifact := range b.artifacts {
		result.WriteString(artifact)
	}
	return result.String()
}

// Console output is a bounded compatibility sink, never an action protocol.
type cliTUILogWriter struct {
	bridge   *cliTUIBridge
	mu       sync.Mutex
	fragment string
}

func (w *cliTUILogWriter) Write(data []byte) (int, error) {
	count := len(data)
	w.mu.Lock()
	defer w.mu.Unlock()
	for len(data) > 0 {
		index := slices.IndexFunc(data, func(b byte) bool { return b == '\n' || b == '\r' })
		part := data
		if index >= 0 {
			part = data[:index]
		}
		remaining := max(0, cliLogLineLimit-len(w.fragment))
		w.fragment += string(part[:min(len(part), remaining)])
		if index < 0 {
			break
		}
		w.bridge.AppendLog("", w.fragment)
		w.fragment = ""
		data = data[index+1:]
	}
	return count, nil
}

func (w *cliTUILogWriter) WriteConsoleLog(label, message string) {
	w.bridge.AppendLog(label, message)
}

func cliBridgeFromOutput(output io.Writer) *cliTUIBridge {
	if writer, ok := output.(*cliPresentationWriter); ok {
		bridge, _ := writer.presenter.(*cliTUIBridge)
		return bridge
	}
	return nil
}

var errCLIUserInterrupt = errors.New("interrupted by user; remote effects may require reconciliation")
