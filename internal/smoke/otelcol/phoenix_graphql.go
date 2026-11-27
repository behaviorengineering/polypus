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

const phoenixCreateModelMutation = `
mutation PhoenixCreateModel($input: CreateModelMutationInput!) {
  createModel(input: $input) {
    model {
      id
      name
      namePattern
    }
  }
}`

const phoenixTraceCostQuery = `
query PhoenixTraceCost($traceId: ID!) {
  projects {
    edges {
      node {
        name
        trace(traceId: $traceId) {
          costSummary {
            total { cost }
          }
          spans(first: 10) {
            edges {
              node {
                tokenCountPrompt
                tokenCountCompletion
                costSummary {
                  total { cost }
                }
              }
            }
          }
        }
      }
    }
  }
}`

// gqlRequest is a GraphQL request body.
type gqlRequest struct {
	Query     string `json:"query"`
	Variables any    `json:"variables,omitempty"`
}

type phoenixTokenPriceInput struct {
	TokenType            string  `json:"tokenType"`
	Kind                 string  `json:"kind"`
	CostPerMillionTokens float64 `json:"costPerMillionTokens"`
}

type phoenixCreateModelInput struct {
	Name        string                   `json:"name"`
	Provider    string                   `json:"provider"`
	NamePattern string                   `json:"namePattern"`
	Costs       []phoenixTokenPriceInput `json:"costs"`
}

type phoenixTraceCost struct {
	totalCost        float64
	promptTokens     int
	completionTokens int
	spanCost         float64
	traceFound       bool
	priced           bool
}

func phoenixSpanCountGraphQL(ctx context.Context, opts CollectorOptions, traceID string) (int, error) {
	traceID, err := normalizeTraceIDHex(traceID)
	if err != nil {
		return 0, err
	}
	body, err := phoenixGraphQL(ctx, opts, phoenixTraceSpansQuery, map[string]any{"traceId": traceID})
	if err != nil {
		return 0, err
	}
	return phoenixGraphQLSpanCount(body, opts.PhoenixProject)
}

func phoenixCreateModel(ctx context.Context, opts CollectorOptions, in phoenixCreateModelInput) error {
	body, err := phoenixGraphQL(ctx, opts, phoenixCreateModelMutation, map[string]any{"input": in})
	if err != nil {
		return err
	}
	return phoenixCreateModelResult(body)
}

func phoenixTraceCostGraphQL(ctx context.Context, opts CollectorOptions, traceID string) (phoenixTraceCost, error) {
	traceID, err := normalizeTraceIDHex(traceID)
	if err != nil {
		return phoenixTraceCost{}, err
	}
	body, err := phoenixGraphQL(ctx, opts, phoenixTraceCostQuery, map[string]any{"traceId": traceID})
	if err != nil {
		return phoenixTraceCost{}, err
	}
	return phoenixGraphQLTraceCost(body, opts.PhoenixProject)
}

