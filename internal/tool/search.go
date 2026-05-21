package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

var searchSchema = Schema(map[string]any{
	"query":           map[string]any{"type": "string", "description": "The search query to use"},
	"allowed_domains": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Only include search results from these domains"},
	"blocked_domains": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Never include search results from these domains"},
}, []string{"query"})

const searchDescription = `Search the web for information. Returns search result information including titles, snippets, and links. Use this tool for accessing information beyond your knowledge cutoff. Results include links as markdown hyperlinks.`

// WebSearchTool performs web searches.
type WebSearchTool struct {
	BaseTool
}

// NewWebSearchTool creates a new WebSearchTool.
func NewWebSearchTool() *WebSearchTool {
	return &WebSearchTool{
		BaseTool: NewBaseTool(
			"WebSearch",
			searchDescription,
			searchSchema,
			WithReadOnly(),
			WithConcurrencySafe(),
		),
	}
}

type searchInput struct {
	Query          string   `json:"query"`
	AllowedDomains []string `json:"allowed_domains,omitempty"`
	BlockedDomains []string `json:"blocked_domains,omitempty"`
}

func (t *WebSearchTool) Execute(ctx context.Context, input json.RawMessage, execCtx *ExecContext) (*ToolResult, error) {
	var in searchInput
	if err := json.Unmarshal(input, &in); err != nil {
		return &ToolResult{Success: false, Content: fmt.Sprintf("Invalid input: %v", err)}, nil
	}

	if in.Query == "" {
		return &ToolResult{Success: false, Content: "query is required"}, nil
	}

	// Use DuckDuckGo's HTML search (no API key needed)
	results, err := duckDuckGoSearch(ctx, in.Query)
	if err != nil {
		return &ToolResult{Success: false, Content: fmt.Sprintf("Search failed: %v", err)}, nil
	}

	// Filter by allowed/blocked domains
	filtered := make([]searchResult, 0)
	for _, r := range results {
		if len(in.BlockedDomains) > 0 && matchesDomain(r.URL, in.BlockedDomains) {
			continue
		}
		if len(in.AllowedDomains) > 0 && !matchesDomain(r.URL, in.AllowedDomains) {
			continue
		}
		filtered = append(filtered, r)
	}

	var output strings.Builder
	output.WriteString(fmt.Sprintf("Search results for: %s\n\n", in.Query))
	for i, r := range filtered {
		if i >= 10 {
			break
		}
		output.WriteString(fmt.Sprintf("%d. **%s**\n", i+1, r.Title))
		output.WriteString(fmt.Sprintf("   %s\n", r.URL))
		output.WriteString(fmt.Sprintf("   %s\n\n", truncate(r.Snippet, 200)))
	}

	if len(filtered) == 0 {
		output.WriteString("No results found.\n")
	}

	return &ToolResult{
		Success: true,
		Content: output.String(),
		Metadata: map[string]any{
			"query":        in.Query,
			"total_results": len(filtered),
		},
	}, nil
}

type searchResult struct {
	Title   string
	URL     string
	Snippet string
}

func duckDuckGoSearch(ctx context.Context, query string) ([]searchResult, error) {
	searchURL := fmt.Sprintf("https://html.duckduckgo.com/html/?q=%s", url.QueryEscape(query))

	client := &http.Client{Timeout: 15 * time.Second}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, searchURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Ergate/0.1.0")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("search returned HTTP %d", resp.StatusCode)
	}

	// Simple HTML result extraction
	body, err := ioReadAll(resp.Body, 256*1024)
	if err != nil {
		return nil, err
	}

	return extractDuckDuckGoResults(string(body)), nil
}

func extractDuckDuckGoResults(html string) []searchResult {
	var results []searchResult

	// Extract result blocks using simple string parsing
	// DuckDuckGo HTML results have class="result" containers
	lines := strings.Split(html, "\n")
	var current searchResult
	inResult := false
	inSnippet := false

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		if strings.Contains(trimmed, `class="result__title"`) || strings.Contains(trimmed, `class="result__a"`) {
			inResult = true
			current = searchResult{}
			// Extract link
			if start := strings.Index(trimmed, `href="`); start >= 0 {
				rest := trimmed[start+6:]
				if end := strings.Index(rest, `"`); end >= 0 {
					current.URL = cleanDuckURL(rest[:end])
				}
			}
		}

		if inResult && strings.Contains(trimmed, `class="result__snippet"`) {
			inSnippet = true
			continue
		}

		if inResult && inSnippet {
			// Collect text content
			text := stripTags(trimmed)
			if text != "" {
				if current.Snippet != "" {
					current.Snippet += " "
				}
				current.Snippet += text
			}
			if strings.Contains(trimmed, "</") {
				inSnippet = false
				if current.Title == "" {
					current.Title = current.Snippet
				}
				results = append(results, current)
				inResult = false
			}
		}

		if inResult && !inSnippet {
			// Collect title text from link
			if strings.Contains(trimmed, "</a>") || strings.Contains(trimmed, `class="result__a"`) {
				text := stripTags(trimmed)
				if text != "" {
					current.Title = text
				}
			}
		}
	}

	return results
}

func cleanDuckURL(raw string) string {
	// DuckDuckGo wraps URLs in redirect
	if strings.Contains(raw, "uddg=") {
		if start := strings.Index(raw, "uddg="); start >= 0 {
			rest := raw[start+5:]
			if end := strings.Index(rest, "&"); end >= 0 {
				rest = rest[:end]
			}
			decoded, err := url.QueryUnescape(rest)
			if err == nil {
				return decoded
			}
		}
	}
	return raw
}

func stripTags(s string) string {
	var b strings.Builder
	inTag := false
	for _, c := range s {
		if c == '<' {
			inTag = true
		} else if c == '>' {
			inTag = false
		} else if !inTag {
			b.WriteRune(c)
		}
	}
	return strings.TrimSpace(b.String())
}

func matchesDomain(urlStr string, domains []string) bool {
	for _, d := range domains {
		if strings.Contains(urlStr, d) {
			return true
		}
	}
	return false
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

func ioReadAll(r io.Reader, limit int64) ([]byte, error) {
	return io.ReadAll(io.LimitReader(r, limit))
}
