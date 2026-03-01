package search

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strconv"
)

// Result represents a single search result.
type Result struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Content string `json:"content"`
}

// Client queries a SearXNG instance.
type Client struct {
	baseURL string
	client  *http.Client
}

// NewClient creates a SearXNG search client.
// Uses SEARXNG_URL env var, defaulting to http://127.0.0.1:8080.
func NewClient() *Client {
	base := os.Getenv("SEARXNG_URL")
	if base == "" {
		base = "http://127.0.0.1:8080"
	}
	return &Client{baseURL: base, client: &http.Client{}}
}

type searxngResponse struct {
	Results []struct {
		Title   string `json:"title"`
		URL     string `json:"url"`
		Content string `json:"content"`
	} `json:"results"`
}

// Search queries SearXNG and returns up to maxResults results.
func (c *Client) Search(ctx context.Context, query string, maxResults int) ([]Result, error) {
	if maxResults <= 0 {
		maxResults = 5
	}

	u, err := url.Parse(c.baseURL + "/search")
	if err != nil {
		return nil, fmt.Errorf("parse URL: %w", err)
	}
	q := u.Query()
	q.Set("q", query)
	q.Set("format", "json")
	q.Set("pageno", "1")
	q.Set("number_of_results", strconv.Itoa(maxResults))
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, "GET", u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("searxng request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("searxng error (%d)", resp.StatusCode)
	}

	var sr searxngResponse
	if err := json.NewDecoder(resp.Body).Decode(&sr); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	results := make([]Result, 0, len(sr.Results))
	for _, r := range sr.Results {
		if len(results) >= maxResults {
			break
		}
		results = append(results, Result{
			Title:   r.Title,
			URL:     r.URL,
			Content: r.Content,
		})
	}

	return results, nil
}
