// Package toolchain downloads and caches the one binary the view layer needs.
//
// The promise is `git clone && aru dev`: no Node, no node_modules, no package
// manager. Having a build step is allowed; being Node is not. So the Tailwind
// standalone CLI is fetched as a single binary, pinned to a version, and cached
// in ~/.arandu/bin -- the same shape the Go toolchain uses for itself.
//
// One binary, not two: the view compiler is kyse, which is part of `aru` rather
// than something to download.
//
// Nothing here reaches npm, and nothing here is optional at runtime: the built
// artifacts are committed to the project, so a deploy still needs only `go build`.
package toolchain

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// DefaultTailwindVersion is pinned rather than tracked. A build that silently
// picks up a new major of Tailwind is a build that breaks on someone else's
// machine and not on yours, which is the worst kind. Override in arandu.toml.
const DefaultTailwindVersion = "v4.3.3"

// tailwindDigests are the SHA-256 of every asset of the Tailwind releases aru
// ships checksums for, by version and asset name.
//
// The release's own sha256sums.txt sits beside the binary it describes, so
// whoever can replace one can replace the other, and checking one against the
// other proves only that the download arrived whole. These were read from the
// release's checksums file, from the digest GitHub records for each asset, and
// from a binary downloaded before, and all three agree.
//
// DefaultTailwindVersion is always here. A version is added when it becomes the
// default and never removed: a project pinning a former default keeps building
// on a machine that has not downloaded it yet.
var tailwindDigests = map[string]map[string]string{
	"v4.3.3": {
		"tailwindcss-linux-arm64":      "55fd0b241214eff3de1e8ee4f22796662f2d2e7a49bcfca7477cfd0bac398195",
		"tailwindcss-linux-arm64-musl": "71ea4be79c9de9827545682df3e040053fb535d37c71ed2cfdedf9385a0868e0",
		"tailwindcss-linux-x64":        "dc61b3ac6b8c9ca874c0cc4c57b2409791a64c5540404ca5f5367360babc313a",
		"tailwindcss-linux-x64-musl":   "a04d34ceacc8f52cbe8920ad846cdeb61d3d0021dba32db0d1f77c9d9fad7a6c",
		"tailwindcss-macos-arm64":      "cdf646702987a743464dff4d9c60fd4480d1c1e73dd819a9a67f1078815dce9d",
		"tailwindcss-macos-x64":        "7922e0953f2110c05976e3bf58f14e643d90427575e766b7d433f5f80cbee7e1",
		"tailwindcss-windows-x64.exe":  "e0e260ce048014e9268f6237ff18f8ccf02cef521cbd0ae04e82c2cdf7aa3955",
	},
}

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
	asset func(goos, goarch, libc string) (string, bool)
	// checksums is the release artifact listing "<sha256>  <name>" lines.
	checksums string
	// digests are the SHA-256 of each asset of this version as aru ships them,
	// keyed by asset name, or nil for a version aru has none for.
	digests map[string]string
	// sums is the SHA-256 of the checksums file, pinned by the project. It is
	// what a version without digests is checked against.
	sums string
	// host is where releases are downloaded from; a test points it elsewhere.
	host string
}

