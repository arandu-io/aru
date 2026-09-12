package pack

import (
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/tools/go/packages"
)

// signPassEnv is where the signing key's password is read from when it was not
// passed in.
//
// It is named after this project because it is this project's interface: a
// variable in the environment is read by whoever sets it, and one carrying
// somebody else's name is one nobody can look up.
const signPassEnv = "ARANDU_SIGNPASS"

type buildInfo struct {
	appID          string
	archs          []string
	ldflags        string
	minsdk         int
	targetsdk      int
	name           string
	pkgDir         string
	pkgPath        string
	iconPath       string
	tags           string
	target         string
	version        Semver
	key            string
	password       string
	notaryAppleID  string
	notaryPassword string
	notaryTeamID   string
	schemes        []string
	packageQueries []string

	// runtime is the package the application's window is opened by, found in
	// the graph below.
	runtime runtimePackage

	// graph is the application's package graph as this target sees it, read
	// once. Android reads the jars and permissions out of it, the browser
	// target the JavaScript beside each package, and both used to ask the go
	// tool a second time for what is already here.
	graph *packages.Package
}

type Semver struct {
	Major, Minor, Patch int
	VersionCode         uint32
}

func newBuildInfo(pkgPath string) (*buildInfo, error) {
	pkgMetadata, err := getPkgMetadata(pkgPath)
	if err != nil {
		return nil, err
	}
	appID := getAppID(pkgMetadata)
	appIcon := filepath.Join(pkgMetadata.Dir, "appicon.png")
	if *iconPath != "" {
		appIcon = *iconPath
	}
	appName := getPkgName(pkgMetadata)
	if *name != "" {
		appName = *name
	}
	ver, err := parseSemver(*version)
	if err != nil {
		return nil, err
	}
	// The environment is the other way in for the signing key's password, so a
	// pipeline can hand it over without writing it into a command line that
	// every process on the machine can read.
	sp := *signPass
	if sp == "" {
		sp = os.Getenv(signPassEnv)
	}
	graph, err := loadPackageGraph(pkgPath)
	if err != nil {
		return nil, err
	}
	runtimePkg, err := findRuntimePackage(graph)
	if err != nil {
		return nil, err
	}
	if err := verifyLinkedSymbols(runtimePkg.path, runtimePkg.dir); err != nil {
		return nil, err
	}
	bi := &buildInfo{
		appID:          appID,
		archs:          getArchs(),
		ldflags:        getLdFlags(appID, runtimePkg.path),
		minsdk:         *minsdk,
		targetsdk:      *targetsdk,
		name:           appName,
		pkgDir:         pkgMetadata.Dir,
		pkgPath:        pkgPath,
		iconPath:       appIcon,
		tags:           *extraTags,
		target:         *target,
		version:        ver,
		key:            *signKey,
		password:       sp,
		notaryAppleID:  *notaryID,
		notaryPassword: *notaryPass,
		notaryTeamID:   *notaryTeamID,
		schemes:        getCommaList(*schemes),
		packageQueries: getCommaList(*pkgQueries),
		runtime:        runtimePkg,
		graph:          graph,
	}
	return bi, nil
}

// UppercaseName returns a string with its first rune in uppercase.
func UppercaseName(name string) string {
	ch, w := utf8.DecodeRuneInString(name)
	return string(unicode.ToUpper(ch)) + name[w:]
}

func (s Semver) String() string {
	return fmt.Sprintf("%d.%d.%d.%d", s.Major, s.Minor, s.Patch, s.VersionCode)
}

func (s Semver) StringCompact() string {
	// Used to meet CFBundleShortVersionString format.
	return fmt.Sprintf("%d.%d.%d", s.Major, s.Minor, s.Patch)
}

