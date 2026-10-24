// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package data

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/png"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/autobrr/upbrr/internal/bbcode"
	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestLookupUnit3DUninspectedImageReferences(t *testing.T) {
	t.Parallel()
	var valid bytes.Buffer
	if err := png.Encode(&valid, image.NewRGBA(image.Rect(0, 0, 1, 1))); err != nil {
		t.Fatal(err)
	}
	// An unknown PNG chunk forces DecodeConfig to consume the existing cap.
	limited := make([]byte, maxImageBytes+1)
	copy(limited, "\x89PNG\r\n\x1a\n")
	binary.BigEndian.PutUint32(limited[8:12], uint32(maxImageBytes))
	copy(limited[12:16], "tEXt")

	for _, tt := range []struct {
		name          string
		rawURL        string
		status        int
		contentType   string
		contentLength int64
		body          []byte
		redirect      string
		custom        bool
		dialTLS       bool
		dialTLSCtx    bool
		tlsNextProto  bool
		cancel        bool
		wantReference bool
		wantValid     bool
		wantRequests  int32
	}{
		{
			name:          "decode unavailable",
			wantReference: true,
			wantRequests:  1,
		},
		{
			name:          "body read failed",
			body:          []byte("\x89PNG\r\n\x1a\n"),
			contentLength: 32,
			wantReference: true,
			wantRequests:  1,
		},
		{
			name:          "no content type",
			contentType:   "-",
			wantReference: true,
			wantRequests:  1,
		},
		{name: "initial private URL", rawURL: "http://127.0.0.1/unknown.png"},
		{name: "initial scheme rejected", rawURL: "ftp://8.8.8.8/unknown.png"},
		{
			name:         "HTTP rejected",
			status:       http.StatusForbidden,
			wantRequests: 1,
		},
		{
			name:         "MIME rejected",
			contentType:  "text/html",
			wantRequests: 1,
		},
		{
			name:          "declared size rejected",
			contentLength: maxImageBytes + 1,
			wantRequests:  1,
		},
		{
			name:         "request failed",
			status:       -1,
			wantRequests: 1,
		},
		{
			name:         "private redirect rejected",
			status:       http.StatusFound,
			redirect:     "http://127.0.0.1/target.png",
			wantRequests: 1,
		},
		{
			name:         "redirect limit rejected",
			status:       http.StatusFound,
			redirect:     "http://93.184.216.34/unknown.png",
			wantRequests: 10,
		},
		{
			name:         "canceled during body inspection",
			cancel:       true,
			wantRequests: 1,
		},
		{
			name:         "inspection limit exhausted",
			body:         limited,
			wantRequests: 1,
		},
		{
			name:         "custom transport not proven",
			custom:       true,
			wantRequests: 1,
		},
		{
			name:         "TLS dial bypass not proven",
			dialTLS:      true,
			wantRequests: 1,
		},
		{
			name:         "TLS context dial bypass not proven",
			dialTLSCtx:   true,
			wantRequests: 1,
		},
		{
			name:         "custom TLS protocol not proven",
			tlsNextProto: true,
			wantRequests: 1,
		},
		{
			name:         "decoded image stays validated",
			body:         valid.Bytes(),
			wantValid:    true,
			wantRequests: 1,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			rawURL := tt.rawURL
			if rawURL == "" {
				rawURL = "http://93.184.216.34/unknown.png"
			}
			sourceDescription := "Release notes\n[url=http://127.0.0.1/page][img]" + rawURL + "[/img][/url]"
			var requests atomic.Int32
			handler := func(req *http.Request) *http.Response {
				if req.URL.Path == "/api/torrents/42" {
					payload, err := json.Marshal(map[string]any{"attributes": map[string]any{
						"imdb_id": 1234567, "description": sourceDescription,
					}})
					if err != nil {
						t.Error(err)
					}
					return &http.Response{
						StatusCode:    http.StatusOK,
						Header:        http.Header{},
						Body:          io.NopCloser(bytes.NewReader(payload)),
						ContentLength: int64(len(payload)),
					}
				}
				requests.Add(1)
				if tt.status == -1 {
					return nil
				}
				status := tt.status
				if status == 0 {
					status = http.StatusOK
				}
				body := tt.body
				if body == nil {
					body = []byte("unknown image format")
				}
				contentType := tt.contentType
				if contentType == "" {
					contentType = "image/png"
				}
				header := http.Header{}
				if contentType != "-" {
					header.Set("Content-Type", contentType)
				}
				if tt.redirect != "" {
					header.Set("Location", tt.redirect)
				}
				if tt.cancel {
					cancel()
				}
				length := tt.contentLength
				if length == 0 {
					length = -1
				}
				return &http.Response{
					StatusCode:    status,
					Header:        header,
					Body:          io.NopCloser(bytes.NewReader(body)),
					ContentLength: length,
				}
			}
			transport := imageReceiptHTTPTransport(t, handler)
			if tt.dialTLS {
				//nolint:staticcheck // SA1019: Exercise rejection of the supported legacy TLS dial hook.
				transport.DialTLS = func(network, address string) (net.Conn, error) {
					conn, err := transport.DialContext(ctx, network, address)
					if err != nil {
						return nil, fmt.Errorf("dial synthetic TLS connection: %w", err)
					}
					return tls.Client(conn, transport.TLSClientConfig), nil
				}
			}
			if tt.dialTLSCtx {
				transport.DialTLSContext = func(ctx context.Context, network, address string) (net.Conn, error) {
					conn, err := transport.DialContext(ctx, network, address)
					if err != nil {
						return nil, fmt.Errorf("dial synthetic TLS connection: %w", err)
					}
					return tls.Client(conn, transport.TLSClientConfig), nil
				}
			}
			if tt.tlsNextProto {
				transport.TLSNextProto = map[string]func(string, *tls.Conn) http.RoundTripper{
					"image-test": func(string, *tls.Conn) http.RoundTripper {
						return imageReceiptRoundTripper(func(req *http.Request) (*http.Response, error) { return handler(req), nil })
					},
				}
			}
			httpClient := &http.Client{Transport: transport}
			if tt.custom {
				httpClient.Transport = imageReceiptRoundTripper(func(req *http.Request) (*http.Response, error) { return handler(req), nil })
			}
			client := NewClientWithRegistry(config.Config{}, api.NopLogger{}, httpClient, testUnit3DRegistry(t, "TEST", "https://93.184.216.34"))
			result, err := client.Lookup(ctx, "TEST", "42", api.UploadSubject{}, "", false, true)
			if (err == nil) != tt.wantValid || result.IMDBID != 1234567 || !strings.Contains(result.Description, "Release notes") {
				t.Fatalf("lookup changed retained data or error: result=%+v err=%v", result, err)
			}
			var want []api.ImportedScreenshotReference
			if tt.wantReference {
				want = []api.ImportedScreenshotReference{{RawURL: rawURL}}
			}
			if !tt.cancel {
				wantFingerprint, fingerprintErr := api.CanonicalWorkflowFingerprint(struct {
					Description string
					RawURLs     []string
				}{Description: sourceDescription, RawURLs: []string{rawURL}})
				if fingerprintErr != nil || result.ImportedImagePolicyVersion != api.ImportedScreenshotEvidencePolicyVersion || result.ImportedImageDescriptionFingerprint != wantFingerprint {
					t.Fatalf("receipt provenance does not bind original description and raw URL: result=%+v err=%v", result, fingerprintErr)
				}
			} else if result.ImportedImagePolicyVersion != "" || result.ImportedImageDescriptionFingerprint != "" {
				t.Fatalf("canceled inspection gained provenance: %+v", result)
			}
			if !slices.Equal(result.UninspectedImages, want) {
				t.Fatalf("uninspected references = %+v, want %+v", result.UninspectedImages, want)
			}
			if tt.wantValid {
				if len(result.Validated) != 1 || result.Validated[0].RawURL != rawURL || !slices.Equal(result.Images, result.Validated) {
					t.Fatalf("decoded image path changed: %+v", result)
				}
			} else if len(result.Images) != 0 || len(result.Validated) != 0 {
				t.Fatalf("uninspected/rejected image became validated: %+v", result)
			}
			if requests.Load() != tt.wantRequests {
				t.Fatalf("image requests = %d, want %d", requests.Load(), tt.wantRequests)
			}
			if tt.cancel && !errors.Is(err, context.Canceled) {
				t.Fatalf("cancellation cause lost: %v", err)
			}
		})
	}
}

