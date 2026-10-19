// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package imagehosting

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image/png"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"net/textproto"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"golang.org/x/net/http/httpguts"
	"golang.org/x/net/publicsuffix"

	"github.com/autobrr/upbrr/internal/httpclient"
	"github.com/autobrr/upbrr/internal/logging"
	"github.com/autobrr/upbrr/internal/redaction"
	"github.com/autobrr/upbrr/pkg/api"
)

// These are local resource limits, not claims about the service's upload limits.
const (
	slowpicsOrigin                    = "https://slow.pics"
	slowpicsMaxImageBytes       int64 = 20 << 20
	slowpicsMaxCollectionBytes  int64 = 100 << 20
	slowpicsMaxImagePixels            = 64 << 20
	slowpicsMaxResponseBytes    int64 = 1 << 20
	slowpicsMaxMemberAttempts         = 3
	slowpicsMaxRetryDelay             = 30 * time.Second
	slowpicsMaxCreateAttempts         = 3
	slowpicsMaxCreateRetryDelay       = 5 * time.Minute
)

// errSlowpicsOutcomeUnknown prevents creating another collection after a create
// attempt with an uncertain result, including a later image upload failure.
var errSlowpicsOutcomeUnknown = errors.New("slow.pics collection may already exist; reconcile before retrying")

type slowpicsCollectionImage struct {
	Name string
	Path string
}

type slowpicsCollectionRequest struct {
	Name    string
	Images  []slowpicsCollectionImage
	Columns int
}

func (r slowpicsCollectionRequest) columns() int {
	// The comparison API rejects one-image rows. A single-source sequence
	// uses one viewer row so every image remains available without duplication.
	if r.Columns == 0 || r.Columns == 1 {
		return len(r.Images)
	}
	return r.Columns
}

// slowpicsCollectionURL is a completed collection page, never a direct image URL.
type slowpicsCollectionURL string

type slowpicsUploader struct {
	client *http.Client
	logger api.Logger
}

type slowpicsImage struct {
	name string
	data []byte
}

type slowpicsCreatedCollection struct {
	CollectionUUID string     `json:"collectionUuid"`
	Key            string     `json:"key"`
	Images         [][]string `json:"images"`
}

type slowpicsResponse struct {
	status      int
	body        []byte
	contentType string
	retryAfter  string
}

// UploadCollection validates and snapshots all PNGs before creating an anonymous
// collection. Only a fully uploaded collection yields a URL. Create requests
// are retried only after an explicit HTTP 429 rejection. Ambiguous creation or
// subsequent member failures retain the unknown-outcome marker.
func (u *slowpicsUploader) UploadCollection(ctx context.Context, input slowpicsCollectionRequest) (slowpicsCollectionURL, error) {
	images, err := validateSlowpicsCollectionRequest(ctx, input)
	if err != nil {
		return "", err
	}
	return u.uploadValidatedCollection(ctx, input, images)
}

