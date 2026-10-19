package service

import (
	"encoding/base64"
	"testing"
)

func TestNewSessionTokenIsRandomURLSafeString(t *testing.T) {
	first, err := newSessionToken()
	if err != nil {
		t.Fatalf("newSessionToken() error: %v", err)
	}
	second, err := newSessionToken()
	if err != nil {
		t.Fatalf("newSessionToken() error: %v", err)
	}

	if first == second {
		t.Fatal("two tokens must differ")
	}
	if want := base64.RawURLEncoding.EncodedLen(sessionTokenBytes); len(first) != want {
		t.Fatalf("token length = %d, want %d", len(first), want)
	}
	if _, err := base64.RawURLEncoding.DecodeString(first); err != nil {
		t.Fatalf("token is not URL-safe base64: %v", err)
	}
}
