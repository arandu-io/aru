package pack

import (
	"bytes"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"image/png"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"text/template"

	"github.com/akavel/rsrc/binutil"
	"github.com/akavel/rsrc/coff"
	"golang.org/x/text/encoding/unicode"
)

func buildWindows(tmpDir string, bi *buildInfo) error {
	signer, err := windowsSignerFor(bi)
	if err != nil {
		return err
	}

	builder := &windowsBuilder{TempDir: tmpDir, Signer: signer}
	builder.DestDir = *destPath
	if builder.DestDir == "" {
		builder.DestDir = bi.pkgPath
	}

	name := bi.name
	if *destPath != "" {
		if filepath.Ext(*destPath) != ".exe" {
			return fmt.Errorf("invalid output name %q, it must end with `.exe`", *destPath)
		}
		name = filepath.Base(*destPath)
	}
	name = strings.TrimSuffix(name, ".exe")
	sdk := bi.minsdk
	if sdk > 10 {
		return fmt.Errorf("invalid minsdk (%d) it's higher than Windows 10", sdk)
	}

	for _, arch := range bi.archs {
		builder.Coff = coff.NewRSRC()
		_ = builder.Coff.Arch(arch)

		if err := builder.embedIcon(bi.iconPath); err != nil {
			return err
		}

		if err := builder.embedManifest(windowsManifest{
			Version:        bi.version.String(),
			WindowsVersion: sdk,
			Name:           name,
		}); err != nil {
			return fmt.Errorf("can't create manifest: %v", err)
		}

		if err := builder.embedInfo(windowsResources{
			Version:      [2]uint32{uint32(bi.version.Major), uint32(bi.version.Minor)<<16 | uint32(bi.version.Patch)},
			VersionHuman: bi.version.String(),
			Name:         name,
			Language:     0x0400, // Process Default Language: https://docs.microsoft.com/en-us/previous-versions/ms957130(v=msdn.10)
		}); err != nil {
			return fmt.Errorf("can't create info: %v", err)
		}

		if err := builder.buildArch(bi, name, arch); err != nil {
			return err
		}
	}

	return builder.publishPrograms()
}

type (
	windowsResources struct {
		Version      [2]uint32
		VersionHuman string
		Language     uint16
		Name         string
	}
	windowsManifest struct {
		Version        string
		WindowsVersion int
		Name           string
	}
	windowsBuilder struct {
		TempDir string
		DestDir string
		Coff    *coff.Coff
		Signer  *windowsSigner
		pending []windowsProgram
		rename  func(string, string) error
		remove  func(string) error
	}
	windowsProgram struct {
		staged string
		final  string
	}
)

// windowsSigner is the external Authenticode tool and the PFX it signs with.
// Windows owns the signature format, and the SDK tool is the authority that
// both writes and verifies it; accepting a key without that tool would produce
// the same unsigned executable as no key at all.
type windowsSigner struct {
	command           string
	key               string
	password          string
	importCertificate func(key, password string) (windowsCertificate, error)
}

type windowsCertificate struct {
	store      string
	thumbprint string
	remove     func() error
}

// windowsSignerFor resolves signing before any output is built. A requested
// signature that cannot be produced must not leave an unsigned executable at
// the destination where a pipeline expects the signed one.
func windowsSignerFor(buildInfo *buildInfo) (*windowsSigner, error) {
	if buildInfo.key == "" {
		return nil, nil
	}
	if _, err := os.Stat(buildInfo.key); err != nil {
		return nil, fmt.Errorf("windows signing key %q could not be read: %w", buildInfo.key, err)
	}

	command, err := exec.LookPath("signtool")
	if err != nil {
		return nil, errors.New("windows signing requires signtool from the Windows SDK in PATH")
	}
	signer := &windowsSigner{command: command, key: buildInfo.key, password: buildInfo.password}
	if buildInfo.password != "" {
		powershell, err := exec.LookPath("powershell")
		if err != nil {
			return nil, errors.New("password-protected Windows signing requires PowerShell in PATH")
		}
		signer.importCertificate = func(key, password string) (windowsCertificate, error) {
			return importWindowsCertificate(powershell, key, password)
		}
	}
	return signer, nil
}

