// SPDX-License-Identifier: Unlicense OR MIT

package pack

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/template"

	"golang.org/x/tools/go/packages"
)

func buildJS(bi *buildInfo) error {
	out := *destPath
	if out == "" {
		out = bi.name
	}
	if err := os.MkdirAll(out, 0o700); err != nil {
		return err
	}
	cmd := exec.Command(
		"go",
		"build",
		"-ldflags="+bi.ldflags,
		"-tags="+bi.tags,
		"-o", filepath.Join(out, "main.wasm"),
		bi.pkgPath,
	)
	cmd.Env = append(
		os.Environ(),
		"GOOS=js",
		"GOARCH=wasm",
		// Off, and set rather than inherited. This platform has no cgo, and a
		// developer whose shell exports it on -- which is an ordinary setting,
		// and is what this project's own commands use -- got the toolchain
		// honouring the variable over the platform: the standard library's
		// user lookup then selected a path with no implementation for this
		// pair, and the failure named five functions inside the standard
		// library and nothing of the project's. The command that builds
		// without packaging already decides this per platform; this is the
		// same decision, on the path that produces the artifact somebody
		// ships.
		"CGO_ENABLED=0",
	)
	_, err := runCmd(cmd)
	if err != nil {
		return err
	}

	var faviconPath string
	if _, err := os.Stat(bi.iconPath); err == nil {
		// Copy icon to the output folder
		icon, err := os.ReadFile(bi.iconPath)
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(out, filepath.Base(bi.iconPath)), icon, 0o600); err != nil {
			return err
		}
		faviconPath = filepath.Base(bi.iconPath)
	}

	indexTemplate, err := template.New("").Parse(jsIndex)
	if err != nil {
		return err
	}

	var b bytes.Buffer
	if err := indexTemplate.Execute(&b, struct {
		Name string
		Icon string
	}{
		Name: bi.name,
		Icon: faviconPath,
	}); err != nil {
		return err
	}

	if err := os.WriteFile(filepath.Join(out, "index.html"), b.Bytes(), 0o600); err != nil {
		return err
	}

	goroot, err := runCmd(exec.Command("go", "env", "GOROOT"))
	if err != nil {
		return err
	}
	// Location of the wasm_exec.js for go>=1.24
	wasmJS := filepath.Join(goroot, "lib", "wasm", "wasm_exec.js")
	if _, err := os.Stat(wasmJS); err != nil {
		// Location of the wasm_exec.js for go<1.24
		wasmJS = filepath.Join(goroot, "misc", "wasm", "wasm_exec.js")
		if _, err := os.Stat(wasmJS); err != nil {
			return fmt.Errorf("failed to find $GOROOT/misc/wasm/wasm_exec.js driver: %v", err)
		}
	}
	var extraJS []string
	err = walkGraph(bi.graph, func(p *packages.Package) (bool, error) {
		if len(p.GoFiles) == 0 {
			return false, nil
		}
		js, err := filepath.Glob(filepath.Join(filepath.Dir(p.GoFiles[0]), "*_js.js"))
		if err != nil {
			return false, err
		}
		extraJS = append(extraJS, js...)
		return true, nil
	})
	if err != nil {
		return err
	}

	return mergeJSFiles(filepath.Join(out, "wasm.js"), append([]string{wasmJS}, extraJS...)...)
}

// mergeJSFiles will merge all files into a single `wasm.js`. It will prepend the jsSetGo
// and append the jsStartGo.
func mergeJSFiles(dst string, files ...string) (err error) {
	w, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer func() {
		if cerr := w.Close(); err != nil {
			err = cerr
		}
	}()
	_, err = io.Copy(w, strings.NewReader(jsSetGo))
	if err != nil {
		return err
	}
	for i := range files {
		r, err := os.Open(files[i])
		if err != nil {
			return err
		}
		_, err = io.Copy(w, r)
		_ = r.Close()
		if err != nil {
			return err
		}
	}
	_, err = io.Copy(w, strings.NewReader(jsStartGo))
	return err
}

const (
	jsIndex = `<!doctype html>
<html>
	<head>
		<meta charset="utf-8">
		<meta name="viewport" content="width=device-width, user-scalable=no">
		<meta name="mobile-web-app-capable" content="yes">
		{{ if .Icon }}<link rel="icon" href="{{.Icon}}" type="image/x-icon" />{{ end }}
		{{ if .Name }}<title>{{.Name}}</title>{{ end }}
		<script src="wasm.js"></script>
		<style>
			body,pre { margin:0;padding:0; }
		</style>
	</head>
	<body>
	</body>
</html>`
	// jsSetGo sets the `window.go` variable.
	jsSetGo = `(() => {
    window.go = {argv: [], env: {}, importObject: {go: {}, gojs: {}}};
	const argv = new URLSearchParams(location.search).get("argv");
	if (argv) {
		window.go["argv"] = argv.split(" ");
	}
})();`
	// jsStartGo initializes the main.wasm.
	jsStartGo = `(() => {
	defaultGo = new Go();
	Object.assign(defaultGo["argv"], defaultGo["argv"].concat(go["argv"]));
	Object.assign(defaultGo["env"], go["env"]);
	for (let key in go["importObject"]) {
		if (typeof defaultGo["importObject"][key] === "undefined") {
			defaultGo["importObject"][key] = {};
		}
		Object.assign(defaultGo["importObject"][key], go["importObject"][key]);
	}
	window.go = defaultGo;
    if (!WebAssembly.instantiateStreaming) { // polyfill
        WebAssembly.instantiateStreaming = async (resp, importObject) => {
            const source = await (await resp).arrayBuffer();
            return await WebAssembly.instantiate(source, importObject);
        };
    }
    WebAssembly.instantiateStreaming(fetch("main.wasm"), go.importObject).then((result) => {
        go.run(result.instance);
    });
})();`
)
