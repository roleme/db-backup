package heartbeat

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/roleme/db-backup/internal/config"
	"github.com/roleme/db-backup/internal/logx"
	"github.com/roleme/db-backup/internal/status"
)

const (
	clockSkew           = 5 * time.Minute
	defaultMaxAge       = 26 * time.Hour
	defaultVerifyMaxAge = 192 * time.Hour
)

type Notifier interface {
	Notify(base, suffix, body string)
}

type Deps struct {
	Dir      string
	Getenv   func(string) string
	Notifier Notifier
	Now      func() time.Time
}

func Problems(kind string, f *config.File, dir string, now time.Time) []string {
	maxAge, action := defaultMaxAge, "backup"
	if kind == "verify" {
		maxAge, action = defaultVerifyMaxAge, "verify"
	}
	if f.Heartbeat != nil {
		maxAge = f.Heartbeat.MaxAge
		if kind == "verify" {
			maxAge = f.Heartbeat.VerifyMaxAge
		}
	}
	var out []string
	for _, e := range f.Entries {
		if e.Err != nil {
			if kind == "dumps" {
				out = append(out, e.Name+": invalid configuration")
			}
			continue
		}
		if kind == "verify" && e.Target.Vars["VERIFY_SCHEDULE"] == "" {
			continue
		}
		entry, found, err := status.Read(dir, e.Name, action)
		switch {
		case err != nil:
			out = append(out, fmt.Sprintf("%s: cannot read the status: %v", e.Name, err))
		case !found:
			out = append(out, fmt.Sprintf("%s: no %s recorded", e.Name, action))
		case !entry.OK:
			out = append(out, fmt.Sprintf("%s: last %s failed", e.Name, action))
		default:
			age := now.Sub(time.Unix(entry.LastOK, 0))
			if age < -clockSkew {
				out = append(out, fmt.Sprintf("%s: last successful %s is in the future", e.Name, action))
			} else if age > maxAge {
				out = append(out, fmt.Sprintf("%s: last successful %s is %d h old", e.Name, action, int(age.Hours())))
			}
		}
	}
	return out
}

func Run(_ context.Context, kind string, f *config.File, d Deps) int {
	if kind != "dumps" && kind != "verify" {
		logx.Errorf("usage: db-backup-heartbeat dumps|verify")
		return 2
	}
	if f.Heartbeat == nil {
		if f.HeartbeatErr != nil {
			logx.Errorf("%v", f.HeartbeatErr)
		} else {
			logx.Errorf("no heartbeat is configured")
		}
		return 1
	}
	dumpsURL, verifyURL, err := f.Heartbeat.URLs(d.Getenv)
	if err != nil {
		logx.Errorf("heartbeat: %v", err)
		return 1
	}
	url := dumpsURL
	if kind == "verify" {
		url = verifyURL
	}
	if url == "" {
		logx.Errorf("heartbeat: no %s URL is configured", kind)
		return 1
	}
	problems := Problems(kind, f, d.Dir, d.Now())
	if len(problems) == 0 {
		d.Notifier.Notify(url, "", "")
		logx.Infof("heartbeat %s ok", kind)
		return 0
	}
	for _, p := range problems {
		logx.Errorf("heartbeat %s: %s", kind, p)
	}
	d.Notifier.Notify(url, "/fail", strings.Join(problems, "\n"))
	return 1
}
