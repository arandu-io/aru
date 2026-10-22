package toolchain

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
)

// A pinned version that is not a release tag is refused where it is read.
//
// The version is joined into a file name under the cache and into the
// download URL, so `../` in it writes the downloaded binary -- which is then
// executed -- outside the cache, and asks GitHub for a path the project never
// named.
func TestAPinThatIsNotAReleaseTagIsRefused(t *testing.T) {
	for _, value := range []string{
		"../../../.zshrc",
		"v4.3.3/../../x",
		`v4.3.3\..\x`,
		"latest",
		"4.3.3",
		"v4.3",
		"v4.3.3-beta.1",
		"v4.3.3 ",
	} {
		root := t.TempDir()
		writePins(t, root, "[tools]\ntailwindcss = \""+value+"\"\n")
		if pins, err := ReadPins(root); err == nil {
			t.Errorf("tailwindcss = %q was accepted as %q", value, pins.Tailwind)
		} else if !strings.Contains(err.Error(), "arandu.toml:2") {
			t.Errorf("tailwindcss = %q was refused without its line: %v", value, err)
		}
	}

	root := t.TempDir()
	writePins(t, root, "[tools]\ntailwindcss = \"v4.3.3\"\n")
	if _, err := ReadPins(root); err != nil {
		t.Errorf("the published skeleton's pin was refused: %v", err)
	}
}

// The checksum pin is read, and a value that is not a SHA-256 is refused.
func TestTheChecksumPinIsRead(t *testing.T) {
	sum := strings.Repeat("ab", 32)
	root := t.TempDir()
	writePins(t, root, "[tools]\ntailwindcss = \"v4.4.0\"\ntailwindcss-sha256sums = \""+sum+"\"\n")
	pins, err := ReadPins(root)
	if err != nil {
		t.Fatalf("ReadPins: %v", err)
	}
	if pins.Tailwind != "v4.4.0" || pins.TailwindSums != sum {
		t.Errorf("pins = %+v", pins)
	}

	for _, value := range []string{"abc", strings.Repeat("AB", 32), "sha256:" + sum} {
		root := t.TempDir()
		writePins(t, root, "[tools]\ntailwindcss-sha256sums = \""+value+"\"\n")
		if _, err := ReadPins(root); err == nil {
			t.Errorf("tailwindcss-sha256sums = %q was accepted", value)
		}
	}
}

// A Tool built with a version that escapes the cache directory has no path.
//
// ReadPins is the front door, and this is the one behind it: whatever builds a
// Tool, the cached binary is a file directly in Dir.
func TestTheCachedPathStaysInTheCacheDirectory(t *testing.T) {
	home(t)
	for _, version := range []string{"../../../tmp/x", "v1.2.3/../../../x", "", "v1.2.3/x"} {
		tool := Tailwind("", "")
		tool.Version = version
		if path, err := tool.Path(); err == nil {
			t.Errorf("version %q is cached at %s", version, path)
		}
	}
	dir, err := Dir()
	if err != nil {
		t.Fatal(err)
	}
	path, err := Tailwind("", "").Path()
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(path) != dir {
		t.Errorf("the default version is cached at %s, outside %s", path, dir)
	}
}

// aru ships the digest of every asset the default version publishes.
//
// An asset the platform table can name and the digest table does not know
// would be refused on that platform, which reads as the release being broken.
func TestEveryDefaultAssetHasAShippedDigest(t *testing.T) {
	tool := Tailwind("", "")
	for _, p := range []struct{ goos, goarch, libc string }{
		{"darwin", "arm64", "gnu"}, {"darwin", "amd64", "gnu"},
		{"linux", "arm64", "gnu"}, {"linux", "amd64", "gnu"},
		{"linux", "arm64", "musl"}, {"linux", "amd64", "musl"},
		{"windows", "amd64", "gnu"},
	} {
		asset, ok := tool.asset(p.goos, p.goarch, p.libc)
		if !ok {
			t.Fatalf("%s/%s/%s is not published", p.goos, p.goarch, p.libc)
		}
		if !sha256Hex.MatchString(tool.digests[asset]) {
			t.Errorf("%s has no shipped digest: %q", asset, tool.digests[asset])
		}
	}
	if n := len(tailwindDigests[DefaultTailwindVersion]); n != 7 {
		t.Errorf("%d digests are shipped for the 7 assets of %s", n, DefaultTailwindVersion)
	}
	if Tailwind("v4.0.0", "").digests != nil {
		t.Error("digests were attached to a version aru ships none for")
	}
}

