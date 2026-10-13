package status

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

const Dir = ".status"

type Entry struct {
	At     int64 `json:"at"`
	OK     bool  `json:"ok"`
	LastOK int64 `json:"last_ok"`
}

func path(dir, target, action string) string {
	return filepath.Join(dir, Dir, target+"."+action+".json")
}

func Read(dir, target, action string) (Entry, bool) {
	b, err := os.ReadFile(path(dir, target, action))
	if err != nil {
		return Entry{}, false
	}
	var e Entry
	if json.Unmarshal(b, &e) != nil {
		return Entry{}, false
	}
	return e, true
}

func Record(dir, target, action string, ok bool, now time.Time) error {
	e := Entry{At: now.Unix(), OK: ok}
	if prev, found := Read(dir, target, action); found {
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
	tmp, err := os.CreateTemp(filepath.Join(dir, Dir), "."+target+"."+action+".*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		os.Remove(name)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(name)
		return err
	}
	if err := os.Rename(name, path(dir, target, action)); err != nil {
		os.Remove(name)
		return err
	}
	return nil
}
