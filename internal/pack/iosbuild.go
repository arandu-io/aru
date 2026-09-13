package pack

import (
	"archive/zip"
	"bytes"
	"crypto/sha1"
	"encoding/base64"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"text/template"
	"time"

	"golang.org/x/sync/errgroup"
)

const (
	// The window backend is written against the scene API, which arrived in
	// iOS 13. Declaring anything older is a claim the compiler checks: with
	// warnings promoted to errors, every use of a scene is an error, and the
	// whole target fails after building the Go half -- which is the slowest
	// possible way to find out that a number in this file disagrees with the
	// code it describes.
	minIOSVersion = 13
	// Some Metal features require tvOS 11.
	minTVOSVersion = 11
	// Metal is available from iOS 8 on devices, yet from version 13 on the
	// simulator.
	minSimulatorVersion = 13
)

func buildIOS(tmpDir, target string, bi *buildInfo) error {
	// The floor is applied here rather than at each site that reads it. Two of
	// the four already did, and the two that did not handed the compiler
	// -miphoneos-version-min=0.0, which it refuses as an invalid version --
	// after building everything else.
	if bi.minsdk == 0 {
		switch {
		case target == "tvos":
			bi.minsdk = minTVOSVersion
		case iosSimulatorBuild(bi.archs):
			bi.minsdk = minSimulatorVersion
		default:
			bi.minsdk = minIOSVersion
		}
	}

	appName := bi.name
	switch *buildMode {
	case "archive":
		framework := *destPath
		if framework == "" {
			framework = fmt.Sprintf("%s.framework", UppercaseName(appName))
		}
		return archiveIOS(tmpDir, target, framework, bi)
	case "exe":
		out := *destPath
		if out == "" {
			out = appName + ".ipa"
		}
		forDevice := strings.HasSuffix(out, ".ipa")
		// Filter out unsupported architectures.
		for i := len(bi.archs) - 1; i >= 0; i-- {
			switch bi.archs[i] {
			case "arm", "arm64":
				if forDevice {
					continue
				}
			case "386", "amd64", "simarm64":
				if !forDevice {
					continue
				}
			}

			bi.archs = slices.Delete(bi.archs, i, i+1)
		}
		if !forDevice && !strings.HasSuffix(out, ".app") {
			return fmt.Errorf("the specified output directory %q does not end in .app or .ipa", out)
		}
		if !forDevice {
			return exeIOS(tmpDir, target, out, bi)
		}
		payload := filepath.Join(tmpDir, "Payload")
		appDir := filepath.Join(payload, appName+".app")
		if err := os.MkdirAll(appDir, 0o755); err != nil {
			return err
		}
		if err := exeIOS(tmpDir, target, appDir, bi); err != nil {
			return err
		}

		embedded := filepath.Join(appDir, "embedded.mobileprovision")

		var provisions []string
		if bi.key != "" {
			if ext := filepath.Ext(bi.key); ext != ".mobileprovision" && ext != ".provisionprofile" {
				return fmt.Errorf("sign: -signkey specifies an Apple provisioning profile, but %q does not end in .mobileprovision or .provisionprofile", bi.key)
			}
			provisions = []string{bi.key}
		} else {
			home, err := os.UserHomeDir()
			if err != nil {
				return err
			}

			provisions, err = iosProvisioningProfiles(home)
			if err != nil {
				return err
			}
		}

		if err := signApple(bi.appID, tmpDir, embedded, appDir, provisions); err != nil {
			return err
		}
		return zipDir(out, tmpDir, "Payload")
	default:
		panic("unreachable")
	}
}

