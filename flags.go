package main

import (
	"fmt"
	"strings"
)

// How arguments are read where the flag package cannot read them, and how every
// command answers a request to explain itself.
//
// Almost everything in this binary parses with flag from the standard library.
// The font commands cannot: flag stops at the first argument that is not a
// flag, and a family name is one or several words that arrive before the flags,
// after them, or on both sides of them. `aru font:add Public Sans --as body` --
// unquoted, two words, a flag behind them -- has to reach the same place as the
// quoted form, so the loop reads every argument and joins the words it finds.
//
// What that loop does not get to change is how a value attaches to its flag.
// --as=body and --as body are both what flag accepts and both what people type,
// so both are read here. A parser that took one of them is a difference nobody
// can guess from outside, and the only way to find it is to get it wrong.

// flagName is the name half of one argument: everything before the first "=",
// so that --as=body and --as reach the same branch of a switch.
//
// An argument that is not a flag comes back shortened when it happens to carry
// an "=", and that costs nothing: it matches no case, and the branch that
// collects words appends the argument itself rather than this.
func flagName(arg string) string {
	name, _, _ := strings.Cut(arg, "=")
	return name
}

// flagValue reads the value of the flag at args[i], and answers with the index
// the loop continues from: the same one when the value came attached, the next
// one when it is the argument after.
//
// A flag at the end of the arguments with nothing after it reads as empty,
// which is the same state as not writing the flag at all -- and every caller
// already has to answer for that state, because a flag can be left out.
func flagValue(args []string, i int) (string, int) {
	if _, value, attached := strings.Cut(args[i], "="); attached {
		return value, i
	}
	if i+1 < len(args) {
		return args[i+1], i + 1
	}
	return "", i
}

// noValue refuses an attached value on a flag that is a switch.
//
// A switch read by name alone answers --variable=false with the filter turned
// on, which is the opposite of what was typed and says nothing. There is no
// honest reading of a value here, so it is refused with the form that works.
func noValue(arg string) error {
	name, value, attached := strings.Cut(arg, "=")
	if !attached {
		return nil
	}
	return fmt.Errorf("%s takes no value, and %q was attached to it: write %s on its own", name, value, name)
}

// unknownFlag refuses an argument that begins with a dash and names no flag.
//
// The argument repeated back was the whole of the message, and it reads as
// "there is no such flag" in the case a reader is most often in: a flag that
// does exist, written with its value attached. So the message says that either
// spelling is read, and names the command that lists the flags -- rather than
// listing them here, which would be a second copy of the usage line the table
// already carries.
func unknownFlag(command, arg string) error {
	return fmt.Errorf("unknown flag %q\n\n"+
		"A value attaches either way: --flag=value and --flag value are read the same.\n"+
		"Run `aru %s --help` for the flags this command takes", arg, command)
}

// wantsHelp answers whether the arguments ask the command to explain itself.
//
// The scan stops at a bare --, because everything after it belongs to the
// program the command starts rather than to the command: `aru serve -- --help`
// is the application's --help, and answering it here would hide a flag this
// binary knows nothing about.
func wantsHelp(args []string) bool {
	for _, arg := range args {
		switch arg {
		case "--":
			return false
		case "-h", "-help", "--help":
			return true
		}
	}
	return false
}
