// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package config

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"golang.org/x/crypto/argon2"

	"github.com/autobrr/upbrr/internal/authmaterial"
)

const (
	// encryptedEnvelopePrefix is the transport/wire-format marker for serialized secret envelopes.
	// This is independent from secretEnvelopeVersionOWASP, which versions the crypto envelope parameters.
	encryptedEnvelopePrefix = "upbrr-enc:v1:"
	webAuthFileName         = authmaterial.WebAuthFileName
	// secretEnvelopeVersionOWASP tracks the algorithm/envelope version (OWASP Argon2id profile).
	secretEnvelopeVersionOWASP = 2

	secretArgon2OWASPTime        = 2
	secretArgon2OWASPMemoryKB    = 19 * 1024
	secretArgon2OWASPParallelism = 1

	secretArgon2KeyLen = 32
)

var ErrSecretEncryptionHelperUnavailable = errors.New("config secret encryption helper unavailable")

type secretEnvelope struct {
	V int    `json:"v"`
	S string `json:"s"`
	C string `json:"c"`
	N string `json:"n"`
	T string `json:"t"`
}

// EncryptConfigSecrets returns a cloned config where known secret fields are encrypted.
func EncryptConfigSecrets(cfg *Config) (*Config, error) {
	if cfg == nil {
		return nil, errors.New("config secret encryption: nil config")
	}

	helper, err := resolveSecretHelper(cfg)
	if err != nil {
		if errors.Is(err, ErrSecretEncryptionHelperUnavailable) {
			hasEncryptedSecrets, scanErr := hasEncryptedSecretEnvelopes(cfg)
			if scanErr != nil {
				return nil, scanErr
			}
			if hasEncryptedSecrets {
				return nil, err
			}
			return cloneConfig(cfg)
		}
		return nil, err
	}

	return encryptConfigSecretsWithHelper(cfg, helper)
}

// DecryptConfigSecrets returns a cloned config where known encrypted secret fields are decrypted.
func DecryptConfigSecrets(cfg *Config) (*Config, error) {
	if cfg == nil {
		return nil, errors.New("config secret decryption: nil config")
	}

	cloned, err := cloneConfig(cfg)
	if err != nil {
		return nil, err
	}

	needsHelper := false
	_ = walkSecretFields(cloned, func(_ string, value *string) error {
		if value != nil && isSecretEnvelope(strings.TrimSpace(*value)) {
			needsHelper = true
		}
		return nil
	})
	if !needsHelper {
		return cloned, nil
	}

	helper, err := resolveSecretHelper(cfg)
	if err != nil {
		return nil, err
	}

	return decryptConfigSecretsWithHelperFrom(cloned, helper, true)
}

func encryptConfigSecretsWithHelper(cfg *Config, helper string) (*Config, error) {
	cloned, err := cloneConfig(cfg)
	if err != nil {
		return nil, err
	}

	if err := walkSecretFields(cloned, func(_ string, value *string) error {
		if value == nil {
			return nil
		}
		trimmed := strings.TrimSpace(*value)
		if trimmed == "" || isSecretEnvelope(trimmed) {
			return nil
		}
		encrypted, err := encryptSecretString(trimmed, helper)
		if err != nil {
			return err
		}
		*value = encrypted
		return nil
	}); err != nil {
		return nil, fmt.Errorf("config secret encryption: %w", err)
	}

	return cloned, nil
}

func decryptConfigSecretsWithHelper(cfg *Config, helper string) (*Config, error) {
	return decryptConfigSecretsWithHelperFrom(cfg, helper, false)
}

func decryptConfigSecretsWithHelperFrom(cfg *Config, helper string, assumeCloned bool) (*Config, error) {
	cloned := cfg
	if !assumeCloned {
		var err error
		cloned, err = cloneConfig(cfg)
		if err != nil {
			return nil, err
		}
	}

	needsHelper := false
	_ = walkSecretFields(cloned, func(_ string, value *string) error {
		if value != nil && isSecretEnvelope(strings.TrimSpace(*value)) {
			needsHelper = true
		}
		return nil
	})
	if !needsHelper {
		return cloned, nil
	}

	if err := walkSecretFields(cloned, func(path string, value *string) error {
		if value == nil {
			return nil
		}
		trimmed := strings.TrimSpace(*value)
		if !isSecretEnvelope(trimmed) {
			return nil
		}
		decrypted, err := decryptSecretString(trimmed, helper)
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		*value = decrypted
		return nil
	}); err != nil {
		return nil, fmt.Errorf("config secret decryption: %w", err)
	}

	return cloned, nil
}

