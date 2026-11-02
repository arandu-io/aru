package pack

import (
	"archive/zip"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"strings"
	"text/template"

	"github.com/arandu-io/aru/internal/buildcache"
	"golang.org/x/sync/errgroup"
	"golang.org/x/tools/go/packages"
)

type androidTools struct {
	buildtools string
	androidjar string
}

// zip.Writer with a sticky error.
type zipWriter struct {
	err error
	w   *zip.Writer
}

// Writer that saves any errors.
type errWriter struct {
	w   io.Writer
	err *error
}

var exeSuffix string

type manifestData struct {
	AppID          string
	Version        Semver
	MinSDK         int
	TargetSDK      int
	Permissions    []string
	Features       []string
	IconSnip       string
	AppName        string
	Schemes        []string
	PackageQueries []string

	// What the packages' and libraries' manifests add: whole elements, written
	// as they were, with their placeholders resolved. See mergeManifests.
	TopLevel   []string
	QueriesXML []string
	Components []string
}

// The ground the window shows before the application has drawn anything, and
// what the system's bars look like over it.
//
// The window background is the first thing a person sees when the application
// opens, and it is the system's to paint: Ayra's own fill only arrives with the
// first frame. A background fixed at white was a flash of white on a phone in
// the dark mode, so the theme has a night variant, and the system picks the one
// that matches its own setting -- which the activity keeps current, because the
// manifest declares uiMode among the changes it handles itself.
//
// The bars are transparent: the application draws behind them and paints them
// with its own background, and a fixed translucent black over a white page is a
// grey band the application did not ask for. A light day gets dark icons on
// them from the first frame; the shell takes over from there and follows the
// scheme the application is in.
const (
	// androidLightGround and androidDarkGround are the backgrounds of Ayra's light
	// and dark palettes (theme.New), which a product's own theme lays over.
	androidLightGround = "#FFFFFFFF"
	androidDarkGround  = "#FF0A0A0A"
)

// androidTheme is Theme.AyraApp over a ground. bars adds the transparent system
// bars, which the oldest releases do not have, and lightStatus the dark status
// bar icons a light ground wants.
func androidTheme(ground string, bars, lightStatus bool) string {
	return androidThemeWith(ground, bars, lightStatus, "")
}

// androidThemeWith is androidTheme with more items, written as they are.
func androidThemeWith(ground string, bars, lightStatus bool, extra string) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="utf-8"?>
<resources>
	<style name="Theme.AyraApp" parent="android:style/Theme.NoTitleBar">
		<item name="android:windowBackground">` + ground + `</item>
`)
	if bars {
		b.WriteString(`
		<item name="android:windowDrawsSystemBarBackgrounds">true</item>
		<item name="android:navigationBarColor">@android:color/transparent</item>
		<item name="android:statusBarColor">@android:color/transparent</item>
`)
	}
	if lightStatus {
		b.WriteString(`		<item name="android:windowLightStatusBar">true</item>
`)
	}
	b.WriteString(extra)
	b.WriteString(`	</style>
</resources>`)
	return b.String()
}

// androidSplashItems point the splash screen of Android 12 at the icon the
// icon generator drew and the ground it chose for each appearance. Both are
// resources the system resolves by itself, the night directory first when the
// device is in the dark, so one pair of items serves both.
const androidSplashItems = `		<item name="android:windowSplashScreenBackground">@color/ic_splash_background</item>
		<item name="android:windowSplashScreenAnimatedIcon">@drawable/ic_splash</item>
