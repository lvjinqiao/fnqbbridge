package main

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLogRotationBounded(t *testing.T) {
	dir := t.TempDir()
	w := &rotatingLog{path: filepath.Join(dir, "bridge.log"), max: 100, backups: 3}
	for i := 0; i < 50; i++ {
		if _, e := w.Write([]byte(strings.Repeat("x", 55))); e != nil {
			t.Fatal(e)
		}
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 4 {
		t.Fatalf("unbounded retention: %d", len(entries))
	}
	for _, f := range entries {
		st, _ := f.Info()
		if st.Size() > 100 {
			t.Fatal("oversize log")
		}
	}
}
func TestDirectoryPreferenceAndSettingsPreserve(t *testing.T) {
	a, _, _ := testApp(t)
	a.cfg.Listen = ":18080"
	if e := a.rememberDirectory("/vol2/1000/中文 空格/downloads"); e != nil {
		t.Fatal(e)
	}
	if e := a.saveSettings(7777, "newSecurePassword123"); e != nil {
		t.Fatal(e)
	}
	b, _ := os.ReadFile(a.configPath)
	var cfg Config
	if e := json.Unmarshal(b, &cfg); e != nil {
		t.Fatal(e)
	}
	if cfg.DownloadPath != "/vol2/1000/中文 空格/downloads" || cfg.Port != 7777 || cfg.Password != "newSecurePassword123" {
		t.Fatal("settings lost directory or password")
	}
	w := httptest.NewRecorder()
	a.directory(w, httptest.NewRequest("GET", "/", nil))
	if !strings.Contains(w.Body.String(), "/vol2/") {
		t.Fatal("preference reload failed")
	}
	if e := a.rememberDirectory(""); e != nil {
		t.Fatal(e)
	}
	if a.cfg.DownloadPath != "" {
		t.Fatal("directory cannot be reset")
	}
}
func TestUpgradeConfigAccepts025(t *testing.T) {
	var cfg Config
	if e := json.Unmarshal([]byte(`{"listen":":18080","port":7777,"password":"examplePassword123"}`), &cfg); e != nil {
		t.Fatal(e)
	}
	if cfg.DownloadPath != "" || cfg.Port != 7777 {
		t.Fatal("old configuration incompatible")
	}
}
func TestCustomPathMissingRejected(t *testing.T) {
	a, _, _ := testApp(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/", strings.NewReader("locationMode=custom"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.ParseForm()
	a.addMultipart(w, r)
	if w.Code != 400 {
		t.Fatal("missing path accepted")
	}
}
func TestVolumePathNormalization(t *testing.T) {
	for _, p := range []string{"/vol1/1000/downloads/", "/vol2/1000/中文 空格/"} {
		v, e := normalizeVolumePath(" " + p + " ")
		if e != nil || strings.HasSuffix(v, "/") {
			t.Fatalf("%q: %s %v", p, v, e)
		}
	}
	for _, p := range []string{"../downloads", "/etc", "/vol-1/x", "/vol1/../../etc"} {
		if _, e := normalizeVolumePath(p); e == nil {
			t.Fatalf("accepted %s", p)
		}
	}
	dir := filepath.Join(t.TempDir(), "中文 空格")
	os.Mkdir(dir, 0700)
	if e := checkWritableDirectory(dir); e != nil {
		t.Fatal(e)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		t.Fatal("write probe leaked")
	}
	if e := checkWritableDirectory(filepath.Join(dir, "missing")); e == nil {
		t.Fatal("nonexistent directory accepted")
	}
}
func TestOversizedLegacyLogClamped(t *testing.T) {
	p := filepath.Join(t.TempDir(), "bridge.log")
	os.WriteFile(p, []byte(strings.Repeat("x", 1000)), 0600)
	w := rotatingLog{path: p, max: 100, backups: 3}
	if _, e := w.Write([]byte("new")); e != nil {
		t.Fatal(e)
	}
	st, _ := os.Stat(p + ".1")
	if st.Size() > 100 {
		t.Fatal("legacy backup unbounded")
	}
}
