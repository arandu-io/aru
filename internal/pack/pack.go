// SPDX-License-Identifier: Unlicense OR MIT

// Package pack turns a compiled native target into the artifact a platform
// installs: an APK, an IPA, an application bundle, a signed executable, or the
// pair of files a browser loads.
//
// It is a library and not a second command. Producing an installable package
// is a step of building one, and a project that had to run one tool to compile
// and another to package would have two answers to "how do I ship this" -- with
// the flags of each spelled differently, and one of them forgotten in a
// pipeline.
//
// The sources under this directory came from an existing packaging tool, at the
// version UPSTREAM.md records, and are kept close to what they were. What
// changed is the shape of the entry point: the tool read its settings from
// command-line flags of its own, and this reads them from [Options], because
// the flags belong to the command the project already types.
package pack

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

// Options is everything a package needs beyond the code itself.
//
// The zero value is not buildable: Package and Target have no sensible default,
// and guessing either produces an artifact somebody has to inspect to find out
// what it is.
type Options struct {
	// Package is the Go package to build, as the go tool would take it.
	Package string

	// Target is the platform: android, ios, tvos, js, windows or macos.
	Target string

	// Arch is the architectures to include. Empty takes the platform's own
	// default set, which is every architecture that platform ships.
	Arch []string

	// Output is where the artifact is written. Empty derives one from the
	// application's name and the target's conventional extension.
	Output string

	// AppID is the identifier the platform files the application under --
	// com.example.app. It is not cosmetic: on a phone it is the identity an
	// upgrade is matched against, and two applications sharing one replace
	// each other.
	AppID string

	// Name is what a person sees under the icon.
	Name string

	// Version is major.minor.patch.code. The last part is the integer an app
	// store orders releases by, and Android refuses an upload that does not
	// increase it.
	Version string

	// Icon is a PNG the packager resizes into every size a platform asks for.
	// Empty ships the platform's placeholder, which is what an application
	// nobody has drawn an icon for should look like.
	Icon string

	// SignKey is the keystore on Android and the provisioning profile on
	// Apple's platforms; SignPass decrypts it. Without them the artifact is
	// unsigned, which installs on a development device and nowhere else.
	SignKey  string
	SignPass string

	// The three notarization fields are Apple's, and all three are required
	// together: an Apple ID, an app-specific password, and the team the
	// certificate belongs to.
	NotaryID     string
	NotaryPass   string
	NotaryTeamID string

	// Schemes are the URL schemes the application answers, and Queries are the
	// packages it may ask Android about. Both are declarations the platform
	// reads out of the manifest rather than behaviour.
	Schemes []string
	Queries []string

	// Args are handed to the application when the platform starts it, for the
	// targets where there is no command line to pass them on.
	Args []string

	// MinSDK and TargetSDK are Android's two floor-and-ceiling numbers. Zero
	// takes the packager's own defaults.
	MinSDK    int
	TargetSDK int

	// BuildMode is exe for something that runs and archive for something
	// another build embeds. Empty is exe.
	BuildMode string

	// LinkMode, Ldflags and Tags reach the Go toolchain unchanged, for the
	// cases a platform needs one and this package has no opinion about.
	LinkMode string
	Ldflags  string
	Tags     string

	// PrintCommands echoes every command run, and KeepWorkdir leaves the
	// temporary tree in place and prints where it is. Both exist for the same
	// moment: a package that built and does not install, where the question is
	// what the tools were actually handed.
	PrintCommands bool
	KeepWorkdir   bool
}

// Targets is every platform this can package for.
//
// Named rather than described, because the set is what a caller validates
// against and a caller that hard-codes its own copy is a caller that disagrees
// after the next one is added.
func Targets() []string {
	return []string{"android", "ios", "tvos", "js", "windows", "macos"}
}

// building serializes the whole package.
//
// The sources this was assembled from read their settings from package-level
// variables, and keeping them that way is what makes a diff against the
// original readable -- which is the whole reason a fork is affordable. The cost
// is that two packages cannot be built at once in one process, and the lock is
// what turns that from a race into a wait.
var building sync.Mutex

