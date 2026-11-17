package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type releaseFixture struct {
	dir, configPath string
	key             ed25519.PrivateKey
	config          config
	manifests       map[string]manifest
}

func newReleaseFixture(t *testing.T) *releaseFixture {
	t.Helper()
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	f := &releaseFixture{dir: t.TempDir(), key: key, manifests: map[string]manifest{}}
	f.configPath = filepath.Join(f.dir, "mygo.json")
	f.config.Slug = "test-wallpapers"
	f.config.Updates.GitHub = "example/wallpapers"
	f.config.Updates.PublicKey = base64.StdEncoding.EncodeToString(pub)
	for _, target := range []string{"darwin-arm64", "darwin-amd64", "windows-amd64", "linux-amd64"} {
		f.manifests[target] = manifest{Version: "1.2.3", signedFile: f.writeSigned(t, "test-wallpapers-1.2.3-"+target+".tar.gz", []byte("archive for "+target))}
	}
	f.save(t)
	return f
}

func (f *releaseFixture) writeSigned(t *testing.T, name string, data []byte) signedFile {
	t.Helper()
	if err := os.WriteFile(filepath.Join(f.dir, name), data, 0o644); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(data)
	return signedFile{
		URL:       "https://github.com/example/wallpapers/releases/download/v1.2.3/" + name,
		Size:      int64(len(data)),
		Signature: base64.StdEncoding.EncodeToString(ed25519.Sign(f.key, digest[:])),
	}
}

func (f *releaseFixture) save(t *testing.T) {
	t.Helper()
	writeJSON := func(path string, value any) {
		t.Helper()
		data, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeJSON(f.configPath, f.config)
	for target, m := range f.manifests {
		writeJSON(filepath.Join(f.dir, "update-"+target+".json"), m)
	}
}

func TestSignedRelease(t *testing.T) {
	f := newReleaseFixture(t)
	for target, m := range f.manifests {
		m.Deltas = []signedFile{f.writeSigned(t, "test-wallpapers-1.2.2-to-1.2.3-"+target+".delta", []byte("delta for "+target))}
		f.manifests[target] = m
	}
	f.save(t)
	for _, platforms := range [][]string{{"darwin/arm64"}, {"darwin/amd64"}, {"windows/amd64"}, {"linux/amd64"}, strings.Split(defaultPlatforms, ",")} {
		if err := check(f.dir, f.configPath, "1.2.3", platforms); err != nil {
			t.Fatal(err)
		}
	}
}

func TestRejectIncompleteOrInvalidUpdates(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*testing.T, *releaseFixture, *manifest)
	}{
		{"wrong version", func(_ *testing.T, _ *releaseFixture, m *manifest) { m.Version = "1.2.2" }},
		{"wrong repository", func(_ *testing.T, _ *releaseFixture, m *manifest) {
			m.URL = strings.Replace(m.URL, "example/wallpapers", "other/repository", 1)
		}},
		{"wrong platform archive", func(_ *testing.T, f *releaseFixture, m *manifest) {
			m.signedFile = f.manifests["windows-amd64"].signedFile
		}},
		{"wrong size", func(_ *testing.T, _ *releaseFixture, m *manifest) { m.Size++ }},
		{"wrong signature", func(_ *testing.T, _ *releaseFixture, m *manifest) {
			m.Signature = base64.StdEncoding.EncodeToString(make([]byte, ed25519.SignatureSize))
		}},
		{"changed archive", func(t *testing.T, f *releaseFixture, _ *manifest) {
			path := filepath.Join(f.dir, "test-wallpapers-1.2.3-darwin-arm64.tar.gz")
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			data[0] ^= 1
			if err := os.WriteFile(path, data, 0o644); err != nil {
				t.Fatal(err)
			}
		}},
		{"missing delta", func(t *testing.T, f *releaseFixture, m *manifest) {
			delta := f.writeSigned(t, "test-wallpapers-1.2.2-to-1.2.3-darwin-arm64.delta", []byte("delta"))
			m.Deltas = []signedFile{delta}
			if err := os.Remove(filepath.Join(f.dir, filepath.Base(delta.URL))); err != nil {
				t.Fatal(err)
			}
		}},
		{"delta outside release", func(t *testing.T, f *releaseFixture, m *manifest) {
			delta := f.writeSigned(t, "test.delta", []byte("delta"))
			delta.URL = "https://other.invalid/test.delta"
			m.Deltas = []signedFile{delta}
		}},
		{"wrong public key", func(t *testing.T, f *releaseFixture, _ *manifest) {
			key, _, err := ed25519.GenerateKey(rand.Reader)
			if err != nil {
				t.Fatal(err)
			}
			f.config.Updates.PublicKey = base64.StdEncoding.EncodeToString(key)
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newReleaseFixture(t)
			m := f.manifests["darwin-arm64"]
			test.change(t, f, &m)
			f.manifests["darwin-arm64"] = m
			f.save(t)
			if err := check(f.dir, f.configPath, "1.2.3", strings.Split(defaultPlatforms, ",")); err == nil {
				t.Fatal("invalid or incomplete update would have been published")
			}
		})
	}
}

