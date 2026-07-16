package mcp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHTTPTransportLifecycleAndGuards(t *testing.T) {
	handler, err := NewHTTPHandler(HTTPOptions{OutDir: t.TempDir(), Version: "v-http", APIKey: "secret"})
	if err != nil {
		t.Fatal(err)
	}

	unauthorizedRequest := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(initializeBody()))
	unauthorizedRequest.Header.Set("Content-Type", "application/json")
	unauthorized := httptest.NewRecorder()
	handler.ServeHTTP(unauthorized, unauthorizedRequest)
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized status = %d, want %d", unauthorized.Code, http.StatusUnauthorized)
	}

	originRequest := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(initializeBody()))
	originRequest.Header.Set("Content-Type", "application/json")
	originRequest.Header.Set("Authorization", "Bearer secret")
	originRequest.Header.Set("Origin", "https://example.com")
	origin := httptest.NewRecorder()
	handler.ServeHTTP(origin, originRequest)
	if origin.Code != http.StatusForbidden {
		t.Fatalf("origin status = %d, want %d", origin.Code, http.StatusForbidden)
	}

	initialized := httpRequest(t, handler, http.MethodPost, "", initializeBody())
	if initialized.Code != http.StatusOK {
		t.Fatalf("initialize status = %d, body = %s", initialized.Code, initialized.Body.String())
	}
	sessionID := initialized.Header().Get("Mcp-Session-Id")
	if sessionID == "" {
		t.Fatal("initialize response omitted Mcp-Session-Id")
	}
	var response wireResponse
	if err := json.Unmarshal(initialized.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Error != nil || response.JSONRPC != "2.0" {
		t.Fatalf("initialize response = %#v", response)
	}

	notification := httpRequest(t, handler, http.MethodPost, sessionID, `{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	if notification.Code != http.StatusAccepted {
		t.Fatalf("notification status = %d, want %d", notification.Code, http.StatusAccepted)
	}
	repeatedInitialize := httpRequest(t, handler, http.MethodPost, sessionID, initializeBody())
	if repeatedInitialize.Code != http.StatusOK || !strings.Contains(repeatedInitialize.Body.String(), "Server already initialized") {
		t.Fatalf("repeated initialize status = %d, body = %s", repeatedInitialize.Code, repeatedInitialize.Body.String())
	}

	listed := httpRequest(t, handler, http.MethodPost, sessionID, `{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`)
	if listed.Code != http.StatusOK || !strings.Contains(listed.Body.String(), `"name":"context"`) {
		t.Fatalf("tools/list status = %d, body = %s", listed.Code, listed.Body.String())
	}

	missingSession := httpRequest(t, handler, http.MethodPost, "", `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	if missingSession.Code != http.StatusBadRequest {
		t.Fatalf("missing session status = %d, want %d", missingSession.Code, http.StatusBadRequest)
	}

	deleted := httpRequest(t, handler, http.MethodDelete, sessionID, "")
	if deleted.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d, body = %s", deleted.Code, deleted.Body.String())
	}
	deletedAgain := httpRequest(t, handler, http.MethodDelete, sessionID, "")
	if deletedAgain.Code != http.StatusNotFound {
		t.Fatalf("second delete status = %d, want %d", deletedAgain.Code, http.StatusNotFound)
	}
}

func TestHTTPTransportValidationAndLimits(t *testing.T) {
	if _, err := NewHTTPHandler(HTTPOptions{Address: "0.0.0.0:8080"}); err == nil || !strings.Contains(err.Error(), "requires an API key") {
		t.Fatalf("non-loopback error = %v", err)
	}
	if _, err := NewHTTPHandler(HTTPOptions{Address: "0.0.0.0:8080", APIKey: "secret"}); err != nil {
		t.Fatalf("non-loopback with key: %v", err)
	}
	if _, err := NewHTTPHandler(HTTPOptions{Path: "mcp"}); err == nil {
		t.Fatal("relative HTTP path accepted")
	}

	handler, err := NewHTTPHandler(HTTPOptions{APIKey: "secret", MaxMessageBytes: 32})
	if err != nil {
		t.Fatal(err)
	}
	tooLarge := httpRequest(t, handler, http.MethodPost, "", strings.Repeat("x", 33))
	if tooLarge.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("large request status = %d, want %d", tooLarge.Code, http.StatusRequestEntityTooLarge)
	}

	wrongContentType := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(initializeBody()))
	wrongContentType.Header.Set("Authorization", "Bearer secret")
	recorded := httptest.NewRecorder()
	handler.ServeHTTP(recorded, wrongContentType)
	if recorded.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("content type status = %d, want %d", recorded.Code, http.StatusUnsupportedMediaType)
	}
	wrongContentType = httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(initializeBody()))
	wrongContentType.Header.Set("Authorization", "Bearer secret")
	wrongContentType.Header.Set("Content-Type", "application/jsonp")
	recorded = httptest.NewRecorder()
	handler.ServeHTTP(recorded, wrongContentType)
	if recorded.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("JSON-like content type status = %d, want %d", recorded.Code, http.StatusUnsupportedMediaType)
	}

	badVersion := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(initializeBody()))
	badVersion.Header.Set("Authorization", "Bearer secret")
	badVersion.Header.Set("Content-Type", "application/json")
	badVersion.Header.Set("MCP-Protocol-Version", "1900-01-01")
	recorded = httptest.NewRecorder()
	handler.ServeHTTP(recorded, badVersion)
	if recorded.Code != http.StatusBadRequest {
		t.Fatalf("protocol version status = %d, want %d", recorded.Code, http.StatusBadRequest)
	}
}

func httpRequest(t *testing.T, handler http.Handler, method, sessionID, body string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, "/mcp", strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer secret")
	if method == http.MethodPost {
		request.Header.Set("Content-Type", "application/json")
	}
	if sessionID != "" {
		request.Header.Set("Mcp-Session-Id", sessionID)
	}
	recorded := httptest.NewRecorder()
	handler.ServeHTTP(recorded, request)
	return recorded
}

func initializeBody() string {
	return `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"test","version":"1"}}}`
}
