package secret

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"strings"

	"golang.org/x/crypto/scrypt"
)

const (
	Prefix   = "dbx-aes-gcm:"
	prefixV1 = Prefix + "v1:"
	prefixV2 = Prefix + "v2:"

	saltSize  = 16
	nonceSize = 12
	keySize   = 32
	keyIDSize = 16

	scryptN = 1 << 15
	scryptR = 8
	scryptP = 1
)

var (
	ErrSecretStoreUnavailable   = errors.New("system credential store is unavailable")
	ErrSecretKeyNotFound        = errors.New("encrypted password key is missing from this machine")
	ErrEncryptedPasswordInvalid = errors.New("encrypted password is invalid")
)

type PassphraseProvider func(context.Context) (string, error)

type passphraseProviderKey struct{}

func WithPassphraseProvider(ctx context.Context, provider PassphraseProvider) context.Context {
	if provider == nil {
		return ctx
	}
	return context.WithValue(ctx, passphraseProviderKey{}, provider)
}

func IsEncryptedValue(value string) bool {
	return strings.HasPrefix(value, Prefix)
}

func EncryptString(passphrase, plaintext string) (string, error) {
	if passphrase == "" {
		return "", fmt.Errorf("master passphrase is required")
	}
	salt := make([]byte, saltSize)
	if _, err := io.ReadFull(rand.Reader, salt); err != nil {
		return "", err
	}
	nonce := make([]byte, nonceSize)
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	key, err := deriveKey(passphrase, salt)
	if err != nil {
		return "", err
	}
	gcm, err := newGCM(key)
	if err != nil {
		return "", err
	}
	ciphertext := gcm.Seal(nil, nonce, []byte(plaintext), []byte(prefixV1))
	payload := make([]byte, 0, saltSize+nonceSize+len(ciphertext))
	payload = append(payload, salt...)
	payload = append(payload, nonce...)
	payload = append(payload, ciphertext...)
	return prefixV1 + base64.RawURLEncoding.EncodeToString(payload), nil
}

func EncryptStringV2(ctx context.Context, plaintext string) (string, error) {
	store, ok := keyStoreFromContext(ctx)
	if !ok {
		return "", fmt.Errorf("%w: no credential store is configured", ErrSecretStoreUnavailable)
	}

	keyIDBytes := make([]byte, keyIDSize)
	if _, err := io.ReadFull(rand.Reader, keyIDBytes); err != nil {
		return "", err
	}
	keyID := base64.RawURLEncoding.EncodeToString(keyIDBytes)

	key := make([]byte, keySize)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return "", err
	}
	nonce := make([]byte, nonceSize)
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	gcm, err := newGCM(key)
	if err != nil {
		return "", err
	}
	header := prefixV2 + keyID + ":"
	ciphertext := gcm.Seal(nil, nonce, []byte(plaintext), []byte(header))
	payload := make([]byte, 0, nonceSize+len(ciphertext))
	payload = append(payload, nonce...)
	payload = append(payload, ciphertext...)

	if err := store.Set(ctx, keyID, key); err != nil {
		return "", err
	}
	return header + base64.RawURLEncoding.EncodeToString(payload), nil
}

func DecryptString(ctx context.Context, value string) (string, error) {
	if strings.HasPrefix(value, prefixV2) {
		return decryptStringV2(ctx, value)
	}
	if !strings.HasPrefix(value, prefixV1) {
		if !strings.HasPrefix(value, Prefix) {
			return "", fmt.Errorf("%w: encrypted password must start with %q", ErrEncryptedPasswordInvalid, Prefix)
		}
		rest := strings.TrimPrefix(value, Prefix)
		version, _, ok := strings.Cut(rest, ":")
		if !ok || version == "" {
			return "", fmt.Errorf("%w: encrypted password is missing a version", ErrEncryptedPasswordInvalid)
		}
		return "", fmt.Errorf("%w: unsupported encrypted password version %q", ErrEncryptedPasswordInvalid, version)
	}

	provider, ok := ctx.Value(passphraseProviderKey{}).(PassphraseProvider)
	if !ok {
		return "", fmt.Errorf("encrypted password requires a master passphrase")
	}
	passphrase, err := provider(ctx)
	if err != nil {
		return "", err
	}
	return DecryptStringWithPassphrase(passphrase, value)
}

