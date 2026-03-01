package weather

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const cacheTTL = 30 * time.Minute

// Client fetches and caches weather data from wttr.in.
type Client struct {
	httpClient *http.Client
	mu         sync.RWMutex
	cache      map[string]*cacheEntry
	cacheDir   string
}

type cacheEntry struct {
	Data      string
	Location  string
	FetchedAt time.Time
}

type diskCache struct {
	Data      string `json:"data"`
	Location  string `json:"location"`
	FetchedAt int64  `json:"fetched_at"`
}

// NewClient creates a weather client with in-memory and disk caching.
func NewClient() *Client {
	cacheDir := os.Getenv("XDG_DATA_HOME")
	if cacheDir == "" {
		home, _ := os.UserHomeDir()
		cacheDir = filepath.Join(home, ".local", "share")
	}
	cacheDir = filepath.Join(cacheDir, "localfreshllm")

	c := &Client{
		httpClient: &http.Client{Timeout: 60 * time.Second},
		cache:      make(map[string]*cacheEntry),
		cacheDir:   cacheDir,
	}
	c.loadDiskCache()
	return c
}

func (c *Client) cacheFile() string {
	return filepath.Join(c.cacheDir, "weather-cache.json")
}

func (c *Client) loadDiskCache() {
	data, err := os.ReadFile(c.cacheFile())
	if err != nil {
		return
	}
	var entries map[string]*diskCache
	if json.Unmarshal(data, &entries) != nil {
		return
	}
	for key, dc := range entries {
		c.cache[key] = &cacheEntry{
			Data:      dc.Data,
			Location:  dc.Location,
			FetchedAt: time.Unix(dc.FetchedAt, 0),
		}
	}
}

func (c *Client) saveDiskCache() {
	entries := make(map[string]*diskCache, len(c.cache))
	for key, ce := range c.cache {
		entries[key] = &diskCache{
			Data:      ce.Data,
			Location:  ce.Location,
			FetchedAt: ce.FetchedAt.Unix(),
		}
	}
	data, err := json.Marshal(entries)
	if err != nil {
		return
	}
	os.MkdirAll(c.cacheDir, 0700)
	os.WriteFile(c.cacheFile(), data, 0600)
}

func cacheKey(location string) string {
	return strings.ToLower(strings.TrimSpace(location))
}

// Get returns formatted weather data for a location, using cache if fresh.
func (c *Client) Get(ctx context.Context, location string) (string, error) {
	key := cacheKey(location)

	// Check cache.
	c.mu.RLock()
	if entry, ok := c.cache[key]; ok && time.Since(entry.FetchedAt) < cacheTTL {
		c.mu.RUnlock()
		return entry.Data, nil
	}
	c.mu.RUnlock()

	// Fetch live.
	formatted, err := c.fetchAndFormat(ctx, location)
	if err != nil {
		return "", err
	}

	// Update cache.
	c.mu.Lock()
	c.cache[key] = &cacheEntry{Data: formatted, Location: location, FetchedAt: time.Now()}
	c.saveDiskCache()
	c.mu.Unlock()

	return formatted, nil
}

// Prefetch fetches weather for a location in the background, warming the cache.
func (c *Client) Prefetch(location string) {
	if location == "" {
		return
	}

	key := cacheKey(location)
	c.mu.RLock()
	if entry, ok := c.cache[key]; ok && time.Since(entry.FetchedAt) < cacheTTL {
		c.mu.RUnlock()
		return // already fresh
	}
	c.mu.RUnlock()

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		c.Get(ctx, location) // result stored in cache
	}()
}

func (c *Client) fetchAndFormat(ctx context.Context, location string) (string, error) {
	url := fmt.Sprintf("https://wttr.in/%s?format=j1", strings.ReplaceAll(location, " ", "+"))
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return "", fmt.Errorf("request error: %w", err)
	}
	req.Header.Set("User-Agent", "localfreshsearch/1.0")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("weather fetch error: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read error: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("weather API error (%d): %s", resp.StatusCode, string(body))
	}

	return formatWeather(body)
}

func formatWeather(body []byte) (string, error) {
	var data struct {
		CurrentCondition []struct {
			TempF        string                 `json:"temp_F"`
			TempC        string                 `json:"temp_C"`
			FeelsLikeF   string                 `json:"FeelsLikeF"`
			FeelsLikeC   string                 `json:"FeelsLikeC"`
			Humidity     string                 `json:"humidity"`
			WeatherDesc  []struct{ Value string } `json:"weatherDesc"`
			WindspeedMph string                 `json:"windspeedMiles"`
			WindDir      string                 `json:"winddir16Point"`
			PrecipMM     string                 `json:"precipMM"`
			Visibility   string                 `json:"visibilityMiles"`
			UVIndex      string                 `json:"uvIndex"`
		} `json:"current_condition"`
		NearestArea []struct {
			AreaName []struct{ Value string } `json:"areaName"`
			Region   []struct{ Value string } `json:"region"`
			Country  []struct{ Value string } `json:"country"`
		} `json:"nearest_area"`
		Weather []struct {
			Date        string `json:"date"`
			MaxTempF    string `json:"maxtempF"`
			MinTempF    string `json:"mintempF"`
			MaxTempC    string `json:"maxtempC"`
			MinTempC    string `json:"mintempC"`
			TotalSnowCM string `json:"totalSnow_cm"`
		} `json:"weather"`
	}

	if err := json.Unmarshal(body, &data); err != nil {
		return "", fmt.Errorf("parse error: %w", err)
	}

	var sb strings.Builder

	if len(data.NearestArea) > 0 {
		a := data.NearestArea[0]
		area, region, country := "", "", ""
		if len(a.AreaName) > 0 {
			area = a.AreaName[0].Value
		}
		if len(a.Region) > 0 {
			region = a.Region[0].Value
		}
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

	return sb.String(), nil
}
