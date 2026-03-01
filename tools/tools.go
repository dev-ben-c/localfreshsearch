package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

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

// toolDef is a provider-agnostic tool definition.
type toolDef struct {
	Name        string
	Description string
	Properties  map[string]map[string]string // param name → {type, description}
	Required    []string
}

var allTools = []toolDef{
	{
		Name:        "web_search",
		Description: "Search the web for current information using a search engine. Use this when you need up-to-date information, facts, or answers that may not be in your training data.",
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
	httpClient      *http.Client
	DefaultLocation string // used by get_weather when no location arg is provided
}

// NewExecutor creates a tool executor.
func NewExecutor() *Executor {
	return &Executor{
		searchClient: search.NewClient(),
		httpClient:   &http.Client{Timeout: 30 * time.Second},
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
	case "current_datetime":
		return e.execCurrentDatetime(call)
	case "get_weather":
		return e.execGetWeather(ctx, call)
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

	url := fmt.Sprintf("https://wttr.in/%s?format=j1", strings.ReplaceAll(location, " ", "+"))
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return ToolResult{ID: call.ID, Name: call.Name, Content: fmt.Sprintf("request error: %v", err), IsError: true}
	}
	req.Header.Set("User-Agent", "localfreshsearch/1.0")

	resp, err := e.httpClient.Do(req)
	if err != nil {
		return ToolResult{ID: call.ID, Name: call.Name, Content: fmt.Sprintf("weather fetch error: %v", err), IsError: true}
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return ToolResult{ID: call.ID, Name: call.Name, Content: fmt.Sprintf("read error: %v", err), IsError: true}
	}

	if resp.StatusCode != http.StatusOK {
		return ToolResult{ID: call.ID, Name: call.Name, Content: fmt.Sprintf("weather API error (%d): %s", resp.StatusCode, string(body)), IsError: true}
	}

	// Parse and format the relevant fields.
	var data struct {
		CurrentCondition []struct {
			TempF        string `json:"temp_F"`
			TempC        string `json:"temp_C"`
			FeelsLikeF   string `json:"FeelsLikeF"`
			FeelsLikeC   string `json:"FeelsLikeC"`
			Humidity     string `json:"humidity"`
			WeatherDesc  []struct{ Value string } `json:"weatherDesc"`
			WindspeedMph string `json:"windspeedMiles"`
			WindDir      string `json:"winddir16Point"`
			PrecipMM     string `json:"precipMM"`
			Visibility   string `json:"visibilityMiles"`
			UVIndex      string `json:"uvIndex"`
		} `json:"current_condition"`
		NearestArea []struct {
			AreaName []struct{ Value string } `json:"areaName"`
			Region   []struct{ Value string } `json:"region"`
			Country  []struct{ Value string } `json:"country"`
		} `json:"nearest_area"`
		Weather []struct {
			Date       string `json:"date"`
			MaxTempF   string `json:"maxtempF"`
			MinTempF   string `json:"mintempF"`
			MaxTempC   string `json:"maxtempC"`
			MinTempC   string `json:"mintempC"`
			TotalSnowCM string `json:"totalSnow_cm"`
			Hourly     []struct {
				Time        string `json:"time"`
				TempF       string `json:"tempF"`
				WeatherDesc []struct{ Value string } `json:"weatherDesc"`
				ChanceOfRain string `json:"chanceofrain"`
			} `json:"hourly"`
		} `json:"weather"`
	}

	if err := json.Unmarshal(body, &data); err != nil {
		return ToolResult{ID: call.ID, Name: call.Name, Content: fmt.Sprintf("parse error: %v", err), IsError: true}
	}

	var sb strings.Builder
	if len(data.NearestArea) > 0 {
		a := data.NearestArea[0]
		area := ""
		if len(a.AreaName) > 0 {
			area = a.AreaName[0].Value
		}
		region := ""
		if len(a.Region) > 0 {
			region = a.Region[0].Value
		}
		country := ""
		if len(a.Country) > 0 {
			country = a.Country[0].Value
		}
		fmt.Fprintf(&sb, "Location: %s, %s, %s\n", area, region, country)
	}

	if len(data.CurrentCondition) > 0 {
		c := data.CurrentCondition[0]
		desc := ""
		if len(c.WeatherDesc) > 0 {
			desc = c.WeatherDesc[0].Value
		}
		fmt.Fprintf(&sb, "\nCurrent Conditions: %s\n", desc)
		fmt.Fprintf(&sb, "Temperature: %s°F / %s°C (feels like %s°F / %s°C)\n", c.TempF, c.TempC, c.FeelsLikeF, c.FeelsLikeC)
		fmt.Fprintf(&sb, "Humidity: %s%%\n", c.Humidity)
		fmt.Fprintf(&sb, "Wind: %s mph %s\n", c.WindspeedMph, c.WindDir)
		fmt.Fprintf(&sb, "Visibility: %s miles\n", c.Visibility)
		fmt.Fprintf(&sb, "UV Index: %s\n", c.UVIndex)
		if c.PrecipMM != "0.0" {
			fmt.Fprintf(&sb, "Precipitation: %s mm\n", c.PrecipMM)
		}
	}

	if len(data.Weather) > 0 {
		sb.WriteString("\nForecast:\n")
		for _, day := range data.Weather {
			fmt.Fprintf(&sb, "  %s: %s°F–%s°F / %s°C–%s°C", day.Date, day.MinTempF, day.MaxTempF, day.MinTempC, day.MaxTempC)
			if day.TotalSnowCM != "0.0" {
				fmt.Fprintf(&sb, " (snow: %s cm)", day.TotalSnowCM)
			}
			sb.WriteString("\n")
		}
	}

	return ToolResult{ID: call.ID, Name: call.Name, Content: sb.String()}
}

// Close releases resources held by the executor.
func (e *Executor) Close() {
	if e.scraper != nil {
		e.scraper.Close()
	}
}