func RewrapSecretsInDatabase(ctx context.Context, repo interface {
	LoadFullConfig(ctx context.Context, dest interface{}) error
	SaveFullConfig(ctx context.Context, cfg interface{}) error
}, oldMaterial, newMaterial authmaterial.Material) error {
	return RewrapSecretsInDatabaseWithFallback(ctx, repo, []authmaterial.Material{oldMaterial}, newMaterial)
}

func RewrapSecretsInDatabaseWithFallback(ctx context.Context, repo interface {
	LoadFullConfig(ctx context.Context, dest interface{}) error
	SaveFullConfig(ctx context.Context, cfg interface{}) error
}, sourceMaterials []authmaterial.Material, newMaterial authmaterial.Material) error {
	if repo == nil {
		return errors.New("config secret rewrap: nil repository")
	}

	sourceHelpers := make([]string, 0, len(sourceMaterials))
	for _, material := range sourceMaterials {
		helper, _, err := material.PrimaryHelper()
		if err != nil {
			return fmt.Errorf("config secret rewrap: derive source helper: %w", err)
		}
		sourceHelpers = append(sourceHelpers, helper)
	}
	newHelper, _, err := newMaterial.PrimaryHelper()
	if err != nil {
		return fmt.Errorf("config secret rewrap: derive new helper: %w", err)
	}

	var stored Config
	if err := repo.LoadFullConfig(ctx, &stored); err != nil {
		return fmt.Errorf("config secret rewrap: load config: %w", err)
	}

	decrypted, err := decryptConfigSecretsWithHelpers(&stored, sourceHelpers)
	if err != nil {
		return fmt.Errorf("config secret rewrap: decrypt: %w", err)
	}
	encrypted, err := encryptConfigSecretsWithHelper(decrypted, newHelper)
	if err != nil {
		return fmt.Errorf("config secret rewrap: encrypt: %w", err)
	}
	if err := repo.SaveFullConfig(ctx, encrypted); err != nil {
		return fmt.Errorf("config secret rewrap: save config: %w", err)
	}

	return nil
}

func decryptConfigSecretsWithHelpers(cfg *Config, helpers []string) (*Config, error) {
	return decryptConfigSecretsWithHelpersFrom(cfg, helpers, false)
}

func decryptConfigSecretsWithHelpersFrom(cfg *Config, helpers []string, assumeCloned bool) (*Config, error) {
	cloned := cfg
	if !assumeCloned {
		var err error
		cloned, err = cloneConfig(cfg)
		if err != nil {
			return nil, err
		}
	}

	needsHelper := false
	_ = walkSecretFields(cloned, func(_ string, value *string) error {
		if value != nil && isSecretEnvelope(strings.TrimSpace(*value)) {
			needsHelper = true
		}
		return nil
	})
	if !needsHelper {
		return cloned, nil
	}

	cleanHelpers := make([]string, 0, len(helpers))
	for _, helper := range helpers {
		helper = strings.TrimSpace(helper)
		if helper == "" {
			continue
		}
		duplicate := false
		for _, existing := range cleanHelpers {
			if existing == helper {
				duplicate = true
				break
			}
		}
		if !duplicate {
			cleanHelpers = append(cleanHelpers, helper)
		}
	}
	if len(cleanHelpers) == 0 {
		return nil, ErrSecretEncryptionHelperUnavailable
	}

	if err := walkSecretFields(cloned, func(path string, value *string) error {
		if value == nil {
			return nil
		}
		trimmed := strings.TrimSpace(*value)
		if !isSecretEnvelope(trimmed) {
			return nil
		}

		var lastErr error
		for _, helper := range cleanHelpers {
			decrypted, err := decryptSecretString(trimmed, helper)
			if err == nil {
				*value = decrypted
				return nil
			}
			lastErr = err
		}

		if lastErr == nil {
			lastErr = ErrSecretEncryptionHelperUnavailable
		}
		return fmt.Errorf("%s: %w", path, lastErr)
	}); err != nil {
		return nil, fmt.Errorf("config secret decryption: %w", err)
	}

	return cloned, nil
}

func cloneConfig(cfg *Config) (*Config, error) {
	payload, err := json.Marshal(cfg)
	if err != nil {
		return nil, fmt.Errorf("config clone: marshal: %w", err)
	}
	var cloned Config
	if err := json.Unmarshal(payload, &cloned); err != nil {
		return nil, fmt.Errorf("config clone: unmarshal: %w", err)
	}
	return &cloned, nil
}