// The default version is checked against the digest aru ships, and the
// release's own checksums file cannot vouch for a replaced binary.
//
// The server plays a release whose binary and sha256sums.txt were both
// replaced, consistently with each other. Checking one against the other --
// all aru did before -- installs it.
func TestTheDefaultVersionIsCheckedAgainstTheShippedDigest(t *testing.T) {
	home(t)
	rel := newRelease(t, []byte("#!/bin/sh\necho replaced\n"))
	tool := rel.tool(Tailwind("", ""))

	if path, err := tool.Ensure(io.Discard); err == nil {
		t.Fatalf("a binary the release replaced together with its checksums file was installed at %s", path)
	} else if !strings.Contains(err.Error(), "does not match its checksum") {
		t.Errorf("the refusal does not say why: %v", err)
	}
	assertNotCached(t, tool)
}

// The default version installs when its bytes are the ones aru ships the
// digest of.
func TestTheDefaultVersionInstallsWhenTheDigestMatches(t *testing.T) {
	home(t)
	binary := []byte("#!/bin/sh\necho tailwind\n")
	rel := newRelease(t, binary)
	tool := rel.tool(Tailwind("", ""))
	tool.digests = map[string]string{rel.asset: digestOf(binary)}

	path, err := tool.Ensure(io.Discard)
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	if got, _ := os.ReadFile(path); string(got) != string(binary) {
		t.Errorf("the cached binary is %q", got)
	}
}

// Another version with nothing pinned is refused before any request is made.
func TestAnotherVersionWithoutAPinIsRefusedBeforeDownloading(t *testing.T) {
	home(t)
	rel := newRelease(t, []byte("binary"))
	tool := rel.tool(Tailwind("v4.4.0", ""))

	_, err := tool.Ensure(io.Discard)
	if err == nil {
		t.Fatal("a version with no digest from outside its release was downloaded")
	}
	for _, want := range []string{"tailwindcss-sha256sums", DefaultTailwindVersion} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not mention %q: %v", want, err)
		}
	}
	if n := rel.requests.Load(); n != 0 {
		t.Errorf("%d requests were made before the refusal", n)
	}
	assertNotCached(t, tool)
}

// Another version is checked through the checksums file whose digest the
// project pinned: installed when the chain holds, refused when the file is
// not the one pinned.
func TestAnotherVersionIsCheckedThroughThePinnedChecksumsFile(t *testing.T) {
	home(t)
	binary := []byte("#!/bin/sh\necho v4.4.0\n")
	rel := newRelease(t, binary)

	if _, err := rel.tool(Tailwind("v4.4.0", strings.Repeat("0", 64))).Ensure(io.Discard); err == nil {
		t.Fatal("a checksums file other than the one pinned was trusted")
	} else if !strings.Contains(err.Error(), "does not match the tailwindcss-sha256sums") {
		t.Errorf("the refusal does not say why: %v", err)
	}

	tool := rel.tool(Tailwind("v4.4.0", digestOf(rel.sums)))
	path, err := tool.Ensure(io.Discard)
	if err != nil {
		t.Fatalf("Ensure with the pinned checksums file: %v", err)
	}
	if got, _ := os.ReadFile(path); string(got) != string(binary) {
		t.Errorf("the cached binary is %q", got)
	}
}

// release is a GitHub release served from a test server: one binary for this
// platform and a sha256sums.txt that lists it.
type release struct {
	server   *httptest.Server
	asset    string
	sums     []byte
	requests atomic.Int64
}

func newRelease(t *testing.T, binary []byte) *release {
	t.Helper()
	asset, ok := Tailwind("", "").asset(runtime.GOOS, runtime.GOARCH, libc())
	if !ok {
		t.Skipf("tailwindcss publishes no binary for %s/%s", runtime.GOOS, runtime.GOARCH)
	}
	r := &release{asset: asset, sums: []byte(digestOf(binary) + "  ./" + asset + "\n")}
	r.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		r.requests.Add(1)
		switch {
		case strings.HasSuffix(req.URL.Path, "/"+asset):
			_, _ = w.Write(binary)
		case strings.HasSuffix(req.URL.Path, "/sha256sums.txt"):
			_, _ = w.Write(r.sums)
		default:
			http.NotFound(w, req)
		}
	}))
	t.Cleanup(r.server.Close)
	return r
}

func (r *release) tool(t Tool) Tool {
	t.host = r.server.URL
	return t
}

func digestOf(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// home points the cache directory at a temporary one.
func home(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)
}

func assertNotCached(t *testing.T, tool Tool) {
	t.Helper()
	path, err := tool.Path()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err == nil {
		t.Errorf("a refused download was cached at %s", path)
	}
}