// signApple is shared between iOS and macOS.
func signApple(appID, tmpDir, embedded, app string, provisions []string) error {
	provInfo := filepath.Join(tmpDir, "provision.plist")
	var avail []string
	var profileErrors []error
	identities, err := installedCodeSigningIdentities()
	if err != nil {
		return err
	}
	matchedBundle := false
	for _, prov := range provisions {
		// Decode the provision file to a plist.
		_, err = runCmd(exec.Command("security", "cms", "-D", "-i", prov, "-o", provInfo))
		if err != nil {
			profileErrors = append(profileErrors, fmt.Errorf("decode %q: %w", prov, err))
			continue
		}
		expUnix, err := runCmd(exec.Command("/usr/libexec/PlistBuddy", "-c", "Print:ExpirationDate", provInfo))
		if err != nil {
			profileErrors = append(profileErrors, fmt.Errorf("read expiration from %q: %w", prov, err))
			continue
		}
		exp, err := time.Parse(time.UnixDate, expUnix)
		if err != nil {
			profileErrors = append(profileErrors, fmt.Errorf("parse expiration from %q: %w", prov, err))
			continue
		}
		if exp.Before(time.Now()) {
			continue
		}
		appIDPrefix, err := runCmd(exec.Command("/usr/libexec/PlistBuddy", "-c", "Print:ApplicationIdentifierPrefix:0", provInfo))
		if err != nil {
			profileErrors = append(profileErrors, fmt.Errorf("read application identifier prefix from %q: %w", prov, err))
			continue
		}

		// iOS/macOS Catalyst
		provAppIDSearchKey := "Print:Entitlements:application-identifier"
		if filepath.Ext(prov) == ".provisionprofile" {
			// macOS
			provAppIDSearchKey = "Print:Entitlements:com.apple.application-identifier"
		}
		provAppID, err := runCmd(exec.Command("/usr/libexec/PlistBuddy", "-c", provAppIDSearchKey, provInfo))
		if err != nil {
			profileErrors = append(profileErrors, fmt.Errorf("read application identifier from %q: %w", prov, err))
			continue
		}
		expAppID := fmt.Sprintf("%s.%s", appIDPrefix, appID)
		avail = append(avail, provAppID)
		if expAppID != provAppID {
			continue
		}
		matchedBundle = true

		profile, err := os.ReadFile(provInfo)
		if err != nil {
			profileErrors = append(profileErrors, fmt.Errorf("read decoded profile %q: %w", prov, err))
			continue
		}
		certificateIDs, err := appleProfileCertificateIDs(profile)
		if err != nil {
			profileErrors = append(profileErrors, fmt.Errorf("read developer certificates from %q: %w", prov, err))
			continue
		}
		identity, found := matchingAppleIdentity(certificateIDs, identities)
		if !found {
			continue
		}

		// Copy provisioning file.
		if err := copyFile(embedded, prov); err != nil {
			return err
		}
		entitlements, err := runCmd(exec.Command("/usr/libexec/PlistBuddy", "-x", "-c", "Print:Entitlements", provInfo))
		if err != nil {
			profileErrors = append(profileErrors, fmt.Errorf("read entitlements from %q: %w", prov, err))
			continue
		}
		entFile := filepath.Join(tmpDir, "entitlements.plist")
		if err := os.WriteFile(entFile, []byte(entitlements), 0o660); err != nil {
			return err
		}
		_, err = runCmd(exec.Command(
			"codesign",
			"--sign", identity,
			"--deep",
			"--force",
			"--options", "runtime",
			"--entitlements",
			entFile,
			app))
		return err
	}
	var result error
	if matchedBundle {
		result = fmt.Errorf("sign: no installed code-signing identity matches the provisioning profiles for bundle id %q", appID)
	} else {
		result = fmt.Errorf("sign: no valid provisioning profile found for bundle id %q among %v", appID, avail)
	}
	if len(profileErrors) > 0 {
		return errors.Join(result, errors.Join(profileErrors...))
	}
	return result
}

// iosProvisioningProfiles returns profiles from both locations used by Xcode.
//
// Older Xcode releases installed profiles under MobileDevice. Current releases
// manage them under Developer/Xcode/UserData instead, and looking in only the
// former makes a configured account indistinguishable from no account at all.
func iosProvisioningProfiles(home string) ([]string, error) {
	patterns := []string{
		filepath.Join(home, "Library", "MobileDevice", "Provisioning Profiles", "*.mobileprovision"),
		filepath.Join(home, "Library", "Developer", "Xcode", "UserData", "Provisioning Profiles", "*.mobileprovision"),
	}

	var profiles []string
	for _, pattern := range patterns {
		found, err := filepath.Glob(pattern)
		if err != nil {
			return nil, err
		}
		profiles = append(profiles, found...)
	}
	slices.Sort(profiles)
	return slices.Compact(profiles), nil
}

