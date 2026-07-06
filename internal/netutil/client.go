package netutil

import (
	"net/http"
	"sync"
	"time"
)

var (
	sharedTransport *http.Transport
	sharedTransportOnce sync.Once
)

func getSharedTransport() *http.Transport {
	sharedTransportOnce.Do(func() {
		sharedTransport = NewTransport()
	})
	return sharedTransport
}

// NewClient returns a new http.Client configured with system proxy auto-detection, custom timeout, and uses a globally shared transport to prevent connection leaks.
func NewClient(timeout time.Duration) *http.Client {
	return &http.Client{
		Transport: getSharedTransport(),
		Timeout:   timeout,
	}
}
