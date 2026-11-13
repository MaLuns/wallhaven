// Package changelog reads the JSON shared by the application and release jobs.
package changelog

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"
)

type Entry struct {
	Version string   `json:"version"`
	Date    string   `json:"date"`
	Changes []string `json:"changes"`
}

//go:embed log.json
var data []byte

var entries = func() []Entry {
	result, err := parse(data)
	if err != nil {
		panic(fmt.Errorf("changelog/log.json: %w", err))
	}
	return result
}()

// Entries returns the release log in JSON order.
func Entries() []Entry {
	result := slices.Clone(entries)
	for i := range result {
		result[i].Changes = slices.Clone(result[i].Changes)
	}
	return result
}

// Markdown renders one version for GitHub Releases and MyGO's update manifest.
func Markdown(version string) (string, error) {
	version = strings.TrimPrefix(version, "v")
	for _, entry := range entries {
		if entry.Version != version {
			continue
		}
		var notes strings.Builder
		fmt.Fprintf(&notes, "## %s\n\n发布日期：%s\n\n", entry.Version, entry.Date)
		for _, change := range entry.Changes {
			fmt.Fprintf(&notes, "- %s\n", change)
		}
		return notes.String(), nil
	}
	return "", fmt.Errorf("log.json has no entry for v%s", version)
}

func parse(data []byte) ([]Entry, error) {
	var result []Entry
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, err
	}
	if len(result) == 0 {
		return nil, fmt.Errorf("the release log is empty")
	}
	versionPattern := regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)
	seen := map[string]bool{}
	for _, entry := range result {
		if !versionPattern.MatchString(entry.Version) || seen[entry.Version] {
			return nil, fmt.Errorf("invalid or duplicate version %q", entry.Version)
		}
		seen[entry.Version] = true
		if _, err := time.Parse(time.DateOnly, entry.Date); err != nil {
			return nil, fmt.Errorf("v%s: invalid release date", entry.Version)
		}
		if len(entry.Changes) == 0 {
			return nil, fmt.Errorf("v%s: release notes are empty", entry.Version)
		}
		for _, change := range entry.Changes {
			if strings.TrimSpace(change) == "" || strings.ContainsAny(change, "\r\n") {
				return nil, fmt.Errorf("v%s: each change must be a nonempty line", entry.Version)
			}
		}
	}
	return result, nil
}
