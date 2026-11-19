package main

import (
	"encoding/json"
	"net/http"
	"os"
)

func (a *App) directory(w http.ResponseWriter, r *http.Request) {
	if r.Method == "GET" {
		a.mu.Lock()
		p := a.cfg.DownloadPath
		a.mu.Unlock()
		jsonOut(w, map[string]string{"path": p})
		return
	}
	if r.Method != "POST" {
		fail(w, 405, "需要 GET 或 POST")
		return
	}
	var v struct {
		Path string `json:"path"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&v) != nil {
		fail(w, 400, "目录参数无效")
		return
	}
	p := ""
	var e error
	if v.Path != "" {
		p, e = validPath(v.Path)
		if e != nil {
			fail(w, 400, e.Error())
			return
		}
	}
	a.mu.Lock()
	cfg := a.cfg
	cfg.DownloadPath = p
	b, _ := json.MarshalIndent(cfg, "", "  ")
	e = os.WriteFile(a.configPath+".tmp", b, 0600)
	if e == nil {
		e = os.Rename(a.configPath+".tmp", a.configPath)
	}
	if e == nil {
		a.cfg = cfg
	}
	a.mu.Unlock()
	if e != nil {
		fail(w, 500, "保存目录配置失败")
		return
	}
	a.event("下载目录偏好已保存")
	jsonOut(w, map[string]string{"path": p})
}
func traceAPI(p string) bool {
	return p == "/api/v2/auth/login" || p == "/api/v2/torrents/add" || p == "/api/v2/torrents/delete" || p == "/api/v2/torrents/setLocation"
}
func safeAPIPath(p string) string {
	if len(p) > 100 {
		return "未知接口"
	}
	for _, c := range p {
		if !(c == '/' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9') {
			return "未知接口"
		}
	}
	return p
}
func (a *App) rememberDirectory(p string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	cfg := a.cfg
	cfg.DownloadPath = p
	b, _ := json.MarshalIndent(cfg, "", "  ")
	if e := os.WriteFile(a.configPath+".tmp", b, 0600); e != nil {
		return e
	}
	if e := os.Rename(a.configPath+".tmp", a.configPath); e != nil {
		return e
	}
	a.cfg = cfg
	return nil
}
