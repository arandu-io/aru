package pack

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
)

// Libraries an Android application is built with that are not Go.
//
// A package of the application's graph may carry Java of its own, and the Java
// may call a library -- the push service's, for one. The packager has no
// dependency resolver and does not download anything on its own account: the
// libraries are files the project names, as .aar or .jar, and what this file
// does with them is what Gradle would have done with the same files -- put their
// classes on the compiler's path and into the dex, merge what their manifests
// declare into the application's, compile their resources, and make the R
// classes their code reads them through.
//
// What the files are, and under whose terms they are used, is the project's
// choice and not something a packager carries: nothing here is vendored.

// androidLibrary is one .aar unpacked, or one .jar.
type androidLibrary struct {
	// name is the file's base name, for a message.
	name string
	// pkg is the package the library's manifest declares, which its R class and
	// its relative component names are in.
	pkg string
	// manifest is the library's AndroidManifest.xml, empty for a plain jar.
	manifest []byte
	// jars are the library's classes: classes.jar and whatever libs/ holds.
	jars []string
	// res is the library's resources directory, empty when it has none.
	res string
	// native are the shared libraries the archive carries, by ABI.
	native []nativeLibrary
}

// nativeLibrary is a .so inside an archive's jni directory.
type nativeLibrary struct {
	abi  string
	file string
}

// androidLibraries is every library of a build.
type androidLibraries []androidLibrary

// classpath answers every jar of every library, in the order they were given.
func (l androidLibraries) classpath() []string {
	var jars []string
	for _, lib := range l {
		jars = append(jars, lib.jars...)
	}
	return jars
}

// resourceDirs answers the resources directories that exist.
func (l androidLibraries) resourceDirs() []string {
	var dirs []string
	for _, lib := range l {
		if lib.res != "" {
			dirs = append(dirs, lib.res)
		}
	}
	return dirs
}

// packages answers the package of every library that declares one, which is
// what the R classes are generated for.
func (l androidLibraries) packages() []string {
	var pkgs []string
	for _, lib := range l {
		if lib.pkg != "" {
			pkgs = append(pkgs, lib.pkg)
		}
	}
	return pkgs
}

// openLibraries reads the libraries the project named into dir.
//
// A path is a file or a directory: a directory contributes every .aar and .jar
// directly inside it, in name order, so that a build does not depend on the
// order the file system lists them in.
func openLibraries(dir string, paths []string) (androidLibraries, error) {
	var files []string
	for _, path := range paths {
		info, err := os.Stat(path)
		if err != nil {
			return nil, fmt.Errorf("android library %s: %w", path, err)
		}
		if !info.IsDir() {
			files = append(files, path)
			continue
		}
		entries, err := os.ReadDir(path)
		if err != nil {
			return nil, err
		}
		var found []string
		for _, e := range entries {
			if ext := filepath.Ext(e.Name()); !e.IsDir() && (ext == ".aar" || ext == ".jar") {
				found = append(found, filepath.Join(path, e.Name()))
			}
		}
		sort.Strings(found)
		files = append(files, found...)
	}

	var libs androidLibraries
	for i, file := range files {
		switch filepath.Ext(file) {
		case ".jar":
			libs = append(libs, androidLibrary{name: filepath.Base(file), jars: []string{file}})
		case ".aar":
			lib, err := openArchive(filepath.Join(dir, fmt.Sprintf("%03d-%s", i, strings.TrimSuffix(filepath.Base(file), ".aar"))), file)
			if err != nil {
				return nil, err
			}
			libs = append(libs, lib)
		default:
			return nil, fmt.Errorf("android library %s is neither an .aar nor a .jar", file)
		}
	}
	return libs, nil
}