func hasEncryptedSecretEnvelopes(cfg *Config) (bool, error) {
	hasEncryptedSecrets := false
	if err := walkSecretFields(cfg, func(_ string, value *string) error {
		if value == nil {
			return nil
		}
		if isSecretEnvelope(strings.TrimSpace(*value)) {
			hasEncryptedSecrets = true
		}
		return nil
	}); err != nil {
		return false, fmt.Errorf("config secret encryption: inspect encrypted secrets: %w", err)
	}

	return hasEncryptedSecrets, nil
}

func walkSecretFields(cfg *Config, visit func(path string, value *string) error) error {
	if cfg == nil {
		return nil
	}

	mainSecrets := []*string{
		&cfg.MainSettings.TMDBAPI,
	}
	for idx, field := range mainSecrets {
		if err := visit(fmt.Sprintf("MainSettings[%d]", idx), field); err != nil {
			return err
		}
	}

	imageSecrets := []*string{
		&cfg.ImageHosting.ImgBBAPI,
		&cfg.ImageHosting.PTPImgAPI,
		&cfg.ImageHosting.LensdumpAPI,
		&cfg.ImageHosting.PTScreensAPI,
		&cfg.ImageHosting.OnlyImageAPI,
		&cfg.ImageHosting.DalexniAPI,
		&cfg.ImageHosting.PassTheImageAPI,
		&cfg.ImageHosting.ZiplineAPIKey,
		&cfg.ImageHosting.SeedpoolCDNAPI,
		&cfg.ImageHosting.ShareXAPIKey,
		&cfg.ImageHosting.UTPPMAPI,
	}
	for idx, field := range imageSecrets {
		if err := visit(fmt.Sprintf("ImageHosting[%d]", idx), field); err != nil {
			return err
		}
	}

	arrSecrets := []*string{
		&cfg.ArrIntegration.SonarrAPIKey,
		&cfg.ArrIntegration.SonarrAPIKey1,
		&cfg.ArrIntegration.SonarrAPIKey2,
		&cfg.ArrIntegration.SonarrAPIKey3,
		&cfg.ArrIntegration.RadarrAPIKey,
		&cfg.ArrIntegration.RadarrAPIKey1,
		&cfg.ArrIntegration.RadarrAPIKey2,
		&cfg.ArrIntegration.RadarrAPIKey3,
	}
	for idx, field := range arrSecrets {
		if err := visit(fmt.Sprintf("ArrIntegration[%d]", idx), field); err != nil {
			return err
		}
	}

	metadataSecrets := []*string{&cfg.Metadata.BTNAPI}
	for idx, field := range metadataSecrets {
		if err := visit(fmt.Sprintf("Metadata[%d]", idx), field); err != nil {
			return err
		}
	}

	trackerNames := make([]string, 0, len(cfg.Trackers.Trackers))
	for name := range cfg.Trackers.Trackers {
		trackerNames = append(trackerNames, name)
	}
	sort.Strings(trackerNames)
	for _, name := range trackerNames {
		entry := cfg.Trackers.Trackers[name]
		trackerSecrets := []struct {
			name  string
			field *string
		}{
			{name: "APIKey", field: &entry.APIKey},
			{name: "ApiKey", field: &entry.PTPAPIKey},
			{name: "ApiUser", field: &entry.PTPAPIUser},
			{name: "Username", field: &entry.Username},
			{name: "Password", field: &entry.Password},
			{name: "Passkey", field: &entry.Passkey},
			{name: "AnnounceURL", field: &entry.AnnounceURL},
			{name: "MyAnnounceURL", field: &entry.MyAnnounceURL},
			{name: "BhdRSSKey", field: &entry.BhdRSSKey},
			{name: "OTPURI", field: &entry.OTPURI},
			{name: "PTGenAPI", field: &entry.PTGenAPI},
			{name: "ImgAPI", field: &entry.ImgAPI},
			{name: "PronfoAPIKey", field: &entry.PronfoAPIKey},
			{name: "PronfoRAPIID", field: &entry.PronfoRAPIID},
			{name: "LoginQuestion", field: &entry.LoginQuestion},
			{name: "LoginAnswer", field: &entry.LoginAnswer},
			{name: "Filebrowser", field: &entry.Filebrowser},
		}
		if strings.EqualFold(strings.TrimSpace(name), "BTN") {
			trackerSecrets = append(trackerSecrets, struct {
				name  string
				field *string
			}{name: "URL", field: &entry.URL})
		}
		for _, item := range trackerSecrets {
			if err := visit("Trackers."+name+"."+item.name, item.field); err != nil {
				return err
			}
		}
		cfg.Trackers.Trackers[name] = entry
	}

	clientNames := make([]string, 0, len(cfg.TorrentClients))
	for name := range cfg.TorrentClients {
		clientNames = append(clientNames, name)
	}
	sort.Strings(clientNames)
	for _, name := range clientNames {
		entry := cfg.TorrentClients[name]
		clientSecrets := []struct {
			name  string
			field *string
		}{
			{name: "QuiProxyURL", field: &entry.QuiProxyURL},
			{name: "Username", field: &entry.Username},
			{name: "Password", field: &entry.Password},
			{name: "QbitUser", field: &entry.QbitUser},
			{name: "QbitPass", field: &entry.QbitPass},
		}
		for _, item := range clientSecrets {
			if err := visit("TorrentClients."+name+"."+item.name, item.field); err != nil {
				return err
			}
		}
		cfg.TorrentClients[name] = entry
	}

	return nil
}

