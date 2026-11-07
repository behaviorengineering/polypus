package otelcol

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/behaviorengineering/polypus/internal/outbound"
)

// probeBodyMax caps a backend answer the smoke reads.
const probeBodyMax = 1 << 20

// probeAnswer is one backend answer: its status and the start of its body.
type probeAnswer struct {
	status int
	line   string // for example "404 Not Found".
	body   []byte
}

func (a probeAnswer) ok() bool { return a.status >= 200 && a.status < 300 }

// fetch sends req through the outbound seam, with o's client and policies, and reads the answer, closing the body
// before it returns. A body over probeBodyMax is an error, not a silent cut that
// fails later as a parse error.
func (o CollectorOptions) fetch(ctx context.Context, req *http.Request) (probeAnswer, error) {
	client := o.HTTP
	if client == nil {
		client = http.DefaultClient
	}
	// A nil Policies is the process default set.
	resp, err := o.Policies.Do(ctx, outbound.DepHealth, client, req)
	if err != nil {
		outbound.CloseResponse(resp)
		return probeAnswer{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, probeBodyMax+1))
	if err != nil {
		return probeAnswer{}, fmt.Errorf("collector smoke: read body: %w", err)
	}
	if len(body) > probeBodyMax {
		return probeAnswer{}, fmt.Errorf("collector smoke: %s answered over %d bytes", req.URL.Redacted(), probeBodyMax)
	}
	return probeAnswer{status: resp.StatusCode, line: resp.Status, body: body}, nil
}

// decodeListOrData reads a bare JSON array, or an object that holds it under "data".
func decodeListOrData[T any](body []byte) ([]T, error) {
	var list []T
	err := json.Unmarshal(body, &list)
	if err == nil {
		return list, nil
	}
	var wrapped struct {
		Data []T `json:"data"`
	}
	if werr := json.Unmarshal(body, &wrapped); werr != nil {
		return nil, err
	}
	return wrapped.Data, nil
}
