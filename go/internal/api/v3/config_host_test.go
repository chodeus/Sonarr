package v3

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeHostConfig satisfies both HostConfigGetter and HostConfigWriter.
// All setter calls are recorded by both incrementing callCount and
// writing the value into the corresponding field so the getter reads
// back what was just set (matching the round-trip-response shape).
type fakeHostConfig struct {
	bindAddress               string
	port                      int
	sslPort                   int
	enableSsl                 bool
	launchBrowser             bool
	authMethod                string
	authRequired              string
	analyticsEnabled          bool
	logLevel                  string
	logSizeLimit              int
	consoleLogLevel           string
	branch                    string
	apiKey                    string
	sslCertPath               string
	sslKeyPath                string
	sslCertPassword           string
	urlBase                   string
	instanceName              string
	trustCgnat                bool
	updateAuto                bool
	updateMech                string
	updateScript              string
	applicationUrl            string
	backupFolder              string
	backupInterval            int
	backupRetention           int
	certValidation            string
	proxyEnabled              bool
	proxyType                 string
	proxyHostname             string
	proxyPort                 int
	proxyUsername             string
	proxyPassword             string
	proxyBypassFilter         string
	proxyBypassLocalAddresses bool

	callCount int
}

func (f *fakeHostConfig) BindAddress(_ context.Context) (string, error) { return f.bindAddress, nil }
func (f *fakeHostConfig) Port(_ context.Context) (int, error)           { return f.port, nil }
func (f *fakeHostConfig) SslPort(_ context.Context) (int, error)        { return f.sslPort, nil }
func (f *fakeHostConfig) EnableSsl(_ context.Context) (bool, error)     { return f.enableSsl, nil }
func (f *fakeHostConfig) LaunchBrowser(_ context.Context) (bool, error) { return f.launchBrowser, nil }
func (f *fakeHostConfig) AuthenticationMethod(_ context.Context) (string, error) {
	return f.authMethod, nil
}
func (f *fakeHostConfig) AuthenticationRequired(_ context.Context) (string, error) {
	return f.authRequired, nil
}
func (f *fakeHostConfig) AnalyticsEnabled(_ context.Context) (bool, error) {
	return f.analyticsEnabled, nil
}
func (f *fakeHostConfig) LogLevel(_ context.Context) (string, error)  { return f.logLevel, nil }
func (f *fakeHostConfig) LogSizeLimit(_ context.Context) (int, error) { return f.logSizeLimit, nil }
func (f *fakeHostConfig) ConsoleLogLevel(_ context.Context) (string, error) {
	return f.consoleLogLevel, nil
}
func (f *fakeHostConfig) Branch(_ context.Context) (string, error)      { return f.branch, nil }
func (f *fakeHostConfig) APIKey(_ context.Context) (string, error)      { return f.apiKey, nil }
func (f *fakeHostConfig) SslCertPath(_ context.Context) (string, error) { return f.sslCertPath, nil }
func (f *fakeHostConfig) SslKeyPath(_ context.Context) (string, error)  { return f.sslKeyPath, nil }
func (f *fakeHostConfig) SslCertPassword(_ context.Context) (string, error) {
	return f.sslCertPassword, nil
}
func (f *fakeHostConfig) URLBase(_ context.Context) (string, error)      { return f.urlBase, nil }
func (f *fakeHostConfig) InstanceName(_ context.Context) (string, error) { return f.instanceName, nil }
func (f *fakeHostConfig) TrustCgnatIpAddresses(_ context.Context) (bool, error) {
	return f.trustCgnat, nil
}
func (f *fakeHostConfig) UpdateAutomatically(_ context.Context) (bool, error) {
	return f.updateAuto, nil
}
func (f *fakeHostConfig) UpdateMechanism(_ context.Context) (string, error) {
	return f.updateMech, nil
}
func (f *fakeHostConfig) UpdateScriptPath(_ context.Context) (string, error) {
	return f.updateScript, nil
}
func (f *fakeHostConfig) ApplicationUrl() string          { return f.applicationUrl }
func (f *fakeHostConfig) BackupFolder() string            { return f.backupFolder }
func (f *fakeHostConfig) BackupInterval() int             { return f.backupInterval }
func (f *fakeHostConfig) BackupRetention() int            { return f.backupRetention }
func (f *fakeHostConfig) CertificateValidation() string   { return f.certValidation }
func (f *fakeHostConfig) ProxyEnabled() bool              { return f.proxyEnabled }
func (f *fakeHostConfig) ProxyType() string               { return f.proxyType }
func (f *fakeHostConfig) ProxyHostname() string           { return f.proxyHostname }
func (f *fakeHostConfig) ProxyPort() int                  { return f.proxyPort }
func (f *fakeHostConfig) ProxyUsername() string           { return f.proxyUsername }
func (f *fakeHostConfig) ProxyPassword() string           { return f.proxyPassword }
func (f *fakeHostConfig) ProxyBypassFilter() string       { return f.proxyBypassFilter }
func (f *fakeHostConfig) ProxyBypassLocalAddresses() bool { return f.proxyBypassLocalAddresses }

