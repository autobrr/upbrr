// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package imagehosting

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/httpclient"
)

type uploadResult struct {
	ImgURL string
	RawURL string
	WebURL string
}

type uploader interface {
	Upload(ctx context.Context, imagePath string) (uploadResult, error)
}

type batchUploader interface {
	UploadBatch(ctx context.Context, imagePaths []string) ([]uploadResult, error)
}

type namedBatchUploader interface {
	UploadBatchWithName(ctx context.Context, imagePaths []string, galleryName string) ([]uploadResult, error)
}

func newUploaderRegistry(cfg config.Config, client *http.Client) map[string]uploader {
	client = httpclient.CloneWithTimeout(client, httpclient.UploadTimeout)
	return map[string]uploader{
		"imgbb":        &imgbbUploader{apiKey: cfg.ImageHosting.ImgBBAPI, client: client},
		"imgbox":       &imgboxUploader{client: client},
		"hdb":          &hdbUploader{username: cfg.Trackers.Trackers["HDB"].Username, passkey: cfg.Trackers.Trackers["HDB"].Passkey, client: client},
		"ptpimg":       &ptpImgUploader{apiKey: cfg.ImageHosting.PTPImgAPI, client: client},
		"pixhost":      &pixhostUploader{client: client},
		"lensdump":     &lensdumpUploader{apiKey: cfg.ImageHosting.LensdumpAPI, client: client},
		"ptscreens":    &ptScreensUploader{apiKey: cfg.ImageHosting.PTScreensAPI, client: client},
		"onlyimage":    &onlyImageUploader{apiKey: cfg.ImageHosting.OnlyImageAPI, client: client},
		"dalexni":      &dalexniUploader{apiKey: cfg.ImageHosting.DalexniAPI, client: client},
		"zipline":      &ziplineUploader{apiKey: cfg.ImageHosting.ZiplineAPIKey, url: cfg.ImageHosting.ZiplineURL, client: client},
		"passtheimage": &passTheImageUploader{apiKey: cfg.ImageHosting.PassTheImageAPI, client: client},
		"seedpool_cdn": &seedpoolUploader{apiKey: cfg.ImageHosting.SeedpoolCDNAPI, client: client},
		"sharex":       &shareXUploader{apiKey: cfg.ImageHosting.ShareXAPIKey, url: cfg.ImageHosting.ShareXURL, client: client},
		"thr":          &thrUploader{apiKey: cfg.Trackers.Trackers["THR"].ImgAPI, client: client},
		"utppm":        &utppmUploader{apiKey: cfg.ImageHosting.UTPPMAPI, client: client},
	}
}

type imgbbUploader struct {
	apiKey string
	client *http.Client
}

