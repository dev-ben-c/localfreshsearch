package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/dev-ben-c/localfreshsearch/scrape"
	"github.com/dev-ben-c/localfreshsearch/search"
)

// ToolCall represents an LLM's request to invoke a tool.
type ToolCall struct {
	ID   string
	Name string
	Args map[string]any
}

// ToolResult is the output returned to the LLM after executing a tool.
type ToolResult struct {
	ID      string
	Name    string
	Content string
	IsError bool
}

// OllamaToolDefs returns tool definitions in Ollama's format.
func OllamaToolDefs() []any {
	return []any{
		map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        "web_search",
				"description": "Search the web for current information using a search engine. Use this when you need up-to-date information, facts, or answers that may not be in your training data.",
				"parameters": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"query": map[string]any{
							"type":        "string",
							"description": "The search query",
						},
					},
					"required": []string{"query"},
				},
			},
		},
		map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        "read_page",
				"description": "Read the text content of a web page. Use this to get detailed information from a specific URL found via web_search or provided by the user.",
				"parameters": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"url": map[string]any{
							"type":        "string",
							"description": "The URL of the web page to read",
						},
					},
					"required": []string{"url"},
				},
			},
		},
	}
}

// AnthropicToolDefs returns tool definitions in Anthropic's format.
func AnthropicToolDefs() []any {
	return []any{
		map[string]any{
			"name":        "web_search",
			"description": "Search the web for current information using a search engine. Use this when you need up-to-date information, facts, or answers that may not be in your training data.",
			"input_schema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"query": map[string]any{
						"type":        "string",
						"description": "The search query",
					},
				},
				"required": []string{"query"},
			},
		},
		map[string]any{
			"name":        "read_page",
			"description": "Read the text content of a web page. Use this to get detailed information from a specific URL found via web_search or provided by the user.",
			"input_schema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"url": map[string]any{
						"type":        "string",
						"description": "The URL of the web page to read",
					},
				},
				"required": []string{"url"},
			},
		},
	}
}

// Executor runs tool calls and returns results.
type Executor struct {
	searchClient *search.Client
	scraper      *scrape.Scraper
}

// NewExecutor creates a tool executor.
func NewExecutor() *Executor {
	return &Executor{
		searchClient: search.NewClient(),
	}
}

func (e *Executor) ensureScraper() {
	if e.scraper == nil {
		e.scraper = scrape.NewScraper()
	}
}

// Execute runs a single tool call and returns the result.
func (e *Executor) Execute(ctx context.Context, call ToolCall) ToolResult {
	switch call.Name {
	case "web_search":
		return e.execSearch(ctx, call)
	case "read_page":
		return e.execReadPage(ctx, call)
	default:
		return ToolResult{
			ID:      call.ID,
			Name:    call.Name,
			Content: fmt.Sprintf("unknown tool: %s", call.Name),
			IsError: true,
		}
	}
}

func (e *Executor) execSearch(ctx context.Context, call ToolCall) ToolResult {
	query, _ := call.Args["query"].(string)
	if query == "" {
		return ToolResult{ID: call.ID, Name: call.Name, Content: "missing required parameter: query", IsError: true}
	}

	results, err := e.searchClient.Search(ctx, query, 5)
	if err != nil {
		return ToolResult{ID: call.ID, Name: call.Name, Content: fmt.Sprintf("search error: %v", err), IsError: true}
	}

	var sb strings.Builder
	for i, r := range results {
		fmt.Fprintf(&sb, "%d. %s\n   %s\n   %s\n\n", i+1, r.Title, r.URL, r.Content)
	}

	return ToolResult{ID: call.ID, Name: call.Name, Content: sb.String()}
}

func (e *Executor) execReadPage(ctx context.Context, call ToolCall) ToolResult {
	rawURL, _ := call.Args["url"].(string)
	if rawURL == "" {
		return ToolResult{ID: call.ID, Name: call.Name, Content: "missing required parameter: url", IsError: true}
	}

	e.ensureScraper()

	result, err := e.scraper.Scrape(ctx, rawURL)
	if err != nil {
		return ToolResult{ID: call.ID, Name: call.Name, Content: fmt.Sprintf("scrape error: %v", err), IsError: true}
	}

	out, _ := json.Marshal(result)
	return ToolResult{ID: call.ID, Name: call.Name, Content: string(out)}
}

// Close releases resources held by the executor.
func (e *Executor) Close() {
	if e.scraper != nil {
		e.scraper.Close()
	}
}
