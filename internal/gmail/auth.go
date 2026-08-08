package gmail

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

const Scope = "https://www.googleapis.com/auth/gmail.modify"

type TokenCache struct {
	path string
}

func NewTokenCache(path string) *TokenCache {
	return &TokenCache{path: path}
}

func (c *TokenCache) Load() (*oauth2.Token, error) {
	data, err := os.ReadFile(c.path)
	if err != nil {
		return nil, fmt.Errorf("reading token cache: %w", err)
	}
	var tok oauth2.Token
	if err := json.Unmarshal(data, &tok); err != nil {
		return nil, fmt.Errorf("parsing token cache: %w", err)
	}
	return &tok, nil
}

func (c *TokenCache) Save(tok *oauth2.Token) error {
	data, err := json.MarshalIndent(tok, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling token: %w", err)
	}
	if err := os.WriteFile(c.path, data, 0o600); err != nil {
		return fmt.Errorf("writing token cache: %w", err)
	}
	return nil
}

func LoadConfig(credentialsJSON []byte) (*oauth2.Config, error) {
	config, err := google.ConfigFromJSON(credentialsJSON, Scope)
	if err != nil {
		return nil, fmt.Errorf("parsing credentials.json: %w", err)
	}
	return config, nil
}

// GetClient returns an authenticated HTTP client, reusing the cached token
// if present and valid, otherwise running the OAuth2 consent flow via
// promptFunc (which is given the auth URL and must return the code the
// user obtained after authorizing).
func GetClient(ctx context.Context, config *oauth2.Config, cache *TokenCache, promptFunc func(authURL string) (code string, err error)) (*http.Client, error) {
	tok, err := cache.Load()
	if err != nil {
		authURL := config.AuthCodeURL("state-token", oauth2.AccessTypeOffline)
		code, err := promptFunc(authURL)
		if err != nil {
			return nil, fmt.Errorf("getting auth code: %w", err)
		}
		tok, err = config.Exchange(ctx, code)
		if err != nil {
			return nil, fmt.Errorf("exchanging auth code: %w", err)
		}
		if err := cache.Save(tok); err != nil {
			return nil, fmt.Errorf("caching token: %w", err)
		}
	}
	return config.Client(ctx, tok), nil
}
