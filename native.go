package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

// nativePackage is where a project's native target lives.
//
// One place, and not a flag: the module that publishes the target writes it
// here, so a project that has one has it here and a project that does not has
// nowhere else it might be hiding.
const nativePackage = "./cmd/native"

// nativeDir is the same path as a directory, for the check that it exists.
const nativeDir = "cmd/native"

// nativeRun builds the native target and runs it.
//
// It builds first and runs the result, rather than using `go run`: a graphical
// program is stopped by closing its window, and `go run` leaves the child alive
// on some platforms while reporting that it exited. Running the binary directly
// means the process somebody sees in their dock is the process this started.
func nativeRun(args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("native:run", flag.ContinueOnError)
	flags.SetOutput(stderr)
	server := flags.String("server", "", "the address of the running application (default: the target's own)")
	dark := flags.Bool("dark", false, "open with the dark palette")
	if err := flags.Parse(args); err != nil {
		return fmt.Errorf("native:run: %w", err)
	}

	root, err := nativeRoot()
	if err != nil {
		return err
	}

	binary := filepath.Join(root, "bin", "native")
	if err := compileNative(root, binary, runtime.GOOS, runtime.GOARCH, stdout, stderr); err != nil {
		return err
	}

	var forwarded []string
	if *server != "" {
		forwarded = append(forwarded, "-server", *server)
	}
	if *dark {
		forwarded = append(forwarded, "-dark")
	}

	cmd := exec.Command(binary, append(forwarded, flags.Args()...)...)
	cmd.Dir = root
	cmd.Stdin = os.Stdin
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		// The window was closed with a failure, or never opened. The target
		// already said which to standard error, so repeating it here would
		// print the same sentence twice.
		return fmt.Errorf("the native application exited with an error")
	}
	return nil
}

// nativeBuild compiles the native target for one platform.
func nativeBuild(args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("native:build", flag.ContinueOnError)
	flags.SetOutput(stderr)
	target := flags.String("target", "", "the platform to build for (default: this machine)")
	output := flags.String("output", "", "where to write the artifact (default: bin/native-<target>)")
	list := flags.Bool("list", false, "list the platforms this can build for, and what each needs")
	pack := flags.Bool("package", false, "write the artifact the platform installs rather than a bare binary")
	appID := flags.String("appid", "", "the identifier the platform files the application under")
	appName := flags.String("name", "", "what a person sees under the icon (default: the project directory)")
	version := flags.String("version", "", "major.minor.patch.code (default: 1.0.0.1)")
	icon := flags.String("icon", "", "a PNG the packager resizes into every size the platform asks for")
	signKey := flags.String("signkey", "", "the keystore or provisioning profile to sign with")
	signPass := flags.String("signpass", "", "the password that decrypts the signing key")
	verbose := flags.Bool("x", false, "print the commands the packager runs")
	if err := flags.Parse(args); err != nil {
		return fmt.Errorf("native:build: %w", err)
	}

	if *list {
		return listNativeTargets(stdout)
	}

	// The project is checked before the platform. Both refusals are true at
	// once when somebody runs this in the wrong directory, and "you are not in
	// a project" is the one that explains the other.
	root, err := nativeRoot()
	if err != nil {
		return err
	}

	name := *target
	if name == "" {
		name = runtime.GOOS + "/" + runtime.GOARCH
	}

	platform, ok := nativeTargets[name]
	if !ok {
		return fmt.Errorf("native:build: %s is not a platform this builds for. `aru native:build -list` says which are", name)
	}
	// Two platforms have no bare binary anybody can install, so asking for one
	// is asking for a file that cannot be used. Packaging is not a flag there.
	packaging := *pack || platform.installable
	if packaging && platform.packages == "" {
		return fmt.Errorf("native:build: %s has no installable artifact; its binary is what ships", name)
	}

	if packaging {
		return packageNative(root, packageOptions{
			platform: platform.packages,
			arch:     archOf(name),
			output:   *output,
			appID:    *appID,
			appName:  *appName,
			version:  *version,
			icon:     *icon,
			signKey:  *signKey,
			signPass: *signPass,
			verbose:  *verbose,
		}, stdout, stderr)
	}

	binary := *output
	if binary == "" {
		binary = filepath.Join("bin", "native-"+strings.ReplaceAll(name, "/", "-")+platform.extension)
	}
	if !filepath.IsAbs(binary) {
		binary = filepath.Join(root, binary)
	}

	goos, goarch, _ := strings.Cut(name, "/")
	if err := compileNative(root, binary, goos, goarch, stdout, stderr); err != nil {
		return err
	}

	relative, err := filepath.Rel(root, binary)
	if err != nil {
		relative = binary
	}

	sum, size, err := checksum(binary)
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "%s  %s  %.1f MB\n%s\n", relative, name, float64(size)/(1<<20), sum)
	return nil
}