// uploadValidatedCollection consumes the same snapshots used by the durable
// coordinator to fingerprint and reserve this upload; it never reopens source files.
func (u *slowpicsUploader) uploadValidatedCollection(
	ctx context.Context,
	input slowpicsCollectionRequest,
	images []slowpicsImage,
) (collectionURL slowpicsCollectionURL, uploadErr error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()
	logger := logging.FromContext(ctx, u.logger)
	stage := "session"
	defer func() {
		if uploadErr != nil {
			// Uploader errors contain fixed diagnostics, never response bodies or session data.
			logger.Warnf("slow.pics: collection upload failed stage=%s outcome_unknown=%t reason=%q",
				stage, errors.Is(uploadErr, errSlowpicsOutcomeUnknown), redaction.RedactValue(uploadErr.Error(), nil))
		}
	}()
	jar, err := cookiejar.New(&cookiejar.Options{PublicSuffixList: publicsuffix.List})
	if err != nil {
		return "", errors.New("slow.pics anonymous session unavailable")
	}
	client := &http.Client{
		Timeout:       httpclient.UploadTimeout,
		Jar:           jar,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}
	if u.client != nil {
		client.Transport = u.client.Transport
	}
	browserID, err := uuid.NewRandom()
	if err != nil {
		return "", errors.New("slow.pics browser identifier unavailable")
	}
	response, err := u.request(ctx, client, http.MethodGet, "/comparison", nil, "")
	if err != nil {
		return "", err
	}
	if response.status != http.StatusOK {
		return "", fmt.Errorf("slow.pics anonymous session unavailable (HTTP %d)", response.status)
	}
	if _, err := slowpicsSessionToken(client, "/upload/comparison"); err != nil {
		return "", err
	}
	fields := map[string]string{
		"collectionName":  input.Name,
		"hentai":          "false",
		"optimizeImages":  "false",
		"stripHdr":        "false",
		"desiredFileType": "image/png",
		"public":          "false",
		"visibility":      "LINK_ONLY",
		"removeAfter":     "",
		"browserId":       browserID.String(),
	}
	columns := input.columns()
	endpoint := "/upload/comparison"
	if len(images) == 1 {
		// A comparison requires two images; a lone plot uses the collection API.
		endpoint = "/upload/collection"
		fields["images[0].name"] = images[0].name
		fields["images[0].hentai"] = "false"
		fields["images[0].sortOrder"] = "0"
	} else {
		for index, image := range images {
			row, column := index/columns, index%columns
			prefix := fmt.Sprintf("comparisons[%d]", row)
			if column == 0 {
				name := fmt.Sprintf("Frame %d", row+1)
				if len(images) == columns {
					name = input.Name
				}
				fields[prefix+".name"] = name
				fields[prefix+".hentai"] = "false"
				fields[prefix+".sortOrder"] = strconv.Itoa(row)
			}
			fields[fmt.Sprintf("%s.images[%d].name", prefix, column)] = image.name
			fields[fmt.Sprintf("%s.images[%d].sortOrder", prefix, column)] = strconv.Itoa(column)
		}
	}
	body, contentType, err := slowpicsMultipart(fields, nil)
	if err != nil {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", fmt.Errorf("slow.pics collection canceled: %w", err)
	}
	stage = "create"
	logger.Debugf("slow.pics: collection layout stage=create mode=%s rows=%d columns=%d members=%d",
		strings.TrimPrefix(endpoint, "/upload/"), len(images)/columns, columns, len(images))
	created, err := u.createCollection(ctx, client, endpoint, body, contentType, len(images), columns)
	if err != nil {
		return "", err
	}
	logger.Debugf("slow.pics: collection created stage=create state=completed members=%d", len(images))
	stage = "member"
	for index, image := range images {
		logger.Debugf("slow.pics: image upload stage=member state=started index=%d count=%d bytes=%d", index+1, len(images), len(image.data))
		if err := u.uploadImage(ctx, client, created.CollectionUUID, created.Images[index/columns][index%columns], browserID.String(), image); err != nil {
			return "", errors.Join(errSlowpicsOutcomeUnknown, fmt.Errorf("slow.pics image %d: %w", index+1, err))
		}
		logger.Debugf("slow.pics: image upload stage=member state=completed index=%d count=%d", index+1, len(images))
	}
	return slowpicsCollectionURL(slowpicsOrigin + "/c/" + created.Key), nil
}

