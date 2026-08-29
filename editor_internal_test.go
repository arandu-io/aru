package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallableEditorAssetsLiveOnlyInTheOfficialExtension(t *testing.T) {
	for _, name := range []string{"kyse.tmLanguage.json", "language-configuration.json"} {
		path := filepath.Join("editors", "vscode", name)
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("duplicate extension asset %s still exists: %v", path, err)
		}
	}
	readme, err := os.ReadFile(filepath.Join("editors", "vscode", "README.md"))
	if err != nil {
		t.Fatalf("read editor documentation: %v", err)
	}
	if !strings.Contains(string(readme), "https://github.com/arandu-io/vscode-arandu") {
		t.Error("editor documentation does not point to the official extension repository")
	}
}

func TestGeneratedEditorSettingsKeepKyseSourcesVisibleAndGeneratedViewsOutOfSearch(t *testing.T) {
	root := t.TempDir()
	if err := writeEditorSettings(root); err != nil {
		t.Fatalf("write editor settings: %v", err)
	}
	contents, err := os.ReadFile(filepath.Join(root, ".vscode", "settings.json"))
	if err != nil {
		t.Fatalf("read generated editor settings: %v", err)
	}

	var settings struct {
		Associations  map[string]string `json:"files.associations"`
		SearchExclude map[string]bool   `json:"search.exclude"`
		FilesExclude  map[string]bool   `json:"files.exclude"`
	}
	if err := json.Unmarshal(contents, &settings); err != nil {
		t.Fatalf("parse generated editor settings: %v", err)
	}
	if got := settings.Associations["*.kyse.go"]; got != "kyse" {
		t.Errorf("files.associations[*.kyse.go] = %q, want kyse", got)
	}
	if len(settings.SearchExclude) != 1 || !settings.SearchExclude["storage/framework/views/**"] {
		t.Errorf("search.exclude = %#v, want only generated views", settings.SearchExclude)
	}
	for pattern, excluded := range settings.SearchExclude {
		if excluded && strings.Contains(filepath.ToSlash(pattern), "resources/views") {
			t.Errorf("search.exclude hides editable view sources with %q", pattern)
		}
	}
	for pattern, excluded := range settings.FilesExclude {
		if excluded && strings.Contains(filepath.ToSlash(pattern), "resources/views") {
			t.Errorf("files.exclude hides editable view sources with %q", pattern)
		}
	}
}
