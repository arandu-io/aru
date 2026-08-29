package main

import (
	"fmt"
	"io"
	"os"

	"github.com/arandu-io/aru/internal/lsp"
)

// runLSP serves editor requests over standard input and standard output.
func runLSP(args []string, stdout, stderr io.Writer) error {
	if len(args) != 0 {
		return fmt.Errorf("usage: aru lsp")
	}
	return lsp.Serve(os.Stdin, stdout)
}