`

// androidSplashThemes are the themes of the releases that have a splash screen
// of their own, which is Android 12 and later. They are only written for an
// application with an icon, because the items name resources only the icon
// generator makes.
func androidSplashThemes() []androidThemeFile {
	return []androidThemeFile{
		{"values-v31", androidThemeWith(androidLightGround, true, true, androidSplashItems)},
		{"values-night-v31", androidThemeWith(androidDarkGround, true, false, androidSplashItems)},
	}
}

// notificationIconElement tells Firebase which drawable a notification it
// draws itself is marked with: without it the system shows the launcher icon,
// which the status bar paints as a grey square.
const notificationIconElement = `<meta-data android:name="com.google.firebase.messaging.default_notification_icon" android:resource="@drawable/ic_stat" />`

// androidThemeFile is one theme resource, by the directory it lives in.
type androidThemeFile struct {
	dir, xml string
}

// androidThemes are the theme resources of the package, in the directories the
// system picks between. Android resolves a resource whole from the best
// matching directory -- the night one first, then the newest release at or below
// the device -- so every directory carries the full style.
func androidThemes() []androidThemeFile {
	return []androidThemeFile{
		{"values", androidTheme(androidLightGround, false, false)},
		{"values-v21", androidTheme(androidLightGround, true, false)},
		{"values-v23", androidTheme(androidLightGround, true, true)},
		{"values-night", androidTheme(androidDarkGround, false, false)},
		{"values-night-v21", androidTheme(androidDarkGround, true, false)},
	}
}

func init() {
	if runtime.GOOS == "windows" {
		exeSuffix = ".exe"
	}
}

func buildAndroid(tmpDir string, bi *buildInfo) error {
	sdk := os.Getenv("ANDROID_HOME")
	if sdk == "" {
		return errors.New("please set ANDROID_HOME to the Android SDK path")
	}
	if _, err := os.Stat(sdk); err != nil {
		return err
	}
	platform, err := latestPlatform(sdk)
	if err != nil {
		return err
	}
	buildtools, err := latestTools(sdk)
	if err != nil {
		return err
	}

	tools := &androidTools{
		buildtools: buildtools,
		androidjar: filepath.Join(platform, "android.jar"),
	}
	perms := []string{"default"}
	// A permission is a package of its own below the runtime's, and its last
	// element is the permission's name. The prefix is built from where the
	// runtime was found rather than written out, so the two cannot disagree.
	permPref := bi.runtime.path + "/permission/"
	var extraJars []string
	err = walkGraph(bi.graph, func(p *packages.Package) (bool, error) {
		if len(p.GoFiles) == 0 {
			return false, nil
		}
		dir := filepath.Dir(p.GoFiles[0])
		jars, err := filepath.Glob(filepath.Join(dir, "*.jar"))
		if err != nil {
			return false, err
		}
		extraJars = append(extraJars, jars...)
		// A package may carry Java and a manifest fragment of its own, in an
		// android directory beside its Go. They are read only when the package
		// is in the graph, so an application that never imports it carries none
		// of it.
		if java, err := filepath.Glob(filepath.Join(dir, "android", "*.java")); err == nil && len(java) > 0 {
			bi.androidJava = append(bi.androidJava, java...)
		}
		if body, err := os.ReadFile(filepath.Join(dir, "android", "AndroidManifest.xml")); err == nil {
			bi.androidFragments = append(bi.androidFragments, manifestSource{name: p.PkgPath, body: body})
		}
		switch {
		case p.PkgPath == "net":
			perms = append(perms, "network")
		case strings.HasPrefix(p.PkgPath, permPref):
			perms = append(perms, p.PkgPath[len(permPref):])
		}
		return true, nil
	})
	if err != nil {
		return err
	}

	libs, err := openLibraries(filepath.Join(tmpDir, "libraries"), bi.libraries)
	if err != nil {
		return err
	}
	bi.androidLibs = libs
	if len(bi.androidJava) > 0 && len(libs) == 0 {
		fmt.Fprintf(output, "note: %d package(s) of the application carry Java that may call a library, and none was given with -android-lib\n", len(bi.androidJava))
	}

	if err := compileAndroid(tmpDir, tools, bi); err != nil {
		return err
	}
	switch *buildMode {
	case "archive":
		return archiveAndroid(tmpDir, bi, perms)
	case "exe":
		file := *destPath
		if file == "" {
			file = fmt.Sprintf("%s.apk", bi.name)
		}

		isBundle := false
		switch filepath.Ext(file) {
		case ".apk":
		case ".aab":
			isBundle = true
		default:
			return fmt.Errorf("the specified output %q does not end in '.apk' or '.aab'", file)
		}

		if err := exeAndroid(tmpDir, tools, bi, extraJars, perms, isBundle); err != nil {
			return err
		}
		if isBundle {
			return signAAB(tmpDir, file, tools, bi)
		}
		return signAPK(tmpDir, file, tools, bi)
	default:
		panic("unreachable")
	}
}

// androidBuildEnv is the environment one architecture's shared library is
// compiled in.
//
// cgo is asked for rather than inherited: the window is opened through a C
// library, and a machine with the variable off would build a library that
// compiles, links, and has no window backend in it.
//
// base is the environment the toolchain is started with, cache included, and
// the platform's settings are appended to it, which is what makes them win.
func androidBuildEnv(base []string, arch, clang string) []string {
	return append(
		base,
		"GOOS=android",
		"GOARCH="+arch,
		"GOARM=7", // Avoid softfloat.
		"CGO_ENABLED=1",
		"CC="+clang,
		"CXX="+clang+"++",
	)
}

func compileAndroid(tmpDir string, tools *androidTools, bi *buildInfo) (err error) {
	androidHome := os.Getenv("ANDROID_HOME")
	if androidHome == "" {
		return errors.New("ANDROID_HOME is not set. Please point it to the root of the Android SDK")
	}
	javac, err := findJavaC()
	if err != nil {
		return fmt.Errorf("could not find javac: %v", err)
	}
	ndkRoot, err := findNDK(androidHome)
	if err != nil {
		return err
	}
	minSDK := max(bi.minsdk, 17)
	tcRoot := filepath.Join(ndkRoot, "toolchains", "llvm", "prebuilt", archNDK())
	var builds errgroup.Group
	for _, a := range bi.archs {
		arch := allArchs[a]
		clang, err := latestCompiler(tcRoot, a, minSDK)
		if err != nil {
			return fmt.Errorf("%s. Please make sure you have NDK >= r19c installed. Use the command `sdkmanager ndk-bundle` to install it.", err)
		}
		if runtime.GOOS == "windows" {
			// Because of https://github.com/android-ndk/ndk/issues/920,
			// we need NDK r19c, not just r19b. Check for the presence of
			// clang++.cmd which is only available in r19c.
			clangpp := clang + "++.cmd"
			if _, err := os.Stat(clangpp); err != nil {
				return fmt.Errorf("NDK version r19b detected, but >= r19c is required. Use the command `sdkmanager ndk-bundle` to install it")
			}
		}
		archDir := filepath.Join(tmpDir, "jni", arch.jniArch)
		if err := os.MkdirAll(archDir, 0o755); err != nil {
			return fmt.Errorf("failed to create %q: %v", archDir, err)
		}
		libFile := filepath.Join(archDir, "libayra.so")
		cmd := buildcache.Command(
			"build",
			"-ldflags=-w -s -extldflags \"-Wl,-z,max-page-size=65536\" "+bi.ldflags,
			"-buildmode=c-shared",
			"-tags", bi.tags,
			"-o", libFile,
			bi.pkgPath,
		)
		cmd.Env = androidBuildEnv(cmd.Env, a, clang)
		builds.Go(func() error {
			_, err := runCmd(cmd)
			return err
		})
	}
	appDir := bi.runtime.dir
	javaFiles, err := filepath.Glob(filepath.Join(appDir, "*.java"))
	if err != nil {
		return err
	}
	if len(javaFiles) == 0 {
		return fmt.Errorf("the %s package carries no .java files, and the Android backend cannot be built without them", bi.runtime.path)
	}
	if len(javaFiles) > 0 {
		classes := filepath.Join(tmpDir, "classes")
		if err := os.MkdirAll(classes, 0o755); err != nil {
			return err
		}
		// The runtime's own Java is compiled with the Java of the packages that
		// carry some, in one pass, because the two call each other: the runtime
		// tells a package about intents, and the package is told through it.
		sources := append(append([]string(nil), javaFiles...), bi.androidJava...)
		sourcepath := []string{appDir}
		for _, f := range bi.androidJava {
			if dir := filepath.Dir(f); !slices.Contains(sourcepath, dir) {
				sourcepath = append(sourcepath, dir)
			}
		}
		args := []string{
			"-target", "1.8",
			"-source", "1.8",
			"-sourcepath", strings.Join(sourcepath, string(os.PathListSeparator)),
			"-bootclasspath", tools.androidjar,
			"-d", classes,
		}
		if jars := bi.androidLibs.classpath(); len(jars) > 0 {
			args = append(args, "-classpath", strings.Join(jars, string(os.PathListSeparator)))
		}
		javac := exec.Command(javac, append(args, sources...)...)
		builds.Go(func() error {
			if _, err := runCmd(javac); err != nil {
				if len(bi.androidLibs) == 0 && len(bi.androidJava) > 0 {
					return fmt.Errorf("%w\n\nA package of this application carries Java that needs a library, and none was given: pass the .aar and .jar files it calls with -android-lib", err)
				}
				return err
			}
			return nil
		})
	}
	return builds.Wait()
}

// androidArchiveManifest writes the manifest an archive another build embeds
// carries.
//
// Shorter than the one an installable package carries, and deliberately: an
// archive declares what it needs of whoever embeds it, and everything about
// launching -- the activity, the theme, the icon, the schemes -- belongs to the
// application that does.
func androidArchiveManifest(data manifestData) ([]byte, error) {
	tmpl, err := template.New("manifest").Funcs(markup).Parse(
		`<manifest xmlns:android="http://schemas.android.com/apk/res/android" package="{{xml .AppID}}">
        <uses-sdk android:minSdkVersion="{{.MinSDK}}"/>
{{range .Permissions}}	<uses-permission android:name="{{xml .}}"/>
{{end}}{{range .Features}}	<uses-feature android:{{.}} android:required="false"/>
{{end}}</manifest>
`)
	if err != nil {
		return nil, err
	}

	var manifest bytes.Buffer
	if err := tmpl.Execute(&manifest, data); err != nil {
		return nil, err
	}
	return manifest.Bytes(), nil
}

func archiveAndroid(tmpDir string, bi *buildInfo, perms []string) (err error) {
	aarFile := *destPath
	if aarFile == "" {
		aarFile = fmt.Sprintf("%s.aar", bi.name)
	}
	if filepath.Ext(aarFile) != ".aar" {
		return fmt.Errorf("the specified output %q does not end in '.aar'", aarFile)
	}
	aar, err := os.Create(aarFile)
	if err != nil {
		return err
	}
	defer func() {
		if cerr := aar.Close(); err == nil {
			err = cerr
		}
	}()
	aarw := newZipWriter(aar)
	defer aarw.Close()
	aarw.Create("R.txt")
	for _, theme := range androidThemes() {
		_, _ = aarw.Create("res/" + theme.dir + "/themes.xml").Write([]byte(theme.xml))
	}
	permissions, features := getPermissions(perms)
	// Disable input emulation on ChromeOS.
	manifestBody, err := androidArchiveManifest(manifestData{
		AppID:       bi.appID,
		MinSDK:      bi.minsdk,
		Permissions: permissions,
		Features:    features,
	})
	if err != nil {
		return err
	}
	manifest := aarw.Create("AndroidManifest.xml")
	_, _ = manifest.Write(manifestBody)
	proguard := aarw.Create("proguard.txt")
	_, _ = proguard.Write([]byte(`-keep class io.arandu.ayra.** { *; }`))

	for _, a := range bi.archs {
		arch := allArchs[a]
		libFile := filepath.Join("jni", arch.jniArch, "libayra.so")
		aarw.Add(filepath.ToSlash(libFile), filepath.Join(tmpDir, libFile))
	}
	classes := filepath.Join(tmpDir, "classes")
	if _, err := os.Stat(classes); err == nil {
		jarFile := filepath.Join(tmpDir, "classes.jar")
		if err := writeJar(jarFile, classes); err != nil {
			return err
		}
		aarw.Add("classes.jar", jarFile)
	}
	return aarw.Close()
}

func exeAndroid(tmpDir string, tools *androidTools, bi *buildInfo, extraJars, perms []string, isBundle bool) (err error) {
	classes := filepath.Join(tmpDir, "classes")
	var classFiles []string
	err = filepath.Walk(classes, func(path string, f os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if filepath.Ext(path) == ".class" {
			classFiles = append(classFiles, path)
		}
		return nil
	})
	classFiles = append(classFiles, extraJars...)
	classFiles = append(classFiles, bi.androidLibs.classpath()...)
	dexDir := filepath.Join(tmpDir, "apk")
	if err := os.MkdirAll(dexDir, 0o755); err != nil {
		return err
	}
	minSDK := max(bi.minsdk, 16)
	// A library says the oldest Android it runs on, and the application cannot
	// claim an older one than the libraries it carries.
	for _, lib := range bi.androidLibs {
		minSDK = max(minSDK, lib.minSDK())
	}
	// https://developer.android.com/distribute/best-practices/develop/target-sdk
	targetSDK := 35
	if bi.targetsdk > 0 {
		targetSDK = bi.targetsdk
	}
	if minSDK > targetSDK {
		targetSDK = minSDK
	}

	// Compile resources.
	resDir := filepath.Join(tmpDir, "res")
	valDir := filepath.Join(resDir, "values")
	v21Dir := filepath.Join(resDir, "values-v21")
	for _, dir := range []string{valDir, v21Dir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	iconSnip := ""
	set, err := bi.iconSet()
	if err != nil {
		return err
	}
	if set != nil {
		files, err := set.Android()
		if err != nil {
			return err
		}
		if err := writeIcons(resDir, files); err != nil {
			return err
		}
		iconSnip = `android:icon="@mipmap/ic_launcher" android:roundIcon="@mipmap/ic_launcher_round"`
	}
	themes := androidThemes()
	if set != nil {
		themes = append(themes, androidSplashThemes()...)
	}
	for _, theme := range themes {
		dir := filepath.Join(resDir, theme.dir)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dir, "themes.xml"), []byte(theme.xml), 0o660); err != nil {
			return err
		}
	}
	if bi.googleServices != "" {
		body, err := os.ReadFile(bi.googleServices)
		if err != nil {
			return fmt.Errorf("reading the google-services.json: %w", err)
		}
		values, err := googleServicesResources(body, bi.appID)
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(valDir, "google_services.xml"), values, 0o660); err != nil {
			return err
		}
	}
	resZip := filepath.Join(tmpDir, "resources.zip")
	aapt2 := filepath.Join(tools.buildtools, "aapt2")
	_, err = runCmd(exec.Command(
		aapt2,
		"compile",
		"-o", resZip,
		"--dir", resDir))
	if err != nil {
		return err
	}
	// Each library's resources are compiled to a zip of their own, and linked
	// with the application's below.
	resZips := []string{resZip}
	for i, dir := range bi.androidLibs.resourceDirs() {
		zipFile := filepath.Join(tmpDir, fmt.Sprintf("library-%03d.zip", i))
		if _, err := runCmd(exec.Command(aapt2, "compile", "-o", zipFile, "--dir", dir)); err != nil {
			return err
		}
		resZips = append(resZips, zipFile)
	}

	// Link APK.
	permissions, features := getPermissions(perms)
	appName := UppercaseName(bi.name)
	manifestSrc := manifestData{
		AppID:          bi.appID,
		Version:        bi.version,
		MinSDK:         minSDK,
		TargetSDK:      targetSDK,
		Permissions:    permissions,
		Features:       features,
		IconSnip:       iconSnip,
		AppName:        appName,
		Schemes:        bi.schemes,
		PackageQueries: bi.packageQueries,
	}
	merged, err := mergeAndroidManifests(bi, &manifestSrc)
	if err != nil {
		return err
	}
	if merged {
		// What the manifests added may ask for a newer Android than the floor.
		manifestSrc.MinSDK = minSDK
	}
	if set != nil {
		manifestSrc.Components = append(manifestSrc.Components, notificationIconElement)
	}
	manifestBody, err := androidManifest(manifestSrc)
	if err != nil {
		return err
	}
	manifest := filepath.Join(tmpDir, "AndroidManifest.xml")
	if err := os.WriteFile(manifest, manifestBody, 0o660); err != nil {
		return err
	}

	linkAPK := filepath.Join(tmpDir, "link.apk")

	args := []string{
		"link",
		"--manifest", manifest,
		"-I", tools.androidjar,
		"-o", linkAPK,
	}
	if isBundle {
		args = append(args, "--proto-format")
	}
	// The R classes the libraries' code reads their resources through are
	// written beside the link, for the packages the libraries declare.
	generated := filepath.Join(tmpDir, "generated")
	if len(bi.androidLibs.resourceDirs()) > 0 {
		if err := os.MkdirAll(generated, 0o755); err != nil {
			return err
		}
		args = append(args, "--auto-add-overlay", "--java", generated)
		if pkgs := bi.androidLibs.packages(); len(pkgs) > 0 {
			args = append(args, "--extra-packages", strings.Join(pkgs, ":"))
		}
	}
	args = append(args, resZips...)

	if _, err := runCmd(exec.Command(aapt2, args...)); err != nil {
		return err
	}

	if err := compileResourceClasses(tmpDir, generated, tools, &classFiles); err != nil {
		return err
	}
	if len(classFiles) > 0 {
		d8 := exec.Command(
			filepath.Join(tools.buildtools, "d8"),
			"--lib", tools.androidjar,
			"--output", dexDir,
			"--min-api", strconv.Itoa(minSDK),
		)
		d8.Args = append(d8.Args, classFiles...)
		if _, err := runCmd(d8); err != nil {
			major, minor, ok := determineJDKVersion()
			if ok && (major != 1 || minor != 8) {
				return fmt.Errorf("unsupported JDK version %d.%d, expected 1.8\nd8 error: %v", major, minor, err)
			}
			return err
		}
	}

	// The Go standard library archive/zip doesn't support appending to zip
	// files. Copy files from `link.apk` (generated by aapt2) along with classes.dex and
	// the Go libraries to a new `app.zip` file.

	// Load link.apk as zip.
	linkAPKZip, err := zip.OpenReader(linkAPK)
	if err != nil {
		return err
	}
	defer linkAPKZip.Close()

	// Create new "APK".
	unsignedAPK := filepath.Join(tmpDir, "app.zip")
	unsignedAPKFile, err := os.Create(unsignedAPK)
	if err != nil {
		return err
	}
	defer func() {
		if cerr := unsignedAPKFile.Close(); err == nil {
			err = cerr
		}
	}()
	unsignedAPKZip := zip.NewWriter(unsignedAPKFile)
	defer unsignedAPKZip.Close()

	// Copy files from linkAPK to unsignedAPK.
	for _, f := range linkAPKZip.File {
		header := zip.FileHeader{
			Name:   f.Name,
			Method: f.Method,
		}

		if isBundle {
			// AAB have pre-defined folders.
			switch header.Name {
			case "AndroidManifest.xml":
				header.Name = "manifest/AndroidManifest.xml"
			}
		}

		w, err := unsignedAPKZip.CreateHeader(&header)
		if err != nil {
			return err
		}
		r, err := f.Open()
		if err != nil {
			return err
		}
		if _, err := io.Copy(w, r); err != nil {
			return err
		}
	}

	// Append new files (that doesn't exists inside the link.apk).
	appendToZip := func(path string, file string) error {
		f, err := os.Open(file)
		if err != nil {
			return err
		}
		defer f.Close()
		w, err := unsignedAPKZip.CreateHeader(&zip.FileHeader{
			Name:   filepath.ToSlash(path),
			Method: zip.Deflate,
		})
		if err != nil {
			return err
		}
		_, err = io.Copy(w, f)
		return err
	}

	// Append Go binaries (libayra.so).
	for _, a := range bi.archs {
		arch := allArchs[a]
		libFile := filepath.Join(arch.jniArch, "libayra.so")
		if err := appendToZip(filepath.Join("lib", libFile), filepath.Join(tmpDir, "jni", libFile)); err != nil {
			return err
		}
	}

	// Append the dex files. d8 writes more than one when the methods do not fit
	// in one, which a library as large as a push service's can cause.
	if len(classFiles) > 0 {
		dexFiles, err := filepath.Glob(filepath.Join(dexDir, "classes*.dex"))
		if err != nil {
			return err
		}
		sort.Strings(dexFiles)
		for _, dex := range dexFiles {
			folder := filepath.Base(dex)
			if isBundle {
				folder = "dex/" + folder
			}
			if err := appendToZip(folder, dex); err != nil {
				return err
			}
		}
	}

	// Native libraries the archives carry, for the architectures built.
	for _, lib := range bi.androidLibs {
		for _, native := range lib.native {
			for _, a := range bi.archs {
				if allArchs[a].jniArch == native.abi {
					if err := appendToZip(filepath.Join("lib", native.abi, filepath.Base(native.file)), native.file); err != nil {
						return err
					}
				}
			}
		}
	}

	return unsignedAPKZip.Close()
}

func determineJDKVersion() (int, int, bool) {
	path, err := findJavaC()
	if err != nil {
		return 0, 0, false
	}
	java := exec.Command(filepath.Join(filepath.Dir(path), "java"), "-version")
	out, err := java.CombinedOutput()
	if err != nil {
		return 0, 0, false
	}
	var vendor string
	var major, minor int
	_, err = fmt.Sscanf(string(out), "%s version \"%d.%d", &vendor, &major, &minor)
	return major, minor, err == nil
}

func signAPK(tmpDir string, apkFile string, tools *androidTools, bi *buildInfo) error {
	if err := zipalign(tools, filepath.Join(tmpDir, "app.zip"), apkFile); err != nil {
		return err
	}

	if bi.key == "" {
		if err := defaultAndroidKeystore(tmpDir, bi); err != nil {
			return err
		}
	}

	cmd := exec.Command(
		filepath.Join(tools.buildtools, "apksigner"),
		"sign",
		"--ks-pass", "env:"+secretEnv,
		"--ks", bi.key,
		apkFile,
	)
	cmd.Env = secretEnviron(bi.password)
	_, err := runCmd(cmd)

	return err
}

func signAAB(tmpDir string, aabFile string, tools *androidTools, bi *buildInfo) error {
	allBundleTools, err := filepath.Glob(filepath.Join(tools.buildtools, "bundletool*.jar"))
	if err != nil {
		return err
	}

	bundletool := ""
	for _, v := range allBundleTools {
		bundletool = v
		break
	}

	if bundletool == "" {
		return fmt.Errorf("bundletool was not found at %s. Download it from https://github.com/google/bundletool/releases and move to the respective folder", tools.buildtools)
	}

	_, err = runCmd(exec.Command(
		"java",
		"-jar", bundletool,
		"build-bundle",
		"--modules="+filepath.Join(tmpDir, "app.zip"),
		"--output="+filepath.Join(tmpDir, "app.aab"),
	))
	if err != nil {
		return err
	}

	if err := zipalign(tools, filepath.Join(tmpDir, "app.aab"), aabFile); err != nil {
		return err
	}

	if bi.key == "" {
		if err := defaultAndroidKeystore(tmpDir, bi); err != nil {
			return err
		}
	}

	list := exec.Command(
		"keytool",
		"-keystore", bi.key,
		"-list",
		"-keypass:env", secretEnv,
		"-v",
	)
	list.Env = secretEnviron(bi.password)
	keytoolList, err := runCmd(list)
	if err != nil {
		return err
	}

	var alias string
	for t := range strings.SplitSeq(keytoolList, "\n") {
		if i, _ := fmt.Sscanf(t, "Alias name: %s", &alias); i > 0 {
			break
		}
	}

	sign := exec.Command(
		filepath.Join("jarsigner"),
		"-sigalg", "SHA256withRSA",
		"-digestalg", "SHA-256",
		"-keystore", bi.key,
		"-storepass:env", secretEnv,
		aabFile,
		strings.TrimSpace(alias),
	)
	sign.Env = secretEnviron(bi.password)
	_, err = runCmd(sign)

	return err
}

func zipalign(tools *androidTools, input, output string) error {
	_, err := runCmd(exec.Command(
		filepath.Join(tools.buildtools, "zipalign"),
		"-f",
		"4", // 32-bit alignment.
		input,
		output,
	))
	return err
}

func defaultAndroidKeystore(tmpDir string, bi *buildInfo) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}

	// Use debug.keystore, if exists.
	bi.key = filepath.Join(home, ".android", "debug.keystore")
	bi.password = "android"
	if _, err := os.Stat(bi.key); err == nil {
		return nil
	}

	// Generate new key.
	bi.key = filepath.Join(tmpDir, "sign.keystore")
	keytool, err := findKeytool()
	if err != nil {
		return err
	}
	cmd := exec.Command(
		keytool,
		"-genkey",
		"-keystore", bi.key,
		"-storepass:env", secretEnv,
		"-alias", "android",
		"-keyalg", "RSA", "-keysize", "2048",
		"-validity", "10000",
		"-noprompt",
		"-dname", "CN=android",
	)
	cmd.Env = secretEnviron(bi.password)
	_, err = runCmd(cmd)
	return err
}

func findNDK(androidHome string) (string, error) {
	ndks, err := filepath.Glob(filepath.Join(androidHome, "ndk", "*"))
	if err != nil {
		return "", err
	}
	if bestNDK, found := latestVersionPath(ndks); found {
		return bestNDK, nil
	}
	// The old NDK path was $ANDROID_HOME/ndk-bundle.
	ndkBundle := filepath.Join(androidHome, "ndk-bundle")
	if _, err := os.Stat(ndkBundle); err == nil {
		return ndkBundle, nil
	}
	// Certain non-standard NDK isntallations set the $ANDROID_NDK_ROOT
	// environment variable
	if ndkBundle, ok := os.LookupEnv("ANDROID_NDK_ROOT"); ok {
		if _, err := os.Stat(ndkBundle); err == nil {
			return ndkBundle, nil
		}
	}

	return "", fmt.Errorf("no NDK found in $ANDROID_HOME (%s). Set $ANDROID_NDK_ROOT or use `sdkmanager ndk-bundle` to install the NDK", androidHome)
}

func findKeytool() (string, error) {
	javaHome := os.Getenv("JAVA_HOME")
	if javaHome == "" {
		return exec.LookPath("keytool")
	}

	// bin, instead of "jre". "jre" was for older JVM it seems.
	keytool := filepath.Join(javaHome, "bin", "keytool"+exeSuffix)
	if _, err := os.Stat(keytool); err != nil {
		return "", err
	}
	return keytool, nil
}

func findJavaC() (string, error) {
	javaHome := os.Getenv("JAVA_HOME")
	if javaHome == "" {
		return exec.LookPath("javac")
	}
	javac := filepath.Join(javaHome, "bin", "javac"+exeSuffix)
	if _, err := os.Stat(javac); err != nil {
		return "", err
	}
	return javac, nil
}

func writeJar(jarFile, dir string) (err error) {
	jar, err := os.Create(jarFile)
	if err != nil {
		return err
	}
	defer func() {
		if cerr := jar.Close(); err == nil {
			err = cerr
		}
	}()
	jarw := newZipWriter(jar)
	const manifestHeader = `Manifest-Version: 1.0