// installedCodeSigningIdentities returns the certificate fingerprints whose
// private keys are available to codesign.
func installedCodeSigningIdentities() (map[string]struct{}, error) {
	listed, err := runCmd(exec.Command("security", "find-identity", "-v", "-p", "codesigning"))
	if err != nil {
		return nil, err
	}
	return parseCodeSigningIdentities(listed), nil
}

func parseCodeSigningIdentities(listed string) map[string]struct{} {
	identities := make(map[string]struct{})
	for _, line := range strings.Split(listed, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 || len(fields[1]) != sha1.Size*2 {
			continue
		}
		if _, err := hex.DecodeString(fields[1]); err != nil {
			continue
		}
		identities[strings.ToLower(fields[1])] = struct{}{}
	}
	return identities
}

// appleProfileCertificateIDs reads every DeveloperCertificates entry rather
// than assuming the first certificate is the one this machine owns.
func appleProfileCertificateIDs(profile []byte) ([]string, error) {
	decoder := xml.NewDecoder(bytes.NewReader(profile))
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			return nil, errors.New("DeveloperCertificates is missing")
		}
		if err != nil {
			return nil, err
		}

		start, ok := token.(xml.StartElement)
		if !ok || start.Name.Local != "key" {
			continue
		}
		var key string
		if err := decoder.DecodeElement(&key, &start); err != nil {
			return nil, err
		}
		if key != "DeveloperCertificates" {
			continue
		}

		for {
			token, err = decoder.Token()
			if err != nil {
				return nil, err
			}
			array, ok := token.(xml.StartElement)
			if !ok {
				continue
			}
			if array.Name.Local != "array" {
				return nil, fmt.Errorf("DeveloperCertificates is %s, want array", array.Name.Local)
			}
			return appleCertificateArray(decoder, array)
		}
	}
}

func appleCertificateArray(decoder *xml.Decoder, array xml.StartElement) ([]string, error) {
	var identities []string
	for {
		token, err := decoder.Token()
		if err != nil {
			return nil, err
		}
		switch element := token.(type) {
		case xml.StartElement:
			if element.Name.Local != "data" {
				return nil, fmt.Errorf("DeveloperCertificates contains %s, want data", element.Name.Local)
			}
			var encoded string
			if err := decoder.DecodeElement(&encoded, &element); err != nil {
				return nil, err
			}
			certificate, err := base64.StdEncoding.DecodeString(strings.Join(strings.Fields(encoded), ""))
			if err != nil {
				return nil, err
			}
			identity := sha1.Sum(certificate)
			identities = append(identities, hex.EncodeToString(identity[:]))
		case xml.EndElement:
			if element.Name == array.Name {
				if len(identities) == 0 {
					return nil, errors.New("DeveloperCertificates is empty")
				}
				return identities, nil
			}
		}
	}
}

func matchingAppleIdentity(certificates []string, installed map[string]struct{}) (string, bool) {
	for _, certificate := range certificates {
		certificate = strings.ToLower(certificate)
		if _, found := installed[certificate]; found {
			return certificate, true
		}
	}
	return "", false
}