// compileNative runs the compiler for one platform.
//
// cgo is on for every platform that draws to a window, and that is not a choice
// this makes: the window, the input and the GPU are reached through the
// operating system's own libraries, and there is no pure-Go path to any of
// them. The browser is the exception, because there the browser is the window.
func compileNative(root, output, goos, goarch string, stdout, stderr io.Writer) error {
	if err := os.MkdirAll(filepath.Dir(output), 0o755); err != nil {
		return err
	}

	cgo := "0"
	if cgoFor(goos) {
		cgo = "1"
	}

	cmd := goCommand("build", "-trimpath", "-o", output, nativePackage)
	cmd.Dir = root
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.Env = append(cmd.Env, "CGO_ENABLED="+cgo, "GOOS="+goos, "GOARCH="+goarch)

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("the native target did not compile for %s/%s", goos, goarch)
	}
	return nil
}

// cgoFor reports whether a platform is built with cgo enabled.
//
// Everywhere but the browser, and that is not a preference: the window, the
// input and the GPU are reached through the operating system's own libraries,
// and a target built without cgo compiles and then opens nothing. In a browser
// the browser is the window, and there is no library to reach.
func cgoFor(goos string) bool { return goos != "js" }

// nativeRoot answers the project root, and refuses when the project has no
// native target.
//
// The refusal says how to get one, because the failure otherwise is the
// compiler reporting that a package does not exist -- which reads as a broken
// checkout rather than as a target nobody has published yet.
func nativeRoot() (string, error) {
	root, err := projectRoot()
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(filepath.Join(root, nativeDir)); err != nil {
		return "", fmt.Errorf("this project has no native target: %s does not exist. `aru vendor:publish` writes one, from a module that offers it", nativeDir)
	}
	return root, nil
}

// nativeTarget is one platform, and what building for it produces.
type nativeTarget struct {
	// needs is the toolchain a platform requires beyond the Go compiler. A
	// platform is listed either way: a name that is absent reads as a platform
	// nobody has thought about, and a name with its price written next to it
	// is a decision somebody can act on.
	//
	// It no longer means the platform cannot be built. It means a person has
	// to have installed something first, and the failure when they have not is
	// the toolchain's own, which says what is missing better than a guess here
	// would.
	needs string
	// extension is what a plain binary is called on that platform.
	extension string
	// packages is the platform name the packager knows, for the targets where
	// an installable artifact is a different thing from the binary. Empty means
	// the binary is the artifact.
	packages string
	// installable is true where a plain binary cannot be installed at all, so
	// packaging is not an option but the only way to produce anything usable.
	installable bool
	// note is what a person deciding between platforms needs to know.
	note string
}

// nativeTargets is every platform the native target can be built for.
var nativeTargets = map[string]nativeTarget{
	"darwin/arm64":  {packages: "macos", note: "Apple silicon; -package writes an application bundle"},
	"darwin/amd64":  {packages: "macos", note: "Intel Macs; -package writes an application bundle"},
	"windows/amd64": {extension: ".exe", packages: "windows", note: "Windows 10 and later; -package embeds the icon and metadata"},
	"windows/arm64": {extension: ".exe", packages: "windows", note: "Windows on ARM"},
	"linux/amd64":   {note: "needs the X11, Wayland and Vulkan development packages"},
	"linux/arm64":   {note: "the same packages, for ARM"},
	"js/wasm":       {extension: ".wasm", packages: "js", note: "preview in a browser; not a second way to do web"},
	"android/arm64": {needs: "the Android SDK, the NDK and a JDK", packages: "android", installable: true, note: "writes an APK"},
	"android/amd64": {needs: "the same, for an emulator", packages: "android", installable: true, note: "writes an APK for an emulator"},
	"ios/arm64":     {needs: "Xcode and a provisioning profile", packages: "ios", installable: true, note: "writes an IPA"},
	"ios/amd64":     {needs: "Xcode", packages: "ios", installable: true, note: "writes an app for the simulator"},
}

// listNativeTargets prints the platforms and what each one costs.
func listNativeTargets(stdout io.Writer) error {
	names := make([]string, 0, len(nativeTargets))
	for name := range nativeTargets {
		names = append(names, name)
	}
	sort.Strings(names)

	fmt.Fprintln(stdout, "platforms the native target builds for:")
	for _, name := range names {
		target := nativeTargets[name]
		mark := "  "
		if target.needs != "" {
			mark = "! "
		}
		fmt.Fprintf(stdout, "  %s%-14s %s\n", mark, name, target.note)
	}
	fmt.Fprintln(stdout, "\n  ! needs a toolchain installed first; the tool says which when it is missing")
	return nil
}
