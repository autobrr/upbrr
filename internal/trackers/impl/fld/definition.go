package fld

import (
	"context"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

type Definition struct{}

func New() *Definition {
	return &Definition{}
}

func (Definition) Name() string {
	return "FLD"
}

func (Definition) Upload(ctx context.Context, req trackers.UploadRequest) (api.UploadSummary, error) {
	return upload(ctx, req)
}

func (Definition) BuildUploadDryRun(ctx context.Context, req trackers.UploadRequest) (api.TrackerDryRunEntry, error) {
	return buildUploadDryRun(ctx, req)
}

func (Definition) BuildDescription(ctx context.Context, req trackers.DescriptionRequest) (trackers.DescriptionResult, error) {
	assets, err := trackers.ResolveDescriptionAssets(ctx, req.Tracker, req.Meta, req.Repo, req.Logger)
	if err != nil {
		assets = trackers.DescriptionAssets{}
	}
	description := buildDescription(trackers.UploadRequest{
		Tracker:       req.Tracker,
		Meta:          req.Meta,
		TrackerConfig: req.TrackerConfig,
		AppConfig:     req.AppConfig,
		Logger:        req.Logger,
		Repo:          req.Repo,
	}, assets)
	return trackers.DescriptionResult{Group: "fld", Description: description}, nil
}
