package retention

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type Store struct {
	Dir    string
	Policy Policy
	Now    func() time.Time
}

func checkUnit(unit string) error {
	if unit == "" || unit == "." || unit == ".." || strings.ContainsRune(unit, filepath.Separator) {
		return fmt.Errorf("unit name %q cannot be used as a directory", unit)
	}
	return nil
}

func (s Store) unitDir(unit string) string {
	return filepath.Join(s.Dir, unit)
}

func (s Store) tierDir(unit string, t Tier) string {
	return filepath.Join(s.unitDir(unit), string(t))
}

func (s Store) Latest(unit, suffix string) string {
	return filepath.Join(s.unitDir(unit), LatestName(suffix))
}

func replace(remove, create func() error) error {
	if err := remove(); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return create()
}

func (s Store) Save(unit, suffix, tmp string) error {
	if err := checkUnit(unit); err != nil {
		return err
	}
	at := s.Now()
	for _, t := range Tiers {
		if err := os.MkdirAll(s.tierDir(unit, t), 0o755); err != nil {
			return err
		}
	}
	lastName := FileName(unit, Last, at, suffix)
	last := filepath.Join(s.tierDir(unit, Last), lastName)
	if err := os.Rename(tmp, last); err != nil {
		return err
	}
	for _, t := range []Tier{Daily, Weekly, Monthly} {
		dst := filepath.Join(s.tierDir(unit, t), FileName(unit, t, at, suffix))
		err := replace(func() error { return os.Remove(dst) }, func() error { return os.Link(last, dst) })
		if err != nil {
			return err
		}
	}
	link := s.Latest(unit, suffix)
	target := filepath.Join(string(Last), lastName)
	return replace(func() error { return os.Remove(link) }, func() error { return os.Symlink(target, link) })
}

func (s Store) Prune(unit, suffix string) error {
	if err := checkUnit(unit); err != nil {
		return err
	}
	now := s.Now()
	for _, t := range Tiers {
		entries, err := os.ReadDir(s.tierDir(unit, t))
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		re := pattern(unit, t, suffix)
		cutoff := s.Policy.cutoff(t, now)
		for _, e := range entries {
			m := re.FindStringSubmatch(e.Name())
			if m == nil {
				continue
			}
			stamped, err := Parse(t, m[1], now.Location())
			if err != nil || !stamped.Before(cutoff) {
				continue
			}
			if err := os.Remove(filepath.Join(s.tierDir(unit, t), e.Name())); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return err
			}
		}
	}
	return nil
}
