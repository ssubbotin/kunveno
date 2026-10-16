package yandex

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/ssubbotin/kunveno/internal/auth"
)

// fakeYandex answers the token exchange for code "good" and the user info for token "T".
func fakeYandex(t *testing.T, info string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			_ = r.ParseForm()
			if r.Form.Get("grant_type") != "authorization_code" || r.Form.Get("code") != "good" ||
				r.Form.Get("client_id") != "cid" || r.Form.Get("client_secret") != "sec" {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"error":"bad_verification_code"}`))
				return
			}
			_, _ = w.Write([]byte(`{"access_token":"T","token_type":"bearer","expires_in":3600}`))
		case "/info":
			if r.Header.Get("Authorization") != "OAuth T" || r.URL.Query().Get("format") != "json" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			_, _ = w.Write([]byte(info))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

func provider(srv *httptest.Server) *Provider {
	p := New("Yandex 360", "cid", "sec")
	p.OAuthBase, p.InfoURL, p.Client = srv.URL, srv.URL+"/info", srv.Client()
	return p
}

func TestOrganizationAccount(t *testing.T) {
	srv := fakeYandex(t, `{"id":"1130000012345678","login":"ann@example.com","real_name":"Анна Иванова","display_name":"ann@example.com"}`)
	defer srv.Close()
	id, err := provider(srv).Exchange(context.Background(), "good", "https://meet.example.com/auth/yandex/callback")
	if err != nil {
		t.Fatal(err)
	}
	want := auth.Identity{Provider: "yandex", ID: "1130000012345678", Login: "ann@example.com", Name: "Анна Иванова"}
	if id != want || !(auth.Rule{"*@example.com"}).Allows(id.Login) {
		t.Fatalf("identity %+v", id)
	}
}

// A personal account with the work address attached as its contact email is not an employee.
func TestPersonalAccountWithAWorkAddressIsRefused(t *testing.T) {
	srv := fakeYandex(t, `{"id":"32179947","login":"ann.ivanova","real_name":"Анна Иванова","display_name":"Анна","default_email":"ann@example.com"}`)
	defer srv.Close()
	id, err := provider(srv).Exchange(context.Background(), "good", "cb")
	if err != nil {
		t.Fatal(err)
	}
	if (auth.Rule{"*@example.com"}).Allows(id.Login) {
		t.Fatalf("personal account allowed: %+v", id)
	}
}

func TestExchangeErrors(t *testing.T) {
	srv := fakeYandex(t, `{"id":"1"}`) // no login
	defer srv.Close()
	p := provider(srv)
	if _, err := p.Exchange(context.Background(), "bad", "cb"); err == nil {
		t.Error("a refused code passed")
	}
	if _, err := p.Exchange(context.Background(), "good", "cb"); err == nil {
		t.Error("an identity without a login passed")
	}
}

func TestAuthURL(t *testing.T) {
	u, err := url.Parse(New("Yandex 360", "cid", "sec").AuthURL("st", "https://meet.example.com/auth/yandex/callback"))
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	if u.Host != "oauth.yandex.ru" || u.Path != "/authorize" || q.Get("response_type") != "code" || q.Get("client_id") != "cid" ||
		q.Get("state") != "st" || q.Get("redirect_uri") != "https://meet.example.com/auth/yandex/callback" || q.Get("force_confirm") != "yes" {
		t.Fatalf("auth url %s", u)
	}
	if strings.Contains(u.String(), "sec") {
		t.Fatal("the secret is in the browser's URL")
	}
}