// Build produces the artifact and returns where it went.
//
// Progress goes to stderr as the underlying tools write it, because packaging
// runs minutes rather than seconds and a command that prints nothing for four
// minutes reads as one that hung.
func Build(o Options, stdout, stderr io.Writer) error {
	if err := o.validate(); err != nil {
		return err
	}

	building.Lock()
	defer building.Unlock()

	restore := o.apply(stdout, stderr)
	defer restore()

	info, err := newBuildInfo(o.Package)
	if err != nil {
		return err
	}

	work, err := os.MkdirTemp("", "arandu-pack-")
	if err != nil {
		return err
	}
	if o.KeepWorkdir {
		fmt.Fprintf(stderr, "work directory kept at %s\n", work)
	} else {
		defer os.RemoveAll(work)
	}

	switch o.Target {
	case "js":
		return buildJS(info)
	case "ios", "tvos":
		return buildIOS(work, o.Target, info)
	case "android":
		return buildAndroid(work, info)
	case "windows":
		return buildWindows(work, info)
	case "macos":
		return buildMac(work, info)
	}
	return fmt.Errorf("pack: %s is not a platform this packages for", o.Target)
}

// validate refuses what cannot produce an artifact, before any tool runs.
//
// Up front rather than deep inside a toolchain: the platform SDKs report a
// missing identifier as a manifest error in a generated file, and whoever reads
// that goes looking at the generated file.
func (o Options) validate() error {
	if o.Package == "" {
		return errors.New("pack: no package to build")
	}
	if o.Target == "" {
		return fmt.Errorf("pack: no target; one of %s", strings.Join(Targets(), ", "))
	}
	if !known(o.Target, Targets()) {
		return fmt.Errorf("pack: %s is not a platform this packages for; one of %s", o.Target, strings.Join(Targets(), ", "))
	}
	if o.BuildMode != "" && o.BuildMode != "exe" && o.BuildMode != "archive" {
		return fmt.Errorf("pack: %s is not a build mode; exe or archive", o.BuildMode)
	}
	if o.SignPass != "" && o.SignKey == "" {
		return errors.New("pack: a signing password was given with no key to decrypt")
	}

	// Notarization needs all three or none. Two of them produce a submission
	// Apple rejects after the upload, which is the slowest way to find out.
	notary := []string{o.NotaryID, o.NotaryPass, o.NotaryTeamID}
	given := 0
	for _, value := range notary {
		if value != "" {
			given++
		}
	}
	if given != 0 && given != len(notary) {
		return errors.New("pack: notarization needs the Apple ID, the app-specific password and the team id together")
	}
	return nil
}

// known reports whether value is in the set.
func known(value string, set []string) bool {
	for _, candidate := range set {
		if candidate == value {
			return true
		}
	}
	return false
}

// apply writes the options into the package-level variables the packaging code
// reads, and returns the function that puts them back.
//
// Restoring matters because this runs inside a long-lived process: a value left
// behind by one build is a value the next one inherits without anybody asking
// for it.
func (o Options) apply(stdout, stderr io.Writer) func() {
	previous := struct {
		target, archNames, buildMode, destPath, appID, name, version string
		linkMode, extraLdflags, extraTags, iconPath                  string
		signKey, signPass                                            string
		notaryID, notaryPass, notaryTeamID                           string
		schemes, pkgQueries                                          string
		appArgs                                                      []string
		minsdk, targetsdk                                            int
		printCommands, keepWorkdir                                   bool
		out, errs                                                    io.Writer
	}{
		*target, *archNames, *buildMode, *destPath, *appID, *name, *version,
		*linkMode, *extraLdflags, *extraTags, *iconPath,
		*signKey, *signPass,
		*notaryID, *notaryPass, *notaryTeamID,
		*schemes, *pkgQueries,
		appArgs,
		*minsdk, *targetsdk,
		*printCommands, *keepWorkdir,
		output, errput,
	}

	*target = o.Target
	*archNames = strings.Join(o.Arch, ",")
	*buildMode = or(o.BuildMode, "exe")
	*destPath = o.Output
	*appID = o.AppID
	*name = o.Name
	*version = or(o.Version, "1.0.0.1")
	*linkMode = o.LinkMode
	*extraLdflags = o.Ldflags
	*extraTags = o.Tags
	*iconPath = o.Icon
	*signKey = o.SignKey
	*signPass = o.SignPass
	*notaryID = o.NotaryID
	*notaryPass = o.NotaryPass
	*notaryTeamID = o.NotaryTeamID
	*schemes = strings.Join(o.Schemes, ",")
	*pkgQueries = strings.Join(o.Queries, ",")
	*minsdk = o.MinSDK
	*targetsdk = o.TargetSDK
	appArgs = o.Args
	*printCommands = o.PrintCommands
	*keepWorkdir = o.KeepWorkdir
	output, errput = stdout, stderr

	return func() {
		*target, *archNames, *buildMode = previous.target, previous.archNames, previous.buildMode
		*destPath, *appID, *name, *version = previous.destPath, previous.appID, previous.name, previous.version
		*linkMode, *extraLdflags, *extraTags, *iconPath = previous.linkMode, previous.extraLdflags, previous.extraTags, previous.iconPath
		*signKey, *signPass = previous.signKey, previous.signPass
		*notaryID, *notaryPass, *notaryTeamID = previous.notaryID, previous.notaryPass, previous.notaryTeamID
		*schemes, *pkgQueries = previous.schemes, previous.pkgQueries
		appArgs = previous.appArgs
		*minsdk, *targetsdk = previous.minsdk, previous.targetsdk
		*printCommands, *keepWorkdir = previous.printCommands, previous.keepWorkdir
		output, errput = previous.out, previous.errs
	}
}

