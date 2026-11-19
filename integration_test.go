package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func testApp(t *testing.T) (*App, *httptest.Server, *http.Client) {
	t.Helper()
	var mu sync.Mutex
	logged := false
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v2/auth/login" {
			r.ParseForm()
			if r.Form.Get("password") != "real+&secret" {
				io.WriteString(w, "Fails.")
				return
			}
			mu.Lock()
			logged = true
			mu.Unlock()
			http.SetCookie(w, &http.Cookie{Name: "SID", Value: "testsid", Path: "/"})
			io.WriteString(w, "Ok.")
			return
		}
		mu.Lock()
		ok := logged
		mu.Unlock()
		c, e := r.Cookie("SID")
		if !ok || e != nil || c.Value != "testsid" {
			w.WriteHeader(403)
			return
		}
		switch r.URL.Path {
		case "/api/v2/transfer/info":
			io.WriteString(w, `{"dl_info_speed":2048,"up_info_speed":100}`)
		case "/api/v2/torrents/info":
			io.WriteString(w, `[{"name":"测试电影","hash":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","progress":0.5,"state":"downloading"}]`)
		case "/api/v2/app/version":
			io.WriteString(w, "v5.1.0")
		case "/api/v2/app/preferences":
			io.WriteString(w, `{"save_path":"/vol1/1000/downloads","web_ui_password":"never-expose"}`)
		case "/api/v2/torrents/add":
			if e := r.ParseMultipartForm(1 << 20); e != nil {
				t.Error(e)
			}
			if r.FormValue("urls") == "" && len(r.MultipartForm.File["torrents"]) == 0 {
				t.Error("missing magnet")
			}
			io.WriteString(w, "Ok.")
		case "/api/v2/torrents/stop", "/api/v2/torrents/start", "/api/v2/torrents/delete":
			r.ParseForm()
			if r.Form.Get("hashes") == "" {
				t.Error("missing hash")
			}
		default:
			t.Errorf("unexpected backend path: %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(backend.Close)
	u, _ := url.Parse(backend.URL)
	jar, _ := cookiejar.New(nil)
	tr := &http.Transport{Proxy: func(r *http.Request) (*url.URL, error) { return u, nil }}
	a := &App{cfg: Config{Password: "examplePassword123", Port: 7777}, configPath: filepath.Join(t.TempDir(), "config.json"), sessions: map[string]session{}, backend: &http.Client{Transport: tr, Jar: jar}, discover: func() (string, string, error) { return "unused", "real+&secret", nil }}
	nativeRows := []nativeRow{{ID: 10, UID: 1000, Type: "qbt", Hash: strings.Repeat("a", 40)}}
	a.native = &nativeOps{Owner: func() (int, error) { return 1000, nil }, Rows: func(context.Context, int) ([]nativeRow, error) { return nativeRows, nil }, Delete: func(context.Context, int, []int64, bool) error { nativeRows = nil; return nil }, EngineHas: func(context.Context, string) (bool, error) { return false, nil }}
	srv := httptest.NewServer(http.HandlerFunc(a.serve))
	t.Cleanup(srv.Close)
	j, _ := cookiejar.New(nil)
	return a, srv, &http.Client{Jar: j}
}
func request(t *testing.T, c *http.Client, method, url, body, ct string) (int, []byte) {
	t.Helper()
	r, _ := http.NewRequest(method, url, strings.NewReader(body))
	r.Header.Set("X-Bridge-Request", "1")
	if ct != "" {
		r.Header.Set("Content-Type", ct)
	}
	resp, e := c.Do(r)
	if e != nil {
		t.Fatal(e)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, b
}
func TestManagementFlow(t *testing.T) {
	a, s, c := testApp(t)
	code, _ := request(t, c, "GET", s.URL+"/manage/status", "", "")
	if code != 401 {
		t.Fatal(code)
	}
	code, _ = request(t, c, "POST", s.URL+"/manage/login", `{"password":"wrong"}`, "application/json")
	if code != 401 {
		t.Fatal(code)
	}
	code, _ = request(t, c, "POST", s.URL+"/manage/login", `{"password":"examplePassword123"}`, "application/json")
	if code != 200 {
		t.Fatal(code)
	}
	code, b := request(t, c, "GET", s.URL+"/manage/status", "", "")
	if code != 200 || !bytes.Contains(b, []byte(`"connected":true`)) {
		t.Fatalf("status %d %s", code, b)
	}
	code, b = request(t, c, "GET", s.URL+"/manage/preferences", "", "")
	if code != 200 || bytes.Contains(b, []byte("never-expose")) {
		t.Fatal("preferences leak")
	}
	for _, action := range []string{"pause", "resume", "delete"} {
		body := `{"action":"` + action + `","hash":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","deleteFiles":false}`
		code, b = request(t, c, "POST", s.URL+"/manage/action", body, "application/json")
		if code != 200 {
			t.Fatalf("action %d %s", code, b)
		}
	}
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	mw.WriteField("urls", "magnet:?xt=urn:btih:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	mw.Close()
	code, b = request(t, c, "POST", s.URL+"/manage/add", buf.String(), mw.FormDataContentType())
	if code != 200 {
		t.Fatalf("add %d %s", code, b)
	}
	code, b = request(t, c, "POST", s.URL+"/manage/settings", `{"port":7777,"password":"newSecurePassword123"}`, "application/json")
	if code != 200 {
		t.Fatalf("settings %d %s", code, b)
	}
	code, _ = request(t, c, "GET", s.URL+"/manage/logs", "", "")
	if code != 401 {
		t.Fatal("old session not invalidated")
	}
	saved, _ := os.ReadFile(a.configPath)
	var cfg Config
	if json.Unmarshal(saved, &cfg) != nil || cfg.Password != "newSecurePassword123" {
		t.Fatal("config not saved")
	}
}
func TestOriginAndValidation(t *testing.T) {
	_, s, c := testApp(t)
	r, _ := http.NewRequest("POST", s.URL+"/manage/login", strings.NewReader(`{"password":"examplePassword123"}`))
	r.Header.Set("Origin", "https://evil.example")
	resp, e := c.Do(r)
	if e != nil {
		t.Fatal(e)
	}
	resp.Body.Close()
	if resp.StatusCode != 403 {
		t.Fatal("cross origin allowed")
	}
	if _, e := validPath("/etc"); e == nil {
		t.Fatal("system directory accepted")
	}
	if _, e := validPath("../../etc"); e == nil {
		t.Fatal("relative path accepted")
	}
}
func TestProxyLogin(t *testing.T) {
	a, _, _ := testApp(t)
	s := httptest.NewServer(a.proxy())
	defer s.Close()
	j, _ := cookiejar.New(nil)
	c := &http.Client{Jar: j}
	code, b := request(t, c, "POST", s.URL+"/api/v2/auth/login", "username=admin", "application/x-www-form-urlencoded")
	if code != 200 || string(b) != "Fails." {
		t.Fatal("missing password accepted")
	}
	code, b = request(t, c, "POST", s.URL+"/api/v2/auth/login", "username=admin&password=examplePassword123", "application/x-www-form-urlencoded")
	if code != 200 || string(b) != "Ok." {
		t.Fatalf("proxy login %d %s", code, b)
	}
	code, _ = request(t, c, "GET", s.URL+"/api/v2/torrents/info", "", "")
	if code != 200 {
		t.Fatal("proxy cookie not forwarded")
	}
}

func TestRealStaticEntry(t *testing.T) {
	a, s, c := testApp(t)
	for _, path := range []string{"/", "/app/fnqbbridge/"} {
		code, b := request(t, c, "GET", s.URL+path, "", "")
		if code != 200 || !bytes.Contains(b, []byte("login-form")) {
			t.Fatalf("entry %s returned %d instead of HTML: %s", path, code, b)
		}
	}
	for _, path := range []string{"/app/fnqbbridge/app.js", "/app/fnqbbridge/style.css", "/app/fnqbbridge/trim-sdk.js"} {
		rr := httptest.NewRecorder()
		a.serve(rr, httptest.NewRequest("GET", path, nil))
		if rr.Code != 200 {
			t.Fatalf("asset %s: %d", path, rr.Code)
		}
	}
}

func TestGatewayOriginLogin(t *testing.T) {
	a, _, _ := testApp(t)
	r := httptest.NewRequest("POST", "http://unix/app/fnqbbridge/manage/login", strings.NewReader(`{"password":"examplePassword123"}`))
	r.Header.Set("Origin", "https://nas.example.com")
	r.Header.Set("X-Bridge-Request", "1")
	w := httptest.NewRecorder()
	a.serve(w, r)
	if w.Code != 200 {
		t.Fatalf("gateway login blocked: %d %s", w.Code, w.Body.String())
	}
}

func TestMultipartProxyLogin(t *testing.T) {
	a, _, _ := testApp(t)
	srv := httptest.NewServer(a.proxy())
	defer srv.Close()
	var b bytes.Buffer
	mw := multipart.NewWriter(&b)
	mw.WriteField("username", "admin")
	mw.WriteField("password", "examplePassword123")
	mw.Close()
	code, body := request(t, http.DefaultClient, "POST", srv.URL+"/api/v2/auth/login", b.String(), mw.FormDataContentType())
	if code != 200 || string(body) != "Ok." {
		t.Fatalf("multipart login failed: %d %s", code, body)
	}
}

func TestTracePreservesMultipartAndRedacts(t *testing.T) {
	a, _, _ := testApp(t)
	var b bytes.Buffer
	mw := multipart.NewWriter(&b)
	mw.WriteField("savepath", "/vol1/1000/MoviePilot/downloads")
	mw.WriteField("category", "外语电影")
	mw.WriteField("autoTMM", "true")
	mw.WriteField("urls", "magnet:?xt=secret-link")
	mw.WriteField("password", "secret-pass")
	f, _ := mw.CreateFormFile("torrents", "private.torrent")
	f.Write([]byte("secret-torrent-content"))
	mw.Close()
	original := append([]byte{}, b.Bytes()...)
	r := httptest.NewRequest("POST", "/api/v2/torrents/add", &b)
	r.Header.Set("Content-Type", mw.FormDataContentType())
	if e := a.traceDownloadFields(r); e != nil {
		t.Fatal(e)
	}
	after, _ := io.ReadAll(r.Body)
	if !bytes.Equal(original, after) {
		t.Fatal("request body changed")
	}
	logs := strings.Join(a.logs, "\n")
	if !strings.Contains(logs, "/vol1/1000/MoviePilot/downloads") || !strings.Contains(logs, "autoTMM") {
		t.Fatal("missing fields")
	}
	for _, secret := range []string{"secret-link", "secret-pass", "secret-torrent-content", "private.torrent"} {
		if strings.Contains(logs, secret) {
			t.Fatal("sensitive data leaked")
		}
	}
}

func TestSeedSubmission(t *testing.T) {
	a, _, _ := testApp(t)
	if e := a.backendLogin(); e != nil {
		t.Fatal(e)
	}
	var b bytes.Buffer
	mw := multipart.NewWriter(&b)
	f, _ := mw.CreateFormFile("torrents", "sample.torrent")
	f.Write([]byte("d4:infodee"))
	mw.WriteField("paused", "true")
	mw.Close()
	r := httptest.NewRequest("POST", "/manage/add", &b)
	r.Header.Set("Content-Type", mw.FormDataContentType())
	r.ParseMultipartForm(1024)
	defer r.MultipartForm.RemoveAll()
	w := httptest.NewRecorder()
	a.addMultipart(w, r)
	if w.Code != 200 {
		t.Fatalf("seed submission: %d %s", w.Code, w.Body.String())
	}
}
func TestProxyDeletionUsesNativeService(t *testing.T) {
	a, _, _ := testApp(t)
	s := httptest.NewServer(a.proxy())
	defer s.Close()
	j, _ := cookiejar.New(nil)
	c := &http.Client{Jar: j}
	code, b := request(t, c, "POST", s.URL+"/api/v2/auth/login", "username=admin&password=examplePassword123", "application/x-www-form-urlencoded")
	if code != 200 || string(b) != "Ok." {
		t.Fatal("login failed")
	}
	code, b = request(t, c, "POST", s.URL+"/api/v2/torrents/delete", "hashes="+strings.Repeat("a", 40)+"&deleteFiles=false", "application/x-www-form-urlencoded")
	if code != 200 {
		t.Fatalf("native proxy delete %d %s", code, b)
	}
	rows, _ := a.native.Rows(context.Background(), 1000)
	if len(rows) != 0 {
		t.Fatal("proxy did not use native service")
	}
}
