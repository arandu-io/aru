package lsp

import (
	"fmt"
	"net/url"
	"runtime"
	"strings"
)

type filePathStyle uint8

const (
	posixFilePath filePathStyle = iota
	windowsFilePath
)

func nativeFilePathStyle() filePathStyle {
	if runtime.GOOS == "windows" {
		return windowsFilePath
	}
	return posixFilePath
}

func pathFromFileURI(raw string, style filePathStyle) (string, error) {
	parsed, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("parse file URI: %w", err)
	}
	if !strings.EqualFold(parsed.Scheme, "file") || parsed.Opaque != "" {
		return "", fmt.Errorf("%q is not a hierarchical file URI", raw)
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" {
		return "", fmt.Errorf("file URI contains credentials, a query or a fragment")
	}
	host := parsed.Host
	if strings.EqualFold(host, "localhost") {
		host = ""
	}
	if parsed.Path == "" {
		return "", fmt.Errorf("file URI has no absolute path")
	}

	if style == posixFilePath {
		if host != "" {
			return "//" + host + parsed.Path, nil
		}
		if !strings.HasPrefix(parsed.Path, "/") {
			return "", fmt.Errorf("file URI path is not absolute")
		}
		return parsed.Path, nil
	}
	if style != windowsFilePath {
		return "", fmt.Errorf("unknown file path style")
	}
	if host != "" {
		if !validUNCHost(host) {
			return "", fmt.Errorf("file URI has an invalid UNC host")
		}
		sharePath := strings.TrimPrefix(parsed.Path, "/")
		if sharePath == "" || strings.Split(sharePath, "/")[0] == "" {
			return "", fmt.Errorf("UNC file URI has no share")
		}
		return `\\` + host + `\` + strings.ReplaceAll(sharePath, "/", `\`), nil
	}
	drivePath := strings.TrimPrefix(parsed.Path, "/")
	if !absoluteWindowsDrivePath(drivePath) {
		return "", fmt.Errorf("file URI has no absolute Windows drive path")
	}
	return strings.ReplaceAll(drivePath, "/", `\`), nil
}

func fileURIFromPath(name string, style filePathStyle) (string, error) {
	if style == posixFilePath {
		if !strings.HasPrefix(name, "/") {
			return "", fmt.Errorf("POSIX path is not absolute")
		}
		return (&url.URL{Scheme: "file", Path: name}).String(), nil
	}
	if style != windowsFilePath {
		return "", fmt.Errorf("unknown file path style")
	}

	windowsPath := strings.ReplaceAll(name, "/", `\`)
	if strings.HasPrefix(windowsPath, `\\?\`) || strings.HasPrefix(windowsPath, `\\.\`) {
		return "", fmt.Errorf("Windows device paths are not file URIs")
	}
	if strings.HasPrefix(windowsPath, `\\`) {
		remainder := strings.TrimPrefix(windowsPath, `\\`)
		separator := strings.Index(remainder, `\`)
		if separator <= 0 || separator == len(remainder)-1 {
			return "", fmt.Errorf("UNC path has no share")
		}
		host := remainder[:separator]
		sharePath := remainder[separator+1:]
		if !validUNCHost(host) || strings.Split(sharePath, `\`)[0] == "" {
			return "", fmt.Errorf("UNC path has an invalid host or share")
		}
		return (&url.URL{
			Scheme: "file",
			Host:   host,
			Path:   "/" + strings.ReplaceAll(sharePath, `\`, "/"),
		}).String(), nil
	}

	slashPath := strings.ReplaceAll(windowsPath, `\`, "/")
	if !absoluteWindowsDrivePath(slashPath) {
		return "", fmt.Errorf("Windows path is not drive-absolute")
	}
	return (&url.URL{Scheme: "file", Path: "/" + slashPath}).String(), nil
}

func absoluteWindowsDrivePath(value string) bool {
	return len(value) >= 3 && asciiLetter(value[0]) && value[1] == ':' && value[2] == '/'
}

func asciiLetter(value byte) bool {
	return value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z'
}

func validUNCHost(host string) bool {
	return host != "" && host != "." && host != "?" && !strings.ContainsAny(host, `\/: `)
}
