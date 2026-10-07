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
	if base == "" {
		return
	}
	url := strings.TrimRight(base, "/") + suffix
	delay := h.Backoff
	for attempt := 0; ; attempt++ {
		if h.try(url) {
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

func (h *HTTP) try(url string) bool {
	resp, err := h.Client.Get(url)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode < 400
}
