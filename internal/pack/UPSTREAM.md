# Upstream

The sources in this directory began as a copy of an existing packaging tool,
at the version recorded below, and are maintained here from now on.

    upstream   gioui.org/cmd
    version    v0.10.0
    licence    Unlicense OR MIT, reproduced in LICENSE

## Why this exists here rather than as a tool somebody installs

Producing an installable package is a step of building one. A project that ran
one command to compile and a second, separately installed, to package would
have two answers to "how do I ship this" — with the flags of each spelled
differently, one of them missing from a pipeline, and the versions of the two
drifting apart.

So it is a library, and `aru native:build` is the one command. Nothing here is
a `main`.

## What was changed

### 1. The entry point takes options rather than flags

**Changed:** the tool parsed its own command line and exited the process.
[Options] carries the same settings, [Build] returns an error, and the flags
belong to the command a project already types.

**Kept:** the settings still reach the packaging code through package-level
variables, written by `Options.apply` and put back afterwards. That is what
makes a diff against the original readable, and reading it is how a fix gets
in. The cost is that one process packages one thing at a time; a mutex turns
that from a race into a wait, and a test proves the values are restored — a
value left behind is an application signed with the previous caller's key.

### 2. The application's arguments no longer come from this process

**Changed:** `build_info.go` read the leftover command-line arguments to pass to
the packaged application. As a library there are none, and the slice expression
panicked on the first call. They arrive through `Options.Args`.

### 3. Every name in a shipped artifact is this project's

**Changed:** the native library inside an APK, the Java package, the three
classes and the sixty-eight JNI symbols that bind them. They were the original
project's, and unlike a comment they reach the packaged application: the
library is visible in the APK, and the activity name is in the manifest a store
lists.

**Verified by measurement, not by search-and-replace:** the built APK carries
`lib/arm64-v8a/libayra.so` exporting sixty-eight `Java_io_arandu_ayra_*`
symbols, and a `classes.dex` declaring exactly the three classes those symbols
bind to, with no occurrence of the old name in either. A rename that missed one
symbol fails at load time on a device and nowhere earlier, which is why the
check is on the artifact.

The engine half of this rename lives in the runtime repository, in the Java
sources and the JNI declarations beside them.

### 4. iOS applies its minimum version once, at the entry

**Changed:** four places read the minimum OS version and two of them applied
the default. The two that did not handed the compiler
`-miphoneos-version-min=0.0`, which it refuses as an invalid version — after
building everything else. It is applied once now, in `buildIOS`.

### 5. A destination is always passed

**Changed:** the caller always names the output file. The packager's own
default writes beside the source package it was given, which put a bundle's
`Contents` directory inside the directory the application's screens are written
in — where the next build compiled them as Go files.

## What was left behind

The end-to-end tests, which drive a browser and an emulator through two more
dependency trees, and the fixture applications they build. What replaces them
here is narrower and runs everywhere: the refusals are unit-tested, and the
artifacts are checked as artifacts — a manifest is read back out of the APK, and
the symbols out of the library inside it.
