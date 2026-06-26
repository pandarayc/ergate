package tool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

var fetchSchema = Schema(map[string]any{
	"url":    map[string]any{"type": "string", "description": "The URL to fetch content from"},
	"prompt": map[string]any{"type": "string", "description": "What information you want to extract from the page"},
}, []string{"url", "prompt"})

const fetchDescription = `Fetch a web page. SECONDARY tool — prefer local sources first. For downloading files, use Bash with curl/wget (those respect HTTP_PROXY). HTTP upgraded to HTTPS. Use ONLY when the task explicitly requires external information not available locally.`

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
		return &ToolResult{Success: false, Content: classifyNetworkError(err)}, nil
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

// classifyNetworkError distinguishes between "network is down" (model should
// stop trying) and other errors. The message guides the model toward using
// local tools instead of retrying failed HTTP requests.
func classifyNetworkError(err error) string {
	// Unwrap url.Error — the standard wrapper for HTTP transport errors.
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		err = urlErr.Err
	}

	var opErr *net.OpError
	if errors.As(err, &opErr) {
		if opErr.Timeout() {
			return "Network unavailable: connection timed out. Do not retry — use local tools (Read, Bash, Glob, Grep) instead."
		}

		// DNS resolution failures.
		var dnsErr *net.DNSError
		if errors.As(opErr.Err, &dnsErr) {
			return fmt.Sprintf("Network unavailable: DNS resolution failed for %q. Do not retry — use local tools instead.", dnsErr.Name)
		}
		if errors.As(err, &dnsErr) {
			return fmt.Sprintf("Network unavailable: DNS resolution failed for %q. Do not retry — use local tools instead.", dnsErr.Name)
		}

		msg := opErr.Err.Error()
		switch {
		case strings.Contains(msg, "connection refused"):
			return "Network unavailable: connection refused. Do not retry — use local tools instead."
		case strings.Contains(msg, "no route to host"):
			return "Network unavailable: no route to host. Do not retry — use local tools instead."
		case strings.Contains(msg, "network is unreachable"):
			return "Network unavailable: network is unreachable. Do not retry — use local tools instead."
		}
	}

	// TLS / certificate errors.
	msg := err.Error()
	if strings.Contains(msg, "tls:") || strings.Contains(msg, "x509:") {
		return "Network unavailable: TLS handshake failed. Do not retry — use local tools instead."
	}
	if strings.Contains(msg, "no such host") {
		return "Network unavailable: host not found. Do not retry — use local tools instead."
	}

	return fmt.Sprintf("Network error: %v", err)
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
