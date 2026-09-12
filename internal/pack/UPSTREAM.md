# Upstream

The sources in this directory began as a copy of an existing packaging tool,
at the version recorded below, and are maintained here from now on.

    upstream   gioui.org/cmd
    version    v0.10.0
    terms      UNLICENSE or MIT, at the recipient's choice, reproduced in
               LICENSE beside these sources

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

### 6. A macOS bundle declares itself an application

**Changed:** `CFBundlePackageType` was `BNDL`, a generic bundle, and is `APPL`.
`CFBundleName` and the two version keys are written as well.

**Why it was worth finding:** with `BNDL`, macOS registers the artifact as a
package of files rather than a program. The binary inside still runs when it is
started directly, which is how the fault survives every check somebody thinks
to make -- it was found by opening the bundle and asking Launch Services what
it had registered.

**Measured after:** the built bundle is reported by the system as an
application, Launch Services registers it under its own name, and the window
server answers that the process owns one window of 960 by 752 points.

### 7. An error that disappeared on the way to an incomplete package

**Changed:** the walk that collects an application's jars and permissions
recursed into each import and dropped whatever that call answered. A jar it
could not find was therefore silently absent from the APK, which builds,
installs, and fails on the screen that needed it.

**And nine more errors are named rather than dropped**, all writes into
in-memory archive writers where the annotation changes nothing but says a
decision was made.

**Three findings are excluded by path rather than fixed**, in `.golangci.yml`,
each with its reason written there: a provisioning profile is matched to a
certificate by the SHA-1 of its DER encoding, which is Apple's format read and
not written, and the archive being read back is the one this program assembled
four functions earlier. The rules stay on everywhere else, because they are what
guards the checksum that verifies every downloaded font and binary.

## What was left behind

The end-to-end tests, which drive a browser and an emulator through two more
dependency trees, and the fixture applications they build. What replaces them
here is narrower and runs everywhere: the refusals are unit-tested, and the
artifacts are checked as artifacts — a manifest is read back out of the APK, and
the symbols out of the library inside it.
