// Verify the signed files that both builders must publish for MyGO's updater.
package main

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

type config struct {
	Slug    string `json:"slug"`
	Updates struct {
		GitHub    string `json:"github"`
		PublicKey string `json:"publicKey"`
		TagPrefix string `json:"tagPrefix"`
	} `json:"updates"`
}

type signedFile struct {
	URL       string `json:"url"`
	Size      int64  `json:"size"`
	Signature string `json:"signature"`
}

type manifest struct {
	Version string `json:"version"`
	signedFile
	Deltas []signedFile `json:"deltas"`
}

func main() {
	directory := flag.String("dir", "release", "directory containing release files")
	version := flag.String("version", "", "version without the v prefix")
	platforms := flag.String("platforms", "darwin/universal,windows/amd64", "comma-separated build platforms")
	configuration := flag.String("config", "mygo.json", "application configuration")
	flag.Parse()
	if err := check(*directory, *configuration, *version, strings.Split(*platforms, ",")); err != nil {
		fmt.Fprintln(os.Stderr, "releasecheck:", err)
		os.Exit(1)
	}
	fmt.Println("Signed update manifests, archives and deltas verified.")
}

func check(directory, configuration, version string, platforms []string) error {
	if version == "" {
		return errors.New("a release version is required")
	}
	data, err := os.ReadFile(configuration)
	if err != nil {
		return err
	}
	var c config
	if err := json.Unmarshal(data, &c); err != nil {
		return err
	}
	key, err := base64.StdEncoding.DecodeString(c.Updates.PublicKey)
	if err != nil || len(key) != ed25519.PublicKeySize {
		return errors.New("updates.publicKey must be an Ed25519 public key")
	}
	if c.Slug == "" || strings.Count(c.Updates.GitHub, "/") != 1 {
		return errors.New("the application slug and GitHub update repository are required")
	}
	prefix := c.Updates.TagPrefix
	if prefix == "" {
		prefix = "v"
	}
	base := "https://github.com/" + c.Updates.GitHub + "/releases/download/" + url.PathEscape(prefix+version) + "/"
	for _, platform := range platforms {
		if platform != "darwin/universal" && platform != "windows/amd64" {
			return fmt.Errorf("unsupported release platform %q", platform)
		}
		target := strings.ReplaceAll(platform, "/", "-")
		name := "update-" + target + ".json"
		data, err := os.ReadFile(filepath.Join(directory, name))
		if err != nil {
			return err
		}
		var m manifest
		if err := json.Unmarshal(data, &m); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		archive := c.Slug + "-" + version + "-" + target + ".tar.gz"
		if m.Version != version || m.URL != base+url.PathEscape(archive) {
			return fmt.Errorf("%s: version or archive URL does not match this release", name)
		}
		for _, file := range append([]signedFile{m.signedFile}, m.Deltas...) {
			if err := verifyFile(directory, base, file, key); err != nil {
				return fmt.Errorf("%s: %w", name, err)
			}
		}
	}
	return nil
}

func verifyFile(directory, base string, file signedFile, key ed25519.PublicKey) error {
	parsed, err := url.Parse(file.URL)
	if err != nil {
		return errors.New("invalid update file URL")
	}
	name := filepath.Base(parsed.Path)
	if name == "." || strings.ContainsAny(name, "/\\") || file.URL != base+url.PathEscape(name) {
		return errors.New("update files must belong to this GitHub release")
	}
	path := filepath.Join(directory, name)
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || file.Size <= 0 || file.Size != info.Size() {
		return fmt.Errorf("%s: update file size or type does not match its manifest", name)
	}
	input, err := os.Open(path)
	if err != nil {
		return err
	}
	defer input.Close()
	digest := sha256.New()
	if _, err := io.Copy(digest, input); err != nil {
		return err
	}
	signature, err := base64.StdEncoding.DecodeString(file.Signature)
	if err != nil || !ed25519.Verify(key, digest.Sum(nil), signature) {
		return fmt.Errorf("%s: the update is not signed with updates.publicKey", name)
	}
	return nil
}