func parseSemver(v string) (Semver, error) {
	var sv Semver
	_, err := fmt.Sscanf(v, "%d.%d.%d.%d", &sv.Major, &sv.Minor, &sv.Patch, &sv.VersionCode)
	if err != nil || sv.String() != v {
		return Semver{}, fmt.Errorf("invalid semver: %q (must match major.minor.patch.versioncode)", v)
	}
	return sv, nil
}

func getArchs() []string {
	if *archNames != "" {
		return strings.Split(*archNames, ",")
	}
	switch *target {
	case "js":
		return []string{"wasm"}
	case "ios", "tvos":
		// Only 64-bit support.
		return []string{"arm64", "amd64"}
	case "android":
		return []string{"arm", "arm64", "386", "amd64"}
	case "windows":
		goarch := os.Getenv("GOARCH")
		if goarch == "" {
			goarch = runtime.GOARCH
		}
		return []string{goarch}
	case "macos":
		return []string{"arm64", "amd64"}
	default:
		// TODO: Add flag tests.
		panic("The target value has already been validated, this will never execute.")
	}
}

func getLdFlags(appID, runtimePath string) string {
	var ldflags []string
	if extra := *extraLdflags; extra != "" {
		ldflags = append(ldflags, strings.Split(extra, " ")...)
	}
	// Pass appID along, to be used for logging on platforms like Android.
	ldflags = append(ldflags, fmt.Sprintf("-X %s.ID=%s", runtimePath, appID))
	// Pass along all remaining arguments to the app. They came from the command
	// line when this was a command of its own; as a library they arrive through
	// Options, and reading the process's arguments here panicked on the empty
	// slice the moment it was called from anywhere else.
	if len(appArgs) > 0 {
		ldflags = append(ldflags, fmt.Sprintf("-X %s.extraArgs=%s", runtimePath, strings.Join(appArgs, "|")))
	}
	if m := *linkMode; m != "" {
		ldflags = append(ldflags, "-linkmode="+m)
	}
	return strings.Join(ldflags, " ")
}

func getCommaList(s string) (list []string) {
	for _, v := range strings.Split(s, ",") {
		if v := strings.TrimSpace(v); v != "" {
			list = append(list, v)
		}
	}
	return list
}

type packageMetadata struct {
	PkgPath string
	Dir     string
}

func getPkgMetadata(pkgPath string) (*packageMetadata, error) {
	pkgImportPath, err := runCmd(exec.Command("go", "list", "-tags", *extraTags, "-f", "{{.ImportPath}}", pkgPath))
	if err != nil {
		return nil, err
	}
	pkgDir, err := runCmd(exec.Command("go", "list", "-tags", *extraTags, "-f", "{{.Dir}}", pkgPath))
	if err != nil {
		return nil, err
	}
	return &packageMetadata{
		PkgPath: pkgImportPath,
		Dir:     pkgDir,
	}, nil
}

func getAppID(pkgMetadata *packageMetadata) string {
	if *appID != "" {
		return *appID
	}
	elems := strings.Split(pkgMetadata.PkgPath, "/")
	domain := strings.Split(elems[0], ".")
	name := ""
	if len(elems) > 1 {
		name = "." + elems[len(elems)-1]
	}
	if len(elems) < 2 && len(domain) < 2 {
		name = "." + domain[0]
		domain[0] = "localhost"
	} else {
		for i := range len(domain) / 2 {
			opp := len(domain) - 1 - i
			domain[i], domain[opp] = domain[opp], domain[i]
		}
	}

	pkgDomain := strings.Join(domain, ".")
	appid := []rune(pkgDomain + name)

	// a Java-language-style package name may contain upper- and lower-case
	// letters and underscores with individual parts separated by '.'.
	// https://developer.android.com/guide/topics/manifest/manifest-element
	for i, c := range appid {
		if !('a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' ||
			c == '_' || c == '.') {
			appid[i] = '_'
		}
	}
	return string(appid)
}

func getPkgName(pkgMetadata *packageMetadata) string {
	return path.Base(pkgMetadata.PkgPath)
}