func exeIOS(tmpDir, target, app string, bi *buildInfo) error {
	if bi.appID == "" {
		return errors.New("app id is empty; use -appid to set it")
	}
	if err := prepareIOSApp(app); err != nil {
		return err
	}
	appName := UppercaseName(bi.name)
	exe := filepath.Join(app, appName)
	lipo := exec.Command("xcrun", "lipo", "-o", exe, "-create")
	var builds errgroup.Group
	for _, a := range bi.archs {
		clang, cflags, err := iosCompilerFor(target, a, bi.minsdk)
		if err != nil {
			return err
		}
		cflags = append(cflags, iosDeploymentFlags(target, a, bi.minsdk)...)
		cflagsLine := strings.Join(cflags, " ")
		exeSlice := filepath.Join(tmpDir, "app-"+a)
		lipo.Args = append(lipo.Args, exeSlice)
		compile := exec.Command(
			"go",
			"build",
			"-ldflags=-s -w "+bi.ldflags,
			"-o", exeSlice,
			"-tags", bi.tags,
			bi.pkgPath,
		)
		compile.Env = iosProgramEnv(a, clang, cflagsLine)
		builds.Go(func() error {
			_, err := runCmd(compile)
			return err
		})
	}
	if err := builds.Wait(); err != nil {
		return err
	}
	if _, err := runCmd(lipo); err != nil {
		return err
	}
	infoPlist, err := iosInfoPlist(iosManifestFor(bi))
	if err != nil {
		return err
	}
	plistFile := filepath.Join(app, "Info.plist")
	if err := os.WriteFile(plistFile, infoPlist, 0o660); err != nil {
		return err
	}
	if _, err := os.Stat(bi.iconPath); err == nil {
		assetPlist, err := iosIcons(bi, tmpDir, app, bi.iconPath)
		if err != nil {
			return err
		}
		// Merge assets plist with Info.plist
		cmd := exec.Command(
			"/usr/libexec/PlistBuddy",
			"-c", "Merge "+assetPlist,
			plistFile,
		)
		if _, err := runCmd(cmd); err != nil {
			return err
		}
	}
	if _, err := runCmd(exec.Command("plutil", "-convert", "binary1", plistFile)); err != nil {
		return err
	}
	return nil
}

func prepareIOSApp(app string) error {
	if err := os.RemoveAll(app); err != nil {
		return err
	}
	return os.MkdirAll(app, 0o755)
}

// iosIcons builds an asset catalog and compile it with the Xcode command actool.
// iosIcons returns the asset plist file to be merged into Info.plist.
func iosIcons(bi *buildInfo, tmpDir, appDir, icon string) (string, error) {
	assets := filepath.Join(tmpDir, "Assets.xcassets")
	if err := os.Mkdir(assets, 0o700); err != nil {
		return "", err
	}
	appIcon := filepath.Join(assets, "AppIcon.appiconset")
	err := buildIcons(appIcon, icon, []iconVariant{
		{path: "ios_2x.png", size: 120},
		{path: "ios_3x.png", size: 180},
		{path: "ipad_1x.png", size: 76},
		{path: "ipad_2x.png", size: 152},
		{path: "ipad_4x.png", size: 228},
		// The App Store icon is not allowed to contain
		// transparent pixels.
		{path: "ios_store.png", size: 1024, fill: true},
	})
	if err != nil {
		return "", err
	}
	contentJson := `{
"images": [
    {
        "size": "60x60",
        "idiom": "iphone",
        "filename": "ios_2x.png",
        "scale": "2x"
    },
    {
        "size": "60x60",
        "idiom": "iphone",
        "filename": "ios_3x.png",
        "scale": "3x"
    },
    {
        "size": "76x76",
        "idiom": "ipad",
        "filename": "ipad_1x.png",
        "scale": "1x"
    },
    {
        "size": "76x76",
        "idiom": "ipad",
        "filename": "ipad_2x.png",
        "scale": "2x"
    },
    {
        "size": "152x152",
        "idiom": "ipad",
        "filename": "ipad_4x.png",
        "scale": "2x"
    },
    {
        "size": "1024x1024",
        "idiom": "ios-marketing",
        "filename": "ios_store.png",
        "scale": "1x"
    }
]
}`
	contentFile := filepath.Join(appIcon, "Contents.json")
	if err := os.WriteFile(contentFile, []byte(contentJson), 0o600); err != nil {
		return "", err
	}
	assetPlist := filepath.Join(tmpDir, "assets.plist")

	minsdk := bi.minsdk
	if minsdk == 0 {
		minsdk = minIOSVersion
	}
	compile := exec.Command(
		"actool",
		"--compile", appDir,
		"--platform", iosPlatformForBuild(bi.target, bi.archs),
		"--minimum-deployment-target", strconv.Itoa(minsdk),
		"--app-icon", "AppIcon",
		"--output-partial-info-plist", assetPlist,
		assets)
	_, err = runCmd(compile)
	return assetPlist, err
}