// or answers value, or fallback when value is empty.
func or(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

// The settings the packaging sources read. They were command-line flags in the
// tool this came from; they are variables here, written by Options.apply, so
// that those sources stay as close to their originals as they can.
var (
	target        = new(string)
	archNames     = new(string)
	minsdk        = new(int)
	targetsdk     = new(int)
	buildMode     = new(string)
	destPath      = new(string)
	appID         = new(string)
	name          = new(string)
	version       = new(string)
	printCommands = new(bool)
	keepWorkdir   = new(bool)
	linkMode      = new(string)
	extraLdflags  = new(string)
	extraTags     = new(string)
	iconPath      = new(string)
	signKey       = new(string)
	signPass      = new(string)
	notaryID      = new(string)
	notaryPass    = new(string)
	notaryTeamID  = new(string)
	schemes       = new(string)
	pkgQueries    = new(string)

	// appArgs is what the application is started with. It was read from the
	// command line, and is a variable here for the same reason as the rest.
	appArgs []string
)

// Where the packaging sources write. They printed to the process's own streams;
// here they print to the ones the command was given, so a test can read them
// and a caller can put them somewhere else.
var (
	output io.Writer = os.Stdout
	errput io.Writer = os.Stderr
)

// runCmdRaw runs one command and answers what it printed.
func runCmdRaw(cmd *exec.Cmd) ([]byte, error) {
	if *printCommands {
		fmt.Fprintf(output, "%s\n", strings.Join(cmd.Args, " "))
	}
	out, err := cmd.Output()
	if err == nil {
		return out, nil
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return nil, fmt.Errorf("%s failed: %s%s", strings.Join(cmd.Args, " "), out, exit.Stderr)
	}
	return nil, err
}

// runCmd runs one command and answers its output as trimmed text.
func runCmd(cmd *exec.Cmd) (string, error) {
	out, err := runCmdRaw(cmd)
	return string(bytes.TrimSpace(out)), err
}

// copyFile copies src to dst.
func copyFile(dst, src string) (err error) {
	r, err := os.Open(src)
	if err != nil {
		return err
	}
	defer r.Close()

	w, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer func() {
		if cerr := w.Close(); err == nil {
			err = cerr
		}
	}()

	_, err = io.Copy(w, r)
	return err
}

// arch is one architecture, under the three names the platform toolchains give
// it. They differ, and the difference is not derivable: arm64 is arm64 to
// Apple, arm64-v8a to Android's packaging and aarch64-linux-android to its
// compiler.
type arch struct {
	iosArch   string
	jniArch   string
	clangArch string
}

var allArchs = map[string]arch{
	"arm": {
		iosArch:   "armv7",
		jniArch:   "armeabi-v7a",
		clangArch: "armv7a-linux-androideabi",
	},
	"arm64": {
		iosArch:   "arm64",
		jniArch:   "arm64-v8a",
		clangArch: "aarch64-linux-android",
	},
	"386": {
		iosArch:   "i386",
		jniArch:   "x86",
		clangArch: "i686-linux-android",
	},
	"amd64": {
		iosArch:   "x86_64",
		jniArch:   "x86_64",
		clangArch: "x86_64-linux-android",
	},
}

// unusedGuard keeps the compiler honest about the symbols the packaging sources
// reach for but this file would otherwise appear to leave behind.
var _ = filepath.Join
