package httpcmd_test

import (
	"bytes"
	"io"
	"net/http"
	"strings"
	"testing"

	"nexshell/internal/httpcmd"
	"nexshell/internal/registry"
)

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestHTTPCmd_GET_JSON(t *testing.T) {
	fakeClient := &http.Client{
		Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
			body := `{"status": "ok", "service": "auth", "latency": 12}`
			return &http.Response{
				StatusCode: 200,
				Status:     "200 OK",
				Body:       io.NopCloser(strings.NewReader(body)),
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Request:    req,
			}, nil
		}),
	}

	cmd := httpcmd.NewHTTPCmdWithClient(fakeClient)
	var out bytes.Buffer
	ctx := registry.Context{
		Stdout: &out,
	}

	if err := cmd.Run(ctx, []string{"GET", "http://localhost:8080/health"}); err != nil {
		t.Fatalf("http GET failed: %v", err)
	}

	res := out.String()
	if !strings.Contains(res, "auth") || !strings.Contains(res, "ok") {
		t.Fatalf("expected json response containing auth and ok, got: %s", res)
	}
}

func TestHTTPCmd_POST(t *testing.T) {
	receivedBody := ""
	fakeClient := &http.Client{
		Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
			buf := new(bytes.Buffer)
			buf.ReadFrom(req.Body)
			receivedBody = buf.String()
			return &http.Response{
				StatusCode: 201,
				Status:     "201 Created",
				Body:       io.NopCloser(strings.NewReader("saved")),
				Header:     make(http.Header),
				Request:    req,
			}, nil
		}),
	}

	cmd := httpcmd.NewHTTPCmdWithClient(fakeClient)
	var out bytes.Buffer
	ctx := registry.Context{
		Stdout: &out,
	}

	if err := cmd.Run(ctx, []string{"POST", "http://localhost:8080/api", "-d", "hello=world"}); err != nil {
		t.Fatalf("http POST failed: %v", err)
	}

	if receivedBody != "hello=world" {
		t.Fatalf("expected hello=world, got %q", receivedBody)
	}
}