// sign applies and then verifies the Authenticode signature. Verification is
// part of packaging: a tool returning success without leaving a signature is
// not a signed artifact.
func (s *windowsSigner) sign(program string) (err error) {
	args := []string{"sign", "/fd", "SHA256"}
	shown := append([]string(nil), args...)
	if s.password != "" {
		if s.importCertificate == nil {
			return errors.New("password-protected Windows signing has no certificate importer")
		}
		certificate, importErr := s.importCertificate(s.key, s.password)
		if importErr != nil {
			return fmt.Errorf("importing Windows signing certificate: %w", importErr)
		}
		defer func() {
			if removeErr := certificate.remove(); removeErr != nil {
				err = errors.Join(err, fmt.Errorf("removing temporary Windows certificate store: %w", removeErr))
			}
		}()
		args = append(args, "/s", certificate.store, "/sha1", certificate.thumbprint)
		shown = append(shown, "/s", certificate.store, "/sha1", certificate.thumbprint)
	} else {
		args = append(args, "/f", s.key)
		shown = append(shown, "/f", s.key)
	}
	args = append(args, program)
	shown = append(shown, program)
	if err := runWindowsSigningCommand(exec.Command(s.command, args...), shown, s.password); err != nil {
		return fmt.Errorf("signing Windows executable: %w", err)
	}

	verify := []string{"verify", "/pa", "/v", program}
	if err := runWindowsSigningCommand(exec.Command(s.command, verify...), verify); err != nil {
		return fmt.Errorf("verifying Windows executable signature: %w", err)
	}
	return nil
}

// importWindowsCertificate puts a password-protected PFX in a unique Current
// User store and returns only its public thumbprint to signtool. Isolation is
// important here: deriving cleanup from changes to the shared My store could
// delete an unrelated certificate imported concurrently, or leave behind a
// private key attached to a certificate that was already there. The password
// stays in the child environment: process command lines, verbose build output
// and signtool diagnostics never receive it.
func importWindowsCertificate(powershell, key, password string) (windowsCertificate, error) {
	storeBytes := make([]byte, 16)
	if _, err := rand.Read(storeBytes); err != nil {
		return windowsCertificate{}, fmt.Errorf("creating a temporary Windows certificate store name: %w", err)
	}
	ownerPID := strconv.Itoa(os.Getpid())
	storePrefix := "Arandu-" + ownerPID + "-" + hex.EncodeToString(storeBytes)
	const importScript = `$ErrorActionPreference = "Stop"
$ownerPid = [int]$args[1]
$storePrefix = $args[2]
$owner = Get-Process -Id $ownerPid -ErrorAction Stop
$ownerStart = $owner.StartTime.ToUniversalTime().Ticks
$storeName = "$storePrefix-$ownerStart"
$storePath = "Cert:\CurrentUser\$storeName"
function Remove-AranduStore([string]$name) {
    $path = "Cert:\CurrentUser\$name"
    $registry = "HKCU:\Software\Microsoft\SystemCertificates\$name"
    if (Test-Path -LiteralPath $path) {
      Get-ChildItem -Path $path | ForEach-Object {
        if ($_.HasPrivateKey) {
            Remove-Item -Path $_.PSPath -DeleteKey -Force
        } else {
            Remove-Item -Path $_.PSPath -Force
        }
      }
    }
    if (Test-Path -LiteralPath $registry) {
        Remove-Item -LiteralPath $registry -Recurse -Force
    }
}
# A hard-killed packager cannot run its Go defer. Stores carry the owning PID
# and process start time, so PID reuse cannot make an orphan look active.
Get-ChildItem -Path "Cert:\CurrentUser" | ForEach-Object {
    $candidate = $_.PSChildName
    if ($candidate -match "^Arandu-([0-9]+)-[0-9a-f]{32}-([0-9]+)$") {
        $candidatePid = [int]$Matches[1]
        $candidateStart = [long]$Matches[2]
        $candidateOwner = Get-Process -Id $candidatePid -ErrorAction SilentlyContinue
        if ($null -eq $candidateOwner -or $candidateOwner.StartTime.ToUniversalTime().Ticks -ne $candidateStart) {
            Remove-AranduStore $candidate
        }
    }
}
$secret = ConvertTo-SecureString $env:ARANDU_SIGNPASS -AsPlainText -Force
try {
    $store = [System.Security.Cryptography.X509Certificates.X509Store]::new(
        $storeName,
        [System.Security.Cryptography.X509Certificates.StoreLocation]::CurrentUser
    )
    $store.Open([System.Security.Cryptography.X509Certificates.OpenFlags]::ReadWrite)
    $store.Close()
    $certificates = @(Import-PfxCertificate -FilePath $args[0] -CertStoreLocation $storePath -Password $secret)
    $certificate = $certificates | Where-Object HasPrivateKey | Select-Object -First 1
    if ($null -eq $certificate) { throw "The PFX contains no certificate with a private key." }
    if ($certificate.Thumbprint -notmatch "^[0-9A-Fa-f]{40}$") { throw "The signing certificate has an invalid thumbprint." }
    Write-Output $storeName
    Write-Output $certificate.Thumbprint
} catch {
    Remove-AranduStore $storeName
    throw
}`
	command := exec.Command(powershell, "-NoProfile", "-NonInteractive", "-Command", importScript, key, ownerPID, storePrefix)
	command.Env = append(os.Environ(), "ARANDU_SIGNPASS="+password)
	result, err := runWindowsSigningCommandOutput(command, []string{"import", key, "into", "temporary", "CurrentUser", "store", storePrefix}, password)
	if err != nil {
		return windowsCertificate{}, err
	}
	remove := removeWindowsCertificateStore(powershell, storePrefix)
	fields := strings.Fields(result)
	if len(fields) != 2 {
		return windowsCertificate{}, errors.Join(
			fmt.Errorf("PowerShell returned %d certificate import fields, want store and thumbprint", len(fields)),
			remove(),
		)
	}
	store := fields[0]
	start, hasPrefix := strings.CutPrefix(store, storePrefix+"-")
	if _, parseErr := strconv.ParseInt(start, 10, 64); !hasPrefix || start == "" || parseErr != nil {
		return windowsCertificate{}, errors.Join(
			fmt.Errorf("PowerShell returned invalid temporary certificate store %q", store),
			remove(),
		)
	}
	thumbprint := fields[1]
	if _, err := hex.DecodeString(thumbprint); err != nil || len(thumbprint) != 40 {
		return windowsCertificate{}, errors.Join(
			fmt.Errorf("PowerShell returned invalid certificate thumbprint %q", thumbprint),
			remove(),
		)
	}
	return windowsCertificate{store: store, thumbprint: strings.ToLower(thumbprint), remove: remove}, nil
}

