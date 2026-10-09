package retention

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

type Store struct {
	Dir    string
	Policy Policy
	Now    func() time.Time
}

func (s Store) tierDir(t Tier) string {
	return filepath.Join(s.Dir, string(t))
}

func replace(remove, create func() error) error {
	if err := remove(); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return create()
}

func (s Store) Save(unit, suffix, tmp string) error {
	at := s.Now()
	for _, t := range Tiers {
		if err := os.MkdirAll(s.tierDir(t), 0o755); err != nil {
			return err
		}
	}
	last := filepath.Join(s.tierDir(Last), FileName(unit, Last, at, suffix))
	if err := os.Rename(tmp, last); err != nil {
		return err
	}
	for _, t := range []Tier{Daily, Weekly, Monthly} {
		dst := filepath.Join(s.tierDir(t), FileName(unit, t, at, suffix))
		err := replace(func() error { return os.Remove(dst) }, func() error { return os.Link(last, dst) })
		if err != nil {
			return err
		}
	}
	for _, t := range Tiers {
		target := FileName(unit, t, at, suffix)
		link := filepath.Join(s.tierDir(t), LatestName(unit, suffix))
		err := replace(func() error { return os.Remove(link) }, func() error { return os.Symlink(target, link) })
		if err != nil {
			return err
		}
	}
	return nil
}

func (s Store) Prune(unit, suffix string) error {
	now := s.Now()
	for _, t := range Tiers {
		entries, err := os.ReadDir(s.tierDir(t))
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
			if err := os.Remove(filepath.Join(s.tierDir(t), e.Name())); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return err
			}
		}
	}
	return nil
}
