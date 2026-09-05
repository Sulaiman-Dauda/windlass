package auth

// OAuth sign-in for GitHub and Google, hand-rolled on net/http: the flow is
// three requests (authorize redirect, token exchange, profile fetch), an
// OAuth library is not justified (principle 10).
//
// Policy: OAuth only signs in EXISTING users, matched by verified email.
// Admins create accounts first; a stray Google account never becomes a user
// by itself.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type OAuthProviderConfig struct {
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
}

func (c OAuthProviderConfig) Configured() bool {
	return c.ClientID != "" && c.ClientSecret != ""
}

type oauthEndpoints struct {
	authURL  string
	tokenURL string
	scope    string
}

var oauthProviders = map[string]oauthEndpoints{
	"github": {
		authURL:  "https://github.com/login/oauth/authorize",
		tokenURL: "https://github.com/login/oauth/access_token",
		scope:    "user:email",
	},
	"google": {
		authURL:  "https://accounts.google.com/o/oauth2/v2/auth",
		tokenURL: "https://oauth2.googleapis.com/token",
		scope:    "openid email",
	},
}

var ErrUnknownProvider = errors.New("unknown oauth provider")

// OAuthAuthorizeURL builds the provider redirect for the start of the flow.
// An empty scope uses the provider's sign-in default; callers needing broader
// access (e.g. the repo scope for git connections) pass their own.
func OAuthAuthorizeURL(provider string, cfg OAuthProviderConfig, redirectURI, state, scope string) (string, error) {
	ep, ok := oauthProviders[provider]
	if !ok {
		return "", ErrUnknownProvider
	}
	if scope == "" {
		scope = ep.scope
	}
	q := url.Values{
		"client_id":     {cfg.ClientID},
		"redirect_uri":  {redirectURI},
		"state":         {state},
		"scope":         {scope},
		"response_type": {"code"},
	}
	return ep.authURL + "?" + q.Encode(), nil
}

// OAuthAccessToken exchanges an authorization code for an access token.
func OAuthAccessToken(ctx context.Context, provider string, cfg OAuthProviderConfig, redirectURI, code string) (string, error) {
	ep, ok := oauthProviders[provider]
	if !ok {
		return "", ErrUnknownProvider
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	form := url.Values{
		"client_id":     {cfg.ClientID},
		"client_secret": {cfg.ClientSecret},
		"code":          {code},
		"redirect_uri":  {redirectURI},
		"grant_type":    {"authorization_code"},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, ep.tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var tok struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tok); err != nil || tok.AccessToken == "" {
		return "", errors.New("oauth token exchange failed")
	}
	return tok.AccessToken, nil
}

// OAuthEmail exchanges the authorization code and returns the account's
// verified email address.
func OAuthEmail(ctx context.Context, provider string, cfg OAuthProviderConfig, redirectURI, code string) (string, error) {
	token, err := OAuthAccessToken(ctx, provider, cfg, redirectURI, code)
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	switch provider {
	case "github":
		return githubEmail(ctx, token)
	case "google":
		return googleEmail(ctx, token)
	}
	return "", ErrUnknownProvider
}

// GitHubLogin returns the username of the account the token belongs to.
func GitHubLogin(ctx context.Context, token string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.github.com/user", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var user struct {
		Login string `json:"login"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&user); err != nil || user.Login == "" {
		return "", errors.New("could not read GitHub profile")
	}
	return user.Login, nil
}

func githubEmail(ctx context.Context, token string) (string, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.github.com/user/emails", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var emails []struct {
		Email    string `json:"email"`
		Primary  bool   `json:"primary"`
		Verified bool   `json:"verified"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&emails); err != nil {
		return "", err
	}
	for _, e := range emails {
		if e.Primary && e.Verified {
			return e.Email, nil
		}
	}
	return "", errors.New("no verified primary email on the GitHub account")
}

func googleEmail(ctx context.Context, token string) (string, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://openidconnect.googleapis.com/v1/userinfo", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", err
	}
	var info struct {
		Email         string `json:"email"`
		EmailVerified bool   `json:"email_verified"`
	}
	if err := json.Unmarshal(body, &info); err != nil {
		return "", err
	}
	if !info.EmailVerified || info.Email == "" {
		return "", fmt.Errorf("google account email not verified")
	}
	return info.Email, nil
}
