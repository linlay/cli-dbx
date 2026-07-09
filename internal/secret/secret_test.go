package secret

import (
	"context"
	"strings"
	"testing"
)

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