// openArchive unpacks an .aar into dir and reads what it carries.
func openArchive(dir, file string) (androidLibrary, error) {
	lib := androidLibrary{name: filepath.Base(file)}
	zr, err := zip.OpenReader(file)
	if err != nil {
		return lib, fmt.Errorf("android library %s: %w", file, err)
	}
	defer zr.Close()

	for _, f := range zr.File {
		target := filepath.Join(dir, filepath.FromSlash(f.Name))
		// An archive is somebody else's file: a name that climbs out of the
		// directory it is unpacked into is refused rather than written.
		if rel, err := filepath.Rel(dir, target); err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return lib, fmt.Errorf("android library %s: %q leaves the directory it is unpacked into", file, f.Name)
		}
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return lib, err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return lib, err
		}
		if err := extractZipFile(f, target); err != nil {
			return lib, fmt.Errorf("android library %s: %w", file, err)
		}
	}

	if body, err := os.ReadFile(filepath.Join(dir, "AndroidManifest.xml")); err == nil {
		lib.manifest = body
		lib.pkg = manifestPackage(body)
	}
	if jar := filepath.Join(dir, "classes.jar"); fileExists(jar) {
		lib.jars = append(lib.jars, jar)
	}
	if more, err := filepath.Glob(filepath.Join(dir, "libs", "*.jar")); err == nil {
		sort.Strings(more)
		lib.jars = append(lib.jars, more...)
	}
	if res := filepath.Join(dir, "res"); dirHasFiles(res) {
		lib.res = res
	}
	if abis, err := os.ReadDir(filepath.Join(dir, "jni")); err == nil {
		for _, abi := range abis {
			sos, _ := filepath.Glob(filepath.Join(dir, "jni", abi.Name(), "*.so"))
			for _, so := range sos {
				lib.native = append(lib.native, nativeLibrary{abi: abi.Name(), file: so})
			}
		}
	}
	return lib, nil
}

func extractZipFile(f *zip.File, target string) error {
	r, err := f.Open()
	if err != nil {
		return err
	}
	defer r.Close()
	w, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	// The size is bounded by what the archive declares, so a small file that
	// inflates into gigabytes is cut off rather than filling the disk.
	if _, err := io.Copy(w, io.LimitReader(r, int64(f.UncompressedSize64)+1)); err != nil {
		w.Close()
		return err
	}
	return w.Close()
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// dirHasFiles reports whether dir holds a file anywhere below it.
func dirHasFiles(dir string) bool {
	found := false
	_ = filepath.Walk(dir, func(_ string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			found = true
			return io.EOF
		}
		return nil
	})
	return found
}

var packageAttr = regexp.MustCompile(`<manifest[^>]*?\spackage\s*=\s*"([^"]*)"`)

// manifestPackage reads the package attribute of a manifest.
func manifestPackage(body []byte) string {
	if m := packageAttr.FindSubmatch(body); m != nil {
		return string(m[1])
	}
	return ""
}

var minSDKAttr = regexp.MustCompile(`<uses-sdk[^>]*?android:minSdkVersion\s*=\s*"(\d+)"`)

// minSDK answers the lowest Android version the library says it runs on, zero
// when it does not say.
func (l androidLibrary) minSDK() int {
	if m := minSDKAttr.FindSubmatch(l.manifest); m != nil {
		n, _ := strconv.Atoi(string(m[1]))
		return n
	}
	return 0
}

// manifestSource is one manifest to merge, and the package its relative names
// are in.
type manifestSource struct {
	name string
	pkg  string
	body []byte
}

// mergedManifest is what the manifests of the packages and the libraries
// contribute to the application's.
type mergedManifest struct {
	// Permissions are the uses-permission names.
	Permissions []string
	// Features are the uses-feature entries, in the form the template writes.
	Features []string
	// TopLevel is the other direct children of the manifest, such as a
	// permission an AndroidX library declares for itself.
	TopLevel []string
	// Queries are the children of the queries elements.
	Queries []string
	// Components are the children of the application element.
	Components []string
}

var (
	toolsAttr     = regexp.MustCompile(`\s+tools:[A-Za-z]+\s*=\s*"[^"]*"`)
	placeholder   = regexp.MustCompile(`\$\{([A-Za-z0-9_.]+)\}`)
	nameAttr      = regexp.MustCompile(`android:name\s*=\s*"([^"]*)"`)
	componentTags = map[string]bool{"activity": true, "activity-alias": true, "service": true, "receiver": true, "provider": true}
)

