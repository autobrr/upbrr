// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package gpw

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/iotest"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/commonhttp"
	"github.com/autobrr/upbrr/internal/trackers/submission"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestHTMLResponseRetainsSafeArtifactAndUnknownOutcome(t *testing.T) {
	for _, status := range []int{200, 502} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			const secret = "SYNTHETIC-PRIVATE-CREDENTIAL"
			const hidden = "SYNTHETIC-CSRF-VALUE"
			encoded := strings.ReplaceAll(secret, "-", "&#45;")
			requests := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodPost {
					requests++
				}
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				w.Header().Set("Set-Cookie", "session="+secret)
				w.WriteHeader(status)
				_, _ = fmt.Fprintf(w, `<html><body><p>Upload temporarily unavailable %s https://private.example/hidden?secret=%s</p><form><input type="hidden" name="csrf" value="%s"><input value="%s"></form><script>%s</script><p>%s%s</p></body></html>`, encoded, secret, hidden, encoded, hidden, strings.Repeat("x", int(commonhttp.DefaultResponsePreviewBytes)-20), secret)
			}))
			defer server.Close()
			root := t.TempDir()
			input := trackers.PreparationInput{
				Meta:          api.UploadSubject{SourcePath: filepath.Join(root, "Example.Release.mkv")},
				Runtime:       trackers.PreparationRuntime{DBPath: filepath.Join(root, "db.sqlite")},
				TrackerConfig: config.TrackerConfig{APIKey: secret},
				Logger:        api.NopLogger{},
			}
			_, err := submitPreparedUpload(t.Context(), input, uploadState{}, nil, "application/octet-stream", server.URL, "", "")
			if err == nil || requests != 1 || submission.OutcomeOf(err) != submission.Unknown || !strings.Contains(err.Error(), "non-JSON") || !strings.Contains(err.Error(), "text/html") {
				t.Fatal("HTML response lost safe context or submission uncertainty")
			}
			artifacts := 0
			walkErr := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
				if walkErr != nil {
					return walkErr
				}
				if !entry.IsDir() && strings.Contains(entry.Name(), "upload_failure") {
					artifacts++
					body, readErr := os.ReadFile(path)
					if readErr != nil {
						return fmt.Errorf("read diagnostic artifact: %w", readErr)
					}
					for _, text := range []string{err.Error(), string(body)} {
						if strings.Contains(text, secret) || strings.Contains(text, "SYNTHETIC-PRIVATE") || strings.Contains(text, "private.example") || strings.Contains(text, hidden) || strings.Contains(text, encoded) {
							t.Fatal("diagnostics exposed private context")
						}
						if !strings.Contains(text, "Upload temporarily unavailable") {
							t.Fatal("diagnostics discarded the safe visible failure")
						}
					}
				}
				return nil
			})
			if walkErr != nil || artifacts != 1 {
				t.Fatal("safe failure artifact was not retained")
			}
		})
	}
}

type diagnosticResponseBody struct {
	io.Reader
	closeErr error
}

func (b diagnosticResponseBody) Close() error { return b.closeErr }

func TestResponseReadFailureCannotExposePartialBodyOrEraseCompleteReceipt(t *testing.T) {
	for _, complete := range []bool{false, true} {
		t.Run(fmt.Sprintf("complete=%t", complete), func(t *testing.T) {
			old := http.DefaultTransport
			defer func() { http.DefaultTransport = old }()
			var reader io.Reader = strings.NewReader(`{"status":200,"response":{"torrent_id":"123"}}`)
			if !complete {
				reader = io.MultiReader(strings.NewReader("SYNTHETIC-PRIVATE"), iotest.ErrReader(errors.New("read failed")))
			}
			http.DefaultTransport = importTestTransport(func(r *http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": {"application/json"}},
					Request:    r,
					Body:       diagnosticResponseBody{Reader: reader, closeErr: errors.New("close failed")},
				}, nil
			})
			summary, err := submitPreparedUpload(t.Context(), trackers.PreparationInput{TrackerConfig: config.TrackerConfig{APIKey: "SYNTHETIC-PRIVATE-CREDENTIAL"}, Logger: api.NopLogger{}}, uploadState{}, nil, "application/octet-stream", baseURL+"/api.php", "", "")
			if complete {
				if err != nil || summary.Uploaded != 1 {
					t.Fatal("close error erased complete terminal receipt")
				}
				return
			}
			if err == nil || submission.OutcomeOf(err) != submission.Unknown || strings.Contains(err.Error(), "SYNTHETIC") || !strings.Contains(err.Error(), "incomplete") {
				t.Fatal("partial read exposed content or submission authority")
			}
		})
	}
}

func TestIncompleteDiagnosticOmitsPartialSecretAndPrivateDestination(t *testing.T) {
	result := commonhttp.UploadExecution{
		StatusCode:   200,
		Header:       http.Header{"Content-Type": {"text/html"}},
		ProtocolBody: []byte("SYNTHETIC-PRIVATE"),
		FinalURL:     "https://private.example/hidden/endpoint?x=secret",
	}
	text := uploadDiagnostic(result, "SYNTHETIC-PRIVATE-CREDENTIAL")
	if strings.Contains(text, "SYNTHETIC") || strings.Contains(text, "private.example") || !strings.Contains(text, "incomplete") {
		t.Fatal("incomplete response exposed partial content")
	}
}
