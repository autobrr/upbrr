// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package webserver

import (
	"github.com/autobrr/upbrr/pkg/api"
)

type apiStatusResponse struct {
	OK        bool   `json:"ok"`
	Version   string `json:"version"`
	CoreReady bool   `json:"coreReady"`
}

type apiErrorResponse struct {
	Error string `json:"error"`
	Code  string `json:"code,omitempty"`
}

type apiOKResponse struct {
	OK bool `json:"ok"`
}

type apiAuthStatusResponse struct {
	Authenticated           bool   `json:"authenticated"`
	NeedsSetup              bool   `json:"needsSetup"`
	Username                string `json:"username"`
	BearerToken             string `json:"bearerToken,omitempty"`
	NativeBrowseEnabled     bool   `json:"nativeBrowseEnabled"`
	BrowseRoot              string `json:"browseRoot"`
	AllowUnrestrictedBrowse bool   `json:"allowUnrestrictedBrowse"`
	NeedsBrowsePolicy       bool   `json:"needsBrowsePolicy"`
}

type apiAuthCredentialsRequest struct {
	Username    string `json:"username"`
	Password    string `json:"password"`
	RetainLogin bool   `json:"retainLogin"`
}

type apiBrowsePolicyRequest struct {
	BrowseRoot              string `json:"browseRoot"`
	AllowUnrestrictedBrowse bool   `json:"allowUnrestrictedBrowse"`
}

type apiTokenRequest struct {
	Name string `json:"name"`
}

type apiTokenStatusResponse struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	CreatedAt  string `json:"createdAt"`
	LastUsedAt string `json:"lastUsedAt,omitempty"`
	RevokedAt  string `json:"revokedAt,omitempty"`
}

type apiCreatedTokenResponse struct {
	Token  string                 `json:"token"`
	Record apiTokenStatusResponse `json:"record"`
}

type apiWebAuthStatusResponse struct {
	Path                    string                   `json:"path"`
	Exists                  bool                     `json:"exists"`
	Usable                  bool                     `json:"usable"`
	CanCreate               bool                     `json:"canCreate"`
	Username                string                   `json:"username"`
	AllowUnencryptedExport  bool                     `json:"allowUnencryptedExport"`
	BrowseRoot              string                   `json:"browseRoot"`
	AllowUnrestrictedBrowse bool                     `json:"allowUnrestrictedBrowse"`
	EncryptionEnabled       bool                     `json:"encryptionEnabled"`
	APITokens               []apiTokenStatusResponse `json:"apiTokens"`
	Message                 string                   `json:"message"`
}

type uiStateSaveRequest struct {
	ID    string      `json:"id"`
	Label string      `json:"label"`
	State api.UIState `json:"state"`
}

type pathRequest struct {
	Path string `json:"path"`
}

type sourcePathRequest struct {
	SourcePath string `json:"sourcePath"`
}

type jobIDResponse struct {
	JobID string `json:"jobID"`
}

type configPayloadRequest struct {
	Payload string `json:"payload"`
}

type importConfigRequest struct {
	FileName    string `json:"fileName"`
	FileContent string `json:"fileContent"`
}

type importConfigResponse struct {
	Result   string   `json:"result"`
	Warnings []string `json:"warnings"`
}

type recentLogsRequest struct {
	Limit int `json:"limit"`
}

type logExclusionsRequest struct {
	Patterns []string `json:"patterns"`
}

type contentRequest struct {
	Path          string                   `json:"path"`
	Overrides     api.ExternalIDOverrides  `json:"overrides"`
	NameOverrides api.ReleaseNameOverrides `json:"nameOverrides"`
	Trackers      []string                 `json:"trackers,omitempty"`
}

type metadataRequest struct {
	Path              string                   `json:"path"`
	SourceLookupURL   string                   `json:"sourceLookupURL"`
	Overrides         api.ExternalIDOverrides  `json:"overrides"`
	NameOverrides     api.ReleaseNameOverrides `json:"nameOverrides"`
	Trackers          []string                 `json:"trackers"`
	ConfirmBDMVRescan bool                     `json:"confirmBDMVRescan"`
}

type preparationRequest struct {
	Path           string                   `json:"path"`
	Overrides      api.ExternalIDOverrides  `json:"overrides"`
	NameOverrides  api.ReleaseNameOverrides `json:"nameOverrides"`
	Trackers       []string                 `json:"trackers"`
	IgnoreDupesFor []string                 `json:"ignoreDupesFor"`
}

