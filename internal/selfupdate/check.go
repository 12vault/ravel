package selfupdate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

type CheckOptions struct {
	CurrentVersion string
	Repository     string
	Endpoint       string
	Client         *http.Client
}

type CheckResult struct {
	CurrentVersion  string `json:"currentVersion"`
	LatestVersion   string `json:"latestVersion"`
	UpdateAvailable bool   `json:"updateAvailable"`
	ReleaseURL      string `json:"releaseUrl"`
}

// Check queries only release metadata. It never downloads an archive or
// changes the current executable.
func Check(ctx context.Context, opts CheckOptions) (CheckResult, error) {
	current, err := parseVersion(opts.CurrentVersion)
	if err != nil {
		return CheckResult{}, fmt.Errorf("current version: %w", err)
	}
	if opts.Repository == "" {
		opts.Repository = "12vault/ravel"
	}
	endpoint := opts.Endpoint
	if endpoint == "" {
		endpoint = "https://api.github.com/repos/" + opts.Repository + "/releases/latest"
	}
	client := opts.Client
	if client == nil {
		client = http.DefaultClient
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return CheckResult{}, err
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	request.Header.Set("User-Agent", "ravel-update-check/"+current.String())
	response, err := client.Do(request)
	if err != nil {
		return CheckResult{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return CheckResult{}, fmt.Errorf("release metadata: %s", response.Status)
	}
	var release struct {
		TagName string `json:"tag_name"`
		HTMLURL string `json:"html_url"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(nil, response.Body, 1<<20))
	if err := decoder.Decode(&release); err != nil {
		return CheckResult{}, fmt.Errorf("decode release metadata: %w", err)
	}
	latest, err := parseVersion(release.TagName)
	if err != nil {
		return CheckResult{}, fmt.Errorf("latest release version: %w", err)
	}
	if release.HTMLURL == "" {
		release.HTMLURL = "https://github.com/" + opts.Repository + "/releases/tag/" + latest.String()
	}
	return CheckResult{
		CurrentVersion:  current.String(),
		LatestVersion:   latest.String(),
		UpdateAvailable: compareVersion(latest, current) > 0,
		ReleaseURL:      release.HTMLURL,
	}, nil
}

type version struct {
	major int
	minor int
	patch int
	pre   []string
}

func (v version) String() string {
	value := fmt.Sprintf("v%d.%d.%d", v.major, v.minor, v.patch)
	if len(v.pre) > 0 {
		value += "-" + strings.Join(v.pre, ".")
	}
	return value
}

func parseVersion(value string) (version, error) {
	value = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(value), "v"))
	if value == "" {
		return version{}, errors.New("version is empty")
	}
	if index := strings.IndexByte(value, '+'); index >= 0 {
		value = value[:index]
	}
	var prerelease []string
	if index := strings.IndexByte(value, '-'); index >= 0 {
		pre := value[index+1:]
		value = value[:index]
		if pre == "" {
			return version{}, errors.New("empty prerelease")
		}
		prerelease = strings.Split(pre, ".")
	}
	parts := strings.Split(value, ".")
	if len(parts) != 3 {
		return version{}, fmt.Errorf("%q is not semantic major.minor.patch", value)
	}
	numbers := make([]int, 3)
	for i, part := range parts {
		if part == "" || (len(part) > 1 && part[0] == '0') {
			return version{}, fmt.Errorf("invalid numeric component %q", part)
		}
		parsed, err := strconv.Atoi(part)
		if err != nil || parsed < 0 {
			return version{}, fmt.Errorf("invalid numeric component %q", part)
		}
		numbers[i] = parsed
	}
	for _, identifier := range prerelease {
		if identifier == "" {
			return version{}, errors.New("empty prerelease identifier")
		}
		for _, char := range identifier {
			if !((char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') || char == '-') {
				return version{}, fmt.Errorf("invalid prerelease identifier %q", identifier)
			}
		}
	}
	return version{major: numbers[0], minor: numbers[1], patch: numbers[2], pre: prerelease}, nil
}

func compareVersion(left, right version) int {
	for _, pair := range [][2]int{{left.major, right.major}, {left.minor, right.minor}, {left.patch, right.patch}} {
		if pair[0] < pair[1] {
			return -1
		}
		if pair[0] > pair[1] {
			return 1
		}
	}
	if len(left.pre) == 0 && len(right.pre) == 0 {
		return 0
	}
	if len(left.pre) == 0 {
		return 1
	}
	if len(right.pre) == 0 {
		return -1
	}
	for i := 0; i < len(left.pre) && i < len(right.pre); i++ {
		leftNumber, leftErr := strconv.Atoi(left.pre[i])
		rightNumber, rightErr := strconv.Atoi(right.pre[i])
		switch {
		case leftErr == nil && rightErr == nil:
			if leftNumber < rightNumber {
				return -1
			}
			if leftNumber > rightNumber {
				return 1
			}
		case leftErr == nil:
			return -1
		case rightErr == nil:
			return 1
		default:
			if left.pre[i] < right.pre[i] {
				return -1
			}
			if left.pre[i] > right.pre[i] {
				return 1
			}
		}
	}
	if len(left.pre) < len(right.pre) {
		return -1
	}
	if len(left.pre) > len(right.pre) {
		return 1
	}
	return 0
}