Created-By: 1.0 (Go)

`
	_, _ = jarw.Create("META-INF/MANIFEST.MF").Write([]byte(manifestHeader))
	err = filepath.Walk(dir, func(path string, f os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if f.IsDir() {
			return nil
		}
		if filepath.Ext(path) == ".class" {
			rel := filepath.ToSlash(path[len(dir)+1:])
			jarw.Add(rel, path)
		}
		return nil
	})
	if err != nil {
		return err
	}
	return jarw.Close()
}

func archNDK() string {
	var arch string
	switch runtime.GOARCH {
	case "386":
		arch = "x86"
	case "amd64":
		arch = "x86_64"
	case "arm64":
		if runtime.GOOS == "darwin" {
			// Workaround for arm64 macOS. This will keep working until
			// Apple deprecates Rosetta 2.
			arch = "x86_64"
		} else {
			panic("unsupported GOARCH: " + runtime.GOARCH)
		}
	default:
		panic("unsupported GOARCH: " + runtime.GOARCH)
	}
	return runtime.GOOS + "-" + arch
}

func getPermissions(ps []string) ([]string, []string) {
	var permissions, features []string
	seenPermissions := make(map[string]bool)
	seenFeatures := make(map[string]bool)
	for _, perm := range ps {
		for _, x := range AndroidPermissions[perm] {
			if !seenPermissions[x] {
				permissions = append(permissions, x)
				seenPermissions[x] = true
			}
		}
		for _, x := range AndroidFeatures[perm] {
			if !seenFeatures[x] {
				features = append(features, x)
				seenFeatures[x] = true
			}
		}
	}
	return permissions, features
}

func latestPlatform(sdk string) (string, error) {
	allPlats, err := filepath.Glob(filepath.Join(sdk, "platforms", "android-*"))
	if err != nil {
		return "", err
	}
	var bestVer int
	var bestPlat string
	for _, platform := range allPlats {
		_, name := filepath.Split(platform)
		// The glob above guarantees the "android-" prefix.
		verStr := name[len("android-"):]
		ver, err := strconv.Atoi(verStr)
		if err != nil {
			continue
		}
		if ver < bestVer {
			continue
		}
		bestVer = ver
		bestPlat = platform
	}
	if bestPlat == "" {
		return "", fmt.Errorf("no platforms found in %q", sdk)
	}
	return bestPlat, nil
}

func latestCompiler(tcRoot, a string, minsdk int) (string, error) {
	arch := allArchs[a]
	allComps, err := filepath.Glob(filepath.Join(tcRoot, "bin", arch.clangArch+"*-clang"))
	if err != nil {
		return "", err
	}
	var bestVer int
	var firstVer int
	var bestCompiler string
	var firstCompiler string
	for _, compiler := range allComps {
		var ver int
		pattern := filepath.Join(tcRoot, "bin", arch.clangArch) + "%d-clang"
		if n, err := fmt.Sscanf(compiler, pattern, &ver); n < 1 || err != nil {
			continue
		}
		if firstCompiler == "" || ver < firstVer {
			firstVer = ver
			firstCompiler = compiler
		}
		if ver < bestVer {
			continue
		}
		if ver > minsdk {
			continue
		}
		bestVer = ver
		bestCompiler = compiler
	}
	if bestCompiler == "" {
		bestCompiler = firstCompiler
	}
	if bestCompiler == "" {
		return "", fmt.Errorf("no NDK compiler found for architecture %s in %s", a, tcRoot)
	}
	return bestCompiler, nil
}

func latestTools(sdk string) (string, error) {
	allTools, err := filepath.Glob(filepath.Join(sdk, "build-tools", "*"))
	if err != nil {
		return "", err
	}
	tools, found := latestVersionPath(allTools)
	if !found {
		return "", fmt.Errorf("no build-tools found in %q", sdk)
	}
	return tools, nil
}

// latestVersionFile finds the path with the highest version
// among paths on the form
//
//	/some/path/major.minor.patch
func latestVersionPath(paths []string) (string, bool) {
	var bestVer [3]int
	var bestDir string
loop:
	for _, path := range paths {
		name := filepath.Base(path)
		s := strings.SplitN(name, ".", 3)
		var version [3]int
		for i, v := range s {
			v, err := strconv.Atoi(v)
			if err != nil {
				continue loop
			}
			if v < bestVer[i] {
				continue loop
			}
			if v > bestVer[i] {
				break
			}
			version[i] = v
		}
		bestVer = version
		bestDir = path
	}
	return bestDir, bestDir != ""
}

func newZipWriter(w io.Writer) *zipWriter {
	return &zipWriter{
		w: zip.NewWriter(w),
	}
}

func (z *zipWriter) Close() error {
	err := z.w.Close()
	if z.err == nil {
		z.err = err
	}
	return z.err
}

func (z *zipWriter) Create(name string) io.Writer {
	if z.err != nil {
		return io.Discard
	}
	w, err := z.w.Create(name)
	if err != nil {
		z.err = err
		return io.Discard
	}
	return &errWriter{w: w, err: &z.err}
}

func (z *zipWriter) Store(name, file string) {
	z.add(name, file, false)
}

func (z *zipWriter) Add(name, file string) {
	z.add(name, file, true)
}

func (z *zipWriter) add(name, file string, compressed bool) {
	if z.err != nil {
		return
	}
	f, err := os.Open(file)
	if err != nil {
		z.err = err
		return
	}
	defer f.Close()
	fh := &zip.FileHeader{
		Name: name,
	}
	if compressed {
		fh.Method = zip.Deflate
	}
	w, err := z.w.CreateHeader(fh)
	if err != nil {
		z.err = err
		return
	}
	if _, err := io.Copy(w, f); err != nil {
		z.err = err
		return
	}
}

func (w *errWriter) Write(p []byte) (n int, err error) {
	if err := *w.err; err != nil {
		return 0, err
	}
	n, err = w.w.Write(p)
	*w.err = err
	return
}

// androidManifest writes the manifest an installable package is described by.
//
// A description in and bytes out: what this file says is what a store lists,
// which permissions the system grants, and which links open the application --
// and none of those answers needs an SDK installed to be read back.
func androidManifest(data manifestData) ([]byte, error) {
	tmpl, err := template.New("manifest").Funcs(markup).Parse(
		`<?xml version="1.0" encoding="utf-8"?>
