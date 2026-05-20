package auth_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"sonarr.tv/sonarr/internal/auth"
	"sonarr.tv/sonarr/internal/datastore/datastoretest"
	"sonarr.tv/sonarr/internal/users"
)

// fakeCookieSetter satisfies auth.CookieSetter.
type fakeCookieSetter struct {
	name    string
	secret  []byte
	urlBase string
}

func (f fakeCookieSetter) SessionCookieName(context.Context) (string, error) {
	return f.name, nil
}
func (f fakeCookieSetter) SessionSecret(context.Context) ([]byte, error) {
	return f.secret, nil
}
func (f fakeCookieSetter) URLBase(context.Context) (string, error) {
	return f.urlBase, nil
}

// newAuthFixture builds an auth.Service backed by an SQLite in-
// memory DB with the user pre-created. Returns the service +
// identifier of the seeded user.
//
//nolint:unparam // username currently always "admin"; param kept for future test diversity.
func newAuthFixture(t *testing.T, username, password string) (*auth.Service, string) {
	t.Helper()
	db := datastoretest.OpenSQLite(t)
	repo, err := users.NewRepo(db)
	if err != nil {
		t.Fatalf("users.NewRepo: %v", err)
	}
	svc := auth.NewService(repo)
	u, err := svc.Add(t.Context(), username, password)
	if err != nil {
		t.Fatalf("svc.Add: %v", err)
	}
	return svc, u.Identifier
}

func TestLoginHandler_FormSuccess(t *testing.T) {
	t.Parallel()
	svc, identifier := newAuthFixture(t, "admin", "hunter2")
	cookies := fakeCookieSetter{name: "TestAuth", secret: auth.SessionSecret("k")}

	form := strings.NewReader("username=admin&password=hunter2&rememberMe=on")
	req := httptest.NewRequest(http.MethodPost, "/login", form)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()
	auth.LoginHandler(svc, cookies).ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body)
	}
	setCookie := rr.Header().Get("Set-Cookie")
	if !strings.Contains(setCookie, "TestAuth=") {
		t.Errorf("Set-Cookie missing cookie name: %q", setCookie)
	}
	if !strings.Contains(setCookie, "HttpOnly") {
		t.Errorf("Set-Cookie should be HttpOnly: %q", setCookie)
	}
	if !strings.Contains(setCookie, "Max-Age=") {
		t.Errorf("rememberMe=on should set Max-Age: %q", setCookie)
	}
	if !strings.Contains(rr.Body.String(), identifier) {
		t.Errorf("response body should carry identifier: %q", rr.Body.String())
	}
}

func TestLoginHandler_JSONSuccess(t *testing.T) {
	t.Parallel()
	svc, _ := newAuthFixture(t, "admin", "hunter2")
	cookies := fakeCookieSetter{name: "TestAuth", secret: auth.SessionSecret("k")}

	req := httptest.NewRequest(http.MethodPost, "/login",
		strings.NewReader(`{"username":"admin","password":"hunter2"}`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	auth.LoginHandler(svc, cookies).ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	// rememberMe not set → session-only cookie (no Max-Age).
	setCookie := rr.Header().Get("Set-Cookie")
	if strings.Contains(setCookie, "Max-Age=") {
		t.Errorf("session-only login should not set Max-Age: %q", setCookie)
	}
}

func TestLoginHandler_WrongPassword(t *testing.T) {
	t.Parallel()
	svc, _ := newAuthFixture(t, "admin", "hunter2")
	cookies := fakeCookieSetter{name: "TestAuth", secret: auth.SessionSecret("k")}

	req := httptest.NewRequest(http.MethodPost, "/login",
		strings.NewReader("username=admin&password=wrong"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()
	auth.LoginHandler(svc, cookies).ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("wrong password should 401; got %d", rr.Code)
	}
	if rr.Header().Get("Set-Cookie") != "" {
		t.Errorf("failed login must not set cookie")
	}
}

func TestLoginHandler_MissingFields(t *testing.T) {
	t.Parallel()
	svc, _ := newAuthFixture(t, "admin", "hunter2")
	cookies := fakeCookieSetter{name: "TestAuth", secret: auth.SessionSecret("k")}
	req := httptest.NewRequest(http.MethodPost, "/login",
		strings.NewReader("username=&password=&rememberMe="))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()
	auth.LoginHandler(svc, cookies).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("empty credentials should 400; got %d", rr.Code)
	}
}

func TestLogoutHandler_ClearsCookie(t *testing.T) {
	t.Parallel()
	cookies := fakeCookieSetter{name: "TestAuth", urlBase: "/sonarr"}
	req := httptest.NewRequest(http.MethodGet, "/logout", nil)
	rr := httptest.NewRecorder()
	auth.LogoutHandler(cookies).ServeHTTP(rr, req)
	if rr.Code != http.StatusNoContent {
		t.Errorf("logout should 204; got %d", rr.Code)
	}
	setCookie := rr.Header().Get("Set-Cookie")
	if !strings.Contains(setCookie, "TestAuth=;") {
		t.Errorf("logout should clear cookie (TestAuth=;): %q", setCookie)
	}
	if !strings.Contains(setCookie, "Max-Age=0") {
		t.Errorf("logout should set Max-Age=0: %q", setCookie)
	}
	if !strings.Contains(setCookie, "Path=/sonarr/") {
		t.Errorf("logout should scope to urlBase Path: %q", setCookie)
	}
}