// mergeManifests merges what each source declares, in order.
//
// It is the part of a manifest merge an application needs and not more: the
// permissions, features, queries and application children of each source are
// added to the application's, once each, with the placeholders written in them
// resolved. An element is the same as another when it has the same kind and the
// same android:name. Nothing is replaced, removed or reconciled -- the tools
// attributes that ask for that are dropped -- so two libraries that disagree
// about one component are not told apart, and the first one wins.
//
// An unresolved placeholder is an error and not an empty string: a manifest
// that says android:value="" for the one setting a service reads is a package
// that builds and then does nothing.
func mergeManifests(sources []manifestSource, values map[string]string) (mergedManifest, error) {
	var out mergedManifest
	seen := map[string]bool{}
	once := func(kind, key string) bool {
		id := kind + "\x00" + key
		if seen[id] {
			return false
		}
		seen[id] = true
		return true
	}

	for _, src := range sources {
		children, err := manifestChildren(src.body)
		if err != nil {
			return out, fmt.Errorf("manifest of %s: %w", src.name, err)
		}
		for _, child := range children {
			text, err := resolvePlaceholders(child.text, values)
			if err != nil {
				return out, fmt.Errorf("manifest of %s: %w", src.name, err)
			}
			attr := nameOf(text)

			switch child.tag {
			case "uses-sdk":
			case "uses-permission":
				if once("uses-permission", attr) {
					out.Permissions = append(out.Permissions, attr)
				}
			case "uses-feature":
				if attr != "" && once("uses-feature", attr) {
					out.Features = append(out.Features, `name="`+attr+`"`)
				}
			case "queries":
				for _, q := range child.children {
					qt, err := resolvePlaceholders(q.text, values)
					if err != nil {
						return out, fmt.Errorf("manifest of %s: %w", src.name, err)
					}
					if once("queries", qt) {
						out.Queries = append(out.Queries, qt)
					}
				}
			case "application":
				for _, c := range child.children {
					ct, err := resolvePlaceholders(c.text, values)
					if err != nil {
						return out, fmt.Errorf("manifest of %s: %w", src.name, err)
					}
					ct = qualifyComponent(c.tag, ct, src.pkg)
					if once(c.tag, nameOf(ct)+"\x00"+firstTag(ct)) {
						out.Components = append(out.Components, ct)
					}
				}
			default:
				if once(child.tag, text) {
					out.TopLevel = append(out.TopLevel, text)
				}
			}
		}
	}
	return out, nil
}

// nameOf answers the android:name of the element whose text this is, from its
// opening tag.
func nameOf(element string) string {
	if m := nameAttr.FindStringSubmatch(firstTag(element)); m != nil {
		return m[1]
	}
	return ""
}

// firstTag is the opening tag of an element, up to its first closing bracket.
func firstTag(element string) string {
	if i := strings.Index(element, ">"); i >= 0 {
		return element[:i+1]
	}
	return element
}

// qualifyComponent gives a component of a library its full class name: a name
// that starts with a dot, or has none, is in the library's package.
func qualifyComponent(tag, element, pkg string) string {
	if !componentTags[tag] || pkg == "" {
		return element
	}
	head := firstTag(element)
	m := nameAttr.FindStringSubmatchIndex(head)
	if m == nil {
		return element
	}
	name := head[m[2]:m[3]]
	switch {
	case strings.HasPrefix(name, "."):
		name = pkg + name
	case !strings.Contains(name, "."):
		name = pkg + "." + name
	default:
		return element
	}
	return head[:m[2]] + name + head[m[3]:] + element[len(head):]
}

// resolvePlaceholders replaces ${name} with its value and drops the tools
// attributes, which ask a merger to do what this one does not.
func resolvePlaceholders(text string, values map[string]string) (string, error) {
	text = toolsAttr.ReplaceAllString(text, "")
	var missing string
	text = placeholder.ReplaceAllStringFunc(text, func(match string) string {
		key := match[2 : len(match)-1]
		v, ok := values[key]
		if !ok && missing == "" {
			missing = key
		}
		return xmlText(v)
	})
	if missing != "" {
		return "", fmt.Errorf("the placeholder ${%s} has no value; give one with -placeholder %s=VALUE", missing, missing)
	}
	return text, nil
}