func (u *slowpicsUploader) createCollection(
	ctx context.Context, client *http.Client, endpoint string, body []byte, contentType string, count, columns int,
) (slowpicsCreatedCollection, error) {
	for attempt := range slowpicsMaxCreateAttempts {
		if err := ctx.Err(); err != nil {
			return slowpicsCreatedCollection{}, fmt.Errorf("slow.pics collection canceled: %w", err)
		}
		response, err := u.request(ctx, client, http.MethodPost, endpoint, body, contentType)
		if err != nil {
			return slowpicsCreatedCollection{}, errors.Join(errSlowpicsOutcomeUnknown, err)
		}
		if response.status != http.StatusTooManyRequests {
			created, err := parseSlowpicsCollection(response, count, columns)
			if err != nil {
				return created, errors.Join(errSlowpicsOutcomeUnknown, err)
			}
			return created, nil
		}
		// HTTP 429 rejects this request. Other creation failures may have committed
		// remotely and must never pass through this retry path.
		if attempt == slowpicsMaxCreateAttempts-1 {
			return slowpicsCreatedCollection{}, errors.New("slow.pics collection creation rate limited (HTTP 429); retry later")
		}
		delay, retry := slowpicsRetryDelay(response.retryAfter, time.Now(), time.Duration(attempt+1)*time.Minute, slowpicsMaxCreateRetryDelay)
		if !retry {
			return slowpicsCreatedCollection{}, errors.New("slow.pics collection creation rate limited (HTTP 429); retry delay exceeds the upload limit")
		}
		logging.FromContext(ctx, u.logger).Infof("slow.pics: collection creation stage=create state=rate_limited attempt=%d http_status=429 delay_ms=%d",
			attempt+2, delay.Milliseconds())
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return slowpicsCreatedCollection{}, fmt.Errorf("slow.pics collection canceled: %w", ctx.Err())
		case <-timer.C:
		}
	}
	return slowpicsCreatedCollection{}, errors.New("slow.pics collection creation rate limited (HTTP 429); retry later")
}

// validateSlowpicsCollectionRequest keeps validated bytes in memory so files
// cannot change between preflight and upload. Limits bound encoded and decoded data.
func validateSlowpicsCollectionRequest(ctx context.Context, input slowpicsCollectionRequest) ([]slowpicsImage, error) {
	if !validSlowpicsName(input.Name) || len(input.Images) == 0 || len(input.Images) > 100 {
		return nil, errors.New("slow.pics requires a collection name and between 1 and 100 PNG images")
	}
	if input.columns() < 1 || input.columns() > len(input.Images) || len(input.Images)%input.columns() != 0 {
		return nil, errors.New("slow.pics requires complete comparison rows")
	}
	images := make([]slowpicsImage, 0, len(input.Images))
	var total int64
	for index, image := range input.Images {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("slow.pics validation canceled: %w", err)
		}
		if !validSlowpicsName(image.Name) {
			return nil, fmt.Errorf("slow.pics image %d requires a valid display name", index+1)
		}
		data, err := readSlowpicsPNG(image.Path, min(slowpicsMaxImageBytes, slowpicsMaxCollectionBytes-total))
		if err != nil {
			return nil, fmt.Errorf("slow.pics image %d: %w", index+1, err)
		}
		total += int64(len(data))
		images = append(images, slowpicsImage{name: image.Name, data: data})
	}
	return images, nil
}

func validSlowpicsName(value string) bool {
	return value != "" && value == api.CollectionLabel(value, "")
}

func readSlowpicsPNG(path string, limit int64) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, errors.New("cannot read local PNG")
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > limit {
		return nil, errors.New("local PNG is not a regular file within the upload size limit")
	}
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil || int64(len(data)) > limit {
		return nil, errors.New("cannot read local PNG within the upload size limit")
	}
	config, err := png.DecodeConfig(bytes.NewReader(data))
	if err != nil || config.Width <= 0 || config.Height <= 0 || int64(config.Width)*int64(config.Height) > slowpicsMaxImagePixels {
		return nil, errors.New("local image is not a valid PNG within the pixel limit")
	}
	if _, err := png.Decode(bytes.NewReader(data)); err != nil {
		return nil, errors.New("local PNG is incomplete or corrupt")
	}
	return data, nil
}

