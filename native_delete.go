package main

import (
	"bufio"
	"context"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type nativeRow struct {
	ID     int64  `json:"id"`
	UID    int    `json:"uid"`
	Type   string `json:"type"`
	Hash   string `json:"hash_v1"`
	TaskID string `json:"task_id"`
}
type nativeOps struct {
	Owner     func() (int, error)
	Rows      func(context.Context, int) ([]nativeRow, error)
	Delete    func(context.Context, int, []int64, bool) error
	EngineHas func(context.Context, string) (bool, error)
}

func validHash(h string) bool {
	if len(h) != 40 && len(h) != 64 {
		return false
	}
	_, e := hex.DecodeString(h)
	return e == nil
}
func planNative(rows []nativeRow, uid int, input string) ([]int64, []string, error) {
	if uid <= 0 {
		return nil, nil, errors.New("无法确认下载引擎所属用户")
	}
	ids := []int64{}
	hashes := []string{}
	seen := map[string]bool{}
	seenIDs := map[int64]bool{}
	for _, part := range strings.Split(input, "|") {
		h := strings.ToLower(strings.TrimSpace(part))
		if !validHash(h) {
			return nil, nil, errors.New("必须提供明确的40/64位Hash，不支持空值或all")
		}
		if seen[h] {
			continue
		}
		seen[h] = true
		matches := []nativeRow{}
		for _, row := range rows {
			if row.UID != uid || row.Type != "qbt" {
				continue
			}
			if strings.EqualFold(row.Hash, h) || strings.EqualFold(row.TaskID, h) {
				matches = append(matches, row)
			}
		}
		if len(matches) != 1 || matches[0].ID <= 0 || matches[0].ID > 2147483647 {
			return nil, nil, fmt.Errorf("Hash未找到唯一原生任务，拒绝删除；所属用户UID=%d，匹配数=%d", uid, len(matches))
		}
		id := matches[0].ID
		if !seenIDs[id] {
			ids = append(ids, id)
			seenIDs[id] = true
		}
		hashes = append(hashes, h)
	}
	return ids, hashes, nil
}
func (a *App) deleteNative(ctx context.Context, input string, files bool) error {
	a.deleteMu.Lock()
	defer a.deleteMu.Unlock()
	ops := a.native
	if ops == nil {
		return errors.New("原生删除未初始化，已阻止底层删除")
	}
	uid, e := ops.Owner()
	if e != nil {
		return e
	}
	rows, e := ops.Rows(ctx, uid)
	if e != nil {
		return e
	}
	ids, hashes, e := planNative(rows, uid, input)
	if e != nil {
		return e
	}
	// Re-read immediately before issuing the one native delete request; do not fall back to qB.
	fresh, e := ops.Rows(ctx, uid)
	if e != nil {
		return e
	}
	freshIDs, _, e := planNative(fresh, uid, input)
	if e != nil || fmt.Sprint(ids) != fmt.Sprint(freshIDs) {
		return errors.New("原生任务映射已变化，未执行删除")
	}
	a.event(fmt.Sprintf("原生删除开始：UID=%d IDs=%v deleteFiles=%t", uid, ids, files))
	callErr := ops.Delete(ctx, uid, ids, files)
	// Native file removal is asynchronous; allow 30 seconds without retrying the mutation.
	a.event("原生删除请求已返回，等待原生记录与引擎任务清理完成（最长30秒）")
	for i := 0; i < 60; i++ {
		after, err := ops.Rows(ctx, uid)
		if err != nil {
			return fmt.Errorf("原生删除结果无法核验：%w", err)
		}
		present := false
		for _, r := range after {
			if r.UID != uid {
				continue
			}
			for _, id := range ids {
				if r.ID == id {
					present = true
				}
			}
		}
		if !present {
			for _, h := range hashes {
				exists, err := ops.EngineHas(ctx, h)
				if err != nil {
					return fmt.Errorf("原生记录已消失，但引擎状态无法核验：%w", err)
				}
				if exists {
					present = true
				}
			}
		}
		if !present {
			if files && callErr != nil {
				return errors.New("任务已消失，但删除文件结果不确定；未自动重试")
			}
			a.event(fmt.Sprintf("原生删除已核验：UID=%d IDs=%v，原生记录与引擎任务均不存在", uid, ids))
			return nil
		}
		select {
		case <-ctx.Done():
			return errors.New("删除结果核验超时；未回退删除底层任务")
		case <-time.After(500 * time.Millisecond):
		}
	}
	if callErr != nil {
		return fmt.Errorf("原生删除失败且仍有残留：%w", callErr)
	}
	return errors.New("30秒内未确认任务清理完成；删除可能仍在后台进行，未自动重试")
}
func readNativeFrame(r io.Reader) ([]byte, error) {
	p := make([]byte, 4)
	if _, e := io.ReadFull(r, p); e != nil {
		return nil, e
	}
	if string(p) == "CPRT" {
		h := make([]byte, 30)
		copy(h, p)
		if _, e := io.ReadFull(r, h[4:]); e != nil {
			return nil, e
		}
		a := uint64(binary.LittleEndian.Uint16(h[14:16]))
		t := uint64(binary.LittleEndian.Uint16(h[16:18]))
		d := uint64(binary.LittleEndian.Uint32(h[18:22]))
		x := uint64(binary.LittleEndian.Uint32(h[26:30]))
		if a+t+d+x > 4<<20 {
			return nil, errors.New("原生响应过大")
		}
		b := make([]byte, a+t+d+x)
		if _, e := io.ReadFull(r, b); e != nil {
			return nil, e
		}
		return b[a+t : a+t+d], nil
	}
	v := binary.LittleEndian.Uint16(p)
	size := 14
	if v == 1 {
		size = 22
	} else if v != 0 {
		return nil, errors.New("未知原生报文版本")
	}
	h := make([]byte, size)
	copy(h, p)
	if _, e := io.ReadFull(r, h[4:]); e != nil {
		return nil, e
	}
	n := binary.LittleEndian.Uint32(h[size-4:])
	if n > 4<<20 {
		return nil, errors.New("原生响应过大")
	}
	b := make([]byte, n)
	_, e := io.ReadFull(r, b)
	return b, e
}
func nativeRPC(ctx context.Context, socket string, uid int, method string, args map[string]any) error {
	if method != "appcgi.downloadcenter.task.delete" {
		return errors.New("不允许的原生方法")
	}
	args["req"] = method
	args["reqid"] = randomToken()
	args["uid"] = uid
	body, _ := json.Marshal(map[string]any{"data": args})
	h := make([]byte, 30)
	copy(h, "CPRT")
	binary.LittleEndian.PutUint64(h[6:14], 1)
	binary.LittleEndian.PutUint32(h[18:22], uint32(len(body)))
	binary.LittleEndian.PutUint32(h[22:26], uint32(uid))
	c, e := (&net.Dialer{Timeout: 3 * time.Second}).DialContext(ctx, "unix", socket)
	if e != nil {
		return errors.New("原生下载服务Socket连接失败")
	}
	defer c.Close()
	deadline := time.Now().Add(8 * time.Second)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	c.SetDeadline(deadline)
	packet := append(h, body...)
	for len(packet) > 0 {
		n, e := c.Write(packet)
		if e != nil {
			return errors.New("原生请求发送失败，结果需核验")
		}
		packet = packet[n:]
	}
	reply, e := readNativeFrame(c)
	if e != nil {
		return errors.New("原生响应失败或超时，结果需核验")
	}
	var envelope struct {
		Data struct {
			Result string          `json:"result"`
			ReqID  string          `json:"reqid"`
			Code   json.RawMessage `json:"errno"`
		} `json:"data"`
	}
	if json.Unmarshal(reply, &envelope) != nil {
		return errors.New("原生响应不是预期JSON")
	}
	if envelope.Data.ReqID != args["reqid"] {
		return errors.New("原生响应请求ID不匹配")
	}
	if envelope.Data.Result != "succ" {
		return fmt.Errorf("原生删除返回失败，错误码=%s", envelope.Data.Code)
	}
	return nil
}
func (a *App) initNative() {
	exe, _ := os.Executable()
	helper := filepath.Join(filepath.Dir(exe), "native-map")
	socket := os.Getenv("FNQB_NATIVE_SOCKET")
	if socket == "" {
		socket = "/run/trim_app_cgi/downloadcenter"
	}
	a.native = &nativeOps{Owner: func() (int, error) {
		wanted, _, e := a.discover()
		if e != nil {
			return 0, e
		}
		entries, e := os.ReadDir("/proc")
		if e != nil {
			return 0, e
		}
		owner := 0
		matches := 0
		for _, entry := range entries {
			if _, e := strconv.Atoi(entry.Name()); e != nil {
				continue
			}
			dir := filepath.Join("/proc", entry.Name())
			b, e := os.ReadFile(filepath.Join(dir, "cmdline"))
			if e != nil {
				continue
			}
			args := strings.Split(string(b), "\x00")
			if len(args) == 0 || filepath.Base(args[0]) != "trim-qbittorrent-nox" {
				continue
			}
			uds, e := getCmdParams(string(b), "webui-sock-path")
			if e != nil || uds != wanted {
				continue
			}
			st, e := os.Stat(dir)
			if e != nil {
				continue
			}
			owner = int(st.Sys().(*syscall.Stat_t).Uid)
			matches++
		}
		if matches != 1 || owner <= 0 {
			return 0, errors.New("无法唯一确定引擎用户，拒绝原生删除")
		}
		return owner, nil
	}, Rows: func(ctx context.Context, uid int) ([]nativeRow, error) {
		child, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		cmd := exec.CommandContext(child, helper, strconv.Itoa(uid))
		out, e := cmd.Output()
		if e != nil {
			return nil, errors.New("原生任务只读映射查询失败，请检查native-map及数据库兼容性")
		}
		if len(out) > 8<<20 {
			return nil, errors.New("原生任务映射过大")
		}
		rows := []nativeRow{}
		scanner := bufio.NewScanner(strings.NewReader(string(out)))
		scanner.Buffer(make([]byte, 4096), 65536)
		for scanner.Scan() {
			line := scanner.Text()
			if !strings.HasPrefix(line, "{") {
				continue
			}
			var r nativeRow
			if json.Unmarshal([]byte(line), &r) != nil || r.UID != uid {
				return nil, errors.New("原生任务映射格式或用户不匹配")
			}
			rows = append(rows, r)
		}
		if e = scanner.Err(); e != nil {
			return nil, e
		}
		return rows, nil
	}, Delete: func(ctx context.Context, uid int, ids []int64, files bool) error {
		return nativeRPC(ctx, socket, uid, "appcgi.downloadcenter.task.delete", map[string]any{"ids": ids, "delete_files": files})
	}, EngineHas: func(ctx context.Context, h string) (bool, error) {
		if e := a.backendLogin(); e != nil {
			return false, e
		}
		b, e := a.qb(http.MethodGet, "torrents/info?hashes="+url.QueryEscape(h), nil, "")
		if e != nil {
			return false, e
		}
		var tasks []map[string]any
		if e = json.Unmarshal(b, &tasks); e != nil {
			return false, e
		}
		for _, t := range tasks {
			for _, k := range []string{"hash", "infohash_v1", "infohash_v2"} {
				if value, ok := t[k].(string); ok && strings.EqualFold(value, h) {
					return true, nil
				}
			}
		}
		return false, nil
	}}
}
