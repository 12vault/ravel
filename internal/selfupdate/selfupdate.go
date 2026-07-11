package selfupdate

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

type Options struct {
	Version     string
	Repository  string
	Executable  string
	ReleaseBase string
	GOOS        string
	GOARCH      string
	Client      *http.Client
}

func Run(ctx context.Context, opts Options) (string, error) {
	if opts.Version == "" {
		opts.Version = "latest"
	}
	if opts.Repository == "" {
		opts.Repository = "12vault/ravel"
	}
	if opts.GOOS == "" {
		opts.GOOS = runtime.GOOS
	}
	if opts.GOARCH == "" {
		opts.GOARCH = runtime.GOARCH
	}
	if opts.Executable == "" {
		var err error
		opts.Executable, err = os.Executable()
		if err != nil {
			return "", err
		}
	}
	base := opts.ReleaseBase
	if base == "" {
		if opts.Version == "latest" {
			base = "https://github.com/" + opts.Repository + "/releases/latest/download"
		} else {
			version := opts.Version
			if !strings.HasPrefix(version, "v") {
				version = "v" + version
			}
			base = "https://github.com/" + opts.Repository + "/releases/download/" + version
		}
	}
	asset, err := assetName(opts.GOOS, opts.GOARCH)
	if err != nil {
		return "", err
	}
	client := opts.Client
	if client == nil {
		client = http.DefaultClient
	}
	checksums, err := download(ctx, client, base+"/checksums.txt", 4<<20)
	if err != nil {
		return "", fmt.Errorf("download checksums: %w", err)
	}
	expected, err := checksumFor(checksums, asset)
	if err != nil {
		return "", err
	}
	archive, err := download(ctx, client, base+"/"+asset, 256<<20)
	if err != nil {
		return "", fmt.Errorf("download %s: %w", asset, err)
	}
	sum := sha256.Sum256(archive)
	if hex.EncodeToString(sum[:]) != expected {
		return "", errors.New("release checksum verification failed")
	}
	binary, err := extractBinary(asset, archive)
	if err != nil {
		return "", err
	}
	if err := replaceExecutable(opts.Executable, binary); err != nil {
		return "", err
	}
	return opts.Executable, nil
}

func assetName(goos, goarch string) (string, error) {
	if goarch != "amd64" && goarch != "arm64" {
		return "", fmt.Errorf("unsupported architecture %s", goarch)
	}
	switch goos {
	case "darwin", "linux":
		return fmt.Sprintf("ravel_%s_%s.tar.gz", goos, goarch), nil
	case "windows":
		return fmt.Sprintf("ravel_windows_%s.zip", goarch), nil
	default:
		return "", fmt.Errorf("unsupported operating system %s", goos)
	}
}

func download(ctx context.Context, client *http.Client, url string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	response, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s", response.Status)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, errors.New("download exceeds safety limit")
	}
	return data, nil
}

func checksumFor(data []byte, asset string) (string, error) {
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[1] == asset && len(fields[0]) == 64 {
			return strings.ToLower(fields[0]), nil
		}
	}
	return "", fmt.Errorf("checksum for %s not found", asset)
}

func extractBinary(asset string, data []byte) ([]byte, error) {
	if strings.HasSuffix(asset, ".zip") {
		reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			return nil, err
		}
		for _, file := range reader.File {
			if filepath.Base(file.Name) == "ravel.exe" {
				handle, err := file.Open()
				if err != nil {
					return nil, err
				}
				defer handle.Close()
				return io.ReadAll(io.LimitReader(handle, 256<<20))
			}
		}
		return nil, errors.New("release archive does not contain ravel.exe")
	}
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	defer gz.Close()
	tarReader := tar.NewReader(gz)
	for {
		header, err := tarReader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		if filepath.Base(header.Name) == "ravel" && header.Typeflag == tar.TypeReg {
			return io.ReadAll(io.LimitReader(tarReader, 256<<20))
		}
	}
	return nil, errors.New("release archive does not contain ravel")
}

func replaceExecutable(path string, binary []byte) error {
	directory := filepath.Dir(path)
	tmp, err := os.CreateTemp(directory, ".ravel-update-")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if _, err := tmp.Write(binary); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpPath, 0o755); err != nil {
		return err
	}
	backup := path + ".previous"
	_ = os.Remove(backup)
	if err := os.Rename(path, backup); err != nil {
		return fmt.Errorf("replace executable: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Rename(backup, path)
		return fmt.Errorf("install executable: %w", err)
	}
	if err := os.Remove(backup); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove previous executable: %w", err)
	}
	return nil
}