// manifestNode is a direct child of an element, with the text it was written
// as.
type manifestNode struct {
	tag      string
	text     string
	children []manifestNode
}

// manifestChildren reads the children of the manifest element, and of the
// application and queries elements among them, keeping each as the text it was
// written in. Keeping the text is what leaves the namespace prefixes exactly as
// the library wrote them, which a decode and re-encode would rewrite.
func manifestChildren(body []byte) ([]manifestNode, error) {
	dec := xml.NewDecoder(bytes.NewReader(body))
	dec.Strict = false

	type frame struct {
		start int64
		tag   string
		kids  []manifestNode
	}
	var stack []frame
	var top []manifestNode
	var last int64

	for {
		before := last
		tok, err := dec.RawToken()
		last = dec.InputOffset()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			stack = append(stack, frame{start: before, tag: t.Name.Local})
		case xml.EndElement:
			if len(stack) == 0 {
				return nil, errors.New("an element is closed that was never opened")
			}
			f := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			node := manifestNode{tag: f.tag, text: strings.TrimSpace(string(body[f.start:last])), children: f.kids}
			switch len(stack) {
			case 0:
				// the manifest element itself
			case 1:
				top = append(top, node)
			default:
				if len(stack) == 2 && (stack[1].tag == "application" || stack[1].tag == "queries") {
					stack[1].kids = append(stack[1].kids, node)
				}
			}
		}
	}
	if len(stack) != 0 {
		return nil, errors.New("the manifest ends inside an element")
	}
	return top, nil
}

// googleServices is the part of google-services.json the application reads.
type googleServices struct {
	ProjectInfo struct {
		ProjectNumber string `json:"project_number"`
		ProjectID     string `json:"project_id"`
		FirebaseURL   string `json:"firebase_url"`
		StorageBucket string `json:"storage_bucket"`
	} `json:"project_info"`
	Client []struct {
		ClientInfo struct {
			AppID   string `json:"mobilesdk_app_id"`
			Android struct {
				Package string `json:"package_name"`
			} `json:"android_client_info"`
		} `json:"client_info"`
		OAuth []struct {
			ClientID   string `json:"client_id"`
			ClientType int    `json:"client_type"`
		} `json:"oauth_client"`
		APIKey []struct {
			CurrentKey string `json:"current_key"`
		} `json:"api_key"`
	} `json:"client"`
}

// googleServicesResources turns a google-services.json into the string
// resources the Firebase libraries read their configuration from, for the
// client whose package is appID.
//
// It is what the Gradle plugin of the same name does with the same file, and the
// names are the ones that plugin writes. The file belongs to the project and
// holds its keys, so nothing here prints it and the packager reads it only to
// write the resources into the artifact it builds.
func googleServicesResources(body []byte, appID string) ([]byte, error) {
	var g googleServices
	if err := json.Unmarshal(body, &g); err != nil {
		return nil, fmt.Errorf("google-services.json is not valid: %w", err)
	}
	if g.ProjectInfo.ProjectNumber == "" {
		return nil, errors.New("google-services.json has no project_info.project_number")
	}
	var available []string
	for _, c := range g.Client {
		if c.ClientInfo.Android.Package != appID {
			available = append(available, c.ClientInfo.Android.Package)
			continue
		}
		values := [][2]string{
			{"gcm_defaultSenderId", g.ProjectInfo.ProjectNumber},
			{"project_id", g.ProjectInfo.ProjectID},
			{"google_app_id", c.ClientInfo.AppID},
			{"firebase_database_url", g.ProjectInfo.FirebaseURL},
			{"google_storage_bucket", g.ProjectInfo.StorageBucket},
		}
		if len(c.APIKey) > 0 {
			values = append(values,
				[2]string{"google_api_key", c.APIKey[0].CurrentKey},
				[2]string{"google_crash_reporting_api_key", c.APIKey[0].CurrentKey})
		}
		for _, o := range c.OAuth {
			if o.ClientType == 3 {
				values = append(values, [2]string{"default_web_client_id", o.ClientID})
				break
			}
		}
		var out bytes.Buffer
		out.WriteString("<?xml version=\"1.0\" encoding=\"utf-8\"?>\n<resources>\n")
		for _, kv := range values {
			if kv[1] == "" {
				continue
			}
			fmt.Fprintf(&out, "\t<string name=\"%s\" translatable=\"false\">%s</string>\n", kv[0], xmlResourceText(kv[1]))
		}
		out.WriteString("</resources>\n")
		return out.Bytes(), nil
	}
	return nil, fmt.Errorf("google-services.json has no client for the package %q (it has: %s); the application id has to be one of them, and -appid sets it",
		appID, strings.Join(available, ", "))
}