func DecryptStringWithPassphrase(passphrase, value string) (string, error) {
	if passphrase == "" {
		return "", fmt.Errorf("master passphrase is required")
	}
	if !strings.HasPrefix(value, Prefix) {
		return "", fmt.Errorf("%w: encrypted password must start with %q", ErrEncryptedPasswordInvalid, Prefix)
	}
	if !strings.HasPrefix(value, prefixV1) {
		rest := strings.TrimPrefix(value, Prefix)
		version, _, ok := strings.Cut(rest, ":")
		if !ok || version == "" {
			return "", fmt.Errorf("%w: encrypted password is missing a version", ErrEncryptedPasswordInvalid)
		}
		return "", fmt.Errorf("%w: unsupported encrypted password version %q", ErrEncryptedPasswordInvalid, version)
	}
	encoded := strings.TrimPrefix(value, prefixV1)
	payload, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return "", fmt.Errorf("%w: encrypted password payload is not valid base64url", ErrEncryptedPasswordInvalid)
	}
	if len(payload) <= saltSize+nonceSize {
		return "", fmt.Errorf("%w: encrypted password payload is too short", ErrEncryptedPasswordInvalid)
	}
	salt := payload[:saltSize]
	nonce := payload[saltSize : saltSize+nonceSize]
	ciphertext := payload[saltSize+nonceSize:]
	key, err := deriveKey(passphrase, salt)
	if err != nil {
		return "", err
	}
	gcm, err := newGCM(key)
	if err != nil {
		return "", err
	}
	plaintext, err := gcm.Open(nil, nonce, ciphertext, []byte(prefixV1))
	if err != nil {
		return "", fmt.Errorf("%w: encrypted password could not be decrypted", ErrEncryptedPasswordInvalid)
	}
	return string(plaintext), nil
}

func decryptStringV2(ctx context.Context, value string) (string, error) {
	rest := strings.TrimPrefix(value, prefixV2)
	keyID, encoded, ok := strings.Cut(rest, ":")
	if !ok || keyID == "" || encoded == "" {
		return "", fmt.Errorf("%w: encrypted password v2 payload is malformed", ErrEncryptedPasswordInvalid)
	}
	keyIDBytes, err := base64.RawURLEncoding.DecodeString(keyID)
	if err != nil || len(keyIDBytes) != keyIDSize {
		return "", fmt.Errorf("%w: encrypted password v2 key id is malformed", ErrEncryptedPasswordInvalid)
	}
	payload, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil || len(payload) <= nonceSize {
		return "", fmt.Errorf("%w: encrypted password v2 payload is malformed", ErrEncryptedPasswordInvalid)
	}

	store, ok := keyStoreFromContext(ctx)
	if !ok {
		return "", fmt.Errorf("%w: no credential store is configured", ErrSecretStoreUnavailable)
	}
	key, err := store.Get(ctx, keyID)
	if err != nil {
		return "", err
	}
	if len(key) != keySize {
		return "", fmt.Errorf("%w: stored encryption key has an invalid size", ErrEncryptedPasswordInvalid)
	}
	gcm, err := newGCM(key)
	if err != nil {
		return "", fmt.Errorf("%w: stored encryption key is invalid", ErrEncryptedPasswordInvalid)
	}
	nonce := payload[:nonceSize]
	ciphertext := payload[nonceSize:]
	header := prefixV2 + keyID + ":"
	plaintext, err := gcm.Open(nil, nonce, ciphertext, []byte(header))
	if err != nil {
		return "", fmt.Errorf("%w: encrypted password could not be decrypted", ErrEncryptedPasswordInvalid)
	}
	return string(plaintext), nil
}

func deriveKey(passphrase string, salt []byte) ([]byte, error) {
	return scrypt.Key([]byte(passphrase), salt, scryptN, scryptR, scryptP, keySize)
}

func newGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}