func removeWindowsCertificateStore(powershell, storePrefix string) func() error {
	return func() error {
		const removeScript = `$ErrorActionPreference = "Stop"
	$storePrefix = $args[0]
	Get-ChildItem -Path "Cert:\CurrentUser" | Where-Object {
	    $_.PSChildName.StartsWith("$storePrefix-")
	} | ForEach-Object {
	    $storeName = $_.PSChildName
	    $storePath = "Cert:\CurrentUser\$storeName"
	    $registryPath = "HKCU:\Software\Microsoft\SystemCertificates\$storeName"
	    Get-ChildItem -Path $storePath | ForEach-Object {
	        if ($_.HasPrivateKey) {
	            Remove-Item -Path $_.PSPath -DeleteKey -Force
	        } else {
	            Remove-Item -Path $_.PSPath -Force
	        }
	    }
	    if (Test-Path -LiteralPath $registryPath) {
	        Remove-Item -LiteralPath $registryPath -Recurse -Force
	    }
	}`
		command := exec.Command(powershell, "-NoProfile", "-NonInteractive", "-Command", removeScript, storePrefix)
		return runWindowsSigningCommand(command, []string{"remove", "temporary", "CurrentUser", "certificate", "store", storePrefix})
	}
}

// runWindowsSigningCommand keeps a PFX password out of verbose output and
// errors while preserving every other argument needed to reproduce a failure.
func runWindowsSigningCommand(cmd *exec.Cmd, shown []string, secrets ...string) error {
	_, err := runWindowsSigningCommandOutput(cmd, shown, secrets...)
	return err
}

func runWindowsSigningCommandOutput(cmd *exec.Cmd, shown []string, secrets ...string) (string, error) {
	display := strings.Join(append([]string{filepath.Base(cmd.Path)}, shown...), " ")
	if *printCommands {
		fmt.Fprintln(output, display)
	}
	out, err := cmd.Output()
	if err == nil {
		return string(out), nil
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		detail := string(out) + string(exit.Stderr)
		for _, secret := range secrets {
			if secret != "" {
				detail = strings.ReplaceAll(detail, secret, "<redacted>")
			}
		}
		return "", fmt.Errorf("%s failed: %s", display, detail)
	}
	return "", fmt.Errorf("%s failed: %w", display, err)
}

