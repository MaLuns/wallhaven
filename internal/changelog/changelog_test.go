package changelog

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestCurrentReleaseNotes(t *testing.T) {
	data, err := os.ReadFile("../../mygo.json")
	if err != nil {
		t.Fatal(err)
	}
	var configuration struct{ Version string }
	if err := json.Unmarshal(data, &configuration); err != nil {
		t.Fatal(err)
	}
	notes, err := Markdown(configuration.Version)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(notes, "## "+configuration.Version+"\n") {
		t.Fatalf("release heading does not match application version: %q", notes)
	}
	var found bool
	for _, entry := range Entries() {
		if entry.Version != configuration.Version {
			continue
		}
		found = true
		if !strings.Contains(notes, entry.Date) {
			t.Fatal("release date is missing")
		}
		for _, change := range entry.Changes {
			if !strings.Contains(notes, "- "+change+"\n") {
				t.Fatalf("JSON change missing from release notes: %q", change)
			}
		}
	}
	if !found {
		t.Fatal("the application version has no JSON log entry")
	}
	if _, err := Markdown("999.999.999"); err == nil {
		t.Fatal("missing release notes must block publishing")
	}
}

func TestRejectInvalidLog(t *testing.T) {
	for _, data := range []string{
		`[]`,
		`[{"version":"01.0.0","date":"2026-11-13","changes":["change"]}]`,
		`[{"version":"1.1.0","date":"2026-02-30","changes":["change"]}]`,
		`[{"version":"1.1.0","date":"2026-11-13","changes":[]}]`,
		`[{"version":"1.1.0","date":"2026-11-13","changes":[" "]}]`,
		`[{"version":"1.1.0","date":"2026-11-13","changes":["one\ntwo"]}]`,
		`[{"version":"1.1.0","date":"2026-11-13","changes":["one"]},{"version":"1.1.0","date":"2026-11-13","changes":["two"]}]`,
	} {
		if _, err := parse([]byte(data)); err == nil {
			t.Fatalf("invalid log accepted: %s", data)
		}
	}
}
