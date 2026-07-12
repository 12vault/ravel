package selfupdate

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"testing"
)

func TestCheckReportsNewerReleaseWithoutDownloadingArchive(t *testing.T) {
	requests := 0
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		requests++
		if request.URL.Path != "/repos/12vault/ravel/releases/latest" {
			t.Fatalf("request path = %q", request.URL.Path)
		}
		if request.Header.Get("Accept") != "application/vnd.github+json" || request.Header.Get("User-Agent") == "" {
			t.Fatalf("request headers = %#v", request.Header)
		}
		body := []byte(`{"tag_name":"v0.3.0","html_url":"https://github.com/12vault/ravel/releases/tag/v0.3.0"}`)
		return &http.Response{StatusCode: http.StatusOK, Status: "200 OK", Body: io.NopCloser(bytes.NewReader(body))}, nil
	})}

	result, err := Check(context.Background(), CheckOptions{CurrentVersion: "v0.2.0", Client: client})
	if err != nil {
		t.Fatal(err)
	}
	if requests != 1 {
		t.Fatalf("requests = %d, want one metadata request", requests)
	}
	if result.CurrentVersion != "v0.2.0" || result.LatestVersion != "v0.3.0" || !result.UpdateAvailable {
		t.Fatalf("result = %#v", result)
	}
	if result.ReleaseURL != "https://github.com/12vault/ravel/releases/tag/v0.3.0" {
		t.Fatalf("release URL = %q", result.ReleaseURL)
	}
}

func TestCheckReportsCurrentAndNewerClientsAsUpToDate(t *testing.T) {
	for _, current := range []string{"v0.2.0", "v0.3.0"} {
		client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			body := []byte(`{"tag_name":"v0.2.0"}`)
			return &http.Response{StatusCode: http.StatusOK, Status: "200 OK", Body: io.NopCloser(bytes.NewReader(body))}, nil
		})}
		result, err := Check(context.Background(), CheckOptions{CurrentVersion: current, Repository: "12vault/ravel", Client: client})
		if err != nil {
			t.Fatal(err)
		}
		if result.UpdateAvailable {
			t.Fatalf("current %s unexpectedly reports update: %#v", current, result)
		}
	}
}

func TestSemanticVersionPrecedence(t *testing.T) {
	tests := []struct {
		left  string
		right string
		want  int
	}{
		{"v0.2.1", "v0.2.0", 1},
		{"v1.0.0", "v0.99.99", 1},
		{"v1.0.0-beta.2", "v1.0.0-beta.1", 1},
		{"v1.0.0-beta.1", "v1.0.0", -1},
		{"v1.0.0+build.2", "v1.0.0+build.1", 0},
	}
	for _, test := range tests {
		left, err := parseVersion(test.left)
		if err != nil {
			t.Fatal(err)
		}
		right, err := parseVersion(test.right)
		if err != nil {
			t.Fatal(err)
		}
		if got := compareVersion(left, right); got != test.want {
			t.Errorf("compareVersion(%s, %s) = %d, want %d", test.left, test.right, got, test.want)
		}
	}
}

func TestCheckRejectsInvalidVersionsAndHTTPFailures(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusInternalServerError, Status: "500 Internal Server Error", Body: io.NopCloser(bytes.NewReader(nil))}, nil
	})}
	if _, err := Check(context.Background(), CheckOptions{CurrentVersion: "development", Client: client}); err == nil {
		t.Fatal("expected invalid current-version error")
	}
	if _, err := Check(context.Background(), CheckOptions{CurrentVersion: "v0.2.0", Client: client}); err == nil {
		t.Fatal("expected HTTP status error")
	}
}
