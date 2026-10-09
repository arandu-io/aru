package main

import (
	"fmt"
	"io"
	"os"

	"github.com/arandu-io/aru/internal/lsp"
)

// runLSP serves editor requests over standard input and standard output.
//
// The server is handed this binary's command table, so an editor that asks
// for the catalogue gets the commands of the aru it is talking to rather than
// a copy it keeps.
func runLSP(args []string, stdout, stderr io.Writer) error {
	if len(args) != 0 {
		return fmt.Errorf("usage: aru lsp")
	}
	return lsp.ServeWith(os.Stdin, stdout, lsp.Options{Commands: lspCommands()})
}