const (
	// https://docs.microsoft.com/en-us/windows/win32/menurc/resource-types
	windowsResourceIcon      = 3
	windowsResourceIconGroup = windowsResourceIcon + 11
	windowsResourceManifest  = 24
	windowsResourceVersion   = 16
)

type bufferCoff struct {
	bytes.Buffer
}

func (b *bufferCoff) Size() int64 {
	return int64(b.Len())
}

func (b *windowsBuilder) embedIcon(path string) (err error) {
	iconFile, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("can't read the icon located at %s: %v", path, err)
	}
	defer iconFile.Close()

	iconImage, err := png.Decode(iconFile)
	if err != nil {
		return fmt.Errorf("can't decode the PNG file (%s): %v", path, err)
	}

	sizes := []int{16, 32, 48, 64, 128, 256}
	var iconHeader bufferCoff

	// GRPICONDIR structure.
	if err := binary.Write(&iconHeader, binary.LittleEndian, [3]uint16{0, 1, uint16(len(sizes))}); err != nil {
		return err
	}

	for _, size := range sizes {
		var iconBuffer bufferCoff

		if err := png.Encode(&iconBuffer, resizeIcon(iconVariant{size: size, fill: false}, iconImage)); err != nil {
			return fmt.Errorf("can't encode image: %v", err)
		}

		b.Coff.AddResource(windowsResourceIcon, uint16(size), &iconBuffer)

		if err := binary.Write(&iconHeader, binary.LittleEndian, struct {
			Size     [2]uint8
			Color    [2]uint8
			Planes   uint16
			BitCount uint16
			Length   uint32
			Id       uint16
		}{
			Size:     [2]uint8{uint8(size % 256), uint8(size % 256)}, // "0" means 256px.
			Planes:   1,
			BitCount: 32,
			Length:   uint32(iconBuffer.Len()),
			Id:       uint16(size),
		}); err != nil {
			return err
		}
	}

	b.Coff.AddResource(windowsResourceIconGroup, 1, &iconHeader)

	return nil
}

// buildArch writes the resource section, links the program against it, and
// takes the resource section back out.
//
// The resource file is an input rather than an artifact, and the Go toolchain
// picks it up by name: go build links every *_windows_*.syso found in the
// package's own directory, without being told to. One left behind is therefore
// linked into whatever is built there next -- the following architecture of
// this same loop, and every ordinary build of that package afterwards, each
// carrying the icon and version block of a packaging run nobody remembers
// starting. It goes on the way out, including when the link failed.
func (b *windowsBuilder) buildArch(buildInfo *buildInfo, name string, arch string) (err error) {
	// The directory the sources are in, which is not the path they are named
	// by: a package is given to the go tool as an import path, and writing a
	// file at one lands wherever the process happens to be standing.
	syso := filepath.Join(buildInfo.pkgDir, name+"_windows_"+arch+".syso")
	defer func() {
		if rerr := os.Remove(syso); rerr != nil && err == nil {
			err = fmt.Errorf("the resource file %s is still there, and the next build of that package would link it: %v", syso, rerr)
		}
	}()

	if err := b.buildResource(syso); err != nil {
		return fmt.Errorf("can't build the resources: %v", err)
	}
	return b.buildProgram(buildInfo, name, arch)
}

func (b *windowsBuilder) buildResource(dst string) error {
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	b.Coff.Freeze()

	// See https://github.com/akavel/rsrc/internal/write.go#L13.
	w := binutil.Writer{W: out}
	_ = binutil.Walk(b.Coff, func(v reflect.Value, path string) error {
		if binutil.Plain(v.Kind()) {
			w.WriteLE(v.Interface())
			return nil
		}
		vv, ok := v.Interface().(binutil.SizedReader)
		if ok {
			w.WriteFromSized(vv)
			return binutil.WALK_SKIP
		}
		return nil
	})

	if w.Err != nil {
		return fmt.Errorf("error writing output file: %s", w.Err)
	}

	return nil
}

