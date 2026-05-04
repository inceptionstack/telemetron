// SPDX-License-Identifier: Apache-2.0

package updater

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"runtime"
	"strings"
	"time"
)

const (
	defaultOwner = "inceptionstack"
	defaultRepo  = "telemetron"
)

// Release represents a GitHub release.
type Release struct {
	TagName string  `json:"tag_name"`
	Assets  []Asset `json:"assets"`
}

// Asset represents a release asset.
type Asset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

// FetchLatest fetches the latest release from GitHub.
// If baseURL is empty, it defaults to the GitHub API.
func FetchLatest(ctx context.Context, client *http.Client, baseURL string) (*Release, error) {
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	if baseURL == "" {
		baseURL = fmt.Sprintf("https://api.github.com/repos/%s/%s", defaultOwner, defaultRepo)
	}
	url := baseURL + "/releases/latest"
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "telemetron-updater")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch latest release: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("GitHub API returned %d", resp.StatusCode)
	}

	var rel Release
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return nil, fmt.Errorf("decode release: %w", err)
	}
	return &rel, nil
}

// AssetName returns the expected archive name for the current platform.
// GoReleaser strips the "v" prefix: tag v0.3.7 → telemetron_0.3.7_linux_arm64.tar.gz
func AssetName(version string) string {
	plain := strings.TrimPrefix(version, "v")
	return fmt.Sprintf("telemetron_%s_%s_%s.tar.gz", plain, runtime.GOOS, runtime.GOARCH)
}

// FindAsset finds the matching asset and checksums.txt in a release.
func FindAsset(rel *Release, name string) (archive, checksums *Asset) {
	for i := range rel.Assets {
		switch rel.Assets[i].Name {
		case name:
			archive = &rel.Assets[i]
		case "checksums.txt":
			checksums = &rel.Assets[i]
		}
	}
	return
}
