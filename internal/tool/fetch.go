package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

var fetchSchema = Schema(map[string]any{
	"url":    map[string]any{"type": "string", "description": "The URL to fetch content from"},
	"prompt": map[string]any{"type": "string", "description": "What information you want to extract from the page"},
}, []string{"url", "prompt"})

const fetchDescription = `Fetches content from a specified URL and processes it. Use this tool when you need to retrieve and analyze web content. The URL must be a fully-formed valid URL. HTTP URLs will be automatically upgraded to HTTPS.`

// WebFetchTool fetches content from URLs.
type WebFetchTool struct {
	BaseTool
}

// NewWebFetchTool creates a new WebFetchTool.
func NewWebFetchTool() *WebFetchTool {
	return &WebFetchTool{
		BaseTool: NewBaseTool(
			"WebFetch",
			fetchDescription,
			fetchSchema,
			WithReadOnly(),
			WithConcurrencySafe(),
		),
	}
}

type fetchInput struct {
	URL    string `json:"url"`
	Prompt string `json:"prompt"`
}

func (t *WebFetchTool) Execute(ctx context.Context, input json.RawMessage, execCtx *ExecContext) (*ToolResult, error) {
	var in fetchInput
	if err := json.Unmarshal(input, &in); err != nil {
		return &ToolResult{Success: false, Content: fmt.Sprintf("Invalid input: %v", err)}, nil
	}

	if in.URL == "" {
		return &ToolResult{Success: false, Content: "url is required"}, nil
	}

	// Upgrade HTTP to HTTPS
	url := in.URL
	if strings.HasPrefix(url, "http://") {
		url = "https://" + strings.TrimPrefix(url, "http://")
	}

	client := &http.Client{
		Timeout: 30 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 3 {
				return fmt.Errorf("too many redirects")
			}
			return nil
		},
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return &ToolResult{Success: false, Content: fmt.Sprintf("Invalid URL: %v", err)}, nil
	}
	req.Header.Set("User-Agent", "Ergate/0.1.0")

	resp, err := client.Do(req)
	if err != nil {
		return &ToolResult{Success: false, Content: fmt.Sprintf("Fetch failed: %v", err)}, nil
	}
	defer resp.Body.Close()

	// Limit response size
	body, err := io.ReadAll(io.LimitReader(resp.Body, 512*1024))
	if err != nil {
		return &ToolResult{Success: false, Content: fmt.Sprintf("Read failed: %v", err)}, nil
	}

	content := string(body)
	// Strip HTML tags for cleaner output
	content = stripHTML(content)
	// Truncate
	if len(content) > 10000 {
		content = content[:10000] + fmt.Sprintf("\n...[truncated, total %d chars]", len(content))
	}

	var output strings.Builder
	output.WriteString(fmt.Sprintf("Fetched %s (HTTP %d, %d bytes)\n\n", url, resp.StatusCode, len(body)))
	output.WriteString(content)

	return &ToolResult{
		Success: resp.StatusCode < 400,
		Content: output.String(),
		Metadata: map[string]any{
			"url":        url,
			"status":     resp.StatusCode,
			"size":       len(body),
			"prompt":     in.Prompt,
		},
	}, nil
}

// stripHTML removes common HTML tags for cleaner text extraction.
func stripHTML(s string) string {
	// Remove script and style blocks
	for {
		start := strings.Index(strings.ToLower(s), "<script")
		if start < 0 {
			break
		}
		end := strings.Index(strings.ToLower(s[start:]), "</script>")
		if end < 0 {
			break
		}
		s = s[:start] + s[start+end+9:]
	}
	for {
		start := strings.Index(strings.ToLower(s), "<style")
		if start < 0 {
			break
		}
		end := strings.Index(strings.ToLower(s[start:]), "</style>")
		if end < 0 {
			break
		}
		s = s[:start] + s[start+end+8:]
	}

	// Remove remaining tags
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

	// Clean up whitespace
	result := b.String()
	for strings.Contains(result, "\n\n\n") {
		result = strings.ReplaceAll(result, "\n\n\n", "\n\n")
	}
	return strings.TrimSpace(result)
}