func resolveSecretHelper(cfg *Config) (string, error) {
	if cfg == nil {
		return "", ErrSecretEncryptionHelperUnavailable
	}

	dbPath := strings.TrimSpace(cfg.MainSettings.DBPath)
	if dbPath == "" {
		return "", ErrSecretEncryptionHelperUnavailable
	}

	authPath := filepath.Join(filepath.Dir(dbPath), webAuthFileName)
	info, err := os.Stat(authPath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", ErrSecretEncryptionHelperUnavailable
		}
		return "", fmt.Errorf("config secret helper: stat web auth: %w", err)
	}

	// Enforce owner-only rw permissions on Unix-like systems.
	if runtime.GOOS != "windows" && info.Mode().Perm()&^0o600 != 0 {
		return "", fmt.Errorf("%w: %s must have permissions 0600 (owner read/write only), got %o", ErrSecretEncryptionHelperUnavailable, webAuthFileName, info.Mode().Perm())
	}

	record, err := authmaterial.LoadFromDBPath(dbPath)
	if err != nil {
		if errors.Is(err, authmaterial.ErrUnavailable) {
			if isBareAuthmaterialUnavailable(err) {
				return "", ErrSecretEncryptionHelperUnavailable
			}
			return "", fmt.Errorf("%w: %w", ErrSecretEncryptionHelperUnavailable, err)
		}
		return "", fmt.Errorf("config secret helper: %w", err)
	}

	helper, _, err := record.PrimaryHelper()
	if err != nil {
		if errors.Is(err, authmaterial.ErrUnavailable) {
			return "", ErrSecretEncryptionHelperUnavailable
		}
		return "", fmt.Errorf("config secret helper: derive helper: %w", err)
	}

	return helper, nil
}

func isBareAuthmaterialUnavailable(err error) bool {
	return err != nil && errors.Is(err, authmaterial.ErrUnavailable) && errors.Unwrap(err) == nil
}

func encryptSecretString(plaintext string, helper string) (string, error) {
	saltBytes, err := generateRandomBytes(16)
	if err != nil {
		return "", err
	}
	salt := base64.StdEncoding.EncodeToString(saltBytes)

	key, err := deriveSecretKey(helper, salt)
	if err != nil {
		return "", err
	}
	defer zeroBytes(key)

	encrypted, err := encryptSecretValue(plaintext, key)
	if err != nil {
		return "", err
	}

	envelope := secretEnvelope{
		V: secretEnvelopeVersionOWASP,
		S: salt,
		C: base64.StdEncoding.EncodeToString(encrypted.ciphertext),
		N: base64.StdEncoding.EncodeToString(encrypted.nonce),
		T: base64.StdEncoding.EncodeToString(encrypted.authTag),
	}

	payload, err := json.Marshal(envelope)
	if err != nil {
		return "", err
	}

	return encryptedEnvelopePrefix + base64.StdEncoding.EncodeToString(payload), nil
}

