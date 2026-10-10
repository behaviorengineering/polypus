package otelcol

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

const phoenixTraceSpansQuery = `
query PhoenixTraceSpans($traceId: ID!) {
  projects {
    edges {
      node {
        name
        trace(traceId: $traceId) {
          spans(first: 10) {
            edges {
              node {
                id
              }
            }
          }
        }
      }
    }
  }
}`

func phoenixSpanCountGraphQL(ctx context.Context, opts CollectorOptions, traceID string) (int, error) {
	traceID, err := normalizeTraceIDHex(traceID)
	if err != nil {
		return 0, err
	}
	base := strings.TrimRight(opts.PhoenixBaseURL, "/")
	payload := map[string]interface{}{
		"query": phoenixTraceSpansQuery,
		"variables": map[string]string{
			"traceId": traceID,
		},
	}
	buf, err := json.Marshal(payload)
	if err != nil {
		return 0, fmt.Errorf("collector smoke: phoenix graphql payload: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/graphql", bytes.NewReader(buf))
	if err != nil {
		return 0, fmt.Errorf("collector smoke: phoenix graphql request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if opts.PhoenixAPIKey != "" {
		req.Header.Set("Authorization", "Bearer "+opts.PhoenixAPIKey)
	}
	resp, err := smokeHTTPDo(ctx, http.DefaultClient, req)
	if err != nil {
		return 0, fmt.Errorf("collector smoke: phoenix graphql: %w", err)
	}
	body, err := readLimitedBody(resp, 1<<20)
	if err != nil {
		return 0, fmt.Errorf("collector smoke: phoenix graphql: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return 0, fmt.Errorf("phoenix graphql: %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}

	var parsed struct {
		Data struct {
			Projects struct {
				Edges []struct {
					Node struct {
						Name  string `json:"name"`
						Trace *struct {
							Spans struct {
								Edges []struct {
									Node struct {
										ID string `json:"id"`
									} `json:"node"`
								} `json:"edges"`
							} `json:"spans"`
						} `json:"trace"`
					} `json:"node"`
				} `json:"edges"`
			} `json:"projects"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return 0, fmt.Errorf("phoenix graphql: decode: %w", err)
	}
	if len(parsed.Errors) > 0 {
		return 0, fmt.Errorf("phoenix graphql: %s", parsed.Errors[0].Message)
	}

	wantProject := strings.TrimSpace(opts.PhoenixProject)
	if wantProject == "" {
		wantProject = "default"
	}
	for _, edge := range parsed.Data.Projects.Edges {
		if edge.Node.Name != wantProject {
			continue
		}
		if edge.Node.Trace == nil {
			return 0, nil
		}
		return len(edge.Node.Trace.Spans.Edges), nil
	}
	return 0, fmt.Errorf("phoenix graphql: project %q not found", wantProject)
}
