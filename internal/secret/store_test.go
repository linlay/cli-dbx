package secret

import (
	"context"
	"errors"
	"testing"

	"github.com/zalando/go-keyring"
)

func TestSystemKeyStoreMapsBackendAndDataErrors(t *testing.T) {
	keyring.MockInit()
	store := systemKeyStore{}
	ctx := context.Background()
	key := []byte("01234567890123456789012345678901")

	if err := store.Set(ctx, "test-key", key); err != nil {
		t.Fatal(err)
	}
	got, err := store.Get(ctx, "test-key")
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(key) {
		t.Fatalf("stored key = %q, want %q", got, key)
	}

	_, err = store.Get(ctx, "missing-key")
	if !errors.Is(err, ErrSecretKeyNotFound) {
		t.Fatalf("expected missing key error, got %v", err)
	}
	if err := keyring.Set(keyringService, keyringAccountPrefix+"corrupt-key", "not-base64url!"); err != nil {
		t.Fatal(err)
	}
	_, err = store.Get(ctx, "corrupt-key")
	if !errors.Is(err, ErrEncryptedPasswordInvalid) {
		t.Fatalf("expected invalid stored key error, got %v", err)
	}

	backendErr := errors.New("test backend unavailable")
	keyring.MockInitWithError(backendErr)
	_, err = store.Get(ctx, "test-key")
	if !errors.Is(err, ErrSecretStoreUnavailable) {
		t.Fatalf("expected unavailable store get error, got %v", err)
	}
	err = store.Set(ctx, "test-key", key)
	if !errors.Is(err, ErrSecretStoreUnavailable) {
		t.Fatalf("expected unavailable store set error, got %v", err)
	}
}
