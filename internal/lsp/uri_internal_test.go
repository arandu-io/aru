package lsp

import (
	"strings"
	"testing"
)

func TestFileURIAndPathRoundTripAcrossPOSIXWindowsAndUNC(t *testing.T) {
	tests := []struct {
		name  string
		style filePathStyle
		path  string
		uri   string
	}{
		{
			name:  "POSIX",
			style: posixFilePath,
			path:  "/Users/Paulo Lima/arandu/resources/views/home.kyse.go",
			uri:   "file:///Users/Paulo%20Lima/arandu/resources/views/home.kyse.go",
		},
		{
			name:  "Windows drive",
			style: windowsFilePath,
			path:  `C:\Users\Paulo Lima\arandu\resources\views\home.kyse.go`,
			uri:   "file:///C:/Users/Paulo%20Lima/arandu/resources/views/home.kyse.go",
		},
		{
			name:  "Windows UNC",
			style: windowsFilePath,
			path:  `\\fileserver\projects\Arandu App\resources\views\home.kyse.go`,
			uri:   "file://fileserver/projects/Arandu%20App/resources/views/home.kyse.go",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			gotURI, err := fileURIFromPath(test.path, test.style)
			if err != nil {
				t.Fatalf("path to file URI: %v", err)
			}
			if gotURI != test.uri {
				t.Errorf("file URI = %q, want %q", gotURI, test.uri)
			}
			if strings.Contains(strings.ToLower(gotURI), "%5c") {
				t.Errorf("clickable file URI contains an encoded backslash: %q", gotURI)
			}

			gotPath, err := pathFromFileURI(test.uri, test.style)
			if err != nil {
				t.Fatalf("file URI to path: %v", err)
			}
			if gotPath != test.path {
				t.Errorf("path = %q, want %q", gotPath, test.path)
			}

			roundTrip, err := pathFromFileURI(gotURI, test.style)
			if err != nil {
				t.Fatalf("round-trip generated URI: %v", err)
			}
			if roundTrip != test.path {
				t.Errorf("round-trip path = %q, want %q", roundTrip, test.path)
			}
		})
	}
}