func (u *imgbbUploader) Upload(ctx context.Context, imagePath string) (uploadResult, error) {
	if strings.TrimSpace(u.apiKey) == "" {
		return uploadResult{}, errors.New("image hosting: imgbb api key missing")
	}
	encoded, err := readBase64(imagePath)
	if err != nil {
		return uploadResult{}, err
	}

	form := url.Values{}
	form.Set("key", strings.TrimSpace(u.apiKey))
	form.Set("image", encoded)

	body, status, err := postForm(ctx, u.client, "https://api.imgbb.com/1/upload", form, nil)
	if err != nil {
		return uploadResult{}, err
	}
	if status != http.StatusOK {
		return uploadResult{}, fmt.Errorf("imgbb upload failed with status %d", status)
	}

	var response struct {
		Success bool `json:"success"`
		Data    struct {
			Image struct {
				URL string `json:"url"`
			} `json:"image"`
			Thumb struct {
				URL string `json:"url"`
			} `json:"thumb"`
			Medium struct {
				URL string `json:"url"`
			} `json:"medium"`
			URLViewer string `json:"url_viewer"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return uploadResult{}, fmt.Errorf("imgbb invalid response: %w", err)
	}
	if !response.Success {
		return uploadResult{}, errors.New("imgbb upload failed")
	}
	imgURL := response.Data.Medium.URL
	if strings.TrimSpace(imgURL) == "" {
		imgURL = response.Data.Thumb.URL
	}

	return uploadResult{
		ImgURL: imgURL,
		RawURL: response.Data.Image.URL,
		WebURL: response.Data.URLViewer,
	}, nil
}

type imgboxUploader struct {
	client *http.Client
}

func (u *imgboxUploader) Upload(ctx context.Context, imagePath string) (uploadResult, error) {
	csrfToken, cookie, err := imgboxGetCsrfAndCookie(ctx, u.client)
	if err != nil {
		return uploadResult{}, err
	}
	uploadToken, err := imgboxGetUploadToken(ctx, u.client, csrfToken, cookie)
	if err != nil {
		return uploadResult{}, err
	}

	fields := map[string]string{
		"token_id":         uploadToken.TokenID,
		"token_secret":     uploadToken.TokenSecret,
		"gallery_id":       uploadToken.GalleryID,
		"gallery_secret":   uploadToken.GallerySecret,
		"content_type":     "1",
		"thumbnail_size":   "350r",
		"comments_enabled": "0",
	}
	headers := imgboxHeaders(cookie, map[string]string{"X-CSRF-Token": csrfToken})
	body, status, err := postMultipart(ctx, u.client, "https://imgbox.com/upload/process", fields, "files[]", imagePath, headers)
	if err != nil {
		return uploadResult{}, fmt.Errorf("imgbox HTTP request failed: %w", err)
	}
	if status != http.StatusOK {
		// Try to extract error message from response
		bodyStr := string(body)
		if len(bodyStr) > 200 {
			bodyStr = bodyStr[:200] + "..."
		}
		return uploadResult{}, fmt.Errorf("imgbox upload failed with status %d, response: %s", status, bodyStr)
	}

	var response struct {
		OK    bool `json:"ok"`
		Files []struct {
			OriginalURL  string `json:"original_url"`
			ThumbnailURL string `json:"thumbnail_url"`
			ImageURL     string `json:"image_url"`
			GalleryURL   string `json:"gallery_url"`
		} `json:"files"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		bodyStr := string(body)
		if len(bodyStr) > 200 {
			bodyStr = bodyStr[:200] + "..."
		}
		return uploadResult{}, fmt.Errorf("imgbox invalid JSON response: %w, body: %s", err, bodyStr)
	}
	if !response.OK && len(response.Files) == 0 {
		errMsg := "unknown error"
		if response.Error != "" {
			errMsg = response.Error
		}
		bodyStr := string(body)
		if len(bodyStr) > 200 {
			bodyStr = bodyStr[:200] + "..."
		}
		return uploadResult{}, fmt.Errorf("imgbox upload rejected: %s (response: %s)", errMsg, bodyStr)
	}
	if len(response.Files) == 0 {
		return uploadResult{}, errors.New("imgbox returned no files in response")
	}

	file := response.Files[0]
	if file.OriginalURL == "" || file.ThumbnailURL == "" {
		return uploadResult{}, fmt.Errorf("imgbox returned incomplete URLs (original: %q, thumbnail: %q)", file.OriginalURL, file.ThumbnailURL)
	}
	webURL := file.ImageURL
	if webURL == "" {
		webURL = file.GalleryURL
	}

	return uploadResult{
		ImgURL: file.ThumbnailURL,
		RawURL: file.OriginalURL,
		WebURL: webURL,
	}, nil
}

type imgboxUploadToken struct {
	TokenID       string
	TokenSecret   string
	GalleryID     string
	GallerySecret string
}

type hdbUploader struct {
	username string
	passkey  string
	client   *http.Client
}

var hdbUploadResultPattern = regexp.MustCompile(`\[url=([^\]]+)\]\[img\]([^\[]+)\[/img\]\[/url\]`)

func (u *hdbUploader) Upload(ctx context.Context, imagePath string) (uploadResult, error) {
	results, err := u.uploadBatchWithGalleryName(ctx, []string{imagePath}, filepath.Base(imagePath))
	if err != nil {
		return uploadResult{}, err
	}
	return results[0], nil
}

func (u *hdbUploader) UploadBatch(ctx context.Context, imagePaths []string) ([]uploadResult, error) {
	if len(imagePaths) == 0 {
		return nil, errors.New("image hosting: no HDB images to upload")
	}
	galleryName := buildHDBGalleryName(imagePaths)
	return u.UploadBatchWithName(ctx, imagePaths, galleryName)
}

func (u *hdbUploader) UploadBatchWithName(ctx context.Context, imagePaths []string, galleryName string) ([]uploadResult, error) {
	if len(imagePaths) == 0 {
		return nil, errors.New("image hosting: no HDB images to upload")
	}
	galleryName = strings.TrimSpace(galleryName)
	if galleryName == "" {
		galleryName = buildHDBGalleryName(imagePaths)
	}
	return u.uploadBatchWithGalleryName(ctx, imagePaths, galleryName)
}

func (u *hdbUploader) uploadBatchWithGalleryName(ctx context.Context, imagePaths []string, galleryName string) ([]uploadResult, error) {
	if strings.TrimSpace(u.username) == "" || strings.TrimSpace(u.passkey) == "" {
		return nil, errors.New("image hosting: hdb username/passkey missing")
	}
	fileFields := make(map[string]string, len(imagePaths))
	for idx, imagePath := range imagePaths {
		fileFields[fmt.Sprintf("images_files[%d]", idx)] = imagePath
	}
	body, status, err := postMultipartWithFields(ctx, u.client, "https://img.hdbits.org/upload_api.php", map[string]string{
		"username":      strings.TrimSpace(u.username),
		"passkey":       strings.TrimSpace(u.passkey),
		"galleryoption": "1",
		"galleryname":   galleryName,
		"thumbsize":     "w300",
	}, fileFields, nil)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("hdb upload failed with status %d", status)
	}
	results, err := parseHDBUploadResults(body)
	if err != nil {
		return nil, err
	}
	if len(results) != len(imagePaths) {
		return nil, fmt.Errorf("hdb upload returned %d images for %d uploads", len(results), len(imagePaths))
	}
	return results, nil
}

