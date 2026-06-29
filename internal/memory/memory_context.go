package memory

import (
	"context"
	"fmt"
)

type MemoryContextSection struct {
	Source  string       `json:"source"`
	Content string       `json:"content"`
	Facts   []JsonObject `json:"facts"`
}

type MemoryContextRequest struct {
	Scope            Scope  `json:"scope"`
	Query            string `json:"query"`
	IncludeShortTerm bool   `json:"include_short_term"`
	IncludeLongTerm  bool   `json:"include_long_term"`
	IncludeReasoning bool   `json:"include_reasoning"`
	IncludeGraph     bool   `json:"include_graph"`
	MaxItems         int    `json:"max_items"`
	GraphLimit       int    `json:"graph_limit"`
}

type MemoryContextResponse struct {
	Sections []MemoryContextSection `json:"sections"`
}

func (c *HTTPClient) GetMemoryContext(ctx context.Context, request MemoryContextRequest) (MemoryContextResponse, error) {
	var response MemoryContextResponse
	if !c.enabled {
		return response, nil
	}
	if err := c.postJSON(ctx, "/v1/memory/context", request, &response); err != nil {
		return response, fmt.Errorf("get combined memory context: %w", err)
	}
	return response, nil
}
