package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// traceDownloadFields inspects a copy and restores the exact request bytes.
// Files, URLs, cookies and authentication fields are never included in logs.
func (a *App) traceDownloadFields(r *http.Request) error {
	b, e := io.ReadAll(io.LimitReader(r.Body, (64<<20)+1))
	if e != nil {
		return e
	}
	r.Body.Close()
	r.Body = io.NopCloser(bytes.NewReader(b))
	if len(b) > 64<<20 {
		return fmt.Errorf("任务请求超过64MB")
	}
	copyReq := r.Clone(r.Context())
	copyReq.Body = io.NopCloser(bytes.NewReader(b))
	copyReq.Form = nil
	copyReq.PostForm = nil
	copyReq.MultipartForm = nil
	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
		e = copyReq.ParseMultipartForm(1 << 20)
	} else {
		e = copyReq.ParseForm()
	}
	if copyReq.MultipartForm != nil {
		defer copyReq.MultipartForm.RemoveAll()
	}
	if e != nil {
		return fmt.Errorf("下载参数解析失败")
	}
	fields := map[string]any{}
	for _, k := range []string{"savepath", "save_path", "download_dir", "location", "category", "autoTMM", "paused", "stopped", "root_folder", "contentLayout"} {
		v, exists := copyReq.PostForm[k]
		if exists {
			fields[k] = v
		}
	}
	fields["body_bytes"] = len(b)
	fields["content_type"] = strings.Split(r.Header.Get("Content-Type"), ";")[0]
	if v := copyReq.PostForm.Get("savepath"); v != "" && strings.TrimSpace(v) != v {
		a.event("警告：MoviePilot 保存路径带首尾空白；代理保留原值，请检查上游目录配置")
	}
	data, _ := json.Marshal(fields)
	a.event("MoviePilot 下载参数（原样转发）：" + string(data))
	if _, ok := copyReq.PostForm["savepath"]; !ok {
		a.event("提示：本次请求未包含 savepath，保存位置可能由下载器默认目录或自动分类规则决定")
	}
	return nil
}
