package gen_test

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/arandu-io/aru/internal/gen"
)

// TestOnlyOneOfTwoWritesOfOnePathCreatesIt.
//
// Write asked whether the file was there and then wrote it, and between those
// two statements is where a second `aru make:migration` lands. Both runs read
// the same directory, both mint the same sequence, both find nothing at the
// path, and the second one replaces the first one's file -- reporting it as
// created, because from inside each run nothing was there.
//
// The number is what the test reads: a path may be created once. A run that did
// not create it has to say so, which is what turns into "already exists. Rerun
// with --force" instead of into silence.
func TestOnlyOneOfTwoWritesOfOnePathCreatesIt(t *testing.T) {
	const racers = 8

	root := t.TempDir()
	const path = "app/Models/Invoice.go"

	var (
		mu      sync.Mutex
		created []string
		start   sync.WaitGroup
		done    sync.WaitGroup
	)
	start.Add(1)
	for i := range racers {
		done.Add(1)
		go func() {
			defer done.Done()
			body := fmt.Appendf(nil, "package models\n\n// racer %d\n", i)
			start.Wait()
			written, _, err := gen.Write(root, []gen.File{{Path: path, Content: body}}, false)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				t.Errorf("racer %d: %v", i, err)
				return
			}
			created = append(created, written...)
		}()
	}
	start.Done()
	done.Wait()

	if len(created) != 1 {
		t.Errorf("%d of %d runs report having created %s, and it can only have been created once: "+
			"the ones that did not create it overwrote whatever was there", len(created), racers, path)
	}
}

// TestWriteDoesNotReachThroughASymlinkStandingWhereTheFileGoes.
//
// The same gap, deterministically. Asking whether the file is there is a read of
// the path, and a dangling symlink answers "no such file" to that question and
// then swallows the write into whatever it points at -- so the generator reports
// having created app/Models/Invoice.go and the bytes are somewhere else
// entirely. Creating the file exclusively asks the one question that cannot be
// answered about a path that is already occupied.
func TestWriteDoesNotReachThroughASymlinkStandingWhereTheFileGoes(t *testing.T) {
	root := t.TempDir()
	const path = "app/Models/Invoice.go"

	link := filepath.Join(root, filepath.FromSlash(path))
	elsewhere := filepath.Join(root, "elsewhere.go")
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(elsewhere, link); err != nil {
		t.Skipf("this filesystem does not take a symlink: %v", err)
	}

	written, skipped, err := gen.Write(root, []gen.File{{Path: path, Content: []byte("package models\n")}}, false)
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if len(written) != 0 {
		t.Errorf("Write reports having created %v, and the path was already taken", written)
	}
	if len(skipped) != 1 || skipped[0] != path {
		t.Errorf("Write skipped %v, want [%s]: a path it did not create is what --force is offered for", skipped, path)
	}
	if _, err := os.Stat(elsewhere); err == nil {
		t.Errorf("the bytes landed in %s, which is not the file the generator named", elsewhere)
	}
}
