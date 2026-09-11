package main

import (
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/arandu-io/aru/internal/pack"
)

// packageOptions is what a project asks for when it wants the artifact a
// platform installs rather than a binary.
type packageOptions struct {
	// platform is the packager's name for the target, and arch the
	// architecture within it.
	platform string
	arch     string

	// output is where the artifact goes. Empty derives one from the project.
	output string

	appID    string
	appName  string
	version  string
	icon     string
	signKey  string
	signPass string
	verbose  bool
}

// packageNative writes the artifact the platform installs.
//
// The identifier is filled in when the project did not give one, because every
// platform requires it and none of them accepts a blank. What is derived from
// the project's own name is a development identifier, and it is announced as
// one: shipping under a guessed identifier means an upgrade that installs
// beside the application instead of replacing it.
func packageNative(root string, o packageOptions, stdout, stderr io.Writer) error {
	project := filepath.Base(root)

	appID := o.appID
	if appID == "" {
		appID = developmentID(project)
		fmt.Fprintf(stderr, "no -appid given; packaging under %s, which is for development and not for a store\n", appID)
	}

	appName := o.appName
	if appName == "" {
		appName = project
	}

	output := o.output
	if output == "" {
		output = filepath.Join(root, "bin", appName+artifactSuffix(o.platform))
	}

	options := pack.Options{
		Package:       nativePackage,
		Target:        o.platform,
		Output:        output,
		AppID:         appID,
		Name:          appName,
		Version:       o.version,
		Icon:          o.icon,
		SignKey:       o.signKey,
		SignPass:      o.signPass,
		PrintCommands: o.verbose,
	}
	if o.arch != "" {
		options.Arch = []string{o.arch}
	}

	if err := pack.Build(options, stdout, stderr); err != nil {
		return fmt.Errorf("native:build: %w", err)
	}

	where := output
	if relative, err := filepath.Rel(root, output); err == nil {
		where = relative
	}
	fmt.Fprintf(stdout, "%s  %s\n", where, o.platform)
	return nil
}

// artifactSuffix is what a platform calls the thing it installs.
//
// A destination is always passed, and never left for the packager to choose:
// its own default writes beside the source it was given, which puts a bundle's
// innards in the directory the screens are written in. Once there, the next
// build compiles them as Go files.
func artifactSuffix(platform string) string {
	switch platform {
	case "macos":
		return ".app"
	case "windows":
		return ".exe"
	case "android":
		return ".apk"
	case "ios", "tvos":
		return ".ipa"
	}
	return ""
}

// developmentID answers the identifier used when a project did not choose one.
//
// The reverse-domain shape is what every platform expects, and the invalid
// characters are replaced rather than dropped: a name reduced to nothing by
// stripping produces an identifier of "com.example.", which fails deep inside
// a manifest parser.
func developmentID(project string) string {
	cleaned := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			return r
		case r >= 'A' && r <= 'Z':
			return r + ('a' - 'A')
		}
		return -1
	}, project)

	if cleaned == "" || (cleaned[0] >= '0' && cleaned[0] <= '9') {
		// An identifier segment cannot start with a digit, and an empty one is
		// not a segment at all.
		cleaned = "app" + cleaned
	}
	return "dev.local." + cleaned
}

// archOf answers the architecture half of an os/arch target name.
func archOf(target string) string {
	_, arch, found := strings.Cut(target, "/")
	if !found {
		return ""
	}
	return arch
}
