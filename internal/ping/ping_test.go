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
