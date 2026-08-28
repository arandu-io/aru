package main

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
)

// appKeyLen is the length, in bytes, of the key key:generate emits, and it is
// the length the framework requires of APP_KEY at startup.
//
// The two numbers are written down separately. It is duplicated rather than
// imported because the CLI is a separate module, and importing the framework
// here would make the CLI's version pin the project's version -- and the
// framework's own constant is unexported, so there is no name to import even if
// that were wanted.
//
// So nothing catches a divergence: not the compiler, in either module, and no
// test on either side. It surfaces in the project rather than here, as every
// generated key being refused at startup for having the wrong length. Changing
// the requirement means changing this number too, on purpose.
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