func (b *windowsBuilder) buildProgram(buildInfo *buildInfo, name string, arch string) error {
	dest := b.DestDir
	if len(buildInfo.archs) > 1 {
		dest = filepath.Join(filepath.Dir(b.DestDir), name+"_"+arch+".exe")
	}
	staged, err := os.CreateTemp(filepath.Dir(dest), "."+filepath.Base(dest)+"-*.tmp")
	if err != nil {
		return fmt.Errorf("preparing the Windows executable: %w", err)
	}
	stagedPath := staged.Name()
	if err := staged.Close(); err != nil {
		return err
	}
	if err := os.Remove(stagedPath); err != nil {
		return err
	}
	keep := false
	defer func() {
		if !keep {
			_ = os.Remove(stagedPath)
		}
	}()

	ldflags := buildInfo.ldflags
	if buildInfo.schemes != nil {
		ldflags += ` -X "` + buildInfo.runtime.path + `.schemesURI=` + strings.Join(buildInfo.schemes, ",") + `" `
	}
	if buildInfo.appID != "" {
		ldflags += ` -X "` + buildInfo.runtime.path + `.ID=` + buildInfo.appID + `" `
	}

	cmd := exec.Command(
		"go",
		"build",
		"-ldflags=-H=windowsgui "+ldflags,
		"-tags="+buildInfo.tags,
		"-o", stagedPath,
		buildInfo.pkgPath,
	)
	cmd.Env = append(
		os.Environ(),
		"GOOS=windows",
		"GOARCH="+arch,
	)
	_, err = runCmd(cmd)
	if err != nil {
		return err
	}
	if b.Signer != nil {
		if err := b.Signer.sign(stagedPath); err != nil {
			if removeErr := os.Remove(stagedPath); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
				return fmt.Errorf("%w; removing the unverified Windows executable: %v", err, removeErr)
			}
			return err
		}
	}
	b.pending = append(b.pending, windowsProgram{staged: stagedPath, final: dest})
	keep = true
	return nil
}

// publishPrograms moves only completely built and, when requested, verified
// programs into their public paths. Until this point an interrupted signing
// run can leave only a hidden temporary file, never an unsigned release
// artifact with its final name.
func (b *windowsBuilder) publishPrograms() (err error) {
	defer func() {
		for _, program := range b.pending {
			_ = b.removeFile(program.staged)
		}
	}()

	type publication struct {
		windowsProgram
		backup    string
		published bool
	}
	publications := make([]publication, len(b.pending))
	rollback := func() error {
		var rollbackErrors []error
		for i := len(publications) - 1; i >= 0; i-- {
			publication := publications[i]
			if publication.published {
				if removeErr := b.removeFile(publication.final); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
					rollbackErrors = append(rollbackErrors, fmt.Errorf("remove incomplete replacement %q: %w", publication.final, removeErr))
				}
			}
			if publication.backup != "" {
				if renameErr := b.renameFile(publication.backup, publication.final); renameErr != nil {
					rollbackErrors = append(rollbackErrors, fmt.Errorf("restore previous executable %q: %w", publication.final, renameErr))
				}
			}
		}
		return errors.Join(rollbackErrors...)
	}
	for i, program := range b.pending {
		publications[i].windowsProgram = program
		if _, statErr := os.Stat(program.final); errors.Is(statErr, os.ErrNotExist) {
			continue
		} else if statErr != nil {
			return errors.Join(fmt.Errorf("inspecting the previous Windows executable %q: %w", program.final, statErr), rollback())
		}
		backup, backupErr := windowsBackupPath(program.final)
		if backupErr != nil {
			return errors.Join(backupErr, rollback())
		}
		if renameErr := b.renameFile(program.final, backup); renameErr != nil {
			return errors.Join(fmt.Errorf("preserving the previous Windows executable %q: %w", program.final, renameErr), rollback())
		}
		publications[i].backup = backup
	}

	for i := range publications {
		publication := &publications[i]
		if renameErr := b.renameFile(publication.staged, publication.final); renameErr != nil {
			return errors.Join(
				fmt.Errorf("publishing the Windows executable %q: %w", publication.final, renameErr),
				rollback(),
			)
		}
		publication.published = true
	}
	for _, publication := range publications {
		if publication.backup == "" {
			continue
		}
		if removeErr := b.removeFile(publication.backup); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			return fmt.Errorf("removing the previous Windows executable backup %q: %w", publication.backup, removeErr)
		}
	}
	return nil
}