func decryptSecretString(value string, helper string) (string, error) {
	envelope, err := parseSecretEnvelope(value)
	if err != nil {
		return "", err
	}

	if envelope.V != secretEnvelopeVersionOWASP {
		return "", fmt.Errorf("unsupported secret envelope version: %d", envelope.V)
	}

	key, err := deriveSecretKey(helper, envelope.S)
	if err != nil {
		return "", err
	}
	defer zeroBytes(key)

	ciphertext, err := base64.StdEncoding.DecodeString(envelope.C)
	if err != nil {
		return "", err
	}
	nonce, err := base64.StdEncoding.DecodeString(envelope.N)
	if err != nil {
		return "", err
	}
	authTag, err := base64.StdEncoding.DecodeString(envelope.T)
	if err != nil {
		return "", err
	}

	return decryptSecretValue(secretPayload{ciphertext: ciphertext, nonce: nonce, authTag: authTag}, key)
}

func isSecretEnvelope(value string) bool {
	return strings.HasPrefix(value, encryptedEnvelopePrefix)
}

func parseSecretEnvelope(value string) (secretEnvelope, error) {
	encoded := strings.TrimPrefix(value, encryptedEnvelopePrefix)
	payload, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return secretEnvelope{}, fmt.Errorf("decode secret envelope: %w", err)
	}

	var envelope secretEnvelope
	if err := json.Unmarshal(payload, &envelope); err != nil {
		return secretEnvelope{}, fmt.Errorf("unmarshal secret envelope: %w", err)
	}
	if envelope.V == 0 {
		return secretEnvelope{}, errors.New("invalid secret envelope: missing V")
	}
	if strings.TrimSpace(envelope.S) == "" {
		return secretEnvelope{}, errors.New("invalid secret envelope: missing S")
	}
	if strings.TrimSpace(envelope.C) == "" {
		return secretEnvelope{}, errors.New("invalid secret envelope: missing C")
	}
	if strings.TrimSpace(envelope.N) == "" {
		return secretEnvelope{}, errors.New("invalid secret envelope: missing N")
	}
	if strings.TrimSpace(envelope.T) == "" {
		return secretEnvelope{}, errors.New("invalid secret envelope: missing T")
	}

	return envelope, nil
}

type secretPayload struct {
	ciphertext []byte
	nonce      []byte
	authTag    []byte
}

func deriveSecretKey(helper string, salt string) ([]byte, error) {
	helper = strings.TrimSpace(helper)
	if len(helper) < 8 {
		return nil, errors.New("secret helper must be at least 8 characters")
	}
	if strings.TrimSpace(salt) == "" {
		return nil, errors.New("secret salt cannot be empty")
	}

	decodedSalt, err := base64.StdEncoding.DecodeString(salt)
	if err != nil {
		return nil, fmt.Errorf("decode secret salt: %w", err)
	}

	return argon2.IDKey(
		[]byte(helper),
		decodedSalt,
		secretArgon2OWASPTime,
		secretArgon2OWASPMemoryKB,
		secretArgon2OWASPParallelism,
		secretArgon2KeyLen,
	), nil
}

func zeroBytes(buf []byte) {
	for i := range buf {
		buf[i] = 0
	}
}

func generateRandomBytes(length int) ([]byte, error) {
	buf := make([]byte, length)
	if _, err := io.ReadFull(rand.Reader, buf); err != nil {
		return nil, fmt.Errorf("generate random bytes: %w", err)
	}
	return buf, nil
}

func encryptSecretValue(plaintext string, key []byte) (secretPayload, error) {
	if len(key) != 32 {
		return secretPayload{}, errors.New("secret key must be 32 bytes")
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return secretPayload{}, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return secretPayload{}, err
	}
	nonce, err := generateRandomBytes(12)
	if err != nil {
		return secretPayload{}, err
	}
	sealed := gcm.Seal(nil, nonce, []byte(plaintext), nil)
	if len(sealed) < 16 {
		return secretPayload{}, errors.New("invalid encrypted payload")
	}
	return secretPayload{
		ciphertext: sealed[:len(sealed)-16],
		nonce:      nonce,
		authTag:    sealed[len(sealed)-16:],
	}, nil
}

func decryptSecretValue(payload secretPayload, key []byte) (string, error) {
	if len(key) != 32 {
		return "", errors.New("secret key must be 32 bytes")
	}
	if len(payload.nonce) != 12 {
		return "", errors.New("secret nonce must be 12 bytes")
	}
	if len(payload.authTag) != 16 {
		return "", errors.New("secret auth tag must be 16 bytes")
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	sealed := append(append([]byte{}, payload.ciphertext...), payload.authTag...)
	plaintext, err := gcm.Open(nil, payload.nonce, sealed, nil)
	if err != nil {
		return "", err
	}
	return string(plaintext), nil
}
