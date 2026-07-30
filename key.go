package main

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
)

// appKeyLen must match config.AppKeyLen in the framework. It is duplicated
// rather than imported: the CLI is a separate module, and importing the framework
// here would make the CLI's version pin the project's version.
const appKeyLen = 32

// keyGenerate prints the APP_KEY line, ready to paste into .env.
//
// It prints rather than writes: rewriting a file that holds every secret of a
// project is not something a command should do without being asked, and a key
// that lands in the wrong .env is a silent session invalidation for everyone.
func keyGenerate(args []string, stdout, stderr io.Writer) error {
	if len(args) > 0 {
		return fmt.Errorf("key:generate takes no arguments")
	}

	key := make([]byte, appKeyLen)
	if _, err := rand.Read(key); err != nil {
		return fmt.Errorf("reading random bytes: %w", err)
	}

	// The base64 prefix is what tells config.Load to decode: 32 random bytes are
	// not printable, so the raw form cannot go into a .env file.
	fmt.Fprintf(stdout, "APP_KEY=base64:%s\n", base64.StdEncoding.EncodeToString(key))
	fmt.Fprintf(stderr, "\ncopy the line above into .env\nrotating this key invalidates every session and CSRF token\n")
	return nil
}
