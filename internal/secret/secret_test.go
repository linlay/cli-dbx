package secret

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

type memoryKeyStore struct {
	keys   map[string][]byte
	getErr error
	setErr error
}

func newMemoryKeyStore() *memoryKeyStore {
	return &memoryKeyStore{keys: map[string][]byte{}}
}

func (s *memoryKeyStore) Get(_ context.Context, keyID string) ([]byte, error) {
	if s.getErr != nil {
		return nil, s.getErr
	}
	key, ok := s.keys[keyID]
	if !ok {
		return nil, ErrSecretKeyNotFound
	}
	return append([]byte(nil), key...), nil
}

func (s *memoryKeyStore) Set(_ context.Context, keyID string, key []byte) error {
	if s.setErr != nil {
		return s.setErr
	}
	s.keys[keyID] = append([]byte(nil), key...)
	return nil
}

func v2KeyID(t *testing.T, value string) string {
	t.Helper()
	rest := strings.TrimPrefix(value, prefixV2)
	keyID, _, ok := strings.Cut(rest, ":")
	if !ok || keyID == "" {
		t.Fatalf("invalid v2 value: %q", value)
	}
	return keyID
}

func TestEncryptDecryptStringRoundTrip(t *testing.T) {
	value, err := EncryptString("master-passphrase", "db-password")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(value, "dbx-aes-gcm:v1:") {
		t.Fatalf("encrypted value prefix = %q", value)
	}

	got, err := DecryptStringWithPassphrase("master-passphrase", value)
	if err != nil {
		t.Fatal(err)
	}
	if got != "db-password" {
		t.Fatalf("decrypted password = %q", got)
	}
}

func TestEncryptDecryptStringV2RoundTrip(t *testing.T) {
	store := newMemoryKeyStore()
	ctx := WithKeyStore(context.Background(), store)

	first, err := EncryptStringV2(ctx, "db-password")
	if err != nil {
		t.Fatal(err)
	}
	second, err := EncryptStringV2(ctx, "db-password")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(first, prefixV2) {
		t.Fatalf("encrypted value prefix = %q", first)
	}
	if first == second {
		t.Fatal("separate encryptions must use distinct keys and nonces")
	}
	if len(store.keys) != 2 {
		t.Fatalf("stored keys = %d, want 2", len(store.keys))
	}

	got, err := DecryptString(ctx, first)
	if err != nil {
		t.Fatal(err)
	}
	if got != "db-password" {
		t.Fatalf("decrypted password = %q", got)
	}
}

func TestDecryptStringV2DoesNotRequestPassphrase(t *testing.T) {
	store := newMemoryKeyStore()
	ctx := WithKeyStore(context.Background(), store)
	value, err := EncryptStringV2(ctx, "db-password")
	if err != nil {
		t.Fatal(err)
	}
	ctx = WithPassphraseProvider(ctx, func(context.Context) (string, error) {
		return "", fmt.Errorf("passphrase provider must not be called")
	})

	got, err := DecryptString(ctx, value)
	if err != nil {
		t.Fatal(err)
	}
	if got != "db-password" {
		t.Fatalf("decrypted password = %q", got)
	}
}

func TestDecryptStringV2RejectsMissingAndUnavailableStores(t *testing.T) {
	store := newMemoryKeyStore()
	ctx := WithKeyStore(context.Background(), store)
	value, err := EncryptStringV2(ctx, "db-password")
	if err != nil {
		t.Fatal(err)
	}

	_, err = DecryptString(context.Background(), value)
	if !errors.Is(err, ErrSecretStoreUnavailable) {
		t.Fatalf("expected unavailable store error, got %v", err)
	}
	_, err = DecryptString(WithKeyStore(context.Background(), newMemoryKeyStore()), value)
	if !errors.Is(err, ErrSecretKeyNotFound) {
		t.Fatalf("expected missing key error, got %v", err)
	}
	unavailable := newMemoryKeyStore()
	unavailable.getErr = fmt.Errorf("%w: test backend unavailable", ErrSecretStoreUnavailable)
	_, err = DecryptString(WithKeyStore(context.Background(), unavailable), value)
	if !errors.Is(err, ErrSecretStoreUnavailable) {
		t.Fatalf("expected unavailable store error, got %v", err)
	}
}

func TestEncryptStringV2PropagatesStoreFailure(t *testing.T) {
	store := newMemoryKeyStore()
	store.setErr = fmt.Errorf("%w: test backend unavailable", ErrSecretStoreUnavailable)
	_, err := EncryptStringV2(WithKeyStore(context.Background(), store), "db-password")
	if !errors.Is(err, ErrSecretStoreUnavailable) {
		t.Fatalf("expected unavailable store error, got %v", err)
	}
}

func TestDecryptStringV2RejectsTamperingAndCorruptKey(t *testing.T) {
	store := newMemoryKeyStore()
	ctx := WithKeyStore(context.Background(), store)
	value, err := EncryptStringV2(ctx, "db-password")
	if err != nil {
		t.Fatal(err)
	}

	tampered := value[:len(value)-1] + "A"
	if tampered == value {
		tampered = value[:len(value)-1] + "B"
	}
	_, err = DecryptString(ctx, tampered)
	if !errors.Is(err, ErrEncryptedPasswordInvalid) {
		t.Fatalf("expected tampered payload error, got %v", err)
	}

	keyID := v2KeyID(t, value)
	store.keys[keyID] = []byte("too-short")
	_, err = DecryptString(ctx, value)
	if !errors.Is(err, ErrEncryptedPasswordInvalid) {
		t.Fatalf("expected corrupt key error, got %v", err)
	}
}

func TestDecryptStringUsesContextPassphraseProvider(t *testing.T) {
	value, err := EncryptString("master-passphrase", "db-password")
	if err != nil {
		t.Fatal(err)
	}
	ctx := WithPassphraseProvider(context.Background(), func(context.Context) (string, error) {
		return "master-passphrase", nil
	})

	got, err := DecryptString(ctx, value)
	if err != nil {
		t.Fatal(err)
	}
	if got != "db-password" {
		t.Fatalf("decrypted password = %q", got)
	}
}

func TestDecryptStringRejectsBadInputs(t *testing.T) {
	value, err := EncryptString("master-passphrase", "db-password")
	if err != nil {
		t.Fatal(err)
	}

	testCases := []struct {
		name       string
		passphrase string
		value      string
		want       string
	}{
		{name: "wrong passphrase", passphrase: "wrong", value: value, want: "could not be decrypted"},
		{name: "bad payload", passphrase: "master-passphrase", value: "dbx-aes-gcm:v1:not base64", want: "not valid base64url"},
		{name: "bad version", passphrase: "master-passphrase", value: "dbx-aes-gcm:v2:abc", want: "unsupported encrypted password version"},
		{name: "missing version", passphrase: "master-passphrase", value: "dbx-aes-gcm:abc", want: "missing a version"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := DecryptStringWithPassphrase(tc.passphrase, tc.value)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected %q error, got %v", tc.want, err)
			}
		})
	}
}

func TestDecryptStringRequiresProvider(t *testing.T) {
	value, err := EncryptString("master-passphrase", "db-password")
	if err != nil {
		t.Fatal(err)
	}
	_, err = DecryptString(context.Background(), value)
	if err == nil || !strings.Contains(err.Error(), "requires a master passphrase") {
		t.Fatalf("expected missing provider error, got %v", err)
	}
}
