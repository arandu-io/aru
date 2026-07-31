// Package toolchain downloads and caches the two binaries the view layer needs.
//
// The promise is `git clone && aru dev`: no Node, no node_modules, no package
// manager. Having a build step is allowed; being Node is not (RULE 13). So templ
// and the Tailwind standalone CLI are fetched as single binaries, pinned to a
// version, and cached in ~/.arandu/bin -- the same shape the Go toolchain uses
// for itself.
//
// Nothing here reaches npm, and nothing here is optional at runtime: the built
// artifacts are committed to the project, so a deploy still needs only `go build`.
package toolchain

import (
	"archive/tar"
	"compress/gzip"
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
	"time"
)

// Tools are pinned rather than tracked. A build that silently picks up a new
// major of Tailwind is a build that breaks on someone else's machine and not on
// yours, which is the worst kind. Override in arandu.toml.
const (
	DefaultTemplVersion    = "v0.3.1020"
	DefaultTailwindVersion = "v4.3.3"
)

// Tool is one managed binary.
type Tool struct {
	// Name is what the binary is called on disk and in messages.
	Name string
	// Repo is the GitHub repository that publishes the release.
	Repo string
	// Version is the release tag, and it is part of the cached path: two
	// projects pinning different versions do not fight over one file.
	Version string
	// asset names the release artifact for a platform, and reports whether the
	// platform is published at all.
	asset func(goos, goarch string) (string, bool)
	// checksums is the release artifact listing "<sha256>  <name>" lines.
	checksums string
	// targz says the asset is a tarball to extract rather than a bare binary.
	targz bool
}

// Templ returns the templ tool: .templ files to Go.
func Templ(version string) Tool {
	if version == "" {
		version = DefaultTemplVersion
	}
	return Tool{
		Name:      "templ",
		Repo:      "a-h/templ",
		Version:   version,
		checksums: "checksums.txt",
		targz:     true,
		asset: func(goos, goarch string) (string, bool) {
			os, ok := map[string]string{"darwin": "Darwin", "linux": "Linux", "windows": "Windows"}[goos]
			if !ok {
				return "", false
			}
			arch, ok := map[string]string{"amd64": "x86_64", "arm64": "arm64", "386": "i386"}[goarch]
			if !ok {
				return "", false
			}
			if os == "Darwin" && arch == "i386" {
				return "", false
			}
			return fmt.Sprintf("templ_%s_%s.tar.gz", os, arch), true
		},
	}
}

// Tailwind returns the standalone Tailwind CLI.
//
// Standalone is the whole point: Tailwind publishes this binary precisely for
// people who do not want Node in the project, and it is the only form of
// Tailwind this project will ever use. Never `npx tailwindcss`, never the npm
// package (doc 14).
func Tailwind(version string) Tool {
	if version == "" {
		version = DefaultTailwindVersion
	}
	return Tool{
		Name:      "tailwindcss",
		Repo:      "tailwindlabs/tailwindcss",
		Version:   version,
		checksums: "sha256sums.txt",
		asset: func(goos, goarch string) (string, bool) {
			switch goos {
			case "darwin":
				switch goarch {
				case "arm64":
					return "tailwindcss-macos-arm64", true
				case "amd64":
					return "tailwindcss-macos-x64", true
				}
			case "linux":
				switch goarch {
				case "arm64":
					return "tailwindcss-linux-arm64", true
				case "amd64":
					return "tailwindcss-linux-x64", true
				}
			case "windows":
				if goarch == "amd64" {
					return "tailwindcss-windows-x64.exe", true
				}
			}
			return "", false
		},
	}
}

// Dir is where the managed binaries live: ~/.arandu/bin.
//
// Outside the project, because a binary per project would mean a download per
// project; and outside GOPATH, because these are not Go modules.
func Dir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("finding the home directory: %w", err)
	}
	return filepath.Join(home, ".arandu", "bin"), nil
}

// Path is where this exact version of the tool is cached.
func (t Tool) Path() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	name := t.Name + "-" + t.Version
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return filepath.Join(dir, name), nil
}

// Ensure returns the path to the binary, downloading it on first use.
//
// It reports progress to w, because a silent thirty-second pause on the first
// run reads as a hang.
func (t Tool) Ensure(w io.Writer) (string, error) {
	path, err := t.Path()
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(path); err == nil {
		return path, nil
	}

	assetName, ok := t.asset(runtime.GOOS, runtime.GOARCH)
	if !ok {
		return "", fmt.Errorf("%s does not publish a binary for %s/%s: install it yourself and put it in %s",
			t.Name, runtime.GOOS, runtime.GOARCH, filepath.Dir(path))
	}

	fmt.Fprintf(w, "downloading %s %s\n", t.Name, t.Version)

	base := fmt.Sprintf("https://github.com/%s/releases/download/%s/", t.Repo, t.Version)
	body, err := fetch(base + assetName)
	if err != nil {
		return "", err
	}

	// The checksum comes from the same release, so it proves the bytes arrived
	// intact -- not that the release itself is trustworthy. That is what pinning
	// the version is for: an existing cached binary is never replaced silently.
	sums, err := fetch(base + t.checksums)
	if err != nil {
		return "", err
	}
	want, err := checksumFor(string(sums), assetName)
	if err != nil {
		return "", err
	}
	got := sha256.Sum256(body)
	if hex.EncodeToString(got[:]) != want {
		return "", fmt.Errorf("%s: the downloaded %s does not match the published checksum", t.Name, assetName)
	}

	binary := body
	if t.targz {
		binary, err = extract(body, t.Name)
		if err != nil {
			return "", fmt.Errorf("%s: %w", t.Name, err)
		}
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	// Write to a temporary name and rename, so two `aru dev` running at once
	// never observe a half-written binary.
	tmp := path + ".partial"
	if err := os.WriteFile(tmp, binary, 0o755); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, path); err != nil {
		return "", err
	}
	return path, nil
}

func fetch(url string) ([]byte, error) {
	client := &http.Client{Timeout: 5 * time.Minute}
	resp, err := client.Get(url)
	if err != nil {
		return nil, fmt.Errorf("downloading %s: %w", url, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("downloading %s: %s", url, resp.Status)
	}
	// A release asset that turns out to be hundreds of megabytes is a redirect
	// gone wrong, not a toolchain.
	return io.ReadAll(io.LimitReader(resp.Body, 200<<20))
}

// checksumFor finds the line for one asset in a checksums file.
//
// Both projects publish "<sha256>  <filename>", but they disagree on the
// filename: templ writes it bare, Tailwind writes "./name", and the sha256sum
// binary format writes "*name" for a binary file. All three are the same asset.
func checksumFor(sums, asset string) (string, error) {
	for _, line := range strings.Split(sums, "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		name := strings.TrimPrefix(strings.TrimPrefix(fields[1], "*"), "./")
		if name == asset {
			return fields[0], nil
		}
	}
	return "", fmt.Errorf("the checksums file has no entry for %s", asset)
}

// extract pulls one named file out of a .tar.gz.
func extract(body []byte, name string) ([]byte, error) {
	gz, err := gzip.NewReader(strings.NewReader(string(body)))
	if err != nil {
		return nil, err
	}
	defer func() { _ = gz.Close() }()

	tr := tar.NewReader(gz)
	for {
		header, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("the archive has no %s", name)
		}
		if err != nil {
			return nil, err
		}
		if header.Typeflag != tar.TypeReg || filepath.Base(header.Name) != name {
			continue
		}
		return io.ReadAll(io.LimitReader(tr, 200<<20))
	}
}
