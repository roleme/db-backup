package ping

import (
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
	var resp *http.Response
	var err error
	if body == "" {
		resp, err = h.Client.Get(url)
	} else {
		resp, err = h.Client.Post(url, "text/plain; charset=utf-8", strings.NewReader(body))
	}
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode < 400
}
