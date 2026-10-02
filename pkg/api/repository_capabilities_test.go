// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package api

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

type nilReleaseStateRepository struct{}

func (*nilReleaseStateRepository) GetByPath(context.Context, string) (FileMetadata, error) {
	return FileMetadata{}, nil
}
func (*nilReleaseStateRepository) Save(context.Context, FileMetadata) error { return nil }
func (*nilReleaseStateRepository) GetExternalIdentity(context.Context, string) (ExternalIdentity, error) {
	return ExternalIdentity{}, nil
}
func (*nilReleaseStateRepository) SaveExternalIdentity(context.Context, ExternalIdentity) error {
	return nil
}
func (*nilReleaseStateRepository) GetExternalMetadata(context.Context, string) (SourceScopedMetadata, error) {
	return SourceScopedMetadata{}, nil
}
func (*nilReleaseStateRepository) SaveExternalMetadata(context.Context, SourceScopedMetadata) error {
	return nil
}
func (*nilReleaseStateRepository) GetDVDMediaInfo(context.Context, string) (DVDMediaInfo, error) {
	return DVDMediaInfo{}, nil
}
func (*nilReleaseStateRepository) SaveDVDMediaInfo(context.Context, DVDMediaInfo) error { return nil }
func (*nilReleaseStateRepository) GetReleaseNameOverrides(context.Context, string) (ReleaseNameOverrides, error) {
	return ReleaseNameOverrides{}, nil
}
func (*nilReleaseStateRepository) SaveReleaseNameOverrides(context.Context, string, ReleaseNameOverrides) error {
	return nil
}
func (*nilReleaseStateRepository) DeleteReleaseNameOverrides(context.Context, string) error {
	return nil
}

func TestRepositoryCapabilitiesRejectMissingAndTypedNil(t *testing.T) {
	t.Parallel()

	if err := (RepositoryCapabilities{}).Validate(); !errors.Is(err, ErrMissingReleaseStateRepository) {
		t.Fatalf("zero bundle error = %v, want %v", err, ErrMissingReleaseStateRepository)
	}
	var typedNil *nilReleaseStateRepository
	capabilities := RepositoryCapabilities{releaseState: typedNil}
	if err := capabilities.Validate(); !errors.Is(err, ErrMissingReleaseStateRepository) {
		t.Fatalf("typed-nil bundle error = %v, want %v", err, ErrMissingReleaseStateRepository)
	}
}

type repositoryCapabilitiesWithoutEvidence struct {
	ReleaseStateRepository
	PreparedReleaseRepository
	ReleaseSelectionRepository
	HistoryRepository
	UploadLedgerRepository
	TrackerStateRepository
	MediaAssetRepository
	ReleaseWorkflowStateRepository
}

func TestRepositoryCapabilitiesRequireMetadataEvidence(t *testing.T) {
	t.Parallel()

	adapter := &repositoryCapabilitiesWithoutEvidence{}
	capabilities := RepositoryCapabilitiesFrom(adapter)
	if err := capabilities.Validate(); !errors.Is(err, ErrMissingMetadataEvidenceRepository) {
		t.Fatalf("missing metadata evidence error = %v, want %v", err, ErrMissingMetadataEvidenceRepository)
	}
	if _, err := NewRepositoryCapabilities(adapter); !errors.Is(err, ErrMissingMetadataEvidenceRepository) {
		t.Fatalf("constructor missing metadata evidence error = %v, want %v", err, ErrMissingMetadataEvidenceRepository)
	}
	var typedNil *struct{ MetadataEvidenceRepository }
	capabilities.metadataEvidence = typedNil
	if err := capabilities.Validate(); !errors.Is(err, ErrMissingMetadataEvidenceRepository) {
		t.Fatalf("typed-nil metadata evidence error = %v, want %v", err, ErrMissingMetadataEvidenceRepository)
	}
}

func TestRepositoryCapabilitiesMetadataEvidenceIsNotZero(t *testing.T) {
	t.Parallel()

	adapter := &struct{ MetadataEvidenceRepository }{}
	capabilities := RepositoryCapabilitiesFrom(adapter)
	if capabilities.IsZero() {
		t.Fatal("metadata-evidence-only repository bundle is zero")
	}
	if capabilities.MetadataEvidence() != adapter {
		t.Fatal("repository bundle lost the metadata evidence adapter")
	}
}

func TestRepositoryCapabilityInterfacesDoNotExposeLifecycleOrSQL(t *testing.T) {
	t.Parallel()

	interfaces := []reflect.Type{
		reflect.TypeFor[ReleaseStateRepository](),
		reflect.TypeFor[PreparedReleaseRepository](),
		reflect.TypeFor[MetadataEvidenceRepository](),
		reflect.TypeFor[ReleaseSelectionRepository](),
		reflect.TypeFor[HistoryRepository](),
		reflect.TypeFor[UploadLedgerRepository](),
		reflect.TypeFor[TrackerStateRepository](),
		reflect.TypeFor[MediaAssetRepository](),
		reflect.TypeFor[MediaReuseRepository](),
		reflect.TypeFor[DescriptionReuseRepository](),
		reflect.TypeFor[ReleaseWorkflowStateRepository](),
		reflect.TypeFor[ScreenshotLifecycleRepository](),
	}
	for _, interfaceType := range interfaces {
		if _, ok := interfaceType.MethodByName("Close"); ok {
			t.Errorf("%s exposes Close", interfaceType)
		}
		for method := range interfaceType.Methods() {
			if strings.Contains(method.Type.String(), "database/sql") {
				t.Errorf("%s.%s exposes SQL type %s", interfaceType, method.Name, method.Type)
			}
		}
	}
}
