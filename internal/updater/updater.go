//go:build windows

package updater

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	repoOwner  = "SagerNet"
	repoName   = "sing-box"
	apiBaseURL = "https://api.github.com"
	userAgent  = "sing-box-tray"
)

// Release is a subset of a GitHub release relevant to the updater.
type Release struct {
	Tag    string
	Assets []Asset
}

// Asset is a subset of a GitHub release asset.
type Asset struct {
	Name string
	URL  string
}

type ghRelease struct {
	TagName    string    `json:"tag_name"`
	Prerelease bool      `json:"prerelease"`
	Draft      bool      `json:"draft"`
	Assets     []ghAsset `json:"assets"`
}

type ghAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

// FetchLatest returns the latest sing-box release for channel ("stable" or
// "alpha"). "stable" picks the newest non-draft, non-prerelease release;
// "alpha" picks the newest non-draft release regardless of prerelease status.
func FetchLatest(channel string) (*Release, error) {
	url := fmt.Sprintf("%s/repos/%s/%s/releases?per_page=10", apiBaseURL, repoOwner, repoName)
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "application/vnd.github+json")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch releases: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch releases: unexpected status %s", resp.Status)
	}

	var releases []ghRelease
	if err := json.NewDecoder(resp.Body).Decode(&releases); err != nil {
		return nil, fmt.Errorf("parse releases: %w", err)
	}

	for _, r := range releases {
		if r.Draft {
			continue
		}
		if channel != "alpha" && r.Prerelease {
			continue
		}
		assets := make([]Asset, 0, len(r.Assets))
		for _, a := range r.Assets {
			assets = append(assets, Asset{Name: a.Name, URL: a.BrowserDownloadURL})
		}
		return &Release{Tag: r.TagName, Assets: assets}, nil
	}
	return nil, fmt.Errorf("no matching release found (channel=%q)", channel)
}

// WindowsAmd64Asset returns the standard (non-legacy) Windows amd64 zip asset.
func (r *Release) WindowsAmd64Asset() (*Asset, error) {
	for _, a := range r.Assets {
		if strings.HasSuffix(a.Name, "-windows-amd64.zip") {
			return &a, nil
		}
	}
	return nil, fmt.Errorf("no windows-amd64 asset found in release %s", r.Tag)
}

// DownloadAndInstall downloads asset's zip, extracts it into
// managedRoot/<rel.Tag>/, removes sibling version directories, and returns
// the path to the extracted sing-box.exe.
func DownloadAndInstall(rel *Release, asset *Asset, managedRoot string) (string, error) {
	zipPath, err := download(asset.URL)
	if err != nil {
		return "", err
	}
	defer os.Remove(zipPath)

	destDir := filepath.Join(managedRoot, rel.Tag)
	if err := extractZip(zipPath, destDir); err != nil {
		return "", err
	}

	if err := pruneSiblings(managedRoot, rel.Tag); err != nil {
		return "", err
	}

	exePath := filepath.Join(destDir, "sing-box.exe")
	if _, err := os.Stat(exePath); err != nil {
		return "", fmt.Errorf("sing-box.exe not found after extraction: %w", err)
	}
	return exePath, nil
}

func download(url string) (string, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("build download request: %w", err)
	}
	req.Header.Set("User-Agent", userAgent)

	client := &http.Client{Timeout: 5 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("download asset: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download asset: unexpected status %s", resp.Status)
	}

	tmp, err := os.CreateTemp("", "sing-box-*.zip")
	if err != nil {
		return "", fmt.Errorf("create temp file: %w", err)
	}
	defer tmp.Close()

	if _, err := io.Copy(tmp, resp.Body); err != nil {
		os.Remove(tmp.Name())
		return "", fmt.Errorf("write downloaded asset: %w", err)
	}
	return tmp.Name(), nil
}

// extractZip extracts a sing-box release zip into destDir, stripping the
// single top-level directory the archive is wrapped in (e.g.
// "sing-box-1.13.14-windows-amd64/").
func extractZip(zipPath, destDir string) error {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return fmt.Errorf("open zip: %w", err)
	}
	defer r.Close()

	prefix := ""
	for _, f := range r.File {
		if i := strings.Index(f.Name, "/"); i >= 0 {
			prefix = f.Name[:i+1]
			break
		}
	}

	cleanDest := filepath.Clean(destDir)
	for _, f := range r.File {
		relPath := strings.TrimPrefix(f.Name, prefix)
		if relPath == "" || strings.HasSuffix(f.Name, "/") {
			continue
		}

		destPath := filepath.Join(destDir, relPath)
		if !strings.HasPrefix(destPath, cleanDest+string(os.PathSeparator)) {
			return fmt.Errorf("invalid entry path in zip: %s", f.Name)
		}

		if err := os.MkdirAll(filepath.Dir(destPath), 0755); err != nil {
			return fmt.Errorf("create dir: %w", err)
		}
		if err := extractFile(f, destPath); err != nil {
			return err
		}
	}
	return nil
}

func extractFile(f *zip.File, destPath string) error {
	rc, err := f.Open()
	if err != nil {
		return fmt.Errorf("open zip entry %s: %w", f.Name, err)
	}
	defer rc.Close()

	out, err := os.Create(destPath)
	if err != nil {
		return fmt.Errorf("create %s: %w", destPath, err)
	}
	defer out.Close()

	if _, err := io.Copy(out, rc); err != nil {
		return fmt.Errorf("extract %s: %w", destPath, err)
	}
	return nil
}

// pruneSiblings removes every directory under managedRoot except keep.
func pruneSiblings(managedRoot, keep string) error {
	entries, err := os.ReadDir(managedRoot)
	if err != nil {
		return fmt.Errorf("read managed root: %w", err)
	}
	for _, e := range entries {
		if e.IsDir() && e.Name() != keep {
			_ = os.RemoveAll(filepath.Join(managedRoot, e.Name()))
		}
	}
	return nil
}

// InstalledVersion returns the version tag currently in use if singBoxPath
// points inside managedRoot/<tag>/sing-box.exe, or "" otherwise (e.g. the
// user pointed sing_box_path somewhere the updater doesn't manage).
func InstalledVersion(singBoxPath, managedRoot string) string {
	rel, err := filepath.Rel(managedRoot, singBoxPath)
	if err != nil || strings.HasPrefix(rel, "..") {
		return ""
	}
	parts := strings.Split(filepath.ToSlash(rel), "/")
	if len(parts) != 2 || parts[1] != "sing-box.exe" {
		return ""
	}
	return parts[0]
}
