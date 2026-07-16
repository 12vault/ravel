package mcp

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	defaultHTTPAddress = "127.0.0.1:8080"
	defaultHTTPPath    = "/mcp"
	maxHTTPSessions    = 128
)

type HTTPOptions struct {
	OutDir          string
	Version         string
	Address         string
	Path            string
	APIKey          string
	MaxMessageBytes int
}

type httpSession struct {
	mu     sync.Mutex
	server *server
}

type httpTransport struct {
	options  HTTPOptions
	graphs   *graphCache
	mu       sync.Mutex
	sessions map[string]*httpSession
}

// ServeStreamableHTTP exposes the read-only MCP tools over JSON responses on
// MCP's Streamable HTTP transport. It intentionally does not offer browser
// CORS or legacy SSE GET streams; clients use POST plus Mcp-Session-Id.
func ServeStreamableHTTP(ctx context.Context, options HTTPOptions) error {
	options, err := normalizeHTTPOptions(options)
	if err != nil {
		return err
	}
	listener, err := net.Listen("tcp", options.Address)
	if err != nil {
		return err
	}
	server := &http.Server{
		Handler:           newHTTPTransport(options),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       90 * time.Second,
		MaxHeaderBytes:    32 << 10,
	}
	result := make(chan error, 1)
	go func() {
		err := server.Serve(listener)
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		result <- err
	}()
	select {
	case err := <-result:
		return err
	case <-ctx.Done():
		shutdownContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownContext); err != nil {
			return err
		}
		return <-result
	}
}

// NewHTTPHandler is the embeddable form used by tests and custom servers.
func NewHTTPHandler(options HTTPOptions) (http.Handler, error) {
	normalized, err := normalizeHTTPOptions(options)
	if err != nil {
		return nil, err
	}
	return newHTTPTransport(normalized), nil
}

func newHTTPTransport(options HTTPOptions) *httpTransport {
	return &httpTransport{
		options: options, graphs: newGraphCache(options.OutDir), sessions: map[string]*httpSession{},
	}
}

func normalizeHTTPOptions(options HTTPOptions) (HTTPOptions, error) {
	options.OutDir = strings.TrimSpace(options.OutDir)
	if options.OutDir == "" {
		options.OutDir = ".reporavel"
	}
	options.Version = strings.TrimSpace(options.Version)
	if options.Version == "" {
		options.Version = "dev"
	}
	options.Address = strings.TrimSpace(options.Address)
	if options.Address == "" {
		options.Address = defaultHTTPAddress
	}
	host, _, err := net.SplitHostPort(options.Address)
	if err != nil {
		return HTTPOptions{}, fmt.Errorf("invalid MCP HTTP address %q: %w", options.Address, err)
	}
	if !loopbackHost(host) && strings.TrimSpace(options.APIKey) == "" {
		return HTTPOptions{}, errors.New("MCP HTTP requires an API key when binding beyond loopback")
	}
	options.APIKey = strings.TrimSpace(options.APIKey)
	options.Path = strings.TrimSpace(options.Path)
	if options.Path == "" {
		options.Path = defaultHTTPPath
	}
	if !strings.HasPrefix(options.Path, "/") || strings.ContainsAny(options.Path, "?#") {
		return HTTPOptions{}, fmt.Errorf("invalid MCP HTTP path %q", options.Path)
	}
	if options.MaxMessageBytes <= 0 {
		options.MaxMessageBytes = defaultMaxMessageBytes
	}
	if options.MaxMessageBytes > 16<<20 {
		return HTTPOptions{}, errors.New("MCP HTTP message limit must not exceed 16 MiB")
	}
	return options, nil
}