func phoenixGraphQL(ctx context.Context, opts CollectorOptions, query string, variables any) ([]byte, error) {
	base := strings.TrimRight(opts.PhoenixBaseURL, "/")
	buf, err := json.Marshal(gqlRequest{Query: query, Variables: variables})
	if err != nil {
		return nil, fmt.Errorf("collector smoke: phoenix graphql payload: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/graphql", bytes.NewReader(buf))
	if err != nil {
		return nil, fmt.Errorf("collector smoke: phoenix graphql request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if opts.PhoenixAPIKey != "" {
		req.Header.Set("Authorization", "Bearer "+opts.PhoenixAPIKey)
	}
	ans, err := opts.fetch(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("collector smoke: phoenix graphql: %w", err)
	}
	if !ans.ok() {
		return nil, fmt.Errorf("phoenix graphql: %s: %s", ans.line, strings.TrimSpace(string(ans.body)))
	}
	return ans.body, nil
}

type phoenixGQLErrors struct {
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

func phoenixGQLFirstError(body []byte) error {
	var parsed phoenixGQLErrors
	if err := json.Unmarshal(body, &parsed); err != nil {
		return fmt.Errorf("phoenix graphql: decode: %w", err)
	}
	if len(parsed.Errors) > 0 {
		return fmt.Errorf("phoenix graphql: %s", parsed.Errors[0].Message)
	}
	return nil
}

// phoenixGraphQLSpanCount reads the span count for project (default "default") from
// a PhoenixTraceSpans answer.
func phoenixGraphQLSpanCount(body []byte, project string) (int, error) {
	var parsed struct {
		phoenixGQLErrors
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
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return 0, fmt.Errorf("phoenix graphql: decode: %w", err)
	}
	if len(parsed.Errors) > 0 {
		return 0, fmt.Errorf("phoenix graphql: %s", parsed.Errors[0].Message)
	}

	wantProject := strings.TrimSpace(project)
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

func phoenixCreateModelResult(body []byte) error {
	if err := phoenixGQLFirstError(body); err != nil {
		return err
	}
	var parsed struct {
		Data struct {
			CreateModel *struct {
				Model struct {
					ID string `json:"id"`
				} `json:"model"`
			} `json:"createModel"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return fmt.Errorf("phoenix graphql: decode createModel: %w", err)
	}
	if parsed.Data.CreateModel == nil || parsed.Data.CreateModel.Model.ID == "" {
		return fmt.Errorf("phoenix graphql: createModel returned no model")
	}
	return nil
}

func phoenixGraphQLTraceCost(body []byte, project string) (phoenixTraceCost, error) {
	if err := phoenixGQLFirstError(body); err != nil {
		return phoenixTraceCost{}, err
	}
	var parsed struct {
		Data struct {
			Projects struct {
				Edges []struct {
					Node struct {
						Name  string `json:"name"`
						Trace *struct {
							CostSummary *struct {
								Total struct {
									Cost *float64 `json:"cost"`
								} `json:"total"`
							} `json:"costSummary"`
							Spans struct {
								Edges []struct {
									Node struct {
										TokenCountPrompt     *int `json:"tokenCountPrompt"`
										TokenCountCompletion *int `json:"tokenCountCompletion"`
										CostSummary          *struct {
											Total struct {
												Cost *float64 `json:"cost"`
											} `json:"total"`
										} `json:"costSummary"`
									} `json:"node"`
								} `json:"edges"`
							} `json:"spans"`
						} `json:"trace"`
					} `json:"node"`
				} `json:"edges"`
			} `json:"projects"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return phoenixTraceCost{}, fmt.Errorf("phoenix graphql: decode cost: %w", err)
	}

	wantProject := strings.TrimSpace(project)
	if wantProject == "" {
		wantProject = "default"
	}
	for _, edge := range parsed.Data.Projects.Edges {
		if edge.Node.Name != wantProject {
			continue
		}
		if edge.Node.Trace == nil {
			return phoenixTraceCost{}, nil
		}
		out := phoenixTraceCost{traceFound: true}
		if cs := edge.Node.Trace.CostSummary; cs != nil && cs.Total.Cost != nil {
			out.totalCost = *cs.Total.Cost
			out.priced = out.totalCost > 0
		}
		for _, spanEdge := range edge.Node.Trace.Spans.Edges {
			node := spanEdge.Node
			if node.TokenCountPrompt != nil {
				out.promptTokens += *node.TokenCountPrompt
			}
			if node.TokenCountCompletion != nil {
				out.completionTokens += *node.TokenCountCompletion
			}
			if node.CostSummary != nil && node.CostSummary.Total.Cost != nil {
				out.spanCost += *node.CostSummary.Total.Cost
				if *node.CostSummary.Total.Cost > 0 {
					out.priced = true
				}
			}
		}
		if !out.priced && out.spanCost > 0 {
			out.priced = true
		}
		return out, nil
	}
	return phoenixTraceCost{}, fmt.Errorf("phoenix graphql: project %q not found", wantProject)
}
