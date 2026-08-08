package gmail

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/oauth2"
)

func TestTokenCache_SaveThenLoad_RoundTrips(t *testing.T) {
	path := filepath.Join(t.TempDir(), "token.json")
	cache := NewTokenCache(path)

	want := &oauth2.Token{
		AccessToken:  "access-123",
		RefreshToken: "refresh-456",
		TokenType:    "Bearer",
		Expiry:       time.Date(2026, 8, 7, 0, 0, 0, 0, time.UTC),
	}

	if err := cache.Save(want); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	got, err := cache.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got.AccessToken != want.AccessToken || got.RefreshToken != want.RefreshToken {
		t.Errorf("Load() = %+v, want %+v", got, want)
	}
}

func TestTokenCache_Load_MissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "does-not-exist.json")
	cache := NewTokenCache(path)

	_, err := cache.Load()
	if err == nil {
		t.Fatal("Load() error = nil, want error for missing file")
	}
}

const fakeCredentialsJSON = `{
	"installed": {
		"client_id": "test-client-id.apps.googleusercontent.com",
		"client_secret": "test-secret",
		"auth_uri": "https://accounts.google.com/o/oauth2/auth",
		"token_uri": "https://oauth2.googleapis.com/token",
		"redirect_uris": ["urn:ietf:wg:oauth:2.0:oob", "http://localhost"]
	}
}`

func TestLoadConfig_ParsesClientCredentials(t *testing.T) {
	config, err := LoadConfig([]byte(fakeCredentialsJSON))
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if config.ClientID != "test-client-id.apps.googleusercontent.com" {
		t.Errorf("ClientID = %q, want test-client-id.apps.googleusercontent.com", config.ClientID)
	}
	if len(config.Scopes) != 1 || config.Scopes[0] != Scope {
		t.Errorf("Scopes = %v, want [%s]", config.Scopes, Scope)
	}
}

func TestLoadConfig_InvalidJSON(t *testing.T) {
	_, err := LoadConfig([]byte("not json"))
	if err == nil {
		t.Fatal("LoadConfig() error = nil, want error for invalid JSON")
	}
}

func TestGetClient_UsesCachedToken_SkipsPrompt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "token.json")
	cache := NewTokenCache(path)
	cachedTok := &oauth2.Token{AccessToken: "cached-access", RefreshToken: "cached-refresh", Expiry: time.Now().Add(time.Hour)}
	if err := cache.Save(cachedTok); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	config := &oauth2.Config{ClientID: "id", Scopes: []string{Scope}}
	promptCalled := false
	promptFunc := func(authURL string) (string, error) {
		promptCalled = true
		return "", errors.New("prompt should not be called")
	}

	_, err := GetClient(context.Background(), config, cache, promptFunc)
	if err != nil {
		t.Fatalf("GetClient() error = %v", err)
	}
	if promptCalled {
		t.Error("promptFunc was called even though a valid cached token existed")
	}
}
