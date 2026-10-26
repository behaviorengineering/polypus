package otelcol

import (
	"context"
	"fmt"
	"io"
	"net/http"

	"github.com/behaviorengineering/polypus/internal/outbound"
)

func smokeHTTPDo(ctx context.Context, client *http.Client, req *http.Request) (*http.Response, error) {
	if client == nil {
		client = http.DefaultClient
	}
	return outbound.Do(ctx, outbound.DepHealth, client, req)
}

func readLimitedBody(resp *http.Response, limit int64) ([]byte, error) {
	if resp == nil || resp.Body == nil {
		return nil, fmt.Errorf("collector smoke: nil response body")
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit))
	if err != nil {
		discardAndClose(resp.Body)
		return nil, fmt.Errorf("collector smoke: read body: %w", err)
	}
	if err := resp.Body.Close(); err != nil {
		return body, fmt.Errorf("collector smoke: close body: %w", err)
	}
	return body, nil
}

func discardAndClose(rc io.ReadCloser) {
	if rc == nil {
		return
	}
	_, _ = io.Copy(io.Discard, rc)
	_ = rc.Close()
}