func TestCheckImageWithholdsReferenceAfterResponseBodyTimeout(t *testing.T) {
	t.Parallel()
	requestContexts := make(chan context.Context, 1)
	transport := imageReceiptHTTPTransport(t, func(*http.Request) *http.Response {
		requestContext := <-requestContexts
		reader, writer := io.Pipe()
		go func() {
			_, _ = writer.Write([]byte("\x89PNG\r\n\x1a\n"))
			<-requestContext.Done()
			_ = writer.CloseWithError(requestContext.Err())
		}()
		return &http.Response{
			StatusCode:    http.StatusOK,
			Header:        http.Header{"Content-Type": {"image/png"}},
			Body:          reader,
			ContentLength: 32,
		}
	})
	transport.Proxy = func(req *http.Request) (*url.URL, error) {
		requestContexts <- req.Context()
		return nil, nil
	}
	client := Unit3DImageHTTPClient(&http.Client{Transport: transport, Timeout: 100 * time.Millisecond})
	reason, reference := checkImage(t.Context(), client, "http://93.184.216.34/timeout.png", true)
	if reason != "image_decode_failed" || reference != "" || t.Context().Err() != nil {
		t.Fatalf("response-body timeout became a receipt: reason=%q reference=%q parent=%v", reason, reference, t.Context().Err())
	}
}