// iosManifestData is everything an iOS application's property list says.
type iosManifestData struct {
	AppName              string
	AppID                string
	Version              string
	VersionCode          uint32
	Platform             string
	MinVersion           int
	SupportPlatform      string
	RequiredCapabilities []string
	Schemes              []string
}

// iosManifestFor answers what a build says about itself.
//
// MinVersion comes from the same field the compiler is handed, and that is the
// point of deriving both here: they are two declarations of one number, and a
// drift between them ships an application the system loads on a device whose
// libraries it was not built against -- which fails at the first call into one
// of them and nowhere earlier.
func iosManifestFor(bi *buildInfo) iosManifestData {
	var platform, supportPlatform string
	switch bi.target {
	case "ios":
		platform = "iphoneos"
		supportPlatform = "iPhoneOS"
	case "tvos":
		platform = "appletvos"
		supportPlatform = "AppleTVOS"
	}
	requiredCapabilities := []string{"arm64"}
	if iosSimulatorBuild(bi.archs) {
		platform = strings.TrimSuffix(platform, "os") + "simulator"
		supportPlatform = strings.TrimSuffix(supportPlatform, "OS") + "Simulator"
		requiredCapabilities = nil
	}

	return iosManifestData{
		AppName:              UppercaseName(bi.name),
		AppID:                bi.appID,
		Version:              bi.version.StringCompact(),
		VersionCode:          bi.version.VersionCode,
		Platform:             platform,
		MinVersion:           bi.minsdk,
		SupportPlatform:      supportPlatform,
		RequiredCapabilities: requiredCapabilities,
		Schemes:              bi.schemes,
	}
}