// xmlResourceText escapes a value for a string resource. aapt2 reads the text of
// one after the XML has been parsed, and gives a backslash, an apostrophe, a
// quote and a leading at sign or question mark a meaning of their own.
func xmlResourceText(v string) string {
	v = strings.NewReplacer(`\`, `\\`, `'`, `\'`, `"`, `\"`).Replace(v)
	if strings.HasPrefix(v, "@") || strings.HasPrefix(v, "?") {
		v = `\` + v
	}
	return xmlText(v)
}

// manifestDefaults are the values the ${name} of a manifest take when the
// project gave none.
var manifestDefaults = map[string]string{
	"pushChannelId":   "ayra_default",
	"pushChannelName": "Notifications",
}

// mergeAndroidManifests adds what the packages' and libraries' manifests
// declare to the application's, and says whether there was anything to add.
func mergeAndroidManifests(bi *buildInfo, data *manifestData) (bool, error) {
	sources := append([]manifestSource(nil), bi.androidFragments...)
	for _, lib := range bi.androidLibs {
		if len(lib.manifest) > 0 {
			sources = append(sources, manifestSource{name: lib.name, pkg: lib.pkg, body: lib.manifest})
		}
	}
	if len(sources) == 0 {
		return false, nil
	}

	values := map[string]string{"applicationId": bi.appID}
	for k, v := range manifestDefaults {
		values[k] = v
	}
	for k, v := range bi.placeholders {
		values[k] = v
	}
	merged, err := mergeManifests(sources, values)
	if err != nil {
		return false, err
	}
	for _, p := range merged.Permissions {
		if !slices.Contains(data.Permissions, p) {
			data.Permissions = append(data.Permissions, p)
		}
	}
	for _, f := range merged.Features {
		if !slices.Contains(data.Features, f) {
			data.Features = append(data.Features, f)
		}
	}
	data.TopLevel = merged.TopLevel
	data.QueriesXML = merged.Queries
	data.Components = merged.Components
	return true, nil
}

// compileResourceClasses compiles the R classes aapt2 wrote and adds them to the
// classes that go into the dex.
func compileResourceClasses(tmpDir, generated string, tools *androidTools, classFiles *[]string) error {
	var sources []string
	_ = filepath.Walk(generated, func(path string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() && filepath.Ext(path) == ".java" {
			sources = append(sources, path)
		}
		return nil
	})
	if len(sources) == 0 {
		return nil
	}
	javac, err := findJavaC()
	if err != nil {
		return fmt.Errorf("could not find javac: %v", err)
	}
	out := filepath.Join(tmpDir, "rclasses")
	if err := os.MkdirAll(out, 0o755); err != nil {
		return err
	}
	args := append([]string{"-target", "1.8", "-source", "1.8", "-bootclasspath", tools.androidjar, "-d", out}, sources...)
	if _, err := runCmd(exec.Command(javac, args...)); err != nil {
		return err
	}
	return filepath.Walk(out, func(path string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() && filepath.Ext(path) == ".class" {
			*classFiles = append(*classFiles, path)
		}
		return err
	})
}