func buildHDBGalleryName(imagePaths []string) string {
	first := filepath.Base(strings.TrimSpace(imagePaths[0]))
	if first == "" {
		return "upbrr"
	}
	ext := filepath.Ext(first)
	base := strings.TrimSpace(strings.TrimSuffix(first, ext))
	if base == "" {
		base = "upbrr"
	}
	return base
}

func parseHDBUploadResults(body []byte) ([]uploadResult, error) {
	matches := hdbUploadResultPattern.FindAllStringSubmatch(string(body), -1)
	if len(matches) == 0 {
		return nil, errors.New("hdb upload did not return image bbcode")
	}
	results := make([]uploadResult, 0, len(matches))
	for _, match := range matches {
		if len(match) != 3 {
			return nil, errors.New("hdb upload returned malformed image bbcode")
		}
		rawURL := strings.TrimSpace(match[2])
		rawURL = strings.Replace(rawURL, "://t.hdbits.org/", "://img.hdbits.org/", 1)
		results = append(results, uploadResult{
			ImgURL: match[2],
			RawURL: rawURL,
			WebURL: match[1],
		})
	}
	return results, nil
}

func imgboxGetCsrfAndCookie(ctx context.Context, client *http.Client) (string, string, error) {
	client = httpclient.CloneWithTimeout(client, httpclient.UploadTimeout)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://imgbox.com/", nil)
	if err != nil {
		return "", "", fmt.Errorf("imgbox create csrf request: %w", err)
	}
	for key, value := range imgboxHeaders("", nil) {
		req.Header.Set(key, value)
	}
	resp, err := client.Do(req)
	if err != nil {
		closeResponseBody(resp)
		return "", "", fmt.Errorf("imgbox send csrf request: %w", err)
	}
	body, err := readAndCloseResponseBody(resp)
	if err != nil {
		return "", "", err
	}
	csrfToken, err := imgboxExtractCsrfToken(string(body))
	if err != nil {
		return "", "", err
	}
	cookie := imgboxPickCookie(resp)
	if cookie == "" {
		return "", "", errors.New("imgbox csrf cookie missing")
	}
	return csrfToken, cookie, nil
}

func imgboxGetUploadToken(ctx context.Context, client *http.Client, csrfToken string, cookie string) (imgboxUploadToken, error) {
	if strings.TrimSpace(csrfToken) == "" {
		return imgboxUploadToken{}, errors.New("imgbox csrf token missing")
	}
	if strings.TrimSpace(cookie) == "" {
		return imgboxUploadToken{}, errors.New("imgbox cookie missing")
	}
	client = httpclient.CloneWithTimeout(client, httpclient.UploadTimeout)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://imgbox.com/ajax/token/generate", nil)
	if err != nil {
		return imgboxUploadToken{}, fmt.Errorf("imgbox create token request: %w", err)
	}
	headers := imgboxHeaders(cookie, map[string]string{"X-CSRF-Token": csrfToken})
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	resp, err := client.Do(req)
	if err != nil {
		closeResponseBody(resp)
		return imgboxUploadToken{}, fmt.Errorf("imgbox send token request: %w", err)
	}
	body, err := readAndCloseResponseBody(resp)
	if err != nil {
		return imgboxUploadToken{}, err
	}
	if resp.StatusCode != http.StatusOK {
		bodyStr := string(body)
		if len(bodyStr) > 200 {
			bodyStr = bodyStr[:200] + "..."
		}
		return imgboxUploadToken{}, fmt.Errorf("imgbox token request failed with status %d, response: %s", resp.StatusCode, bodyStr)
	}
	var tokenResp map[string]interface{}
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		bodyStr := string(body)
		if len(bodyStr) > 200 {
			bodyStr = bodyStr[:200] + "..."
		}
		return imgboxUploadToken{}, fmt.Errorf("imgbox token response invalid JSON: %w, body: %s", err, bodyStr)
	}
	return imgboxUploadToken{
		TokenID:       imgboxJSONValue(tokenResp["token_id"]),
		TokenSecret:   imgboxJSONValue(tokenResp["token_secret"]),
		GalleryID:     imgboxJSONValue(tokenResp["gallery_id"]),
		GallerySecret: imgboxJSONValue(tokenResp["gallery_secret"]),
	}, nil
}