func parseSlowpicsCollection(response slowpicsResponse, count, columns int) (slowpicsCreatedCollection, error) {
	var created slowpicsCreatedCollection
	if response.status < http.StatusOK || response.status >= http.StatusMultipleChoices {
		return created, fmt.Errorf("slow.pics collection creation unavailable (HTTP %d)", response.status)
	}
	contentType, _, err := mime.ParseMediaType(response.contentType)
	if err != nil || contentType != "application/json" || json.Unmarshal(response.body, &created) != nil {
		return created, errors.New("slow.pics collection response is not valid JSON")
	}
	if !validSlowpicsUUID(created.CollectionUUID) || !validSlowpicsKey(created.Key) || columns < 1 || count%columns != 0 ||
		len(created.Images) != count/columns {
		return created, errors.New("slow.pics collection response is incomplete or invalid")
	}
	seen := make(map[string]struct{}, count)
	for _, row := range created.Images {
		if len(row) != columns {
			return created, errors.New("slow.pics collection response is incomplete or invalid")
		}
		for _, id := range row {
			normalized := strings.ToLower(id)
			if !validSlowpicsUUID(id) {
				return created, errors.New("slow.pics collection response contains an invalid image identifier")
			}
			if _, exists := seen[normalized]; exists {
				return created, errors.New("slow.pics collection response contains duplicate image identifiers")
			}
			seen[normalized] = struct{}{}
		}
	}
	return created, nil
}

func validSlowpicsUUID(value string) bool {
	parsed, err := uuid.Parse(value)
	return err == nil && parsed != uuid.Nil && parsed.String() == strings.ToLower(value)
}

func validSlowpicsKey(key string) bool {
	if len(key) == 0 || len(key) > 128 {
		return false
	}
	for _, char := range key {
		if char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' || char == '-' || char == '_' {
			continue
		}
		return false
	}
	return true
}

func (u *slowpicsUploader) uploadImage(ctx context.Context, client *http.Client, collectionID, imageID, browserID string, image slowpicsImage) error {
	body, contentType, err := slowpicsMultipart(map[string]string{
		"collectionUuid": collectionID,
		"imageUuid":      imageID,
		"browserId":      browserID,
		"originalName":   "",
	}, image.data)
	if err != nil {
		return err
	}
	for attempt := range slowpicsMaxMemberAttempts {
		response, err := u.request(ctx, client, http.MethodPost, "/upload/image/"+imageID, body, contentType)
		if err != nil {
			return err
		}
		if response.status >= http.StatusOK && response.status < http.StatusMultipleChoices &&
			!strings.Contains(strings.ToLower(response.contentType), "html") &&
			!bytes.HasPrefix(bytes.TrimSpace(response.body), []byte("<")) {
			return nil
		}
		if attempt == slowpicsMaxMemberAttempts-1 ||
			response.status != http.StatusTooManyRequests && (response.status < http.StatusInternalServerError || response.status >= 600) {
			return fmt.Errorf("image upload was not acknowledged (HTTP %d)", response.status)
		}
		delay, retry := slowpicsRetryDelay(response.retryAfter, time.Now(), time.Duration(attempt+1)*time.Second, slowpicsMaxRetryDelay)
		if !retry {
			return errors.New("image upload retry delay exceeds the upload limit")
		}
		logging.FromContext(ctx, u.logger).Debugf("slow.pics: image upload stage=member state=retrying attempt=%d http_status=%d delay_ms=%d",
			attempt+2, response.status, delay.Milliseconds())
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return fmt.Errorf("image upload canceled: %w", ctx.Err())
		case <-timer.C:
		}
	}
	return errors.New("image upload was not acknowledged")
}

func slowpicsRetryDelay(value string, now time.Time, fallback, limit time.Duration) (time.Duration, bool) {
	delay := fallback
	seconds, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	if errors.Is(err, strconv.ErrRange) {
		return 0, false
	}
	if err == nil && seconds >= 0 {
		if seconds > int64(limit/time.Second) {
			return 0, false
		}
		delay = time.Duration(seconds) * time.Second
	} else if when, err := http.ParseTime(value); err == nil {
		delay = max(0, when.Sub(now))
	}
	return delay, delay <= limit
}

