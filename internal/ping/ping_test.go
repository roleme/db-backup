package ping

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/roleme/db-backup/internal/logx"
)

func fast(h *HTTP) *HTTP {
	h.Backoff = time.Millisecond
	return h
}

func TestPingSuccessAndFailPaths(t *testing.T) {
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.Method+" "+r.URL.Path)
	}))
	defer srv.Close()
	h := fast(NewHTTP())
	h.Ping(srv.URL+"/abc/", "")
	h.Ping(srv.URL+"/abc", "/fail")
	if strings.Join(paths, ",") != "GET /abc,GET /abc/fail" {
		t.Errorf("paths = %v", paths)
	}
}

func TestPingRetriesThenSucceeds(t *testing.T) {
	var n int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&n, 1) < 3 {
			http.Error(w, "boom", http.StatusInternalServerError)
		}
	}))
	defer srv.Close()
	fast(NewHTTP()).Ping(srv.URL, "")
	if atomic.LoadInt32(&n) != 3 {
		t.Errorf("attempts = %d, want 3", n)
	}
}

func TestPingFailureWarnsWithoutTheURL(t *testing.T) {
	var buf bytes.Buffer
	defer func(old io.Writer) { logx.Err = old }(logx.Err)
	logx.Err = &buf
	fast(NewHTTP()).Ping("http://127.0.0.1:1/SECRETTOKEN", "/fail")
	if !strings.Contains(buf.String(), "WARN: ping /fail failed") {
		t.Errorf("log = %q", buf.String())
	}
	if strings.Contains(buf.String(), "SECRETTOKEN") {
		t.Error("the ping URL must never be logged")
	}
}

func TestEmptyBaseDoesNothing(t *testing.T) {
	fast(NewHTTP()).Ping("", "/fail")
}

func TestNotifyPostsTheMessageAndGetsWithoutOne(t *testing.T) {
	var got []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got = append(got, r.Method+" "+r.URL.Path+" "+string(b))
	}))
	defer srv.Close()
	h := fast(NewHTTP())

	h.Notify(srv.URL+"/abc/", "", "")
	h.Notify(srv.URL+"/abc", "/fail", "alpha: no backup recorded\nbeta: last backup failed")

	want := "GET /abc ,POST /abc/fail alpha: no backup recorded\nbeta: last backup failed"
	if strings.Join(got, ",") != want {
		t.Errorf("requests = %q, want %q", strings.Join(got, ","), want)
	}
}

func TestNotifyRetriesAndNeverPanicsOnAnEmptyBase(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&calls, 1) < 3 {
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	defer srv.Close()
	h := fast(NewHTTP())

	h.Notify(srv.URL, "/fail", "reason")
	if atomic.LoadInt32(&calls) != 3 {
		t.Errorf("calls = %d, want 3", calls)
	}
	h.Notify("", "/fail", "reason")
}
