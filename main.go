package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httputil"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

//go:embed web/*
var assets embed.FS

type Config struct {
	Listen       string `json:"listen"`
	Password     string `json:"password"`
	Port         int    `json:"port"`
	DownloadPath string `json:"download_path,omitempty"`
}
type session struct{ expires time.Time }
type App struct {
	deleteMu   sync.Mutex
	native     *nativeOps
	mu         sync.Mutex
	cfg        Config
	configPath string
	sessions   map[string]session
	logs       []string
	backend    *http.Client
	discover   func() (string, string, error)
	bridge     *http.Server
	listener   net.Listener
}

func loginBody(body []byte, expected, real string) ([]byte, bool) {
	v, e := url.ParseQuery(string(body))
	if e != nil || v.Get("username") != "admin" || subtle.ConstantTimeCompare([]byte(v.Get("password")), []byte(expected)) != 1 {
		return nil, false
	}
	v.Set("password", real)
	return []byte(v.Encode()), true
}
func (a *App) event(s string) {
	if !allowEvent(s) {
		return
	}
	if len(s) > 8192 {
		s = s[:8192] + "…"
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.logs = append(a.logs, time.Now().Format("2006-01-02 15:04:05")+"  "+s)
	if len(a.logs) > 200 {
		a.logs = a.logs[len(a.logs)-200:]
	}
	log.Print(s)
}
func jsonOut(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(v)
}
func fail(w http.ResponseWriter, code int, s string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": s})
}
func randomToken() string {
	b := make([]byte, 32)
	if _, e := rand.Read(b); e != nil {
		panic(e)
	}
	return hex.EncodeToString(b)
}
func (a *App) password() string { a.mu.Lock(); defer a.mu.Unlock(); return a.cfg.Password }
func (a *App) authorized(r *http.Request) bool {
	c, e := r.Cookie("bridge_session")
	if e != nil {
		return false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	s, ok := a.sessions[c.Value]
	return ok && s.expires.After(time.Now())
}
func sameOrigin(r *http.Request) bool {
	o := r.Header.Get("Origin")
	if o == "" {
		return r.Header.Get("Sec-Fetch-Site") != "cross-site"
	}
	u, e := url.Parse(o)
	return e == nil && strings.EqualFold(u.Host, r.Host)
}
func (a *App) qb(method, path string, body io.Reader, ct string) ([]byte, error) {
	for attempt := 0; attempt < 2; attempt++ {
		req, e := http.NewRequest(method, "http://unix/api/v2/"+path, body)
		if e != nil {
			return nil, e
		}
		if ct != "" {
			req.Header.Set("Content-Type", ct)
		}
		resp, e := a.backend.Do(req)
		if e != nil {
			return nil, errors.New("无法连接飞牛下载器，请先打开飞牛原生下载应用")
		}
		b, e := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
		resp.Body.Close()
		if e != nil {
			return nil, e
		}
		if resp.StatusCode == 403 && attempt == 0 && method == "GET" {
			if e = a.backendLogin(); e != nil {
				return nil, e
			}
			continue
		}
		if resp.StatusCode >= 400 {
			return nil, fmt.Errorf("下载器接口返回 %d", resp.StatusCode)
		}
		return b, nil
	}
	return nil, errors.New("下载器会话失效")
}
func (a *App) backendLogin() error {
	_, p, e := a.discover()
	if e != nil {
		return errors.New("未发现飞牛下载器进程，请先打开飞牛原生下载应用")
	}
	v := url.Values{"username": {"admin"}, "password": {p}}
	r, e := a.backend.PostForm("http://unix/api/v2/auth/login", v)
	if e != nil {
		return errors.New("下载器本地接口不可用")
	}
	defer r.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(r.Body, 1024))
	if string(b) != "Ok." {
		return errors.New("飞牛下载器认证失败，当前系统版本可能不兼容")
	}
	return nil
}
func (a *App) proxy() http.Handler {
	p := &httputil.ReverseProxy{ModifyResponse: func(resp *http.Response) error {
		if resp.Request.URL.Path == "/api/v2/auth/login" && resp.StatusCode == 200 {
			b, e := io.ReadAll(io.LimitReader(resp.Body, 4096))
			resp.Body.Close()
			if e != nil {
				return e
			}
			resp.Body = io.NopCloser(bytes.NewReader(b))
			resp.ContentLength = int64(len(b))
			resp.Header.Set("Content-Length", strconv.Itoa(len(b)))
			if string(b) == "Ok." {
				t := randomToken()
				a.mu.Lock()
				a.sessions[t] = session{time.Now().Add(12 * time.Hour)}
				a.mu.Unlock()
				resp.Header.Add("Set-Cookie", (&http.Cookie{Name: "bridge_api", Value: t, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: 43200}).String())
			}
		}
		if resp.Request.URL.Path == "/api/v2/torrents/add" {
			a.event(fmt.Sprintf("下载器添加任务响应：HTTP %d", resp.StatusCode))
		}
		return nil
	}, Rewrite: func(p *httputil.ProxyRequest) {
		p.Out.URL.Scheme = "http"
		p.Out.URL.Host = "unix"
		p.Out.Host = "unix"
		p.Out.Header.Del("Origin")
		p.Out.Header.Del("Referer")
	}, Transport: a.backend.Transport, ErrorHandler: func(w http.ResponseWriter, r *http.Request, e error) {
		a.event("MoviePilot 接口连接下载器失败")
		http.Error(w, "下载器未就绪", 502)
	}}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		path := r.URL.Path
		if traceAPI(path) {
			a.event("MoviePilot 请求开始：" + r.Method + " " + path)
		}
		rw := &statusWriter{ResponseWriter: w, status: 200}
		w = rw
		defer func() {
			if traceAPI(path) || rw.status >= 400 {
				a.event(fmt.Sprintf("MoviePilot 请求结束：%s %s 状态=%d 耗时=%s", r.Method, safeAPIPath(path), rw.status, time.Since(started).Round(time.Millisecond)))
			}
		}()
		ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
		defer cancel()
		r = r.WithContext(ctx)
		if !strings.HasPrefix(r.URL.Path, "/api/v2/") {
			http.Error(w, "MoviePilot qBittorrent API endpoint", 404)
			return
		}
		if r.URL.Path != "/api/v2/auth/login" {
			c, e := r.Cookie("bridge_api")
			ok := false
			if e == nil {
				a.mu.Lock()
				ss, exists := a.sessions[c.Value]
				ok = exists && ss.expires.After(time.Now())
				a.mu.Unlock()
			}
			if !ok {
				http.Error(w, "Forbidden", 403)
				return
			}
		}
		if r.URL.Path == "/api/v2/auth/login" {
			if r.Method != "POST" {
				http.Error(w, "Forbidden", 403)
				return
			}
			b, e := io.ReadAll(http.MaxBytesReader(w, r.Body, 16384))
			if e != nil {
				http.Error(w, "Bad request", 400)
				return
			}
			if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
				r.Body = io.NopCloser(bytes.NewReader(b))
				if e := r.ParseMultipartForm(16384); e != nil {
					a.event("MoviePilot 登录失败：multipart 表单解析失败")
					io.WriteString(w, "Fails.")
					return
				}
				if r.MultipartForm != nil {
					defer r.MultipartForm.RemoveAll()
				}
				b = []byte(r.PostForm.Encode())
			}
			if _, ok := loginBody(b, a.password(), ""); !ok {
				values, _ := url.ParseQuery(string(b))
				reason := "密码不匹配"
				if values.Get("username") != "admin" {
					reason = "用户名不是 admin"
				}
				a.event("MoviePilot 登录失败：" + reason)
				io.WriteString(w, "Fails.")
				return
			}
			_, pass, e := a.discover()
			if e != nil {
				a.event("MoviePilot 登录失败：飞牛下载器未就绪")
				http.Error(w, "下载器未就绪", 502)
				return
			}
			out, _ := loginBody(b, a.password(), pass)
			r.Body = io.NopCloser(bytes.NewReader(out))
			r.ContentLength = int64(len(out))
			r.Header.Del("Content-Length")
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			a.event("收到 MoviePilot 登录请求")
		}
		if r.URL.Path == "/api/v2/torrents/delete" {
			if r.Method != "POST" {
				http.Error(w, "POST required", 405)
				return
			}
			r.Body = http.MaxBytesReader(w, r.Body, 16384)
			if e := r.ParseForm(); e != nil {
				http.Error(w, "invalid form", 400)
				return
			}
			files := false
			var e error
			if v := r.PostForm.Get("deleteFiles"); v != "" {
				files, e = strconv.ParseBool(v)
			}
			if e != nil {
				http.Error(w, "invalid deleteFiles", 400)
				return
			}
			if e = a.deleteNative(r.Context(), r.PostForm.Get("hashes"), files); e != nil {
				a.event("原生删除失败：" + e.Error())
				http.Error(w, e.Error(), 409)
				return
			}
			w.WriteHeader(200)
			return
		}
		if r.Method == "POST" && (r.URL.Path == "/api/v2/torrents/add" || r.URL.Path == "/api/v2/torrents/setLocation" || r.URL.Path == "/api/v2/torrents/createCategory" || r.URL.Path == "/api/v2/torrents/editCategory") {
			if e := a.traceDownloadFields(r); e != nil {
				a.event("MoviePilot 下载参数检查失败：" + e.Error())
				http.Error(w, "invalid download request", 400)
				return
			}
		}
		p.ServeHTTP(w, r)
	})
}
func (a *App) saveSettings(port int, password string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if port < 1024 || port > 65535 || port == 18080 {
		return errors.New("端口需为 1024–65535，且不能为管理端口 18080")
	}
	if password == "" {
		password = a.cfg.Password
	}
	if len(password) < 12 || len(password) > 128 {
		return errors.New("密码需为 12–128 个字节")
	}
	var ln net.Listener
	var e error
	if port != a.cfg.Port {
		ln, e = net.Listen("tcp", fmt.Sprintf(":%d", port))
		if e != nil {
			return errors.New("端口已被占用，设置未保存")
		}
	}
	cfg := a.cfg
	cfg.Password = password
	cfg.Port = port
	b, _ := json.MarshalIndent(cfg, "", "  ")
	tmp := a.configPath + ".tmp"
	if e = os.WriteFile(tmp, b, 0600); e == nil {
		e = os.Rename(tmp, a.configPath)
	}
	if e != nil {
		if ln != nil {
			ln.Close()
		}
		return errors.New("保存配置失败")
	}
	if password != a.cfg.Password {
		a.sessions = make(map[string]session)
	}
	a.cfg = cfg
	if ln != nil {
		old := a.bridge
		a.listener = ln
		a.bridge = &http.Server{Handler: a.proxy(), ReadHeaderTimeout: 10 * time.Second}
		go a.bridge.Serve(ln)
		if old != nil {
			go old.Close()
		}
	}
	return nil
}
func (a *App) serve(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "same-origin")
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != "GET" && r.Method != "HEAD" && r.Header.Get("X-Bridge-Request") != "1" {
		fail(w, 403, "请求来源不匹配")
		return
	}
	path := r.URL.Path
	if path == "/app/fnqbbridge" {
		http.Redirect(w, r, "/app/fnqbbridge/", 302)
		return
	}
	path = strings.TrimPrefix(path, "/app/fnqbbridge")
	if path == "" {
		path = "/"
	}
	if path == "/manage/login" {
		if r.Method != "POST" {
			fail(w, 405, "需要 POST")
			return
		}
		var v struct {
			Password string `json:"password"`
		}
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&v) != nil {
			fail(w, 400, "请求无效")
			return
		}
		if subtle.ConstantTimeCompare([]byte(v.Password), []byte(a.password())) != 1 {
			time.Sleep(300 * time.Millisecond)
			fail(w, 401, "密码不正确")
			return
		}
		t := randomToken()
		a.mu.Lock()
		for k, s := range a.sessions {
			if time.Now().After(s.expires) {
				delete(a.sessions, k)
			}
		}
		if len(a.sessions) > 128 {
			a.sessions = make(map[string]session)
		}
		a.sessions[t] = session{time.Now().Add(12 * time.Hour)}
		a.mu.Unlock()
		http.SetCookie(w, &http.Cookie{Name: "bridge_session", Value: t, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: r.TLS != nil, MaxAge: 43200})
		jsonOut(w, map[string]bool{"ok": true})
		return
	}
	if strings.HasPrefix(path, "/manage/") {
		if !a.authorized(r) {
			fail(w, 401, "请先登录")
			return
		}
		switch path {
		case "/manage/logout":
			if r.Method != "POST" {
				fail(w, 405, "需要 POST")
				return
			}
			if c, e := r.Cookie("bridge_session"); e == nil {
				a.mu.Lock()
				delete(a.sessions, c.Value)
				a.mu.Unlock()
			}
			http.SetCookie(w, &http.Cookie{Name: "bridge_session", Path: "/", MaxAge: -1, HttpOnly: true})
			jsonOut(w, map[string]bool{"ok": true})
		case "/manage/status":
			if r.Method != "GET" {
				fail(w, 405, "需要 GET")
				return
			}
			a.mu.Lock()
			port := a.cfg.Port
			a.mu.Unlock()
			result := map[string]any{"port": port, "version": "0.2.7", "connected": false}
			b, e := a.qb("GET", "transfer/info", nil, "")
			if e != nil {
				result["error"] = e.Error()
				jsonOut(w, result)
				return
			}
			var transfer any
			json.Unmarshal(b, &transfer)
			b, e = a.qb("GET", "torrents/info", nil, "")
			if e != nil {
				result["error"] = e.Error()
				jsonOut(w, result)
				return
			}
			var tasks any
			json.Unmarshal(b, &tasks)
			result["connected"] = true
			result["transfer"] = transfer
			result["tasks"] = tasks
			jsonOut(w, result)
		case "/manage/logs":
			if r.Method != "GET" {
				fail(w, 405, "需要 GET")
				return
			}
			a.mu.Lock()
			ls := append([]string{}, a.logs...)
			a.mu.Unlock()
			jsonOut(w, ls)
		case "/manage/settings":
			if r.Method != "POST" {
				fail(w, 405, "需要 POST")
				return
			}
			var v struct {
				Port     int    `json:"port"`
				Password string `json:"password"`
			}
			if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&v) != nil {
				fail(w, 400, "设置无效")
				return
			}
			if e := a.saveSettings(v.Port, v.Password); e != nil {
				fail(w, 400, e.Error())
				return
			}
			a.event("连接设置已更新")
			jsonOut(w, map[string]bool{"ok": true, "relogin": v.Password != ""})
		case "/manage/test":
			if r.Method != "POST" {
				fail(w, 405, "需要 POST")
				return
			}
			if e := a.backendLogin(); e != nil {
				a.event("连接检测失败：" + e.Error())
				fail(w, 502, e.Error())
				return
			}
			b, e := a.qb("GET", "app/version", nil, "")
			if e != nil {
				fail(w, 502, e.Error())
				return
			}
			a.event("连接检测成功，qBittorrent API 已就绪")
			jsonOut(w, map[string]string{"version": string(b)})
		case "/manage/action":
			if r.Method != "POST" {
				fail(w, 405, "需要 POST")
				return
			}
			var v struct {
				Action      string `json:"action"`
				Hash        string `json:"hash"`
				DeleteFiles bool   `json:"deleteFiles"`
			}
			if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&v) != nil {
				fail(w, 400, "参数无效")
				return
			}
			if len(v.Hash) != 40 && len(v.Hash) != 64 {
				fail(w, 400, "任务标识无效")
				return
			}
			if _, e := hex.DecodeString(v.Hash); e != nil {
				fail(w, 400, "任务标识无效")
				return
			}
			if v.Action != "pause" && v.Action != "resume" && v.Action != "delete" {
				fail(w, 400, "操作无效")
				return
			}
			if v.Action == "delete" {
				if e := a.deleteNative(r.Context(), v.Hash, v.DeleteFiles); e != nil {
					fail(w, 409, e.Error())
					return
				}
				jsonOut(w, map[string]bool{"ok": true})
				return
			}
			if e := a.backendLogin(); e != nil {
				fail(w, 502, e.Error())
				return
			}
			verb := v.Action
			version, e := a.qb("GET", "app/version", nil, "")
			if e != nil {
				fail(w, 502, e.Error())
				return
			}
			if strings.HasPrefix(strings.TrimPrefix(string(version), "v"), "5.") {
				if verb == "pause" {
					verb = "stop"
				}
				if verb == "resume" {
					verb = "start"
				}
			}
			values := url.Values{"hashes": {v.Hash}, "deleteFiles": {strconv.FormatBool(v.DeleteFiles)}}
			_, e = a.qb("POST", "torrents/"+verb, strings.NewReader(values.Encode()), "application/x-www-form-urlencoded")
			if e != nil {
				fail(w, 502, e.Error())
				return
			}
			a.event("任务操作成功：" + v.Action)
			jsonOut(w, map[string]bool{"ok": true})
		case "/manage/directory":
			a.directory(w, r)
		case "/manage/preferences":
			if r.Method != "GET" {
				fail(w, 405, "需要 GET")
				return
			}
			b, e := a.qb("GET", "app/preferences", nil, "")
			if e != nil {
				fail(w, 502, e.Error())
				return
			}
			var v map[string]any
			json.Unmarshal(b, &v)
			jsonOut(w, map[string]any{"save_path": v["save_path"]})
		case "/manage/add":
			a.add(w, r)
		case "/manage/path":
			if r.Method != "POST" {
				fail(w, 405, "需要 POST")
				return
			}
			var v struct {
				Path string `json:"path"`
			}
			if json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&v) != nil {
				fail(w, 400, "路径无效")
				return
			}
			p, e := validPath(v.Path)
			if e != nil {
				fail(w, 400, e.Error())
				return
			}
			jsonOut(w, map[string]string{"path": p})
		default:
			fail(w, 404, "接口不存在")
		}
		return
	}
	r.URL.Path = path
	sub, _ := fs.Sub(assets, "web")
	http.FileServer(http.FS(sub)).ServeHTTP(w, r)
}
func normalizeVolumePath(p string) (string, error) {
	p = filepath.Clean(strings.TrimSpace(p))
	if !filepath.IsAbs(p) {
		return "", errors.New("请选择绝对目录路径")
	}
	parts := strings.Split(strings.TrimPrefix(p, "/"), "/")
	if len(parts) < 2 || !strings.HasPrefix(parts[0], "vol") {
		return "", errors.New("请选择飞牛存储卷内的目录")
	}
	n, e := strconv.Atoi(strings.TrimPrefix(parts[0], "vol"))
	if e != nil || n < 1 {
		return "", errors.New("存储卷路径无效")
	}
	return p, nil
}
func checkWritableDirectory(p string) error {
	f, e := os.Stat(p)
	if e != nil || !f.IsDir() {
		return errors.New("所选路径不是可访问的目录")
	}
	probe, e := os.CreateTemp(p, ".fnqbbridge-write-test-")
	if e != nil {
		return errors.New("目录不可写")
	}
	name := probe.Name()
	probe.Close()
	return os.Remove(name)
}
func validPath(p string) (string, error) {
	p, e := normalizeVolumePath(p)
	if e != nil {
		return "", e
	}
	real, e := filepath.EvalSymlinks(p)
	if e != nil {
		return "", errors.New("目录不存在或无法访问")
	}
	if _, e = normalizeVolumePath(real); e != nil {
		return "", e
	}
	if e = checkWritableDirectory(real); e != nil {
		return "", e
	}
	return real, nil
}