// --- setters ---

func (f *fakeHostConfig) SetBindAddress(_ context.Context, v string) error {
	f.bindAddress = v
	f.callCount++
	return nil
}
func (f *fakeHostConfig) SetPort(_ context.Context, v int) error {
	f.port = v
	f.callCount++
	return nil
}
func (f *fakeHostConfig) SetSslPort(_ context.Context, v int) error {
	f.sslPort = v
	f.callCount++
	return nil
}
func (f *fakeHostConfig) SetEnableSsl(_ context.Context, v bool) error {
	f.enableSsl = v
	f.callCount++
	return nil
}
func (f *fakeHostConfig) SetLaunchBrowser(_ context.Context, v bool) error {
	f.launchBrowser = v
	f.callCount++
	return nil
}
func (f *fakeHostConfig) SetAuthenticationMethod(_ context.Context, v string) error {
	f.authMethod = v
	f.callCount++
	return nil
}
func (f *fakeHostConfig) SetAuthenticationRequired(_ context.Context, v string) error {
	f.authRequired = v
	f.callCount++
	return nil
}
func (f *fakeHostConfig) SetAnalyticsEnabled(_ context.Context, v bool) error {
	f.analyticsEnabled = v
	f.callCount++
	return nil
}
func (f *fakeHostConfig) SetLogLevel(_ context.Context, v string) error {
	f.logLevel = v
	f.callCount++
	return nil
}
func (f *fakeHostConfig) SetLogSizeLimit(_ context.Context, v int) error {
	f.logSizeLimit = v
	f.callCount++
	return nil
}
func (f *fakeHostConfig) SetConsoleLogLevel(_ context.Context, v string) error {
	f.consoleLogLevel = v
	f.callCount++
	return nil
}
func (f *fakeHostConfig) SetBranch(_ context.Context, v string) error {
	f.branch = v
	f.callCount++
	return nil
}
func (f *fakeHostConfig) SetSslCertPath(_ context.Context, v string) error {
	f.sslCertPath = v
	f.callCount++
	return nil
}
func (f *fakeHostConfig) SetSslKeyPath(_ context.Context, v string) error {
	f.sslKeyPath = v
	f.callCount++
	return nil
}
func (f *fakeHostConfig) SetSslCertPassword(_ context.Context, v string) error {
	f.sslCertPassword = v
	f.callCount++
	return nil
}
func (f *fakeHostConfig) SetURLBase(_ context.Context, v string) error {
	f.urlBase = v
	f.callCount++
	return nil
}
func (f *fakeHostConfig) SetInstanceName(_ context.Context, v string) error {
	f.instanceName = v
	f.callCount++
	return nil
}
func (f *fakeHostConfig) SetTrustCgnatIpAddresses(_ context.Context, v bool) error {
	f.trustCgnat = v
	f.callCount++
	return nil
}
func (f *fakeHostConfig) SetUpdateAutomatically(_ context.Context, v bool) error {
	f.updateAuto = v
	f.callCount++
	return nil
}
func (f *fakeHostConfig) SetUpdateMechanism(_ context.Context, v string) error {
	f.updateMech = v
	f.callCount++
	return nil
}
func (f *fakeHostConfig) SetUpdateScriptPath(_ context.Context, v string) error {
	f.updateScript = v
	f.callCount++
	return nil
}
func (f *fakeHostConfig) SetApplicationUrl(_ context.Context, v string) error {
	f.applicationUrl = v
	f.callCount++
	return nil
}
func (f *fakeHostConfig) SetBackupFolder(_ context.Context, v string) error {
	f.backupFolder = v
	f.callCount++
	return nil
}
func (f *fakeHostConfig) SetBackupInterval(_ context.Context, v int) error {
	f.backupInterval = v
	f.callCount++
	return nil
}
func (f *fakeHostConfig) SetBackupRetention(_ context.Context, v int) error {
	f.backupRetention = v
	f.callCount++
	return nil
}
func (f *fakeHostConfig) SetCertificateValidation(_ context.Context, v string) error {
	f.certValidation = v
	f.callCount++
	return nil
}
func (f *fakeHostConfig) SetProxyEnabled(_ context.Context, v bool) error {
	f.proxyEnabled = v
	f.callCount++
	return nil
}
func (f *fakeHostConfig) SetProxyType(_ context.Context, v string) error {
	f.proxyType = v
	f.callCount++
	return nil
}
func (f *fakeHostConfig) SetProxyHostname(_ context.Context, v string) error {
	f.proxyHostname = v
	f.callCount++
	return nil
}
func (f *fakeHostConfig) SetProxyPort(_ context.Context, v int) error {
	f.proxyPort = v
	f.callCount++
	return nil
}
func (f *fakeHostConfig) SetProxyUsername(_ context.Context, v string) error {
	f.proxyUsername = v
	f.callCount++
	return nil
}
func (f *fakeHostConfig) SetProxyPassword(_ context.Context, v string) error {
	f.proxyPassword = v
	f.callCount++
	return nil
}
func (f *fakeHostConfig) SetProxyBypassFilter(_ context.Context, v string) error {
	f.proxyBypassFilter = v
	f.callCount++
	return nil
}
func (f *fakeHostConfig) SetProxyBypassLocalAddresses(_ context.Context, v bool) error {
	f.proxyBypassLocalAddresses = v
	f.callCount++
	return nil
}