func slowpicsMultipart(fields map[string]string, image []byte) ([]byte, string, error) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for name, value := range fields {
		if err := writer.WriteField(name, value); err != nil {
			return nil, "", errors.New("slow.pics cannot prepare upload fields")
		}
	}
	if image != nil {
		header := make(textproto.MIMEHeader)
		header.Set("Content-Disposition", `form-data; name="file"; filename="image.png"`)
		header.Set("Content-Type", "image/png")
		part, err := writer.CreatePart(header)
		if err != nil {
			return nil, "", errors.New("slow.pics cannot prepare PNG upload")
		}
		if _, err := part.Write(image); err != nil {
			return nil, "", errors.New("slow.pics cannot prepare PNG upload")
		}
	}
	if err := writer.Close(); err != nil {
		return nil, "", errors.New("slow.pics cannot finish upload fields")
	}
	return body.Bytes(), writer.FormDataContentType(), nil
}

func (u *slowpicsUploader) request(
	ctx context.Context, client *http.Client, method, endpoint string, body []byte, contentType string,
) (result slowpicsResponse, requestErr error) {
	logger := logging.FromContext(ctx, u.logger)
	stage := "member"
	switch endpoint {
	case "/comparison":
		stage = "session"
	case "/upload/comparison", "/upload/collection":
		stage = "create"
	}
	started := time.Now()
	responseType := "none"
	logger.Tracef("slow.pics: request stage=%s state=started method=%s request_bytes=%d", stage, method, len(body))
	defer func() {
		logger.Debugf("slow.pics: request stage=%s state=finished http_status=%d response_type=%s response_bytes=%d duration_ms=%d failed=%t",
			stage, result.status, responseType, len(result.body), time.Since(started).Milliseconds(), requestErr != nil)
	}()
	request, err := http.NewRequestWithContext(ctx, method, slowpicsOrigin+endpoint, bytes.NewReader(body))
	if err != nil {
		return result, errors.New("slow.pics cannot prepare request")
	}
	request.Header.Set("User-Agent", "upbrr")
	request.Header.Set("Accept", "*/*")
	request.Header.Set("Accept-Language", "en-US,en;q=0.9")
	request.Header.Set("Origin", slowpicsOrigin)
	request.Header.Set("Referer", slowpicsOrigin+"/comparison")
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	if method == http.MethodPost {
		token, err := slowpicsSessionToken(client, endpoint)
		if err != nil {
			return result, err
		}
		request.Header.Set("X-Xsrf-Token", token)
	}
	response, err := client.Do(request)
	if err != nil {
		if err := ctx.Err(); err != nil {
			return result, fmt.Errorf("slow.pics request canceled: %w", err)
		}
		return result, errors.New("slow.pics request failed")
	}
	defer response.Body.Close()
	result.status = response.StatusCode
	result.contentType = response.Header.Get("Content-Type")
	mediaType, _, _ := mime.ParseMediaType(result.contentType)
	switch mediaType {
	case "application/json":
		responseType = "json"
	case "text/html":
		responseType = "html"
	default:
		responseType = "other"
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, slowpicsMaxResponseBytes+1))
	if err != nil || int64(len(data)) > slowpicsMaxResponseBytes {
		return result, errors.New("slow.pics response unavailable or exceeds the size limit")
	}
	return slowpicsResponse{
		status:      response.StatusCode,
		body:        data,
		contentType: response.Header.Get("Content-Type"),
		retryAfter:  response.Header.Get("Retry-After"),
	}, nil
}

// slowpicsSessionToken checks cookies applicable to the actual upload endpoint,
// rather than accepting a token scoped only to the comparison page.
func slowpicsSessionToken(client *http.Client, endpoint string) (string, error) {
	target, err := url.Parse(slowpicsOrigin + endpoint)
	if err != nil || client.Jar == nil {
		return "", errors.New("slow.pics anonymous session token is unavailable")
	}
	for _, cookie := range client.Jar.Cookies(target) {
		if cookie.Name != "XSRF-TOKEN" {
			continue
		}
		token, err := url.PathUnescape(cookie.Value)
		if err != nil || strings.TrimSpace(token) == "" || !httpguts.ValidHeaderFieldValue(token) {
			return "", errors.New("slow.pics anonymous session token is invalid")
		}
		return token, nil
	}
	return "", errors.New("slow.pics anonymous session token is unavailable")
}
