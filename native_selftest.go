package main

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/cookiejar"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func selfTestApp() *App {
	a := &App{}
	a.discover = func() (string, string, error) { q := NewQbit(); e := q.refresh(); return q.uds, q.password, e }
	tr := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		s, _, e := a.discover()
		if e != nil {
			return nil, e
		}
		return (&net.Dialer{Timeout: 3 * time.Second}).DialContext(ctx, "unix", s)
	}, ResponseHeaderTimeout: 10 * time.Second}
	jar, _ := cookiejar.New(nil)
	a.backend = &http.Client{Transport: tr, Jar: jar, Timeout: 15 * time.Second}
	a.initNative()
	return a
}
func runNativeSelfTest(root string) error {
	if os.Geteuid() != 0 {
		return fmt.Errorf("请使用sudo运行")
	}
	root, e := validPath(root)
	if e != nil {
		return e
	}
	a := selfTestApp()
	uid, e := a.native.Owner()
	if e != nil {
		return e
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	baseline, e := a.native.Rows(ctx, uid)
	if e != nil {
		return e
	}
	fmt.Printf("baseline_uid=%d existing_tasks=%d; existing task IDs are protected\n", uid, len(baseline))
	for _, deleteFiles := range []bool{false, true} {
		dir, e := os.MkdirTemp(root, "fnqbbridge-delete-test-")
		if e != nil {
			return e
		}
		if e = os.Chown(dir, uid, -1); e != nil {
			return e
		}
		name := "fnqbbridge-test-" + randomToken()[:16] + ".txt"
		content := []byte("fnqbbridge isolated deletion test\n" + randomToken() + "\n")
		target := filepath.Join(dir, name)
		if e = os.WriteFile(target, content, 0644); e != nil {
			return e
		}
		if e = os.Chown(target, uid, -1); e != nil {
			return e
		}
		marker := filepath.Join(dir, ".fnqbbridge-test-owned")
		os.WriteFile(marker, []byte(name), 0600)
		piece := sha1.Sum(content)
		info := []byte(fmt.Sprintf("d6:lengthi%de4:name%d:%s12:piece lengthi16384e6:pieces20:", len(content), len(name), name))
		info = append(info, piece[:]...)
		info = append(info, 'e')
		digest := sha1.Sum(info)
		hash := hex.EncodeToString(digest[:])
		torrent := append([]byte("d4:info"), info...)
		torrent = append(torrent, 'e')
		if e = a.backendLogin(); e != nil {
			return e
		}
		var body bytes.Buffer
		mw := multipart.NewWriter(&body)
		f, _ := mw.CreateFormFile("torrents", name+".torrent")
		f.Write(torrent)
		mw.WriteField("savepath", dir)
		mw.WriteField("autoTMM", "false")
		mw.WriteField("paused", "true")
		mw.WriteField("stopped", "true")
		mw.WriteField("skip_checking", "true")
		mw.Close()
		reply, e := a.qb("POST", "torrents/add", bytes.NewReader(body.Bytes()), mw.FormDataContentType())
		if e != nil || strings.TrimSpace(string(reply)) != "Ok." {
			return fmt.Errorf("测试任务添加失败，目录保留：%s；%v", dir, e)
		}
		fmt.Printf("isolated_test delete_files=%t hash=%s directory=%s\n", deleteFiles, hash, dir)
		found := false
		for i := 0; i < 60; i++ {
			rows, e := a.native.Rows(ctx, uid)
			if e != nil {
				return e
			}
			for _, r := range rows {
				if r.UID == uid && (strings.EqualFold(r.Hash, hash) || strings.EqualFold(r.TaskID, hash)) {
					for _, old := range baseline {
						if old.ID == r.ID {
							return fmt.Errorf("测试ID与原任务冲突，已停止")
						}
					}
					found = true
				}
			}
			if found {
				break
			}
			time.Sleep(time.Second)
		}
		if !found {
			return fmt.Errorf("原生未同步测试任务，已停止；仅可手动处理上述测试Hash，目录保留")
		}
		// Ensure files and all contents are exactly our generated fixture before mutation.
		check, e := os.ReadFile(target)
		if e != nil || !bytes.Equal(check, content) {
			return fmt.Errorf("测试文件已变化，拒绝删除")
		}
		list, e := os.ReadDir(dir)
		if e != nil || len(list) != 2 {
			return fmt.Errorf("测试目录出现其他文件，拒绝删除")
		}
		if e = a.deleteNative(ctx, hash, deleteFiles); e != nil {
			return fmt.Errorf("隔离测试失败，测试目录保留：%s；%w", dir, e)
		}
		if deleteFiles {
			if _, e = os.Stat(target); !os.IsNotExist(e) {
				return fmt.Errorf("任务已删除，但测试文件仍在：%s", target)
			}
		} else {
			b, e := os.ReadFile(target)
			if e != nil || !bytes.Equal(b, content) {
				return fmt.Errorf("保留文件模式未保留测试文件")
			}
		}
		after, e := a.native.Rows(ctx, uid)
		if e != nil {
			return e
		}
		for _, old := range baseline {
			same := false
			for _, r := range after {
				if old.ID == r.ID && old.UID == r.UID && old.Hash == r.Hash && old.TaskID == r.TaskID {
					same = true
				}
			}
			if !same {
				return fmt.Errorf("原有任务映射发生变化，停止剩余测试")
			}
		}
		fmt.Printf("PASS delete_files=%t: native record absent; engine task absent; file behavior correct; %d baseline identities unchanged\n", deleteFiles, len(baseline))
	}
	io.WriteString(os.Stdout, "ALL ISOLATED TESTS PASSED; no manual cleanup; any remaining own test directories retained for inspection\n")
	return nil
}

// Read-only observation: never issues task deletion, task creation, or file writes.
func runNativeDeleteStatus(hash, dir string) error {
	if os.Geteuid() != 0 || !validHash(hash) {
		return fmt.Errorf("需要sudo及明确Hash")
	}
	a := selfTestApp()
	uid, e := a.native.Owner()
	if e != nil {
		return e
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	rows, e := a.native.Rows(ctx, uid)
	if e != nil {
		return e
	}
	count := 0
	for _, r := range rows {
		if strings.EqualFold(r.Hash, hash) || strings.EqualFold(r.TaskID, hash) {
			count++
			fmt.Printf("native_present=true id=%d uid=%d type=%s\n", r.ID, r.UID, r.Type)
		}
	}
	fmt.Printf("read_only=true native_matches=%d total_tasks=%d\n", count, len(rows))
	present, e := a.native.EngineHas(ctx, hash)
	if e != nil {
		return e
	}
	fmt.Printf("engine_present=%t\n", present)
	entries, e := os.ReadDir(dir)
	if os.IsNotExist(e) {
		fmt.Println("test_directory_absent=true")
		return nil
	}
	if e != nil {
		return e
	}
	files := 0
	for _, entry := range entries {
		if entry.Name() != ".fnqbbridge-test-owned" {
			files++
		}
	}
	fmt.Printf("test_directory_present=true non_marker_entries=%d\n", files)
	return nil
}