// hostBody returns a minimal-valid PUT body. AuthenticationMethod=none
// keeps Username/Password optional; ProxyEnabled=false skips ProxyType
// decoding.
func hostBody() string {
	return `{"id":1,` +
		`"bindAddress":"*","port":8989,"sslPort":9898,"enableSsl":false,"launchBrowser":true,` +
		`"authenticationMethod":"none","authenticationRequired":"enabled","analyticsEnabled":true,` +
		`"username":"","password":"","passwordConfirmation":"",` +
		`"logLevel":"info","logSizeLimit":1,"consoleLogLevel":"",` +
		`"branch":"main","apiKey":"","sslCertPath":"","sslKeyPath":"","sslCertPassword":"",` +
		`"urlBase":"","instanceName":"Sonarr","applicationUrl":"",` +
		`"updateAutomatically":false,"updateMechanism":"builtIn","updateScriptPath":"",` +
		`"proxyEnabled":false,"proxyType":"http","proxyHostname":"","proxyPort":8080,` +
		`"proxyUsername":"","proxyPassword":"","proxyBypassFilter":"","proxyBypassLocalAddresses":true,` +
		`"certificateValidation":"enabled","backupFolder":"/tmp/backup","backupInterval":7,"backupRetention":28,` +
		`"trustCgnatIpAddresses":false}`
}

func putHostReq(body string) *http.Request {
	req := httptest.NewRequest(http.MethodPut, "/api/v3/config/host/1", strings.NewReader(body))
	req.SetPathValue("id", "1")
	return req
}

func TestHostConfigUpdateHandler_Success(t *testing.T) {
	t.Parallel()
	w := &fakeHostConfig{}
	rr := httptest.NewRecorder()
	HostConfigUpdateHandler(w).ServeHTTP(rr, putHostReq(hostBody()))
	if rr.Code != http.StatusAccepted {
		t.Fatalf("status: %d; body=%s", rr.Code, rr.Body.String())
	}
	if w.callCount != 33 {
		t.Errorf("expected 33 setter calls (34 minus ProxyType which is gated on ProxyEnabled=false); got %d", w.callCount)
	}
	if w.authMethod != "None" {
		t.Errorf("AuthenticationMethod stored as PascalCase: got %q", w.authMethod)
	}
	if w.updateMech != "BuiltIn" {
		t.Errorf("UpdateMechanism PascalCase: got %q", w.updateMech)
	}
	if w.certValidation != "Enabled" {
		t.Errorf("CertificateValidation PascalCase: got %q", w.certValidation)
	}
	// Response body is the inbound resource echoed back.
	var got HostConfigResource
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Port != 8989 {
		t.Errorf("response Port: %d", got.Port)
	}
}

