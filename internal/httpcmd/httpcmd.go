// Package httpcmd provides HTTP client commands for API inspection and pipelines.
// Satisfies Demo 4: `nex> http GET http://localhost:8080/health`
package httpcmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"nexshell/internal/pipeline"
	"nexshell/internal/registry"
)

// HTTPCmd sends HTTP requests and emits structured responses.
type HTTPCmd struct {
	client *http.Client
}

func NewHTTPCmd() *HTTPCmd {
	return NewHTTPCmdWithClient(&http.Client{
		Timeout: 15 * time.Second,
	})
}

func NewHTTPCmdWithClient(client *http.Client) *HTTPCmd {
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	return &HTTPCmd{client: client}
}

func (c *HTTPCmd) Name() string        { return "http" }
func (c *HTTPCmd) Description() string { return "Make HTTP requests: http [GET|POST|PUT|DELETE] <url> [-H header] [-d body]" }
func (c *HTTPCmd) PipelineSafe() bool  { return true }

func (c *HTTPCmd) Run(ctx registry.Context, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("http: missing url operand (e.g. http GET https://api.example.com)")
	}

	method := "GET"
	rawURL := ""
	headers := make(http.Header)
	var bodyData []byte

	// Parse arguments
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch arg {
		case "-H", "--header":
			if i+1 < len(args) {
				i++
				parts := strings.SplitN(args[i], ":", 2)
				if len(parts) == 2 {
					headers.Add(strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1]))
				}
			}
		case "-d", "--data":
			if i+1 < len(args) {
				i++
				bodyData = []byte(args[i])
			}
		case "GET", "POST", "PUT", "DELETE", "PATCH", "HEAD", "OPTIONS":
			method = arg
		default:
			if strings.HasPrefix(arg, "-") {
				return fmt.Errorf("http: unknown option %q", arg)
			}
			if rawURL == "" {
				rawURL = arg
			} else if len(bodyData) == 0 {
				bodyData = []byte(arg)
			}
		}
	}

	if rawURL == "" {
		return fmt.Errorf("http: missing url operand")
	}

	if !strings.HasPrefix(rawURL, "http://") && !strings.HasPrefix(rawURL, "https://") {
		rawURL = "http://" + rawURL
	}

	var bodyReader io.Reader
	if len(bodyData) > 0 {
		bodyReader = bytes.NewReader(bodyData)
		if headers.Get("Content-Type") == "" {
			if bodyData[0] == '{' || bodyData[0] == '[' {
				headers.Set("Content-Type", "application/json")
			}
		}
	}

	req, err := http.NewRequestWithContext(ctx.Cancellation(), method, rawURL, bodyReader)
	if err != nil {
		return fmt.Errorf("http: %w", err)
	}
	req.Header = headers

	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("http: request failed: %w", err)
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("http: read response body: %w", err)
	}

	trimmedBody := bytes.TrimSpace(respBytes)

	// If piped into another command or if JSON, serialize appropriately
	if len(trimmedBody) > 0 && (trimmedBody[0] == '{' || trimmedBody[0] == '[') {
		// Try parsing as JSON object or array
		if trimmedBody[0] == '[' {
			var ds pipeline.Dataset
			if err := json.Unmarshal(trimmedBody, &ds); err == nil {
				return pipeline.WriteOutput(ctx.Output(), ds, false)
			}
		} else {
			var rec pipeline.Record
			if err := json.Unmarshal(trimmedBody, &rec); err == nil {
				// Single object: emit as 1-element dataset
				return pipeline.WriteOutput(ctx.Output(), pipeline.Dataset{rec}, false)
			}
		}
	}

	// Output plain text or status
	fmt.Fprintf(ctx.Output(), "HTTP %d %s\n", resp.StatusCode, resp.Status)
	if len(respBytes) > 0 {
		_, err = ctx.Output().Write(respBytes)
		fmt.Fprintln(ctx.Output())
		return err
	}
	return nil
}
