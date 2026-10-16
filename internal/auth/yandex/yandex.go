// Package yandex signs people in with Yandex ID (OAuth 2.0, authorization code). For Yandex 360
// organization accounts the login carries the organization's domain (name@domain); a personal
// account's login has none, whatever contact address it lists. The identity therefore carries the
// login, and default_email is not read.
package yandex

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

	"github.com/ssubbotin/kunveno/internal/auth"
)

type Provider struct {
	label, clientID, secret string
	OAuthBase               string       // https://oauth.yandex.ru; tests point it at a fake
	InfoURL                 string       // https://login.yandex.ru/info
	Client                  *http.Client // 10 s timeout
}

var _ auth.Provider = (*Provider)(nil)

func New(label, clientID, secret string) *Provider {
	return &Provider{label: label, clientID: clientID, secret: secret, OAuthBase: "https://oauth.yandex.ru",
		InfoURL: "https://login.yandex.ru/info", Client: &http.Client{Timeout: 10 * time.Second}}
}

func (p *Provider) Type() string  { return "yandex" }
func (p *Provider) Label() string { return p.label }

// AuthURL asks Yandex for a code; force_confirm lets a person signed in with a personal account
// choose the organization one.
func (p *Provider) AuthURL(state, callback string) string {
	q := url.Values{"response_type": {"code"}, "client_id": {p.clientID}, "redirect_uri": {callback},
		"state": {state}, "force_confirm": {"yes"}}
	return "https://oauth.yandex.ru/authorize?" + q.Encode()
}

const maxBody = 64 << 10

func (p *Provider) Exchange(ctx context.Context, code, callback string) (auth.Identity, error) {
	form := url.Values{"grant_type": {"authorization_code"}, "code": {code}, "client_id": {p.clientID},
		"client_secret": {p.secret}, "redirect_uri": {callback}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.OAuthBase+"/token", strings.NewReader(form.Encode()))
	if err != nil {
		return auth.Identity{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	var tok struct {
		AccessToken string `json:"access_token"`
		Error       string `json:"error"`
	}
	if err := p.do(req, &tok); err != nil {
		return auth.Identity{}, fmt.Errorf("yandex token: %w", err)
	}
	if tok.AccessToken == "" {
		return auth.Identity{}, fmt.Errorf("yandex token: %s", tok.Error)
	}
	req, err = http.NewRequestWithContext(ctx, http.MethodGet, p.InfoURL+"?format=json", nil)
	if err != nil {
		return auth.Identity{}, err
	}
	req.Header.Set("Authorization", "OAuth "+tok.AccessToken)
	var info struct {
		ID          string `json:"id"`
		Login       string `json:"login"`
		RealName    string `json:"real_name"`
		DisplayName string `json:"display_name"`
	}
	if err := p.do(req, &info); err != nil {
		return auth.Identity{}, fmt.Errorf("yandex info: %w", err)
	}
	if info.ID == "" || info.Login == "" {
		return auth.Identity{}, errors.New("yandex info: no id or login")
	}
	name := info.RealName
	if strings.TrimSpace(name) == "" {
		name = info.DisplayName
	}
	return auth.Identity{Provider: "yandex", ID: info.ID, Login: info.Login, Name: name}, nil
}

// do sends req and decodes a JSON answer; a non-2xx status is an error (its body is not echoed:
// it may name the code).
func (p *Provider) do(req *http.Request, v any) error {
	res, err := p.Client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	body, err := io.ReadAll(io.LimitReader(res.Body, maxBody))
	if err != nil {
		return err
	}
	if res.StatusCode/100 != 2 {
		return fmt.Errorf("status %s", res.Status)
	}
	return json.Unmarshal(body, v)
}