func TestHostConfigUpdateHandler_ProxyEnabledRunsProxyType(t *testing.T) {
	t.Parallel()
	w := &fakeHostConfig{}
	rr := httptest.NewRecorder()
	body := strings.Replace(hostBody(), `"proxyEnabled":false`, `"proxyEnabled":true`, 1)
	HostConfigUpdateHandler(w).ServeHTTP(rr, putHostReq(body))
	if rr.Code != http.StatusAccepted {
		t.Fatalf("status: %d; body=%s", rr.Code, rr.Body.String())
	}
	if w.callCount != 34 {
		t.Errorf("expected 34 setter calls when ProxyEnabled=true; got %d", w.callCount)
	}
	if w.proxyType != "Http" {
		t.Errorf("ProxyType PascalCase: got %q", w.proxyType)
	}
}

func TestHostConfigUpdateHandler_BasicAuth_400(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	body := strings.Replace(hostBody(), `"authenticationMethod":"none"`, `"authenticationMethod":"basic"`, 1)
	HostConfigUpdateHandler(&fakeHostConfig{}).ServeHTTP(rr, putHostReq(body))
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "no longer supported") {
		t.Errorf("expected friendly Basic-deprecated message; body=%s", rr.Body.String())
	}
}

func TestHostConfigUpdateHandler_FormsRequiresUsername_400(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	body := strings.Replace(hostBody(), `"authenticationMethod":"none"`, `"authenticationMethod":"forms"`, 1)
	HostConfigUpdateHandler(&fakeHostConfig{}).ServeHTTP(rr, putHostReq(body))
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: %d", rr.Code)
	}
}

func TestHostConfigUpdateHandler_PasswordMismatch_400(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	body := strings.Replace(hostBody(), `"password":""`, `"password":"abc"`, 1)
	HostConfigUpdateHandler(&fakeHostConfig{}).ServeHTTP(rr, putHostReq(body))
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "Must match Password") {
		t.Errorf("expected mismatch message; body=%s", rr.Body.String())
	}
}

func TestHostConfigUpdateHandler_BadPort_400(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	body := strings.Replace(hostBody(), `"port":8989`, `"port":0`, 1)
	HostConfigUpdateHandler(&fakeHostConfig{}).ServeHTTP(rr, putHostReq(body))
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: %d", rr.Code)
	}
}

func TestHostConfigUpdateHandler_SslPortEqualsPort_400(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	body := strings.Replace(hostBody(), `"enableSsl":false`, `"enableSsl":true`, 1)
	body = strings.Replace(body, `"sslPort":9898`, `"sslPort":8989`, 1)
	body = strings.Replace(body, `"sslCertPath":""`, `"sslCertPath":"/tmp/cert.pfx"`, 1)
	HostConfigUpdateHandler(&fakeHostConfig{}).ServeHTTP(rr, putHostReq(body))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: %d; body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "must not equal Port") {
		t.Errorf("expected SslPort==Port rejection; body=%s", rr.Body.String())
	}
}

func TestHostConfigUpdateHandler_SslRequiresCertPath_400(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	body := strings.Replace(hostBody(), `"enableSsl":false`, `"enableSsl":true`, 1)
	// SslCertPath stays empty.
	HostConfigUpdateHandler(&fakeHostConfig{}).ServeHTTP(rr, putHostReq(body))
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: %d", rr.Code)
	}
}

// J404: SSL enabled + absolute cert path that does NOT exist on disk →
// 400 with the FileExists message (injected fake checker).
func TestHostConfigUpdateHandler_SslCertPathMissing_400_J404(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	body := strings.Replace(hostBody(), `"enableSsl":false`, `"enableSsl":true`, 1)
	body = strings.Replace(body, `"sslCertPath":""`, `"sslCertPath":"/etc/sonarr/cert.pfx"`, 1)
	HostConfigUpdateHandler(&fakeHostConfig{}, func(string) bool { return false }).
		ServeHTTP(rr, putHostReq(body))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: %d; body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "SslCertPath file does not exist") {
		t.Errorf("expected FileExists rejection; body=%s", rr.Body.String())
	}
}