func (b *windowsBuilder) renameFile(oldPath, newPath string) error {
	if b.rename != nil {
		return b.rename(oldPath, newPath)
	}
	return os.Rename(oldPath, newPath)
}

func (b *windowsBuilder) removeFile(path string) error {
	if b.remove != nil {
		return b.remove(path)
	}
	return os.Remove(path)
}

func windowsBackupPath(final string) (string, error) {
	backup, err := os.CreateTemp(filepath.Dir(final), "."+filepath.Base(final)+"-*.previous")
	if err != nil {
		return "", fmt.Errorf("reserving a Windows executable backup beside %q: %w", final, err)
	}
	path := backup.Name()
	if err := backup.Close(); err != nil {
		return "", err
	}
	if err := os.Remove(path); err != nil {
		return "", err
	}
	return path, nil
}

func (b *windowsBuilder) embedManifest(v windowsManifest) error {
	body, err := windowsManifestXML(v)
	if err != nil {
		return err
	}

	var manifest bufferCoff
	if _, err := manifest.Write(body); err != nil {
		return err
	}
	b.Coff.AddResource(windowsResourceManifest, 1, &manifest)

	return nil
}

// windowsManifestXML writes the application manifest Windows reads out of the
// resource section.
//
// A description in and bytes out: what this file says decides which versions of
// the system will run the program and whether it is told the real size of the
// screen, and neither answer needs a Windows machine to be checked.
func windowsManifestXML(v windowsManifest) ([]byte, error) {
	t, err := template.New("manifest").Funcs(markup).Parse(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<assembly manifestVersion="1.0" xmlns="urn:schemas-microsoft-com:asm.v1" xmlns:asmv3="urn:schemas-microsoft-com:asm.v3">
    <assemblyIdentity type="win32" name="{{xml .Name}}" version="{{xml .Version}}" />
    <description>{{xml .Name}}</description>
    <compatibility xmlns="urn:schemas-microsoft-com:compatibility.v1">
        <application>
            {{if (le .WindowsVersion 10)}}<supportedOS Id="{8e0f7a12-bfb3-4fe8-b9a5-48fd50a15a9a}"/>
{{end}}
            {{if (le .WindowsVersion 9)}}<supportedOS Id="{1f676c76-80e1-4239-95bb-83d0f6d0da78}"/>
{{end}}
            {{if (le .WindowsVersion 8)}}<supportedOS Id="{4a2f28e3-53b9-4441-ba9c-d69d4a4a6e38}"/>
{{end}}
            {{if (le .WindowsVersion 7)}}<supportedOS Id="{35138b9a-5d96-4fbd-8e2d-a2440225f93a}"/>
{{end}}
            {{if (le .WindowsVersion 6)}}<supportedOS Id="{e2011457-1546-43c5-a5fe-008deee3d3f0}"/>
{{end}}
        </application>
    </compatibility>
    <trustInfo xmlns="urn:schemas-microsoft-com:asm.v3">
        <security>
            <requestedPrivileges>
                <requestedExecutionLevel level="asInvoker" uiAccess="false" />
            </requestedPrivileges>
        </security>
    </trustInfo>
	<asmv3:application>
		<asmv3:windowsSettings>
			<dpiAware xmlns="http://schemas.microsoft.com/SMI/2005/WindowsSettings">true</dpiAware>
		</asmv3:windowsSettings>
	</asmv3:application>
</assembly>`)
	if err != nil {
		return nil, err
	}

	var manifest bytes.Buffer
	if err := t.Execute(&manifest, v); err != nil {
		return nil, err
	}
	return manifest.Bytes(), nil
}

func (b *windowsBuilder) embedInfo(v windowsResources) error {
	page := uint16(1)

	// https://docs.microsoft.com/pt-br/windows/win32/menurc/vs-versioninfo
	t := newValue(valueBinary, "VS_VERSION_INFO", []io.WriterTo{
		// https://docs.microsoft.com/pt-br/windows/win32/api/VerRsrc/ns-verrsrc-vs_fixedfileinfo
		windowsInfoValueFixed{
			Signature:      0xFEEF04BD,
			StructVersion:  0x00010000,
			FileVersion:    v.Version,
			ProductVersion: v.Version,
			FileFlagMask:   0x3F,
			FileFlags:      0,
			FileOS:         0x40004,
			FileType:       0x1,
			FileSubType:    0,
		},
		// https://docs.microsoft.com/pt-br/windows/win32/menurc/stringfileinfo
		newValue(valueText, "StringFileInfo", []io.WriterTo{
			// https://docs.microsoft.com/pt-br/windows/win32/menurc/stringtable
			newValue(valueText, fmt.Sprintf("%04X%04X", v.Language, page), []io.WriterTo{
				// https://docs.microsoft.com/pt-br/windows/win32/menurc/string-str
				newValue(valueText, "ProductVersion", v.VersionHuman),
				newValue(valueText, "FileVersion", v.VersionHuman),
				newValue(valueText, "FileDescription", v.Name),
				newValue(valueText, "ProductName", v.Name),
				// TODO carry the rest of the block a Windows installer shows:
				// company name, copyright, and the legal strings beside them.
				// Nothing here has a way to be told any of them yet.
			}),
		}),
		// https://docs.microsoft.com/pt-br/windows/win32/menurc/varfileinfo
		newValue(valueBinary, "VarFileInfo", []io.WriterTo{
			// https://docs.microsoft.com/pt-br/windows/win32/menurc/var-str
			newValue(valueBinary, "Translation", uint32(page)<<16|uint32(v.Language)),
		}),
	})

	// For some reason the ValueLength of the VS_VERSIONINFO must be the byte-length of `windowsInfoValueFixed`:
	t.ValueLength = 52

	var verrsrc bufferCoff
	if _, err := t.WriteTo(&verrsrc); err != nil {
		return err
	}

	b.Coff.AddResource(windowsResourceVersion, 1, &verrsrc)

	return nil
}

type windowsInfoValueFixed struct {
	Signature      uint32
	StructVersion  uint32
	FileVersion    [2]uint32
	ProductVersion [2]uint32
	FileFlagMask   uint32
	FileFlags      uint32
	FileOS         uint32
	FileType       uint32
	FileSubType    uint32
	FileDate       [2]uint32
}

func (v windowsInfoValueFixed) WriteTo(w io.Writer) (_ int64, err error) {
	return 0, binary.Write(w, binary.LittleEndian, v)
}

type windowsInfoValue struct {
	Length      uint16
	ValueLength uint16
	Type        uint16
	Key         []byte
	Value       []byte
}

func (v windowsInfoValue) WriteTo(w io.Writer) (_ int64, err error) {
	// binary.Write doesn't support []byte inside struct.
	if err = binary.Write(w, binary.LittleEndian, [3]uint16{v.Length, v.ValueLength, v.Type}); err != nil {
		return 0, err
	}
	if _, err = w.Write(v.Key); err != nil {
		return 0, err
	}
	if _, err = w.Write(v.Value); err != nil {
		return 0, err
	}
	return 0, nil
}

const (
	valueBinary uint16 = 0
	valueText   uint16 = 1
)

func newValue(valueType uint16, key string, input any) windowsInfoValue {
	v := windowsInfoValue{
		Type:   valueType,
		Length: 6,
	}

	padding := func(in []byte) []byte {
		if l := uint16(len(in)) + v.Length; l%4 != 0 {
			return append(in, make([]byte, 4-l%4)...)
		}
		return in
	}

	v.Key = padding(utf16Encode(key))
	v.Length += uint16(len(v.Key))

	switch in := input.(type) {
	case string:
		v.Value = padding(utf16Encode(in))
		v.ValueLength = uint16(len(v.Value) / 2)
	case []io.WriterTo:
		var buff bytes.Buffer
		for k := range in {
			if _, err := in[k].WriteTo(&buff); err != nil {
				panic(err)
			}
		}
		v.Value = buff.Bytes()
	default:
		var buff bytes.Buffer
		if err := binary.Write(&buff, binary.LittleEndian, in); err != nil {
			panic(err)
		}
		v.ValueLength = uint16(buff.Len())
		v.Value = buff.Bytes()
	}

	v.Length += uint16(len(v.Value))

	return v
}

// utf16Encode encodes the string to UTF16 with null-termination.
func utf16Encode(s string) []byte {
	b, err := unicode.UTF16(unicode.LittleEndian, unicode.IgnoreBOM).NewEncoder().Bytes([]byte(s))
	if err != nil {
		panic(err)
	}
	return append(b, 0x00, 0x00) // null-termination.
}
