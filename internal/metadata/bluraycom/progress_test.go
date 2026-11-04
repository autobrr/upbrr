// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package bluraycom

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	internalerrors "github.com/autobrr/upbrr/internal/errors"
	"github.com/autobrr/upbrr/internal/metadata/evidence"
	"github.com/autobrr/upbrr/internal/metadata/progress"
	"github.com/autobrr/upbrr/pkg/api"
)

type pageEvidenceStore map[string]api.MetadataEvidence

func (s pageEvidenceStore) LoadMetadataEvidence(_ context.Context, _, _, domain, key string) (api.MetadataEvidence, error) {
	v, ok := s[domain+key]
	if !ok {
		return v, internalerrors.ErrNotFound
	}
	return v, nil
}
func (s pageEvidenceStore) SaveMetadataEvidence(_ context.Context, v api.MetadataEvidence) error {
	s[v.Domain+v.Key] = v
	return nil
}

func TestLookupProgressDiscoveryWaitingPartialAndRetainedPages(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var mu sync.Mutex
		var updates []api.PreparationProgressUpdate
		observed := api.WithPreparationProgressReporter(t.Context(), func(u api.PreparationProgressUpdate) { mu.Lock(); defer mu.Unlock(); updates = append(updates, u) })
		requests := 0
		client := NewClient(&http.Client{Transport: releaseFixtureTransport(func(r *http.Request) (*http.Response, error) {
			requests++
			var body strings.Builder
			switch r.URL.Path {
			case "/search/":
				for i := 1; i <= 2; i++ {
					fmt.Fprintf(&body, `<div class="figure"><a class="alphaborder" href="https://www.blu-ray.com/movies/Example/%d/">Example</a></div>`, i)
				}
			case "/products/menu_ajax.php":
				body.WriteString(`<h3>Blu-ray Editions</h3>`)
				for i := range 50 {
					fmt.Fprintf(&body, `<img width="18" height="12" title="United States"><a href="https://www.blu-ray.com/movies/Edition/%s%02d/">Edition</a>`, r.URL.Query().Get("p"), i)
				}
			default:
				if strings.Contains(r.URL.Path, "105/") {
					time.Sleep(6 * time.Second)
				}
				if strings.Contains(r.URL.Path, "106/") {
					return nil, errors.New("synthetic request failure")
				}
				body.WriteString(`<a href="/movies/releasedates.php?year=2020&month=12#December01">Dec 1, 2020</a>`)
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(body.String())),
				Header:     make(http.Header),
				Request:    r,
			}, nil
		})})
		store := pageEvidenceStore{}
		source := filepath.Join(t.TempDir(), "Example.2020.USA.BluRay")
		var first *api.BlurayMetadata
		for pass := range 2 {
			ctx, scope := evidence.WithScope(observed, store, source, "fingerprint", api.ExternalFreshnessReuse, nil)
			ctx, finish := progress.StartStage(ctx, api.PreparationPhaseMediaFacts)
			result, err := client.Lookup(ctx, LookupInput{
				Details:    &api.BlurayDetailRequest{All: true},
				IMDBID:     1234567,
				DiscType:   "BDMV",
				Resolution: "1080p",
			})
			finish(err)
			if err != nil || scope.Err() != nil {
				t.Fatalf("lookup: %v %v", err, scope.Err())
			}
			if len(result.Candidates) != 100 || result.Candidates[len(result.Candidates)-1].DetailsStatus != "failed" {
				t.Fatalf("partial candidates = %#v", result)
			}
			if pass == 0 {
				first = result
			} else if result.Candidates[0].ReleaseDate != first.Candidates[0].ReleaseDate {
				t.Fatal("cached edition date changed")
			}
		}
		if requests != 103 {
			t.Fatalf("requests=%d, want 103 (second pass must reuse)", requests)
		}
		mu.Lock()
		defer mu.Unlock()
		known, waiting, reused, partial := false, false, false, false
		for _, u := range updates {
			if u.Substep == "Read Blu-ray.com release details" {
				known = known || (u.CompletedItems == 100 && u.TotalItems == 100)
				waiting = waiting || (u.CompletedItems == 5 && u.ElapsedSeconds >= 5)
			}
			reused = reused || strings.Contains(u.Message, "retained")
			partial = partial || strings.Contains(u.Message, "1 unavailable") || strings.Contains(u.Message, "1 editions unavailable")
		}
		if !known || !waiting || !reused || !partial {
			t.Fatalf("known=%v waiting=%v retained=%v partial=%v", known, waiting, reused, partial)
		}
	})
}