// Tailwind returns the standalone Tailwind CLI at version, downloaded against
// sums: the SHA-256 of the release's sha256sums.txt, which a version other
// than the ones aru ships checksums for needs, and those do not.
//
// Standalone is the whole point: Tailwind publishes this binary precisely for
// people who do not want Node in the project, and it is the only form of
// Tailwind this project will ever use. Never `npx tailwindcss`, never the npm
// package.
func Tailwind(version, sums string) Tool {
	if version == "" {
		version = DefaultTailwindVersion
	}
	return Tool{
		Name:      "tailwindcss",
		Repo:      "tailwindlabs/tailwindcss",
		Version:   version,
		checksums: "sha256sums.txt",
		digests:   tailwindDigests[version],
		sums:      sums,
		host:      "https://github.com",
		asset: func(goos, goarch, libc string) (string, bool) {
			switch goos {
			case "darwin":
				switch goarch {
				case "arm64":
					return "tailwindcss-macos-arm64", true
				case "amd64":
					return "tailwindcss-macos-x64", true
				}
			case "linux":
				// Two Linux builds, and picking the wrong one fails in a way
				// that says the opposite of what is wrong:
				//
				//	fork/exec …/tailwindcss-v4.3.3: no such file or directory
				//
				// naming the file that was just downloaded and is right there.
				// What is missing is the dynamic loader -- the glibc build on a
				// musl system, which is every Alpine image, and Alpine is what
				// the Dockerfile builds in.
				//
				// The alternative was gcompat in every Dockerfile. Downloading
				// the right binary is one place instead of one per project.
				suffix := ""
				if libc == "musl" {
					suffix = "-musl"
				}
				switch goarch {
				case "arm64":
					return "tailwindcss-linux-arm64" + suffix, true
				case "amd64":
					return "tailwindcss-linux-x64" + suffix, true
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

// libc is which C library this system has: "musl" or "gnu".
//
// It is decided by looking for musl's dynamic loader rather than by reading
// /etc/os-release, because the question is what can load a binary and not which
// distribution somebody is on -- a glibc binary on Alpine fails the same way
// whatever the release file says.
//
// Anything that is not Linux is "gnu", which is a lie and does not matter: the
// macOS and Windows assets have no variants to choose between.
func libc() string {
	if runtime.GOOS != "linux" {
		return "gnu"
	}
	for _, loader := range []string{
		"/lib/ld-musl-x86_64.so.1",
		"/lib/ld-musl-aarch64.so.1",
	} {
		if _, err := os.Stat(loader); err == nil {
			return "musl"
		}
	}
	return "gnu"
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
//
// The version is part of the file name, so it is refused unless it is a
// release tag, and the joined path is refused unless it is a file directly in
// Dir: a version carrying a separator or `..` would otherwise name a file
// anywhere the user can write, and the binary downloaded into it is executed.
func (t Tool) Path() (string, error) {
	if !releaseTag.MatchString(t.Version) {
		return "", fmt.Errorf("%s version %q is not a release tag, want one written as %s", t.Name, t.Version, DefaultTailwindVersion)
	}
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	name := t.Name + "-" + t.Version
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	path := filepath.Join(dir, name)
	if filepath.Dir(path) != filepath.Clean(dir) {
		return "", fmt.Errorf("%s version %q names a file outside %s", t.Name, t.Version, dir)
	}
	return path, nil
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

	assetName, ok := t.asset(runtime.GOOS, runtime.GOARCH, libc())
	if !ok {
		return "", fmt.Errorf("%s does not publish a binary for %s/%s: install it yourself and put it in %s",
			t.Name, runtime.GOOS, runtime.GOARCH, filepath.Dir(path))
	}

	// Refused before anything is downloaded: a version with no digest from
	// outside its release has nothing to be checked against.
	known, shipped := t.digests[assetName]
	if !shipped && t.sums == "" {
		return "", fmt.Errorf("%s %s: aru ships no checksums for this version, so nothing outside the release could check this download.\n"+
			"    Pin the SHA-256 of that release's %s in arandu.toml, under [tools]:\n"+
			"        tailwindcss-sha256sums = \"<sha256 of %s>\"\n"+
			"    or remove the tailwindcss line to build with %s",
			t.Name, t.Version, t.checksums, t.checksums, DefaultTailwindVersion)
	}

	fmt.Fprintf(w, "downloading %s %s\n", t.Name, t.Version)

	base := fmt.Sprintf("%s/%s/releases/download/%s/", t.host, t.Repo, t.Version)
	body, err := fetch(base + assetName)
	if err != nil {
		return "", err
	}

	want := known
	if t.sums != "" {
		// The checksums file is trusted because its own digest is the one the
		// project pinned, and the line for this asset is trusted because the
		// file is. Where aru ships a digest as well, the two must agree.
		sums, err := fetch(base + t.checksums)
		if err != nil {
			return "", err
		}
		if digest := sha256.Sum256(sums); hex.EncodeToString(digest[:]) != t.sums {
			return "", fmt.Errorf("%s %s: the release's %s does not match the tailwindcss-sha256sums pinned in arandu.toml", t.Name, t.Version, t.checksums)
		}
		listed, err := checksumFor(string(sums), assetName)
		if err != nil {
			return "", err
		}
		if shipped && listed != known {
			return "", fmt.Errorf("%s %s: the release's %s lists %s with a checksum other than the one aru ships", t.Name, t.Version, t.checksums, assetName)
		}
		want = listed
	}
	got := sha256.Sum256(body)
	if hex.EncodeToString(got[:]) != want {
		return "", fmt.Errorf("%s %s: the downloaded %s does not match its checksum", t.Name, t.Version, assetName)
	}

	// The asset is the binary itself. Tailwind publishes it that way, and it is
	// the only tool this package fetches, so there is no archive to extract.
	binary := body

	if err := install(path, binary); err != nil {
		return "", err
	}
	return path, nil
}

// install writes the binary at path, executable, and either completely or not at
// all.
//
// The write goes to a temporary in the same directory and is renamed into place,
// so two `aru dev` running at once never observe a half-written binary. The
// temporary is named for this call rather than a fixed "<path>.partial": a fixed
// one is the same file for both of them, so they interleave their bytes into it
// and one renames what the other is still writing, which is the outcome the
// rename exists to prevent.
//
// Nothing is left behind on the way out. A temporary named for one call is a
// file nothing will ever look at again -- the next run writes its own -- so a
// failed rename used to leave it in the cache directory for good.
func install(path string, binary []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".partial-*")
	if err != nil {
		return err
	}
	// After a successful rename there is nothing left to remove, and saying so
	// is not a failure worth reporting.
	defer func() { _ = os.Remove(tmp.Name()) }()

	if _, err := tmp.Write(binary); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	// CreateTemp opens at 0o600 and this file is executed, so the mode is set
	// before the rename rather than after: the cached path is never a binary
	// somebody could find and be unable to run.
	if err := os.Chmod(tmp.Name(), 0o755); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// Sweep deletes cached binaries for tools that no longer exist, naming each one
// it removed on w.
//
// Nothing else ever deletes them. The cached file name carries the version
// ([Tool.Path]), so a new pin writes a new file instead of replacing the old
// one, and a tool that stops being asked for is never asked for again -- templ's
// binary is 13 MB and was last useful before kyse existed.
//
// It touches only what this package recognises as retired. A tool still in use
// is left alone, and so is any other file: the message for an unpublished
// platform tells people to put their own binary in this directory, and a sweep
// that deleted it would be answering one problem by causing another.
func Sweep(w io.Writer) error {
	dir, err := Dir()
	if err != nil {
		return err
	}
	return sweep(dir, w)
}

func sweep(dir string, w io.Writer) error {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		// Nothing has been downloaded on this machine yet.
		return nil
	}
	if err != nil {
		return fmt.Errorf("reading %s: %w", dir, err)
	}
	for _, e := range entries {
		if e.IsDir() || !isRetiredBinary(e.Name()) {
			continue
		}
		if err := os.Remove(filepath.Join(dir, e.Name())); err != nil {
			return fmt.Errorf("removing the cached binary for a retired tool: %w", err)
		}
		fmt.Fprintf(w, "removed %s from %s: it is no longer a tool aru uses\n", e.Name(), dir)
	}
	return nil
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
// The format is "<sha256>  <filename>", and the filename comes in three spellings
// in the wild: bare, "./name" as Tailwind publishes it, and "*name" as the
// sha256sum binary format writes it. All three name the same asset, and a parser
// that handles one of them fails on first use, on somebody else's machine.
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
