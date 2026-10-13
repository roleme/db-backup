package ping

import (
	"context"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/roleme/db-backup/internal/logx"
)

const (
	RequestTimeout = 10 * time.Second
	Retries        = 3
	InitialBackoff = time.Second
)

type Pinger interface {
	Ping(base, suffix string)
}

type HTTP struct {
	Client  *http.Client
	Retries int
	Backoff time.Duration
}

func NewHTTP() *HTTP {
	return &HTTP{Client: &http.Client{Timeout: RequestTimeout}, Retries: Retries, Backoff: InitialBackoff}
}

func (h *HTTP) Ping(base, suffix string) {
	h.Notify(base, suffix, "")
}

func (h *HTTP) Notify(base, suffix, body string) {
	if base == "" {
		return
	}
	url := strings.TrimRight(base, "/") + suffix
	delay := h.Backoff
	for attempt := 0; ; attempt++ {
		if h.try(url, body) {
			return
		}
		if attempt >= h.Retries {
			break
		}
		time.Sleep(delay)
		delay *= 2
	}
	what := suffix
	if what == "" {
		what = "success"
	}
	logx.Warnf("ping %s failed", what)
}

func (h *HTTP) try(url, body string) bool {
	method, reader := http.MethodGet, io.Reader(nil)
	if body != "" {
		method, reader = http.MethodPost, strings.NewReader(body)
	}
	req, err := http.NewRequestWithContext(context.Background(), method, url, reader)
	if err != nil {
		return false
	}
	if body != "" {
		req.Header.Set("Content-Type", "text/plain; charset=utf-8")
	}
	resp, err := h.Client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode < 400
}
