package status

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	Dir       = ".status"
	StaleTemp = time.Hour
)

type Entry struct {
	At     int64 `json:"at"`
	OK     bool  `json:"ok"`
	LastOK int64 `json:"last_ok"`
}

func path(dir, target, action string) string {
	return filepath.Join(dir, Dir, target+"."+action+".json")
}

func Read(dir, target, action string) (Entry, bool, error) {
	b, err := os.ReadFile(path(dir, target, action))
	if errors.Is(err, os.ErrNotExist) {
		return Entry{}, false, nil
	}
	if err != nil {
		return Entry{}, false, err
	}
	var e Entry
	if err := json.Unmarshal(b, &e); err != nil {
		return Entry{}, false, err
	}
	return e, true, nil
}

func sweep(dir, target, action string) {
	prefix := "." + target + "." + action + "."
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), prefix) {
			continue
		}
		if info, err := e.Info(); err == nil && time.Since(info.ModTime()) > StaleTemp {
			_ = os.Remove(filepath.Join(dir, e.Name()))
		}
	}
}

func Record(dir, target, action string, ok bool, now time.Time) error {
	e := Entry{At: now.Unix(), OK: ok}
	if prev, found, _ := Read(dir, target, action); found {
		e.LastOK = prev.LastOK
	}
	if ok {
		e.LastOK = e.At
	}
	b, err := json.Marshal(e)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(dir, Dir), 0o755); err != nil {
		return err
	}
	sweep(filepath.Join(dir, Dir), target, action)
	tmp, err := os.CreateTemp(filepath.Join(dir, Dir), "."+target+"."+action+".*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	if _, err := tmp.Write(b); err != nil {
		_ = tmp.Close()
		_ = os.Remove(name)
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		_ = os.Remove(name)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(name)
		return err
	}
	if err := os.Rename(name, path(dir, target, action)); err != nil {
		_ = os.Remove(name)
		return err
	}
	return syncDir(filepath.Join(dir, Dir))
}

func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}
