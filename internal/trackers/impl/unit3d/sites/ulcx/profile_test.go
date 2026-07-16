package ulcx

import (
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestBuildNameRemovesHybridFromWebDV(t *testing.T) {
	meta := api.UploadSubject{
		ReleaseName: "Example Release 2026 Hybrid 1080p WEB-DL DDP5.1 DV H.265-GRP",
		Type:        "WEBDL",
		Edition:     "Hybrid",
		WebDV:       true,
	}
	if got := Profile().Site.BuildName(meta, config.TrackerConfig{}); strings.Contains(got, "Hybrid") {
		t.Fatalf("name = %q", got)
	}
}