func imgboxHeaders(cookie string, extra map[string]string) map[string]string {
	headers := map[string]string{
		"DNT":              "1",
		"Origin":           "https://imgbox.com",
		"Referer":          "https://imgbox.com/",
		"Accept":           "application/json, text/javascript, */*; q=0.01",
		"User-Agent":       "Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:109.0) Gecko/20100101 Firefox/117.0",
		"X-Requested-With": "XMLHttpRequest",
		"Accept-Language":  "en-US,en;q=0.5",
		"Accept-Encoding":  "gzip, deflate, br",
		"Sec-GPC":          "1",
		"Connection":       "keep-alive",
	}
	if strings.TrimSpace(cookie) != "" {
		headers["Cookie"] = cookie
	}
	for key, value := range extra {
		headers[key] = value
	}
	return headers
}

func imgboxExtractCsrfToken(body string) (string, error) {
	patterns := []string{
		`name="authenticity_token"[^>]*value="([^"]+)"`,
		`name='authenticity_token'[^>]*value='([^']+)'`,
	}
	for _, pattern := range patterns {
		re := regexp.MustCompile(pattern)
		match := re.FindStringSubmatch(body)
		if len(match) >= 2 && strings.TrimSpace(match[1]) != "" {
			return strings.TrimSpace(match[1]), nil
		}
	}
	return "", errors.New("imgbox authenticity token not found")
}

func imgboxPickCookie(resp *http.Response) string {
	if resp == nil {
		return ""
	}
	cookies := resp.Header.Values("Set-Cookie")
	if len(cookies) == 0 {
		return ""
	}
	parts := make([]string, 0, len(cookies))
	for _, raw := range cookies {
		segment := strings.SplitN(raw, ";", 2)[0]
		if strings.TrimSpace(segment) != "" {
			parts = append(parts, segment)
		}
	}
	return strings.Join(parts, "; ")
}

func imgboxJSONValue(value interface{}) string {
	switch typed := value.(type) {
	case nil:
		return "null"
	case string:
		if strings.TrimSpace(typed) == "" {
			return "null"
		}
		return typed
	case json.Number:
		return typed.String()
	case float64:
		return strconv.FormatInt(int64(typed), 10)
	case int64:
		return strconv.FormatInt(typed, 10)
	case int:
		return strconv.Itoa(typed)
	default:
		return fmt.Sprintf("%v", typed)
	}
}

type dalexniUploader struct {
	apiKey string
	client *http.Client
}

