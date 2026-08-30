package main

import (
	"context"
	"io"
	"net/http"
	"strings"
	"time"
)

var smokeHTTPClient = &http.Client{Timeout: 120 * time.Second}

func httpNewGet(ctx context.Context, url string) (*http.Request, error) {
	return http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
}

func httpNewPost(ctx context.Context, url, body string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	return req, nil
}

func httpDefaultDo(req *http.Request) (*http.Response, error) {
	return smokeHTTPClient.Do(req)
}

func ioReadAllLimit(r io.Reader, n int64) ([]byte, error) {
	return io.ReadAll(io.LimitReader(r, n))
}