// iosInfoPlist writes the property list an iOS application is described by.
func iosInfoPlist(data iosManifestData) ([]byte, error) {
	tmpl, err := template.New("manifest").Funcs(markup).Parse(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>CFBundleDevelopmentRegion</key>
	<string>en</string>
	<key>CFBundleExecutable</key>
	<string>{{xml .AppName}}</string>
	<key>CFBundleIdentifier</key>
	<string>{{xml .AppID}}</string>
	<key>CFBundleInfoDictionaryVersion</key>
	<string>6.0</string>
	<key>CFBundleName</key>
	<string>{{xml .AppName}}</string>
	<key>CFBundlePackageType</key>
	<string>APPL</string>
	<key>CFBundleShortVersionString</key>
	<string>{{.Version}}</string>
	<key>CFBundleVersion</key>
	<string>{{.VersionCode}}</string>
	<key>UILaunchStoryboardName</key>
	<string>LaunchScreen</string>
{{if .RequiredCapabilities}}
	<key>UIRequiredDeviceCapabilities</key>
	<array>{{range .RequiredCapabilities}}<string>{{xml .}}</string>{{end}}</array>
{{end}}
	<key>DTPlatformName</key>
	<string>{{.Platform}}</string>
	<key>MinimumOSVersion</key>
	<string>{{.MinVersion}}.0</string>
	<key>UIDeviceFamily</key>
	<array>
		<integer>1</integer>
		<integer>2</integer>
	</array>
	<key>CFBundleSupportedPlatforms</key>
	<array>
		<string>{{.SupportPlatform}}</string>
	</array>
	<key>UISupportedInterfaceOrientations</key>
	<array>
		<string>UIInterfaceOrientationPortrait</string>
		<string>UIInterfaceOrientationPortraitUpsideDown</string>
		<string>UIInterfaceOrientationLandscapeLeft</string>
		<string>UIInterfaceOrientationLandscapeRight</string>
	</array>
	<key>UIRequiresFullScreen</key>
	<true/>
	<key>UILaunchScreen</key>
	<true/>
    {{if .Schemes}}
	<key>CFBundleURLTypes</key>
	<array>
	  {{range .Schemes}}
	  <dict>
		<key>CFBundleURLSchemes</key>
		<array>
		  <string>{{xml .}}</string>
		</array>
	  </dict>
	  {{end}}
	</array>
    {{end}}
</dict>
</plist>`)
	if err != nil {
		return nil, err
	}

	var manifest bytes.Buffer
	if err := tmpl.Execute(&manifest, data); err != nil {
		return nil, err
	}
	return manifest.Bytes(), nil
}

// iosDeploymentFlags are the compiler flags that declare the version the code
// is built against.
//
// The number is the build's own, and so is the one the property list declares.
// Two sites reading one field is what keeps a binary compiled for one version
// from saying it needs another.
func iosDeploymentFlags(target, arch string, minsdk int) []string {
	platform := target
	if target == "ios" && !iosSimulatorArch(arch) {
		platform = "iphoneos"
	} else if iosSimulatorArch(arch) {
		platform += "-simulator"
	}
	return []string{
		"-fobjc-arc",
		fmt.Sprintf("-m%s-version-min=%d.0", platform, minsdk),
	}
}

// iosProgramEnv is the environment the executable inside an application bundle
// is compiled in.
//
// cgo is asked for rather than inherited: the window is opened through a C
// library, and a machine with the variable off would build a program that
// compiles, links, and has no window backend in it.
func iosProgramEnv(arch, clang, cflags string) []string {
	return append(
		os.Environ(),
		"GOOS=ios",
		"GOARCH="+iosGoArch(arch),
		"CGO_ENABLED=1",
		"CC="+clang,
		"CXX="+clang+"++",
		"CGO_CFLAGS="+cflags,
		"CGO_CXXFLAGS="+cflags,
		"CGO_LDFLAGS=-lresolv "+cflags,
	)
}

// iosFrameworkEnv is the environment an archive another build embeds is
// compiled in.
//
// It is the program's environment without the C++ half and without the
// resolver library: what is produced here is linked into somebody else's
// application, which brings its own.
func iosFrameworkEnv(arch, clang, cflags string) []string {
	return append(
		os.Environ(),
		"GOOS=ios",
		"GOARCH="+iosGoArch(arch),
		"CGO_ENABLED=1",
		"CC="+clang,
		"CGO_CFLAGS="+cflags,
		"CGO_LDFLAGS="+cflags,
	)
}

func iosPlatformFor(target string) string {
	switch target {
	case "ios":
		return "iphoneos"
	case "tvos":
		return "appletvos"
	default:
		panic("invalid platform " + target)
	}
}

func iosPlatformForBuild(target string, archs []string) string {
	platform := iosPlatformFor(target)
	if iosSimulatorBuild(archs) {
		platform = strings.TrimSuffix(platform, "os") + "simulator"
	}
	return platform
}

func iosSimulatorArch(arch string) bool {
	return arch == "386" || arch == "amd64" || arch == "simarm64"
}

func iosSimulatorBuild(archs []string) bool {
	return len(archs) > 0 && iosSimulatorArch(archs[0])
}

func iosGoArch(arch string) string {
	if arch == "simarm64" {
		return "arm64"
	}
	return arch
}

func archiveIOS(tmpDir, target, frameworkRoot string, bi *buildInfo) error {
	framework := filepath.Base(frameworkRoot)
	const suf = ".framework"
	if !strings.HasSuffix(framework, suf) {
		return fmt.Errorf("the specified output %q does not end in '.framework'", frameworkRoot)
	}
	framework = framework[:len(framework)-len(suf)]
	if err := os.RemoveAll(frameworkRoot); err != nil {
		return err
	}
	frameworkDir := filepath.Join(frameworkRoot, "Versions", "A")
	for _, dir := range []string{"Headers", "Modules"} {
		p := filepath.Join(frameworkDir, dir)
		if err := os.MkdirAll(p, 0o755); err != nil {
			return err
		}
	}
	symlinks := [][2]string{
		{"Versions/Current/Headers", "Headers"},
		{"Versions/Current/Modules", "Modules"},
		{"Versions/Current/" + framework, framework},
		{"A", filepath.Join("Versions", "Current")},
	}
	for _, l := range symlinks {
		if err := os.Symlink(l[0], filepath.Join(frameworkRoot, l[1])); err != nil && !os.IsExist(err) {
			return err
		}
	}
	exe := filepath.Join(frameworkDir, framework)
	lipo := exec.Command("xcrun", "lipo", "-o", exe, "-create")
	var builds errgroup.Group
	tags := bi.tags
	for _, a := range bi.archs {
		clang, cflags, err := iosCompilerFor(target, a, bi.minsdk)
		if err != nil {
			return err
		}
		lib := filepath.Join(tmpDir, "ayra-"+a)
		cmd := exec.Command(
			"go",
			"build",
			"-ldflags=-s -w "+bi.ldflags,
			"-buildmode=c-archive",
			"-o", lib,
			"-tags", tags,
			bi.pkgPath,
		)
		lipo.Args = append(lipo.Args, lib)
		cflagsLine := strings.Join(cflags, " ")
		cmd.Env = iosFrameworkEnv(a, clang, cflagsLine)
		builds.Go(func() error {
			_, err := runCmd(cmd)
			return err
		})
	}
	if err := builds.Wait(); err != nil {
		return err
	}
	if _, err := runCmd(lipo); err != nil {
		return err
	}
	headerDst := filepath.Join(frameworkDir, "Headers", framework+".h")
	headerSrc := filepath.Join(bi.runtime.dir, "framework_ios.h")
	if err := copyFile(headerDst, headerSrc); err != nil {
		return err
	}
	module := fmt.Sprintf(`framework module "%s" {
    header "%[1]s.h"

    export *
}`, framework)
	moduleFile := filepath.Join(frameworkDir, "Modules", "module.modulemap")
	return os.WriteFile(moduleFile, []byte(module), 0o644)
}

func iosCompilerFor(target, arch string, minsdk int) (string, []string, error) {
	config, err := iosCompilerConfiguration(target, arch, minsdk)
	if err != nil {
		return "", nil, err
	}
	sdkPath, err := runCmd(exec.Command("xcrun", "--sdk", config.platformSDK, "--show-sdk-path"))
	if err != nil {
		return "", nil, err
	}
	clang, err := runCmd(exec.Command("xcrun", "--sdk", config.platformSDK, "--find", "clang"))
	if err != nil {
		return "", nil, err
	}
	cflags := []string{
		"-arch", config.toolchainArch,
		"-isysroot", sdkPath,
		"-m" + config.platformOS + "-version-min=" + strconv.Itoa(config.minSDK),
	}
	return clang, cflags, nil
}

type iosCompilerConfig struct {
	platformSDK   string
	platformOS    string
	toolchainArch string
	minSDK        int
}

func iosCompilerConfiguration(target, arch string, minsdk int) (iosCompilerConfig, error) {
	var config iosCompilerConfig
	switch target {
	case "ios":
		config.platformOS = "ios"
		config.platformSDK = "iphone"
	case "tvos":
		config.platformOS = "tvos"
		config.platformSDK = "appletv"
	default:
		return iosCompilerConfig{}, fmt.Errorf("unsupported Apple target: %s", target)
	}
	switch arch {
	case "arm", "arm64":
		config.platformSDK += "os"
		config.toolchainArch = allArchs[arch].iosArch
		if minsdk == 0 {
			minsdk = minIOSVersion
			if target == "tvos" {
				minsdk = minTVOSVersion
			}
		}
	case "386", "amd64", "simarm64":
		config.platformOS += "-simulator"
		config.platformSDK += "simulator"
		config.toolchainArch = allArchs[iosGoArch(arch)].iosArch
		if minsdk == 0 {
			minsdk = minSimulatorVersion
		}
	default:
		return iosCompilerConfig{}, fmt.Errorf("unsupported -arch: %s", arch)
	}
	config.minSDK = minsdk
	return config, nil
}

func zipDir(dst, base, dir string) (err error) {
	f, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer func() {
		if cerr := f.Close(); err == nil {
			err = cerr
		}
	}()
	zipf := zip.NewWriter(f)
	err = filepath.Walk(filepath.Join(base, dir), func(path string, f os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if f.IsDir() {
			return nil
		}
		rel := filepath.ToSlash(path[len(base)+1:])
		entry, err := zipf.Create(rel)
		if err != nil {
			return err
		}
		src, err := os.Open(path)
		if err != nil {
			return err
		}
		defer src.Close()
		_, err = io.Copy(entry, src)
		return err
	})
	if err != nil {
		return err
	}
	return zipf.Close()
}
