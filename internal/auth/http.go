package auth

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// LoginCookie carries the state and the return path between start and callback.
const LoginCookie = "__Host-kunveno-login"

const loginTTL = 10 * time.Minute

// Gate is the sign-in of one server: its providers, their rules, sessions and invitations.
type Gate struct {
	Providers []Provider
	Rules     map[string]Rule // by provider type
	Sessions  *Sessions
	Invites   *Invites
	PublicURL string // https://meet.example.com, no trailing slash
	Log       *slog.Logger
	OnSignIn  func(provider, result string) // metrics; may be nil
}

type ProviderInfo struct {
	Type  string `json:"type"`
	Label string `json:"label"`
}

func (g *Gate) ProviderInfos() []ProviderInfo {
	out := make([]ProviderInfo, len(g.Providers))
	for i, p := range g.Providers {
		out[i] = ProviderInfo{p.Type(), p.Label()}
	}
	return out
}

func (g *Gate) provider(typ string) Provider {
	for _, p := range g.Providers {
		if p.Type() == typ {
			return p
		}
	}
	return nil
}

func (g *Gate) Session(r *http.Request) (Identity, bool) { return g.Sessions.Read(r) }

// SameOrigin reports whether the request comes from our own pages.
func (g *Gate) SameOrigin(r *http.Request) bool { return r.Header.Get("Origin") == g.PublicURL }

// Require lets through only signed-in requests; state-changing ones must also come from our site.
func (g *Gate) Require(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := g.Session(r); !ok {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "auth_required"})
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead && !g.SameOrigin(r) {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "cross-site request"})
			return
		}
		h.ServeHTTP(w, r)
	})
}

func (g *Gate) Routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /auth/{provider}/start", g.start)
	mux.HandleFunc("GET /auth/{provider}/callback", g.callback)
	mux.HandleFunc("POST /auth/logout", g.logout)
	mux.HandleFunc("GET /auth/me", g.me)
}

// localPath keeps the return path on our site: one leading "/" (not "//"), no control bytes (browsers
// strip tab, CR and LF, which would turn "/\t/x" into "//x") and no backslash, and it parses as a
// plain path.
func localPath(p string) string {
	if !strings.HasPrefix(p, "/") || strings.HasPrefix(p, "//") {
		return "/"
	}
	for i := 0; i < len(p); i++ {
		if p[i] < 0x20 || p[i] == 0x7f || p[i] == '\\' {
			return "/"
		}
	}
	if u, err := url.Parse(p); err != nil || u.Scheme != "" || u.Host != "" {
		return "/"
	}
	return p
}

func (g *Gate) callbackURL(typ string) string { return g.PublicURL + "/auth/" + typ + "/callback" }

func (g *Gate) start(w http.ResponseWriter, r *http.Request) {
	p := g.provider(r.PathValue("provider"))
	if p == nil {
		http.NotFound(w, r)
		return
	}
	state, err := random(24)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	b, _ := json.Marshal(map[string]string{"s": state, "p": p.Type(), "r": localPath(r.URL.Query().Get("return")),
		"e": strconv.FormatInt(g.Sessions.Now().Add(loginTTL).Unix(), 10)})
	http.SetCookie(w, cookie(LoginCookie, g.Sessions.sign("login", b), int(loginTTL.Seconds())))
	http.Redirect(w, r, p.AuthURL(state, g.callbackURL(p.Type())), http.StatusFound)
}

func (g *Gate) callback(w http.ResponseWriter, r *http.Request) {
	p := g.provider(r.PathValue("provider"))
	if p == nil {
		http.NotFound(w, r)
		return
	}
	http.SetCookie(w, cookie(LoginCookie, "", -1)) // single use
	var st map[string]string
	c, err := r.Cookie(LoginCookie)
	if err == nil {
		if b, ok := g.Sessions.verify("login", c.Value); ok {
			_ = json.Unmarshal(b, &st)
		}
	}
	q := r.URL.Query()
	exp, _ := strconv.ParseInt(st["e"], 10, 64)
	if st == nil || g.Sessions.Now().Unix() >= exp || st["p"] != p.Type() || st["s"] == "" || q.Get("state") != st["s"] {
		http.Error(w, "the sign-in expired or did not start here; try again", http.StatusBadRequest)
		return
	}
	id, err := p.Exchange(r.Context(), q.Get("code"), g.callbackURL(p.Type()))
	if err != nil {
		g.Log.Warn("sign-in failed", "provider", p.Type(), "err", err)
		g.count(p.Type(), "error")
		http.Error(w, "sign-in is unavailable, try later", http.StatusBadGateway)
		return
	}
	if !g.Rules[p.Type()].Allows(id.Login) {
		g.Log.Info("sign-in refused", "provider", p.Type(), "login", id.Login)
		g.count(p.Type(), "refused")
		http.Error(w, "this account is not allowed", http.StatusForbidden)
		return
	}
	g.Log.Info("signed in", "provider", p.Type(), "login", id.Login)
	g.count(p.Type(), "ok")
	g.Sessions.Issue(w, id)
	http.Redirect(w, r, localPath(st["r"]), http.StatusFound)
}

func (g *Gate) count(provider, result string) {
	if g.OnSignIn != nil {
		g.OnSignIn(provider, result)
	}
}

func (g *Gate) logout(w http.ResponseWriter, r *http.Request) {
	if !g.SameOrigin(r) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "cross-site request"})
		return
	}
	g.Sessions.Clear(w)
	w.WriteHeader(http.StatusNoContent)
}

func (g *Gate) me(w http.ResponseWriter, r *http.Request) {
	id, ok := g.Session(r)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "auth_required"})
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]string{"name": DisplayName(id), "login": id.Login, "provider": id.Provider})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