func (a *App) add(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		fail(w, 405, "需要 POST")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<20)
	if e := r.ParseMultipartForm(4 << 20); e != nil {
		fail(w, 400, "任务内容无效或超过 64MB")
		return
	}
	defer r.MultipartForm.RemoveAll()
	if e := a.backendLogin(); e != nil {
		fail(w, 502, e.Error())
		return
	}
	// Forward the validated multipart data using a new multipart writer.
	a.addMultipart(w, r)
}
func main() {
	if len(os.Args) == 4 && os.Args[1] == "--native-delete-status" {
		if err := runNativeDeleteStatus(os.Args[2], os.Args[3]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) == 3 && os.Args[1] == "--isolated-native-delete-test" {
		if e := runNativeSelfTest(os.Args[2]); e != nil {
			fmt.Fprintln(os.Stderr, e)
			os.Exit(1)
		}
		return
	}

	if len(os.Args) != 2 {
		log.Fatal("usage: fnqbbridge config.json")
	}
	writer := &rotatingLog{path: filepath.Join(filepath.Dir(os.Args[1]), "bridge.log"), max: 2 << 20, backups: 3}
	log.SetOutput(writer)
	raw, e := os.ReadFile(os.Args[1])
	if e != nil {
		log.Fatal(e)
	}
	var cfg Config
	if json.Unmarshal(raw, &cfg) != nil || len(cfg.Password) < 12 {
		log.Fatal("配置密码至少12位")
	}
	if cfg.Listen == "" {
		cfg.Listen = ":18080"
	}
	if cfg.Port == 0 {
		cfg.Port = 7777
	}
	a := &App{cfg: cfg, configPath: os.Args[1], sessions: make(map[string]session)}
	a.discover = func() (string, string, error) { q := NewQbit(); e := q.refresh(); return q.uds, q.password, e }
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		s, _, e := a.discover()
		if e != nil {
			return nil, e
		}
		return (&net.Dialer{Timeout: 10 * time.Second}).DialContext(ctx, "unix", s)
	}, ResponseHeaderTimeout: 20 * time.Second}
	jar, _ := cookiejar.New(nil)
	a.backend = &http.Client{Transport: transport, Jar: jar, Timeout: 40 * time.Second}
	a.initNative()
	ln, e := net.Listen("tcp", fmt.Sprintf(":%d", cfg.Port))
	if e != nil {
		log.Fatal(e)
	}
	a.listener = ln
	a.bridge = &http.Server{Handler: a.proxy(), ReadHeaderTimeout: 10 * time.Second}
	go a.bridge.Serve(ln)
	a.event(fmt.Sprintf("代理服务启动，MoviePilot 端口 %d", cfg.Port))
	if dest := os.Getenv("TRIM_APPDEST"); dest != "" {
		p := filepath.Join(dest, "app.sock")
		os.Remove(p)
		if socket, e := net.Listen("unix", p); e == nil {
			os.Chmod(p, 0666)
			go http.Serve(socket, http.HandlerFunc(a.serve))
			defer os.Remove(p)
		} else {
			log.Fatal(e)
		}
	}
	srv := &http.Server{Addr: cfg.Listen, Handler: http.HandlerFunc(a.serve), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		if e := srv.ListenAndServe(); e != nil && e != http.ErrServerClosed {
			log.Fatal(e)
		}
	}()
	a.event("原生风格管理界面已启动")
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	srv.Shutdown(ctx)
	a.mu.Lock()
	bridge := a.bridge
	a.mu.Unlock()
	bridge.Shutdown(ctx)
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int)        { w.status = code; w.ResponseWriter.WriteHeader(code) }
func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