func TestRejectMissingPlatform(t *testing.T) {
	for _, target := range []string{"darwin-arm64", "darwin-amd64", "windows-amd64", "linux-amd64"} {
		t.Run(target, func(t *testing.T) {
			f := newReleaseFixture(t)
			name := "update-" + target + ".json"
			if err := os.Remove(filepath.Join(f.dir, name)); err != nil {
				t.Fatal(err)
			}
			if err := check(f.dir, f.configPath, "1.2.3", strings.Split(defaultPlatforms, ",")); err == nil || !strings.Contains(err.Error(), name) {
				t.Fatalf("missing platform manifest was not rejected: %v", err)
			}
		})
	}
}

func TestRejectInvalidLinuxUpdates(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*testing.T, *releaseFixture, *manifest)
	}{
		{"wrong platform archive", func(_ *testing.T, f *releaseFixture, m *manifest) {
			m.signedFile = f.manifests["windows-amd64"].signedFile
		}},
		{"missing archive", func(t *testing.T, f *releaseFixture, m *manifest) {
			if err := os.Remove(filepath.Join(f.dir, filepath.Base(m.URL))); err != nil {
				t.Fatal(err)
			}
		}},
		{"wrong signature", func(_ *testing.T, _ *releaseFixture, m *manifest) {
			m.Signature = base64.StdEncoding.EncodeToString(make([]byte, ed25519.SignatureSize))
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newReleaseFixture(t)
			m := f.manifests["linux-amd64"]
			test.change(t, f, &m)
			f.manifests["linux-amd64"] = m
			f.save(t)
			if err := check(f.dir, f.configPath, "1.2.3", strings.Split(defaultPlatforms, ",")); err == nil || !strings.Contains(err.Error(), "update-linux-amd64.json") {
				t.Fatalf("invalid Linux update was not rejected: %v", err)
			}
		})
	}
}

func TestRejectMacOSArchitectureMismatch(t *testing.T) {
	for target, other := range map[string]string{"darwin-arm64": "darwin-amd64", "darwin-amd64": "darwin-arm64"} {
		t.Run(target, func(t *testing.T) {
			f := newReleaseFixture(t)
			m := f.manifests[target]
			m.signedFile = f.manifests[other].signedFile
			f.manifests[target] = m
			f.save(t)
			if err := check(f.dir, f.configPath, "1.2.3", strings.Split(defaultPlatforms, ",")); err == nil || !strings.Contains(err.Error(), "update-"+target+".json") {
				t.Fatalf("manifest pointing at the other macOS architecture was not rejected: %v", err)
			}
		})
	}
}

func TestRejectUniversalPlatform(t *testing.T) {
	f := newReleaseFixture(t)
	if err := check(f.dir, f.configPath, "1.2.3", []string{"darwin/universal"}); err == nil || !strings.Contains(err.Error(), "unsupported release platform") {
		t.Fatalf("Universal release platform was not rejected: %v", err)
	}
}
