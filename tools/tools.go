package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/dev-ben-c/localfreshsearch/scrape"
	"github.com/dev-ben-c/localfreshsearch/search"
	"github.com/dev-ben-c/localfreshsearch/weather"
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

// toolDef is a provider-agnostic tool definition.
type toolDef struct {
	Name        string
	Description string
	Properties  map[string]map[string]string
	Required    []string
}

var allTools = []toolDef{
	{
		Name:        "web_search",
		Description: "Search the web for current information. Returns titles, URLs, and brief snippets. If the snippets don't contain the specific data you need (e.g. prices, stats, detailed answers), follow up with read_page on the most relevant URL to get the full content.",
		Properties:  map[string]map[string]string{"query": {"type": "string", "description": "The search query"}},
		Required:    []string{"query"},
	},
	{
		Name:        "read_page",
		Description: "Read the text content of a web page. Use this to get detailed information from a specific URL found via web_search or provided by the user.",
		Properties:  map[string]map[string]string{"url": {"type": "string", "description": "The URL of the web page to read"}},
		Required:    []string{"url"},
	},
	{
		Name:        "current_datetime",
		Description: "Get the current date and time. Use this when the user asks what time or date it is, or when you need to know the current date/time for any reason.",
		Properties:  map[string]map[string]string{},
		Required:    nil,
	},
	{
		Name:        "get_weather",
		Description: "Get current weather conditions and forecast for a location. Use this when the user asks about weather, temperature, or forecast. If no location is provided, the user's default location is used.",
		Properties:  map[string]map[string]string{"location": {"type": "string", "description": "City name, zip code, or location (e.g. 'New York', '90210', 'London'). Optional if user has a default location set."}},
		Required:    nil,
	},
}

func buildProps(props map[string]map[string]string) map[string]any {
	out := make(map[string]any, len(props))
	for name, p := range props {
		out[name] = map[string]any{"type": p["type"], "description": p["description"]}
	}
	return out
}

// OllamaToolDefs returns tool definitions in Ollama's format.
func OllamaToolDefs() []any {
	out := make([]any, 0, len(allTools))
	for _, t := range allTools {
		params := map[string]any{
			"type":       "object",
			"properties": buildProps(t.Properties),
		}
		if len(t.Required) > 0 {
			params["required"] = t.Required
		}
		out = append(out, map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        t.Name,
				"description": t.Description,
				"parameters":  params,
			},
		})
	}
	return out
}

// AnthropicToolDefs returns tool definitions in Anthropic's format.
func AnthropicToolDefs() []any {
	out := make([]any, 0, len(allTools))
	for _, t := range allTools {
		schema := map[string]any{
			"type":       "object",
			"properties": buildProps(t.Properties),
		}
		if len(t.Required) > 0 {
			schema["required"] = t.Required
		}
		out = append(out, map[string]any{
			"name":         t.Name,
			"description":  t.Description,
			"input_schema": schema,
		})
	}
	return out
}

// Executor runs tool calls and returns results.
type Executor struct {
	searchClient    *search.Client
	scraper         *scrape.Scraper
	weatherClient   *weather.Client
	DefaultLocation string
}

// NewExecutor creates a tool executor. It prefetches weather for defaultLocation
// in the background if set.
func NewExecutor() *Executor {
	return &Executor{
		searchClient:  search.NewClient(),
		weatherClient: weather.NewClient(),
	}
}

// Prefetch starts background fetches to warm caches.
func (e *Executor) Prefetch() {
	e.weatherClient.Prefetch(e.DefaultLocation)
}

func (e *Executor) ensureScraper() {
	if e.scraper == nil {
		e.scraper = scrape.NewScraper()
	}
}

// Execute runs a single tool call and returns the result.
// All outputs are sanitized and wrapped to prevent prompt injection.
func (e *Executor) Execute(ctx context.Context, call ToolCall) ToolResult {
	var result ToolResult
	switch call.Name {
	case "web_search":
		result = e.execSearch(ctx, call)
	case "read_page":
		result = e.execReadPage(ctx, call)
	case "current_datetime":
		result = e.execCurrentDatetime(call)
	case "get_weather":
		result = e.execGetWeather(ctx, call)
	default:
		return ToolResult{
			ID:      call.ID,
			Name:    call.Name,
			Content: fmt.Sprintf("unknown tool: %s", call.Name),
			IsError: true,
		}
	}

	// Sanitize and frame all tool output.
	result.Content = sanitize(result.Content)
	if !result.IsError {
		result.Content = wrapToolOutput(result.Name, result.Content)
	}
	return result
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
		title := truncate(sanitize(r.Title), 200)
		content := truncate(sanitize(r.Content), 500)
		fmt.Fprintf(&sb, "%d. %s\n   %s\n   %s\n\n", i+1, title, r.URL, content)
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

	// Sanitize scraped content before passing to LLM.
	result.Title = truncate(sanitize(result.Title), 200)
	result.Content = sanitize(result.Content)

	out, _ := json.Marshal(result)
	return ToolResult{ID: call.ID, Name: call.Name, Content: string(out)}
}

func (e *Executor) execCurrentDatetime(call ToolCall) ToolResult {
	now := time.Now()
	zone, offset := now.Zone()
	content := fmt.Sprintf("Current date and time: %s\nTimezone: %s (UTC%+d)\nUnix timestamp: %d",
		now.Format("Monday, January 2, 2006 3:04:05 PM MST"),
		zone, offset/3600,
		now.Unix(),
	)
	return ToolResult{ID: call.ID, Name: call.Name, Content: content}
}

func (e *Executor) execGetWeather(ctx context.Context, call ToolCall) ToolResult {
	location, _ := call.Args["location"].(string)
	if location == "" {
		location = e.DefaultLocation
	}
	if location == "" {
		return ToolResult{ID: call.ID, Name: call.Name, Content: "no location provided and no default location set. Ask the user where they are, or have them run /location to set a default.", IsError: true}
	}

	data, err := e.weatherClient.Get(ctx, location)
	if err != nil {
		return ToolResult{ID: call.ID, Name: call.Name, Content: fmt.Sprintf("weather error: %v", err), IsError: true}
	}

	return ToolResult{ID: call.ID, Name: call.Name, Content: data}
}

// Close releases resources held by the executor.
func (e *Executor) Close() {
	if e.scraper != nil {
		e.scraper.Close()
	}
}
