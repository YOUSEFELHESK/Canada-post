package service

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"fmt"
	"net/http"

	"lexmodo-plugin/config"

	"golang.org/x/oauth2"
)

// =========================
// 🔐 OAUTH STATE MANAGEMENT
// =========================

func GenerateState() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// =========================
// 🌍 OAUTH CONFIGURATION
// =========================

func NewOAuthConfig(cfg config.Config) *oauth2.Config {
	config := &oauth2.Config{
		ClientID:     cfg.AppClientID,
		ClientSecret: cfg.AppSecret,
		RedirectURL:  cfg.RedirectURI,
		Endpoint: oauth2.Endpoint{
			AuthURL:  cfg.AuthorizeURL,
			TokenURL: cfg.TokenURL,
		},
	}
	fmt.Println("✅ OAuth Config Initialized")
	return config
}

func ExchangeCodeForToken(oauthConfig *oauth2.Config, code string) (*oauth2.Token, error) {
	ctx := context.Background()

	// 🧩 For development only: skip TLS cert verification
	httpClient := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
	}

	token, err := oauthConfig.Exchange(
		context.WithValue(ctx, oauth2.HTTPClient, httpClient),
		code,
	)
	if err != nil {
		return nil, fmt.Errorf("token exchange failed: %v", err)
	}

	fmt.Printf("✅ Token exchange result: AccessToken=%s RefreshToken=%s Expires=%v\n",
		token.AccessToken, token.RefreshToken, token.Expiry)

	return token, nil
}

func GetStringValue(s string) string {
	if s == "" {
		return ""
	}
	return s
}
