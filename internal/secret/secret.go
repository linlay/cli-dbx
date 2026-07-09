package secret

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"strings"

	"golang.org/x/crypto/scrypt"
)

const (
	Prefix   = "dbx-aes-gcm:"
	prefixV1 = Prefix + "v1:"

	saltSize  = 16
	nonceSize = 12
	keySize   = 32

	scryptN = 1 << 15
	scryptR = 8
	scryptP = 1
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

func DecryptString(ctx context.Context, value string) (string, error) {
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
		return "", fmt.Errorf("encrypted password must start with %q", Prefix)
	}
	if !strings.HasPrefix(value, prefixV1) {
		rest := strings.TrimPrefix(value, Prefix)
		version, _, ok := strings.Cut(rest, ":")
		if !ok || version == "" {
			return "", fmt.Errorf("encrypted password is missing a version")
		}
		return "", fmt.Errorf("unsupported encrypted password version %q", version)
	}
	encoded := strings.TrimPrefix(value, prefixV1)
	payload, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return "", fmt.Errorf("encrypted password payload is not valid base64url: %w", err)
	}
	if len(payload) <= saltSize+nonceSize {
		return "", fmt.Errorf("encrypted password payload is too short")
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
		return "", fmt.Errorf("encrypted password could not be decrypted")
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
