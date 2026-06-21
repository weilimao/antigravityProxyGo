package netutil

import (
	"net/http"
	"time"
)

// NewClient returns a new http.Client configured with system proxy auto-detection and custom timeout.
func NewClient(timeout time.Duration) *http.Client {
	return &http.Client{
		Transport: NewTransport(),
		Timeout:   timeout,
	}
}