// J404: SSL enabled + absolute cert path that DOES exist → 202 (the
// FileExists check passes via the injected checker).
func TestHostConfigUpdateHandler_SslCertPathExists_OK_J404(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	body := strings.Replace(hostBody(), `"enableSsl":false`, `"enableSsl":true`, 1)
	body = strings.Replace(body, `"sslCertPath":""`, `"sslCertPath":"/etc/sonarr/cert.pfx"`, 1)
	HostConfigUpdateHandler(&fakeHostConfig{}, func(string) bool { return true }).
		ServeHTTP(rr, putHostReq(body))
	if rr.Code != http.StatusAccepted {
		t.Fatalf("status: %d; body=%s", rr.Code, rr.Body.String())
	}
}

// J404: a non-empty SslKeyPath that doesn't exist → 400 even when SSL is
// off (C# gates the key FileExists on the field being set, not EnableSsl).
func TestHostConfigUpdateHandler_SslKeyPathMissing_400_J404(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	body := strings.Replace(hostBody(), `"sslKeyPath":""`, `"sslKeyPath":"/etc/sonarr/key.pem"`, 1)
	HostConfigUpdateHandler(&fakeHostConfig{}, func(string) bool { return false }).
		ServeHTTP(rr, putHostReq(body))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: %d; body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "SslKeyPath file does not exist") {
		t.Errorf("expected SslKeyPath FileExists rejection; body=%s", rr.Body.String())
	}
}

// J404: the production default ([osFileExists]) accepts a real file on
// disk — SSL enabled + a cert path pointing at a temp file → 200.
func TestHostConfigUpdateHandler_SslCertPathRealFile_OK_J404(t *testing.T) {
	t.Parallel()
	certPath := filepath.Join(t.TempDir(), "cert.pfx")
	if err := os.WriteFile(certPath, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	rr := httptest.NewRecorder()
	body := strings.Replace(hostBody(), `"enableSsl":false`, `"enableSsl":true`, 1)
	body = strings.Replace(body, `"sslCertPath":""`, `"sslCertPath":"`+certPath+`"`, 1)
	// No checker passed → default osFileExists exercised.
	HostConfigUpdateHandler(&fakeHostConfig{}).ServeHTTP(rr, putHostReq(body))
	if rr.Code != http.StatusAccepted {
		t.Fatalf("status: %d; body=%s", rr.Code, rr.Body.String())
	}
}

func TestHostConfigUpdateHandler_BindAddressInvalid_400(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	body := strings.Replace(hostBody(), `"bindAddress":"*"`, `"bindAddress":"not.an.ip"`, 1)
	HostConfigUpdateHandler(&fakeHostConfig{}).ServeHTTP(rr, putHostReq(body))
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: %d", rr.Code)
	}
}

func TestHostConfigUpdateHandler_InstanceNameRejected_400(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	body := strings.Replace(hostBody(), `"instanceName":"Sonarr"`, `"instanceName":"Foo"`, 1)
	HostConfigUpdateHandler(&fakeHostConfig{}).ServeHTTP(rr, putHostReq(body))
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: %d", rr.Code)
	}
}

func TestHostConfigUpdateHandler_BackupIntervalOutOfRange_400(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	body := strings.Replace(hostBody(), `"backupInterval":7`, `"backupInterval":8`, 1)
	HostConfigUpdateHandler(&fakeHostConfig{}).ServeHTTP(rr, putHostReq(body))
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: %d", rr.Code)
	}
}

func TestHostConfigUpdateHandler_UpdateScriptPathNotAbs_400(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	body := strings.Replace(hostBody(), `"updateMechanism":"builtIn"`, `"updateMechanism":"script"`, 1)
	body = strings.Replace(body, `"updateScriptPath":""`, `"updateScriptPath":"relative/path"`, 1)
	HostConfigUpdateHandler(&fakeHostConfig{}).ServeHTTP(rr, putHostReq(body))
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: %d", rr.Code)
	}
}

func TestHostConfigUpdateHandler_MalformedBody_400(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/v3/config/host/1", strings.NewReader("not json"))
	req.SetPathValue("id", "1")
	HostConfigUpdateHandler(&fakeHostConfig{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: %d", rr.Code)
	}
}