<manifest xmlns:android="http://schemas.android.com/apk/res/android"
	package="{{xml .AppID}}"
	android:versionCode="{{.Version.VersionCode}}"
	android:versionName="{{.Version}}">
	{{if .PackageQueries}}
 	<queries>
	    {{range .PackageQueries}}
        <package android:name="{{xml .}}" />
        {{end}}
    </queries>
	{{end}}
	<uses-sdk android:minSdkVersion="{{.MinSDK}}" android:targetSdkVersion="{{.TargetSDK}}" />{{if .QueriesXML}}
	<queries>{{range .QueriesXML}}
		{{.}}{{end}}
	</queries>{{end}}
{{range .Permissions}}	<uses-permission android:name="{{xml .}}"/>
{{end}}{{range .Features}}	<uses-feature android:{{.}} android:required="false"/>
{{end}}{{range .TopLevel}}	{{.}}
{{end}}	<application {{.IconSnip}} android:label="{{xml .AppName}}">
		<activity android:name="io.arandu.ayra.AyraActivity"
			android:label="{{xml .AppName}}"
			android:theme="@style/Theme.AyraApp"
			android:configChanges="screenSize|screenLayout|smallestScreenSize|orientation|keyboardHidden|uiMode"
			android:windowSoftInputMode="adjustResize"
			android:launchMode="singleInstance"
			android:exported="true">
			<intent-filter>
				<action android:name="android.intent.action.MAIN" />
				<category android:name="android.intent.category.LAUNCHER" />
			</intent-filter>
			{{range .Schemes}}
			<intent-filter>
				<action android:name="android.intent.action.VIEW"></action>
				<category android:name="android.intent.category.DEFAULT"></category>
				<category android:name="android.intent.category.BROWSABLE"></category>
				<data android:scheme="{{xml .}}"></data>
			</intent-filter>
			{{end}}
		</activity>{{range .Components}}
		{{.}}{{end}}
	</application>
</manifest>`)
	if err != nil {
		return nil, err
	}

	var manifest bytes.Buffer
	if err := tmpl.Execute(&manifest, data); err != nil {
		return nil, err
	}
	return manifest.Bytes(), nil
}