func (u *dalexniUploader) Upload(ctx context.Context, imagePath string) (uploadResult, error) {
	if strings.TrimSpace(u.apiKey) == "" {
		return uploadResult{}, errors.New("image hosting: dalexni api key missing")
	}
	encoded, err := readBase64(imagePath)
	if err != nil {
		return uploadResult{}, err
	}
	form := url.Values{}
	form.Set("key", strings.TrimSpace(u.apiKey))
	form.Set("image", encoded)

	body, status, err := postForm(ctx, u.client, "https://dalexni.com/1/upload", form, nil)
	if err != nil {
		return uploadResult{}, err
	}
	if status != http.StatusOK {
		return uploadResult{}, fmt.Errorf("dalexni upload failed with status %d", status)
	}

	var response struct {
		Success bool `json:"success"`
		Data    struct {
			Image struct {
				URL string `json:"url"`
			} `json:"image"`
			Thumb struct {
				URL string `json:"url"`
			} `json:"thumb"`
			Medium struct {
				URL string `json:"url"`
			} `json:"medium"`
			URLViewer string `json:"url_viewer"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return uploadResult{}, fmt.Errorf("dalexni invalid response: %w", err)
	}
	if !response.Success {
		return uploadResult{}, errors.New("dalexni upload failed")
	}
	imgURL := response.Data.Medium.URL
	if strings.TrimSpace(imgURL) == "" {
		imgURL = response.Data.Thumb.URL
	}

	return uploadResult{
		ImgURL: imgURL,
		RawURL: response.Data.Image.URL,
		WebURL: response.Data.URLViewer,
	}, nil
}

type onlyImageUploader struct {
	apiKey string
	client *http.Client
}

func (u *onlyImageUploader) Upload(ctx context.Context, imagePath string) (uploadResult, error) {
	if strings.TrimSpace(u.apiKey) == "" {
		return uploadResult{}, errors.New("image hosting: onlyimage api key missing")
	}
	encoded, err := readBase64(imagePath)
	if err != nil {
		return uploadResult{}, err
	}

	form := url.Values{}
	form.Set("image", encoded)
	headers := map[string]string{"X-API-Key": strings.TrimSpace(u.apiKey)}

	body, status, err := postForm(ctx, u.client, "https://onlyimage.org/api/1/upload", form, headers)
	if err != nil {
		return uploadResult{}, err
	}
	if status != http.StatusOK {
		return uploadResult{}, fmt.Errorf("onlyimage upload failed with status %d", status)
	}

	var response struct {
		Success bool `json:"success"`
		Data    struct {
			Image struct {
				URL string `json:"url"`
			} `json:"image"`
			Medium struct {
				URL string `json:"url"`
			} `json:"medium"`
			URLViewer string `json:"url_viewer"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return uploadResult{}, fmt.Errorf("onlyimage invalid response: %w", err)
	}
	if !response.Success {
		return uploadResult{}, errors.New("onlyimage upload failed")
	}

	return uploadResult{
		ImgURL: response.Data.Medium.URL,
		RawURL: response.Data.Image.URL,
		WebURL: response.Data.URLViewer,
	}, nil
}

type lensdumpUploader struct {
	apiKey string
	client *http.Client
}

func (u *lensdumpUploader) Upload(ctx context.Context, imagePath string) (uploadResult, error) {
	if strings.TrimSpace(u.apiKey) == "" {
		return uploadResult{}, errors.New("image hosting: lensdump api key missing")
	}
	encoded, err := readBase64(imagePath)
	if err != nil {
		return uploadResult{}, err
	}

	form := url.Values{}
	form.Set("image", encoded)
	headers := map[string]string{"X-API-Key": strings.TrimSpace(u.apiKey)}

	body, status, err := postForm(ctx, u.client, "https://lensdump.com/api/1/upload", form, headers)
	if err != nil {
		return uploadResult{}, err
	}
	if status != http.StatusOK {
		return uploadResult{}, fmt.Errorf("lensdump upload failed with status %d", status)
	}

	var response struct {
		StatusCode int `json:"status_code"`
		Data       struct {
			Image struct {
				URL string `json:"url"`
			} `json:"image"`
			URLViewer string `json:"url_viewer"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return uploadResult{}, fmt.Errorf("lensdump invalid response: %w", err)
	}
	if response.StatusCode != http.StatusOK {
		return uploadResult{}, errors.New("lensdump upload failed")
	}

	return uploadResult{
		ImgURL: response.Data.Image.URL,
		RawURL: response.Data.Image.URL,
		WebURL: response.Data.URLViewer,
	}, nil
}

type ptScreensUploader struct {
	apiKey string
	client *http.Client
}

func (u *ptScreensUploader) Upload(ctx context.Context, imagePath string) (uploadResult, error) {
	if strings.TrimSpace(u.apiKey) == "" {
		return uploadResult{}, errors.New("image hosting: ptscreens api key missing")
	}
	headers := map[string]string{"X-API-Key": strings.TrimSpace(u.apiKey)}
	body, status, err := postMultipart(ctx, u.client, "https://ptscreens.com/api/1/upload", nil, "source", imagePath, headers)
	if err != nil {
		return uploadResult{}, err
	}
	if status != http.StatusOK {
		return uploadResult{}, fmt.Errorf("ptscreens upload failed with status %d", status)
	}

	var response struct {
		Image struct {
			Medium struct {
				URL string `json:"url"`
			} `json:"medium"`
			URL       string `json:"url"`
			URLViewer string `json:"url_viewer"`
		} `json:"image"`
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return uploadResult{}, fmt.Errorf("ptscreens invalid response: %w", err)
	}
	if response.Image.URL == "" {
		return uploadResult{}, fmt.Errorf("ptscreens upload failed: %s", strings.TrimSpace(response.Error.Message))
	}

	return uploadResult{
		ImgURL: response.Image.Medium.URL,
		RawURL: response.Image.URL,
		WebURL: response.Image.URLViewer,
	}, nil
}

type utppmUploader struct {
	apiKey string
	client *http.Client
}

func (u *utppmUploader) Upload(ctx context.Context, imagePath string) (uploadResult, error) {
	if strings.TrimSpace(u.apiKey) == "" {
		return uploadResult{}, errors.New("image hosting: utppm api key missing")
	}
	encoded, err := readBase64(imagePath)
	if err != nil {
		return uploadResult{}, err
	}

	form := url.Values{}
	form.Set("source", encoded)
	headers := map[string]string{"X-API-Key": strings.TrimSpace(u.apiKey)}

	body, status, err := postForm(ctx, u.client, "https://utp.pm/api/1/upload", form, headers)
	if err != nil {
		return uploadResult{}, err
	}
	if status != http.StatusOK {
		return uploadResult{}, fmt.Errorf("utppm upload failed with status %d", status)
	}

	var response struct {
		Image struct {
			Medium struct {
				URL string `json:"url"`
			} `json:"medium"`
			URL       string `json:"url"`
			URLViewer string `json:"url_viewer"`
		} `json:"image"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return uploadResult{}, fmt.Errorf("utppm invalid response: %w", err)
	}

	return uploadResult{
		ImgURL: response.Image.Medium.URL,
		RawURL: response.Image.URL,
		WebURL: response.Image.URLViewer,
	}, nil
}

type thrUploader struct {
	apiKey string
	client *http.Client
}

func (u *thrUploader) Upload(ctx context.Context, imagePath string) (uploadResult, error) {
	if strings.TrimSpace(u.apiKey) == "" {
		return uploadResult{}, errors.New("image hosting: thr api key missing")
	}
	body, status, err := postMultipart(ctx, u.client, "https://img2.torrenthr.org/api/1/upload", map[string]string{
		"key": strings.TrimSpace(u.apiKey),
	}, "source", imagePath, nil)
	if err != nil {
		return uploadResult{}, err
	}
	if status != http.StatusOK {
		return uploadResult{}, fmt.Errorf("thr upload failed with status %d", status)
	}

	var response struct {
		Image struct {
			URL string `json:"url"`
		} `json:"image"`
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return uploadResult{}, fmt.Errorf("thr invalid response: %w", err)
	}
	imageURL := strings.TrimSpace(response.Image.URL)
	if imageURL == "" {
		message := strings.TrimSpace(response.Error.Message)
		if message == "" {
			message = "thr upload failed"
		}
		return uploadResult{}, fmt.Errorf("thr upload failed: %s", message)
	}
	return uploadResult{ImgURL: imageURL, RawURL: imageURL, WebURL: imageURL}, nil
}

type ptpImgUploader struct {
	apiKey string
	client *http.Client
}

func (u *ptpImgUploader) Upload(ctx context.Context, imagePath string) (uploadResult, error) {
	if strings.TrimSpace(u.apiKey) == "" {
		return uploadResult{}, errors.New("image hosting: ptpimg api key missing")
	}
	fields := map[string]string{
		"format":  "json",
		"api_key": strings.TrimSpace(u.apiKey),
	}
	body, status, err := postMultipart(ctx, u.client, "https://ptpimg.me/upload.php", fields, "file-upload[0]", imagePath, map[string]string{"referer": "https://ptpimg.me/index.php"})
	if err != nil {
		return uploadResult{}, err
	}
	if status != http.StatusOK {
		return uploadResult{}, fmt.Errorf("ptpimg upload failed with status %d", status)
	}

	var response []struct {
		Code interface{} `json:"code"`
		Ext  string      `json:"ext"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return uploadResult{}, fmt.Errorf("ptpimg invalid response: %w", err)
	}
	if len(response) == 0 {
		return uploadResult{}, errors.New("ptpimg upload failed")
	}
	code := strings.TrimSpace(fmt.Sprint(response[0].Code))
	ext := strings.TrimSpace(response[0].Ext)
	if code == "" || ext == "" {
		return uploadResult{}, errors.New("ptpimg upload failed")
	}
	urlValue := fmt.Sprintf("https://ptpimg.me/%s.%s", code, ext)

	return uploadResult{ImgURL: urlValue, RawURL: urlValue, WebURL: urlValue}, nil
}

type pixhostUploader struct {
	client *http.Client
}

func (u *pixhostUploader) Upload(ctx context.Context, imagePath string) (uploadResult, error) {
	fields := map[string]string{
		"content_type": "0",
		"max_th_size":  "350",
	}
	body, status, err := postMultipart(ctx, u.client, "https://api.pixhost.to/images", fields, "img", imagePath, nil)
	if err != nil {
		return uploadResult{}, err
	}
	if status != http.StatusOK {
		return uploadResult{}, fmt.Errorf("pixhost upload failed with status %d", status)
	}

	var response struct {
		ThumbnailURL string `json:"th_url"`
		ShowURL      string `json:"show_url"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return uploadResult{}, fmt.Errorf("pixhost invalid response: %w", err)
	}
	if response.ThumbnailURL == "" {
		return uploadResult{}, errors.New("pixhost upload failed")
	}
	rawURL := strings.ReplaceAll(response.ThumbnailURL, "https://t", "https://img")
	rawURL = strings.ReplaceAll(rawURL, "/thumbs/", "/images/")

	return uploadResult{ImgURL: response.ThumbnailURL, RawURL: rawURL, WebURL: response.ShowURL}, nil
}

type ziplineUploader struct {
	apiKey string
	url    string
	client *http.Client
}

func (u *ziplineUploader) Upload(ctx context.Context, imagePath string) (uploadResult, error) {
	if strings.TrimSpace(u.url) == "" || strings.TrimSpace(u.apiKey) == "" {
		return uploadResult{}, errors.New("image hosting: zipline url or api key missing")
	}
	headers := map[string]string{"Authorization": strings.TrimSpace(u.apiKey)}
	body, status, err := postMultipart(ctx, u.client, strings.TrimSpace(u.url), nil, "file", imagePath, headers)
	if err != nil {
		return uploadResult{}, err
	}
	if status != http.StatusOK {
		return uploadResult{}, fmt.Errorf("zipline upload failed with status %d", status)
	}

	var response struct {
		Files []string `json:"files"`
		Data  struct {
			Files []string `json:"files"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return uploadResult{}, fmt.Errorf("zipline invalid response: %w", err)
	}
	files := response.Files
	if len(files) == 0 {
		files = response.Data.Files
	}
	if len(files) == 0 {
		return uploadResult{}, errors.New("zipline upload failed")
	}
	urlValue := strings.TrimSpace(files[0])
	if urlValue == "" {
		return uploadResult{}, errors.New("zipline upload failed")
	}
	rawURL := strings.Replace(urlValue, "/u/", "/r/", 1)

	return uploadResult{ImgURL: urlValue, RawURL: rawURL, WebURL: rawURL}, nil
}

type passTheImageUploader struct {
	apiKey string
	client *http.Client
}

func (u *passTheImageUploader) Upload(ctx context.Context, imagePath string) (uploadResult, error) {
	if strings.TrimSpace(u.apiKey) == "" {
		return uploadResult{}, errors.New("image hosting: passtheimage api key missing")
	}
	headers := map[string]string{"X-API-Key": strings.TrimSpace(u.apiKey)}
	body, status, err := postMultipart(ctx, u.client, "https://passtheima.ge/api/1/upload", nil, "source", imagePath, headers)
	if err != nil {
		return uploadResult{}, err
	}
	if status != http.StatusOK {
		return uploadResult{}, fmt.Errorf("passtheimage upload failed with status %d", status)
	}

	var response struct {
		StatusCode int `json:"status_code"`
		Image      struct {
			URL       string `json:"url"`
			URLViewer string `json:"url_viewer"`
		} `json:"image"`
		Error struct {
			Message string `json:"message"`
			Code    string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return uploadResult{}, fmt.Errorf("passtheimage invalid response: %w", err)
	}
	if response.StatusCode != http.StatusOK {
		message := strings.TrimSpace(response.Error.Message)
		if message == "" {
			message = "passtheimage upload failed"
		}
		return uploadResult{}, fmt.Errorf("passtheimage upload failed: %s", message)
	}
	if response.Image.URL == "" {
		return uploadResult{}, errors.New("passtheimage upload failed")
	}

	return uploadResult{
		ImgURL: response.Image.URL,
		RawURL: response.Image.URL,
		WebURL: response.Image.URLViewer,
	}, nil
}

type seedpoolUploader struct {
	apiKey string
	client *http.Client
}

func (u *seedpoolUploader) Upload(ctx context.Context, imagePath string) (uploadResult, error) {
	if strings.TrimSpace(u.apiKey) == "" {
		return uploadResult{}, errors.New("image hosting: seedpool api key missing")
	}
	headers := map[string]string{"Authorization": "Bearer " + strings.TrimSpace(u.apiKey)}
	body, status, err := postMultipart(ctx, u.client, "https://i.seedpool.org/upload", nil, "files[]", imagePath, headers)
	if err != nil {
		return uploadResult{}, err
	}
	if status != http.StatusOK && status != http.StatusCreated {
		return uploadResult{}, fmt.Errorf("seedpool upload failed with status %d", status)
	}

	var response struct {
		Files []struct {
			URL          string            `json:"url"`
			Variants     map[string]string `json:"variants"`
			ThumbnailURL string            `json:"thumbnail_url"`
		} `json:"files"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return uploadResult{}, fmt.Errorf("seedpool invalid response: %w", err)
	}
	if len(response.Files) == 0 {
		return uploadResult{}, errors.New("seedpool upload failed")
	}
	file := response.Files[0]
	imgURL := strings.TrimSpace(file.ThumbnailURL)
	if imgURL == "" {
		if variant, ok := file.Variants["thumb"]; ok {
			imgURL = variant
		}
	}
	if imgURL == "" {
		if variant, ok := file.Variants["medium"]; ok {
			imgURL = variant
		}
	}
	if imgURL == "" {
		imgURL = file.URL
	}

	return uploadResult{ImgURL: imgURL, RawURL: file.URL, WebURL: file.URL}, nil
}

type shareXUploader struct {
	apiKey string
	url    string
	client *http.Client
}

func (u *shareXUploader) Upload(ctx context.Context, imagePath string) (uploadResult, error) {
	urlValue := strings.TrimSpace(u.url)
	if urlValue == "" {
		urlValue = "https://img.digitalcore.club/api/upload"
	}
	if strings.TrimSpace(u.apiKey) == "" {
		return uploadResult{}, errors.New("image hosting: sharex api key missing")
	}
	headers := map[string]string{"Authorization": strings.TrimSpace(u.apiKey)}
	fields := map[string]string{"title": "upbrr screenshot"}
	body, status, err := postMultipart(ctx, u.client, urlValue, fields, "file", imagePath, headers)
	if err != nil {
		return uploadResult{}, err
	}
	if status != http.StatusOK && status != http.StatusCreated {
		return uploadResult{}, fmt.Errorf("sharex upload failed with status %d", status)
	}

	var response struct {
		Data struct {
			Link string `json:"link"`
		} `json:"data"`
		Link    string `json:"link"`
		Message string `json:"message"`
		Error   string `json:"error"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return uploadResult{}, fmt.Errorf("sharex invalid response: %w", err)
	}
	link := strings.TrimSpace(response.Data.Link)
	if link == "" {
		link = strings.TrimSpace(response.Link)
	}
	if link == "" {
		message := strings.TrimSpace(response.Message)
		if message == "" {
			message = strings.TrimSpace(response.Error)
		}
		if message == "" {
			message = "sharex upload failed"
		}
		return uploadResult{}, fmt.Errorf("sharex upload failed: %s", message)
	}

	return uploadResult{ImgURL: link, RawURL: link, WebURL: link}, nil
}

func postForm(ctx context.Context, client *http.Client, target string, data url.Values, headers map[string]string) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, strings.NewReader(data.Encode()))
	if err != nil {
		return nil, 0, fmt.Errorf("image hosting: create form request for %s: %w", target, err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	resp, err := client.Do(req)
	if err != nil {
		closeResponseBody(resp)
		return nil, 0, fmt.Errorf("image hosting: send form request to %s: %w", target, err)
	}
	body, err := readAndCloseResponseBody(resp)
	if err != nil {
		return nil, resp.StatusCode, err
	}
	return body, resp.StatusCode, nil
}

func postMultipart(ctx context.Context, client *http.Client, target string, fields map[string]string, fileField string, filePath string, headers map[string]string) ([]byte, int, error) {
	return postMultipartWithFields(ctx, client, target, fields, map[string]string{fileField: filePath}, headers)
}

func postMultipartWithFields(ctx context.Context, client *http.Client, target string, fields map[string]string, fileFields map[string]string, headers map[string]string) ([]byte, int, error) {
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	fieldKeys := make([]string, 0, len(fields))
	for key := range fields {
		fieldKeys = append(fieldKeys, key)
	}
	sort.Strings(fieldKeys)
	for _, key := range fieldKeys {
		value := fields[key]
		if err := writer.WriteField(key, value); err != nil {
			return nil, 0, fmt.Errorf("image hosting: write multipart field %q: %w", key, err)
		}
	}
	fileFieldKeys := make([]string, 0, len(fileFields))
	for key := range fileFields {
		fileFieldKeys = append(fileFieldKeys, key)
	}
	sort.Strings(fileFieldKeys)
	for _, fileField := range fileFieldKeys {
		filePath := fileFields[fileField]
		file, err := os.Open(filePath)
		if err != nil {
			return nil, 0, fmt.Errorf("image hosting: open multipart file: %w", err)
		}
		part, err := writer.CreateFormFile(fileField, filepath.Base(filePath))
		if err != nil {
			_ = file.Close()
			return nil, 0, fmt.Errorf("image hosting: create multipart file %q: %w", fileField, err)
		}
		if _, err := io.Copy(part, file); err != nil {
			_ = file.Close()
			return nil, 0, fmt.Errorf("image hosting: copy multipart file: %w", err)
		}
		if err := file.Close(); err != nil {
			return nil, 0, fmt.Errorf("image hosting: close multipart file: %w", err)
		}
	}
	if err := writer.Close(); err != nil {
		return nil, 0, fmt.Errorf("image hosting: close multipart writer: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, body)
	if err != nil {
		return nil, 0, fmt.Errorf("image hosting: create multipart request for %s: %w", target, err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	for key, value := range headers {
		req.Header.Set(key, value)
	}

	resp, err := client.Do(req)
	if err != nil {
		closeResponseBody(resp)
		return nil, 0, fmt.Errorf("image hosting: send multipart request to %s: %w", target, err)
	}
	bodyBytes, err := readAndCloseResponseBody(resp)
	if err != nil {
		return nil, resp.StatusCode, err
	}
	return bodyBytes, resp.StatusCode, nil
}

func readAndCloseResponseBody(resp *http.Response) ([]byte, error) {
	if resp == nil || resp.Body == nil {
		return nil, nil
	}
	defer closeResponseBody(resp)
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("image hosting: read response body: %w", err)
	}
	return body, nil
}

func closeResponseBody(resp *http.Response) {
	if resp == nil || resp.Body == nil {
		return
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
}

func readBase64(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("image hosting: read base64 file: %w", err)
	}
	return base64.StdEncoding.EncodeToString(data), nil
}