func loopbackHost(host string) bool {
	host = strings.Trim(host, "[]")
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func (transport *httpTransport) ServeHTTP(w http.ResponseWriter, request *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if request.URL.Path != transport.options.Path {
		http.NotFound(w, request)
		return
	}
	if request.Header.Get("Origin") != "" {
		http.Error(w, "browser origins are not accepted", http.StatusForbidden)
		return
	}
	if !transport.authorized(request) {
		w.Header().Set("WWW-Authenticate", `Bearer realm="ravel-mcp"`)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if version := strings.TrimSpace(request.Header.Get("MCP-Protocol-Version")); version != "" && !supportedProtocolVersions[version] {
		http.Error(w, "unsupported MCP protocol version", http.StatusBadRequest)
		return
	}
	switch request.Method {
	case http.MethodPost:
		transport.handlePost(w, request)
	case http.MethodDelete:
		transport.handleDelete(w, request)
	default:
		w.Header().Set("Allow", "POST, DELETE")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (transport *httpTransport) authorized(request *http.Request) bool {
	want := transport.options.APIKey
	if want == "" {
		return true
	}
	provided := strings.TrimSpace(request.Header.Get("X-API-Key"))
	if authorization := strings.TrimSpace(request.Header.Get("Authorization")); strings.HasPrefix(strings.ToLower(authorization), "bearer ") {
		provided = strings.TrimSpace(authorization[len("Bearer "):])
	}
	if len(provided) != len(want) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(provided), []byte(want)) == 1
}

func (transport *httpTransport) handlePost(w http.ResponseWriter, httpRequest *http.Request) {
	contentType, _, err := mime.ParseMediaType(httpRequest.Header.Get("Content-Type"))
	if err != nil || !strings.EqualFold(contentType, "application/json") {
		http.Error(w, "Content-Type must be application/json", http.StatusUnsupportedMediaType)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, httpRequest.Body, int64(transport.options.MaxMessageBytes)))
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			http.Error(w, "MCP request body too large", http.StatusRequestEntityTooLarge)
			return
		}
		http.Error(w, "read MCP request", http.StatusBadRequest)
		return
	}
	decoded, requestError := decodeRequest(body)
	if requestError != nil {
		writeHTTPResponse(w, requestError)
		return
	}

	sessionID := strings.TrimSpace(httpRequest.Header.Get("Mcp-Session-Id"))
	var session *httpSession
	createdSession := false
	if decoded.method == "initialize" && sessionID == "" {
		sessionID, session, createdSession = transport.createSession()
		if !createdSession {
			http.Error(w, "too many MCP sessions", http.StatusServiceUnavailable)
			return
		}
	} else {
		session = transport.session(sessionID)
		if session == nil {
			http.Error(w, "missing or unknown Mcp-Session-Id", http.StatusBadRequest)
			return
		}
	}

	session.mu.Lock()
	result := session.server.handle(decoded)
	session.mu.Unlock()
	if decoded.method == "initialize" {
		if createdSession && (result == nil || result.Error != nil) {
			transport.deleteSession(sessionID)
		} else if result != nil && result.Error == nil {
			w.Header().Set("Mcp-Session-Id", sessionID)
		}
	}
	if result == nil {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	writeHTTPResponse(w, result)
}

func (transport *httpTransport) handleDelete(w http.ResponseWriter, request *http.Request) {
	sessionID := strings.TrimSpace(request.Header.Get("Mcp-Session-Id"))
	if sessionID == "" || !transport.deleteSession(sessionID) {
		http.Error(w, "unknown Mcp-Session-Id", http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (transport *httpTransport) createSession() (string, *httpSession, bool) {
	transport.mu.Lock()
	defer transport.mu.Unlock()
	if len(transport.sessions) >= maxHTTPSessions {
		return "", nil, false
	}
	for {
		bytes := make([]byte, 32)
		if _, err := rand.Read(bytes); err != nil {
			return "", nil, false
		}
		id := hex.EncodeToString(bytes)
		if transport.sessions[id] != nil {
			continue
		}
		session := &httpSession{server: &server{graphs: transport.graphs, version: transport.options.Version}}
		transport.sessions[id] = session
		return id, session, true
	}
}

func (transport *httpTransport) session(id string) *httpSession {
	if id == "" {
		return nil
	}
	transport.mu.Lock()
	defer transport.mu.Unlock()
	return transport.sessions[id]
}

func (transport *httpTransport) deleteSession(id string) bool {
	transport.mu.Lock()
	defer transport.mu.Unlock()
	if transport.sessions[id] == nil {
		return false
	}
	delete(transport.sessions, id)
	return true
}

func writeHTTPResponse(w http.ResponseWriter, result *response) {
	body, err := json.Marshal(result)
	if err != nil {
		http.Error(w, "encode MCP response", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}