type trackerDryRunRequest struct {
	Path                 string                        `json:"path"`
	Overrides            api.ExternalIDOverrides       `json:"overrides"`
	NameOverrides        api.ReleaseNameOverrides      `json:"nameOverrides"`
	Trackers             []string                      `json:"trackers"`
	IgnoreRuleFailures   bool                          `json:"ignoreRuleFailures"`
	IgnoreDupesFor       []string                      `json:"ignoreDupesFor"`
	QuestionnaireAnswers map[string]map[string]string  `json:"questionnaireAnswers"`
	DescriptionGroups    []api.DescriptionBuilderGroup `json:"descriptionGroups"`
	Debug                bool                          `json:"debug"`
	RunLogLevel          string                        `json:"runLogLevel"`
}

type renderDescriptionRequest struct {
	Raw string `json:"raw"`
}

type saveDescriptionOverrideRequest struct {
	Path          string                   `json:"path"`
	GroupKey      string                   `json:"groupKey"`
	Raw           string                   `json:"raw"`
	Trackers      []string                 `json:"trackers"`
	Overrides     api.ExternalIDOverrides  `json:"overrides"`
	NameOverrides api.ReleaseNameOverrides `json:"nameOverrides"`
}

type playlistSelectionRequest struct {
	Path      string   `json:"path"`
	Playlists []string `json:"playlists"`
	UseAll    bool     `json:"useAll"`
}

type screenshotPlanRequest struct {
	Path          string                   `json:"path"`
	Overrides     api.ExternalIDOverrides  `json:"overrides"`
	NameOverrides api.ReleaseNameOverrides `json:"nameOverrides"`
}

type generateScreenshotsRequest struct {
	Path          string                    `json:"path"`
	Overrides     api.ExternalIDOverrides   `json:"overrides"`
	NameOverrides api.ReleaseNameOverrides  `json:"nameOverrides"`
	Selections    []api.ScreenshotSelection `json:"selections"`
	Purpose       api.ScreenshotPurpose     `json:"purpose"`
}

type previewScreenshotFrameRequest struct {
	Path             string                   `json:"path"`
	Overrides        api.ExternalIDOverrides  `json:"overrides"`
	NameOverrides    api.ReleaseNameOverrides `json:"nameOverrides"`
	TimestampSeconds float64                  `json:"timestampSeconds"`
}

type screenshotImageRequest struct {
	Path          string                   `json:"path"`
	Overrides     api.ExternalIDOverrides  `json:"overrides"`
	NameOverrides api.ReleaseNameOverrides `json:"nameOverrides"`
	ImagePath     string                   `json:"imagePath"`
}

type trackerImageURLRequest struct {
	Path          string                   `json:"path"`
	Overrides     api.ExternalIDOverrides  `json:"overrides"`
	NameOverrides api.ReleaseNameOverrides `json:"nameOverrides"`
	URL           string                   `json:"url"`
}

type finalScreenshotSelectionsRequest struct {
	Path          string                   `json:"path"`
	Overrides     api.ExternalIDOverrides  `json:"overrides"`
	NameOverrides api.ReleaseNameOverrides `json:"nameOverrides"`
	Images        []api.ScreenshotImage    `json:"images"`
}

type uploadImagesRequest struct {
	Path          string                   `json:"path"`
	Overrides     api.ExternalIDOverrides  `json:"overrides"`
	NameOverrides api.ReleaseNameOverrides `json:"nameOverrides"`
	Host          string                   `json:"host"`
	Images        []api.ScreenshotImage    `json:"images"`
}

type deleteUploadedImageRequest struct {
	Path      string `json:"path"`
	ImagePath string `json:"imagePath"`
	Host      string `json:"host"`
}

type trackerUploadRequest struct {
	Path                 string                        `json:"path"`
	Overrides            api.ExternalIDOverrides       `json:"overrides"`
	NameOverrides        api.ReleaseNameOverrides      `json:"nameOverrides"`
	Trackers             []string                      `json:"trackers"`
	IgnoreRuleFailures   bool                          `json:"ignoreRuleFailures"`
	IgnoreDupesFor       []string                      `json:"ignoreDupesFor"`
	QuestionnaireAnswers map[string]map[string]string  `json:"questionnaireAnswers"`
	DescriptionGroups    []api.DescriptionBuilderGroup `json:"descriptionGroups"`
	Debug                bool                          `json:"debug"`
	RunLogLevel          string                        `json:"runLogLevel"`
}
