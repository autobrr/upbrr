// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestLiveTestPolicyFailureAndConcurrentReceipt(t *testing.T) {
	t.Parallel()
	journal := filepath.Join(t.TempDir(), "private-images.jsonl")
	policy, err := NewLiveTestPolicy("synthetic-run", journal, 0)
	if err != nil {
		t.Fatal(err)
	}
	before := policy.Snapshot()
	var group sync.WaitGroup
	for _, operation := range []OperationKind{OperationKindUploadExecute, OperationKindClientInjection} {
		for range 10 {
			group.Go(func() {
				for _, reject := range []func(OperationKind) error{policy.RejectRequest, policy.RejectMutation} {
					err := fmt.Errorf("caller: %w", reject(operation))
					failure, ok := AsOperationFailure(err)
					if !errors.Is(err, ErrLiveTestMutationDisabled) || !ok ||
						failure.Code != OperationFailureLiveTestMutationDisabled || failure.Operation != operation ||
						failure.Recovery != OperationRecoveryNone {
						t.Errorf("denial = %v, failure = %#v", err, failure)
					}
					_ = policy.Snapshot()
				}
			})
		}
	}
	group.Wait()
	after := policy.Snapshot()
	want := LiveTestEffectCounts{RequestsDenied: 10, MutationCallsDenied: 10}
	if after.TrackerSubmission != want || after.ClientMutation != want {
		t.Fatalf("receipt = %#v, want both effects %#v", after, want)
	}
	if before.TrackerSubmission != (LiveTestEffectCounts{}) || before.ClientMutation != (LiveTestEffectCounts{}) {
		t.Fatalf("previous snapshot changed: %#v", before)
	}
	if after.Mode != "live_test" || after.RunID != "synthetic-run" || after.TrackerSubmissionAllowed ||
		after.ClientMutationAllowed || !after.ImageUploadsRequireJournal || policy.ImageJournalPath() != journal {
		t.Fatalf("capabilities = %#v", after)
	}
	encoded, err := json.Marshal(after)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "private-images") || strings.Contains(string(encoded), "journalPath") {
		t.Fatalf("public snapshot exposed journal: %s", encoded)
	}
}

func TestLiveTestPolicyNilAndInvalidIdentity(t *testing.T) {
	t.Parallel()
	var policy *LiveTestPolicy
	if policy.RejectRequest(OperationKindUploadExecute) != nil || policy.RejectMutation(OperationKindClientInjection) != nil ||
		policy.Snapshot() != nil || policy.RunID() != "" || policy.ImageJournalPath() != "" {
		t.Fatal("nil policy changed ordinary execution")
	}
	for _, test := range []struct{ runID, journal string }{{" ", "journal"}, {"run", "\t"}} {
		if policy, err := NewLiveTestPolicy(test.runID, test.journal, 0); err == nil || policy != nil {
			t.Fatalf("invalid policy accepted: %#v, %v", policy, err)
		}
	}
}
