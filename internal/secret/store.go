package secret

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"

	"github.com/zalando/go-keyring"
)

const (
	keyringService       = "com.zenmind.dbx"
	keyringAccountPrefix = "v2/"
)

type KeyStore interface {
	Get(context.Context, string) ([]byte, error)
	Set(context.Context, string, []byte) error
}

type keyStoreContextKey struct{}

type systemKeyStore struct{}

func WithKeyStore(ctx context.Context, store KeyStore) context.Context {
	if store == nil {
		return ctx
	}
	return context.WithValue(ctx, keyStoreContextKey{}, store)
}

func WithSystemKeyStore(ctx context.Context) context.Context {
	if _, ok := keyStoreFromContext(ctx); ok {
		return ctx
	}
	return WithKeyStore(ctx, systemKeyStore{})
}

func keyStoreFromContext(ctx context.Context) (KeyStore, bool) {
	if ctx == nil {
		return nil, false
	}
	store, ok := ctx.Value(keyStoreContextKey{}).(KeyStore)
	return store, ok && store != nil
}

func (systemKeyStore) Get(_ context.Context, keyID string) ([]byte, error) {
	encoded, err := keyring.Get(keyringService, keyringAccountPrefix+keyID)
	if errors.Is(err, keyring.ErrNotFound) {
		return nil, ErrSecretKeyNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrSecretStoreUnavailable, err)
	}
	key, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil || len(key) != keySize {
		return nil, fmt.Errorf("%w: stored encryption key is malformed", ErrEncryptedPasswordInvalid)
	}
	return key, nil
}

func (systemKeyStore) Set(_ context.Context, keyID string, key []byte) error {
	if len(key) != keySize {
		return fmt.Errorf("%w: encryption key has an invalid size", ErrEncryptedPasswordInvalid)
	}
	encoded := base64.RawURLEncoding.EncodeToString(key)
	if err := keyring.Set(keyringService, keyringAccountPrefix+keyID, encoded); err != nil {
		return fmt.Errorf("%w: %v", ErrSecretStoreUnavailable, err)
	}
	return nil
}