func TestPrepareDescriptionImageReferencesPreserveOnlyAcceptedRawURLs(t *testing.T) {
	t.Parallel()
	var valid bytes.Buffer
	if err := png.Encode(&valid, image.NewRGBA(image.Rect(0, 0, 1, 1))); err != nil {
		t.Fatal(err)
	}
	var requests atomic.Int32
	client := &http.Client{Transport: imageReceiptHTTPTransport(t, func(req *http.Request) *http.Response {
		requests.Add(1)
		body := []byte("unknown image")
		if req.URL.Path == "/valid.png" {
			body = valid.Bytes()
		}
		return &http.Response{
			StatusCode:    http.StatusOK,
			Header:        http.Header{"Content-Type": {"image/png"}},
			Body:          io.NopCloser(bytes.NewReader(body)),
			ContentLength: int64(len(body)),
		}
	})}
	images := []bbcode.Image{
		{
			RawURL: "https://wsrv.nl/?url=http%3A%2F%2F93.184.216.34%2Ffirst.png",
			ImgURL: "http://127.0.0.1/preview",
			WebURL: "http://127.0.0.1/page",
		},
		{RawURL: "http://93.184.216.34/valid.png"},
		{
			RawURL: "http://93.184.216.34/last.png",
			ImgURL: "http://127.0.0.1/preview",
			WebURL: "http://127.0.0.1/page",
		},
	}
	validated, references, err := prepareDescriptionImages(t.Context(), client, "TEST", nil, images)
	if err == nil || len(validated) != 3 || validated[0] != (bbcode.Image{}) || validated[1] != images[1] || validated[2] != (bbcode.Image{}) {
		t.Fatalf("validated positions changed: images=%+v err=%v", validated, err)
	}
	want := []api.ImportedScreenshotReference{{RawURL: "http://93.184.216.34/first.png"}, {RawURL: "http://93.184.216.34/last.png"}}
	if !slices.Equal(references, want) || requests.Load() != 3 {
		t.Fatalf("reference URLs or request count changed: references=%+v requests=%d", references, requests.Load())
	}
}

func TestPrepareDescriptionImagesKeepsUnknownInspectionRejected(t *testing.T) {
	t.Parallel()
	client := &http.Client{Transport: imageReceiptHTTPTransport(t, func(*http.Request) *http.Response {
		return &http.Response{
			StatusCode:    http.StatusOK,
			Header:        http.Header{"Content-Type": {"image/png"}},
			Body:          io.NopCloser(strings.NewReader("unknown image")),
			ContentLength: -1,
		}
	})}
	images, err := PrepareDescriptionImages(t.Context(), client, "TEST", nil, []bbcode.Image{{RawURL: "http://93.184.216.34/unknown.png"}})
	if err == nil || len(images) != 0 {
		t.Fatalf("legacy preparation admitted unavailable image inspection: images=%+v err=%v", images, err)
	}
}

// imageReceiptHTTPTransport exercises the standard public-dial wrapper without
// opening a socket. The pipe serves one HTTP response and then closes, including
// deliberately truncated responses used to exercise body-read failures.
func imageReceiptHTTPTransport(t *testing.T, handler func(*http.Request) *http.Response) *http.Transport {
	t.Helper()
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		IPAddresses:  []net.IP{net.ParseIP("93.184.216.34")},
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, publicKey, privateKey)
	if err != nil {
		t.Fatal(err)
	}
	certificate, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(certificate)
	serverTLS := &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: privateKey}}}
	transport := &http.Transport{
		DisableKeepAlives: true,
		TLSClientConfig: &tls.Config{
			MinVersion: tls.VersionTLS12,
			RootCAs:    roots,
			ServerName: "93.184.216.34",
		},
		DialContext: func(_ context.Context, _, address string) (net.Conn, error) {
			client, pipe := net.Pipe()
			server := pipe
			if strings.HasSuffix(address, ":443") {
				server = tls.Server(pipe, serverTLS)
			}
			go func() {
				defer server.Close()
				req, err := http.ReadRequest(bufio.NewReader(server))
				if err != nil {
					return
				}
				defer req.Body.Close()
				response := handler(req)
				if response == nil {
					return
				}
				defer response.Body.Close()
				response.ProtoMajor = 1
				response.ProtoMinor = 1
				response.Close = true
				_ = response.Write(server)
			}()
			return client, nil
		},
	}
	t.Cleanup(transport.CloseIdleConnections)
	return transport
}

type imageReceiptRoundTripper func(*http.Request) (*http.Response, error)

func (f imageReceiptRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func validateImages(ctx context.Context, client *http.Client, images []bbcode.Image, tracker string, logger api.Logger) []bbcode.Image {
	validated, _ := validateImagesWithReferences(ctx, client, images, tracker, logger)
	return validated
}
