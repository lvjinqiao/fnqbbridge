package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type Qbit struct {
	password string
	uds      string
}

func NewQbit() *Qbit {
	return &Qbit{}
}

func (q *Qbit) GetPassword() (string, error) {
	if err := q.refresh(); err != nil {
		return "", fmt.Errorf("refresh qbittorrent parameters: %w", err)
	}
	return q.password, nil
}

func (q *Qbit) GetUds() (string, error) {
	// uds should not be changed once set
	if q.uds != "" {
		return q.uds, nil
	}
	if err := q.refresh(); err != nil {
		return "", fmt.Errorf("refresh qbittorrent parameters: %w", err)
	}
	return q.uds, nil
}

// refresh checks if the qbittorrent-nox process is running and updates password and uds path
func (q *Qbit) refresh() error {

	cmdline, err := findQbittorrentProcess()
	if err != nil {
		return fmt.Errorf("find process: %w", err)
	}

	if q.password, err = getCmdParams(cmdline, "webui-password"); err != nil {
		return fmt.Errorf("get webui-password: %w", err)
	}
	if q.uds, err = getCmdParams(cmdline, "webui-sock-path"); err != nil {
		return fmt.Errorf("get webui-sock-path: %w", err)
	}
	return nil
}

func findQbittorrentProcess() (string, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return "", err
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}

		content, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid))
		if err != nil {
			continue
		}
		// cmdline arguments are separated by null bytes
		args := strings.Split(string(content), "\x00")
		if len(args) > 0 && filepath.Base(args[0]) == "trim-qbittorrent-nox" {
			return string(content), nil
		}
	}
	return "", fmt.Errorf("qbittorrent-nox process not found")
}

func getCmdParams(cmd string, parameter string) (string, error) {
	args := strings.Split(cmd, "\x00")
	for i, arg := range args {
		if v, ok := strings.CutPrefix(arg, "--"+parameter+"="); ok && v != "" {
			return v, nil
		}
		if arg == "--"+parameter && i+1 < len(args) && args[i+1] != "" {
			return args[i+1], nil
		}
	}
	return "", fmt.Errorf("missing downloader parameter: %s", parameter)
}
