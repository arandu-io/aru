package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/arandu-io/aru/internal/buildcache"
	"github.com/arandu-io/aru/internal/gomod"
	"github.com/arandu-io/hesape/arandutest/browser/cdp"
)

// playwrightModule is the driver module that runs the journeys in Chromium,
// Firefox and WebKit through Playwright.
const playwrightModule = "github.com/hyz-is/arandu-playwright"

// playwrightInstaller is the command of that module that installs its driver
// and its browsers into the user's cache, outside the project tree.
const playwrightInstaller = playwrightModule + "/cmd/install-browsers"

// e2eInstallUsage is the usage line of e2e:install-browsers.
const e2eInstallUsage = "aru e2e:install-browsers [arguments for the driver's installer]"

// e2eInstallHelp is what `aru e2e:install-browsers --help` prints.
const e2eInstallHelp = `The native driver, cdp, drives the Chrome or Chromium already installed --
CHROME_PATH, or the standard places -- and needs nothing installed for it; this
command says which browser it found.

When go.mod requires ` + playwrightModule + `, the command runs that module's
installer at the version go.mod pins, which downloads Playwright's driver and its
browsers into the user's cache, outside the project tree. Every argument is
handed to the installer as it is.`

// e2eInstallBrowsers is the command.
func e2eInstallBrowsers(args []string, stdout, stderr io.Writer) error {
	root, err := projectRoot()
	if err != nil {
		return err
	}
	if requiresModule(root, playwrightModule) {
		if _, err := exec.LookPath("go"); err != nil {
			return errors.New("the go toolchain was not found in PATH, and the installer of " + playwrightModule + " is run with it")
		}
		fmt.Fprintf(stdout, "go.mod requires %s: running its installer\n", playwrightModule)
		cmd := buildcache.Command(append([]string{"run", playwrightInstaller}, args...)...)
		cmd.Dir = root
		cmd.Stdout = stdout
		cmd.Stderr = stderr
		cmd.Stdin = os.Stdin
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("the installer of %s failed: %w", playwrightModule, err)
		}
	} else if len(args) > 0 {
		return fmt.Errorf("e2e:install-browsers takes arguments only for the installer of %s, which go.mod does not require", playwrightModule)
	}

	path, err := cdp.Find()
	if err != nil {
		fmt.Fprintf(stdout, "The native driver, cdp, found no browser: %v\n", err)
		if !requiresModule(root, playwrightModule) {
			return errors.New("install Google Chrome or Chromium, or set CHROME_PATH; the native driver installs nothing itself")
		}
		return nil
	}
	fmt.Fprintf(stdout, "The native driver, cdp, uses the installed browser at %s, and needs nothing installed.\n", path)
	return nil
}

// requiresModule reports whether the go.mod at root requires module.
func requiresModule(root, module string) bool {
	body, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		return false
	}
	_, ok := gomod.Parse(string(body)).Versions[module]
	return ok
}
