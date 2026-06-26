package tool

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
	"testing"
)

func TestClassifyNetworkError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		contains string
		isNetUnavailable bool
	}{
		{
			name: "DNS not found",
			err: &url.Error{Op: "Get", URL: "https://example.com", Err: &net.OpError{
				Op: "dial", Net: "tcp",
				Err: &net.DNSError{Err: "no such host", Name: "example.com"},
			}},
			contains: "DNS resolution failed",
			isNetUnavailable: true,
		},
		{
			name: "TCP timeout",
			err: &url.Error{Op: "Get", URL: "https://example.com", Err: &net.OpError{
				Op: "dial", Net: "tcp", Err: &timeoutError{},
			}},
			contains: "timed out",
			isNetUnavailable: true,
		},
		{
			name: "connection refused",
			err: &url.Error{Op: "Get", URL: "https://example.com", Err: &net.OpError{
				Op: "dial", Net: "tcp", Err: errors.New("connection refused"),
			}},
			contains: "connection refused",
			isNetUnavailable: true,
		},
		{
			name: "TLS error",
			err: errors.New("tls: failed to verify certificate: x509: certificate signed by unknown authority"),
			contains: "TLS handshake failed",
			isNetUnavailable: true,
		},
		{
			name: "plain HTTP error not classified",
			err: fmt.Errorf("search returned HTTP 502"),
			contains: "Network error",
			isNetUnavailable: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := classifyNetworkError(tt.err)
			if !strings.Contains(result, tt.contains) {
				t.Errorf("classifyNetworkError(%v) = %q, want to contain %q", tt.err, result, tt.contains)
			}
			hasUnavailable := strings.Contains(result, "Network unavailable")
			if hasUnavailable != tt.isNetUnavailable {
				t.Errorf("isNetUnavailable = %v, want %v (result: %q)", hasUnavailable, tt.isNetUnavailable, result)
			}
		})
	}
}

type timeoutError struct{}
func (e *timeoutError) Error() string { return "i/o timeout" }
func (e *timeoutError) Timeout() bool { return true }
