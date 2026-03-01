package scrape

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/playwright-community/playwright-go"
)

const maxChars = 8000

// Result holds the scraped page content.
type Result struct {
	URL       string `json:"url"`
	Title     string `json:"title"`
	Content   string `json:"content"`
	Truncated bool   `json:"truncated"`
}

// Scraper uses a persistent Playwright browser to scrape web pages.
type Scraper struct {
	mu      sync.Mutex
	pw      *playwright.Playwright
	browser playwright.Browser
}

// NewScraper creates a Scraper. The browser is launched lazily on first use.
func NewScraper() *Scraper {
	return &Scraper{}
}

func (s *Scraper) init() error {
	if s.browser != nil {
		return nil
	}

	pw, err := playwright.Run()
	if err != nil {
		return fmt.Errorf("start playwright: %w", err)
	}
	s.pw = pw

	browser, err := pw.Chromium.Launch(playwright.BrowserTypeLaunchOptions{
		Channel:  playwright.String("chrome"),
		Headless: playwright.Bool(true),
	})
	if err != nil {
		s.pw.Stop()
		s.pw = nil
		return fmt.Errorf("launch browser: %w", err)
	}
	s.browser = browser
	return nil
}

// Scrape fetches a URL and returns its text content.
func (s *Scraper) Scrape(ctx context.Context, url string) (*Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.init(); err != nil {
		return nil, err
	}

	page, err := s.browser.NewPage()
	if err != nil {
		return nil, fmt.Errorf("new page: %w", err)
	}
	defer page.Close()

	if _, err := page.Goto(url, playwright.PageGotoOptions{
		WaitUntil: playwright.WaitUntilStateDomcontentloaded,
	}); err != nil {
		return nil, fmt.Errorf("navigate to %s: %w", url, err)
	}

	// Remove noisy elements before extracting text.
	page.Evaluate(`() => {
		for (const sel of ['script','style','nav','footer','header','aside','.sidebar','.menu','.ad','[role="navigation"]','[role="banner"]']) {
			document.querySelectorAll(sel).forEach(el => el.remove());
		}
	}`)

	title, _ := page.Title()

	body := page.Locator("body")
	text, err := body.InnerText()
	if err != nil {
		return nil, fmt.Errorf("extract text: %w", err)
	}

	// Collapse whitespace.
	lines := strings.Split(text, "\n")
	var cleaned []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			cleaned = append(cleaned, line)
		}
	}
	content := strings.Join(cleaned, "\n")

	truncated := false
	if len(content) > maxChars {
		content = content[:maxChars]
		truncated = true
	}

	return &Result{
		URL:       url,
		Title:     title,
		Content:   content,
		Truncated: truncated,
	}, nil
}

// Close shuts down the browser and Playwright.
func (s *Scraper) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.browser != nil {
		s.browser.Close()
		s.browser = nil
	}
	if s.pw != nil {
		s.pw.Stop()
		s.pw = nil
	}
}
