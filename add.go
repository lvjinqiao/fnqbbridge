package main

import (
	"bytes"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
)

func (a *App) addMultipart(w http.ResponseWriter, r *http.Request) {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	rawPath := r.FormValue("savepath")
	if rawPath == "" {
		rawPath = r.FormValue("save_path")
	}
	path := strings.TrimSpace(rawPath)
	if r.FormValue("locationMode") == "custom" && path == "" {
		fail(w, 400, "指定保存位置时必须填写目录")
		return
	}
	if path != "" {
		p, e := validPath(path)
		if e != nil {
			fail(w, 400, e.Error())
			return
		}
		path = p
		mw.WriteField("savepath", p)
	}
	urls := strings.TrimSpace(r.FormValue("urls"))
	files := r.MultipartForm.File["torrents"]
	if urls == "" && len(files) == 0 {
		fail(w, 400, "请填写磁力链接或选择种子文件")
		return
	}
	if len(files) > 30 {
		fail(w, 400, "每次最多添加30个种子")
		return
	}
	if urls != "" {
		for _, line := range strings.Split(urls, "\n") {
			u, e := url.Parse(strings.TrimSpace(line))
			if e != nil || u.Scheme != "magnet" || u.Query().Get("xt") == "" {
				fail(w, 400, "请输入有效的磁力链接，每行一个")
				return
			}
		}
		mw.WriteField("urls", urls)
	}
	for _, f := range files {
		if strings.ToLower(filepath.Ext(f.Filename)) != ".torrent" || f.Size > 16<<20 {
			fail(w, 400, "只接受小于16MB的 .torrent 文件")
			return
		}
		src, e := f.Open()
		if e != nil {
			fail(w, 400, "读取种子失败")
			return
		}
		head := make([]byte, 1)
		_, e = src.Read(head)
		if e != nil || head[0] != 'd' {
			src.Close()
			fail(w, 400, "种子文件内容不是有效的 bencode 字典")
			return
		}
		part, e := mw.CreateFormFile("torrents", filepath.Base(f.Filename))
		if e != nil {
			src.Close()
			fail(w, 500, "构建任务失败")
			return
		}
		part.Write(head)
		_, e = io.Copy(part, src)
		src.Close()
		if e != nil {
			fail(w, 400, "读取种子失败")
			return
		}
	}
	for _, k := range []string{"category", "tags"} {
		v := r.FormValue(k)
		if len(v) > 256 {
			fail(w, 400, "分类或标签过长")
			return
		}
		mw.WriteField(k, v)
	}
	paused := r.FormValue("paused") == "true"
	mw.WriteField("paused", fmt.Sprint(paused))
	mw.WriteField("stopped", fmt.Sprint(paused))
	mw.WriteField("autoTMM", "false")
	mw.Close()
	b, e := a.qb("POST", "torrents/add", bytes.NewReader(buf.Bytes()), mw.FormDataContentType())
	if e != nil {
		fail(w, 502, e.Error())
		return
	}
	if strings.TrimSpace(string(b)) != "Ok." {
		fail(w, 502, "下载器拒绝任务：请检查种子、磁力链接和目录权限")
		return
	}
	a.event("已提交下载任务，保存目录：" + fmt.Sprintf("%q", path))
	result := map[string]any{"ok": true}
	if e := a.rememberDirectory(path); e != nil {
		result["preference_error"] = "任务已提交，但目录偏好保存失败"
		a.event("目录偏好保存失败")
	}
	jsonOut(w, result)
}
