package selfupdate

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

func TestRunVerifiesAndReplacesExecutable(t *testing.T) {
	archive := tarball(t, []byte("new-ravel"))
	sum := sha256.Sum256(archive)
	asset := "ravel_linux_amd64.tar.gz"
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		var body []byte
		switch filepath.Base(r.URL.Path) {
		case "checksums.txt":
			body = []byte(fmt.Sprintf("%s  %s\n", hex.EncodeToString(sum[:]), asset))
		case asset:
			body = archive
		default:
			return &http.Response{StatusCode: http.StatusNotFound, Status: "404 Not Found", Body: io.NopCloser(bytes.NewReader(nil))}, nil
		}
		return &http.Response{StatusCode: http.StatusOK, Status: "200 OK", Body: io.NopCloser(bytes.NewReader(body))}, nil
	})}
	executable := filepath.Join(t.TempDir(), "ravel")
	if err := os.WriteFile(executable, []byte("old-ravel"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(context.Background(), Options{Executable: executable, ReleaseBase: "https://releases.example", GOOS: "linux", GOARCH: "amd64", Client: client}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(executable)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "new-ravel" {
		t.Fatalf("updated executable = %q", data)
	}
}

func TestRunRejectsChecksumMismatch(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		var body []byte
		if filepath.Base(r.URL.Path) == "checksums.txt" {
			body = []byte("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa  ravel_linux_amd64.tar.gz\n")
		} else {
			body = tarball(t, []byte("bad"))
		}
		return &http.Response{StatusCode: http.StatusOK, Status: "200 OK", Body: io.NopCloser(bytes.NewReader(body))}, nil
	})}
	executable := filepath.Join(t.TempDir(), "ravel")
	if err := os.WriteFile(executable, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(context.Background(), Options{Executable: executable, ReleaseBase: "https://releases.example", GOOS: "linux", GOARCH: "amd64", Client: client}); err == nil {
		t.Fatal("expected checksum error")
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

func tarball(t *testing.T, binary []byte) []byte {
	t.Helper()
	var buffer bytes.Buffer
	gz := gzip.NewWriter(&buffer)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: "ravel", Mode: 0o755, Size: int64(len(binary)), Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(binary); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}
