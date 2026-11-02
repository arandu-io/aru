# Release Notes

## [v0.84.0](https://github.com/arandu-io/aru/compare/v0.83.1...v0.84.0) - 2026-11-02

**Full Changelog**: https://github.com/arandu-io/aru/compare/v0.83.1...v0.84.0

## v0.84.0 - 2026-11-02

### Added

- `aru native:icons <image>` draws every icon an application needs from one picture,
  a PNG, a JPEG or an SVG, with its background or without. `internal/icons` is Go
  and nothing else: no Node, no ImageMagick, no C library. The mark is measured, its
  margins cut away, and it is centred and scaled until the circle that holds all of
  it is as wide as the platform asks, so one picture comes out the same size
  everywhere. Android gets the adaptive icon (the foreground inside the 66 of 108
  units every launcher mask leaves visible, a background colour and the monochrome
  layer of Android 13's themed icon), the flat and round icons of the releases before
  8, the status bar icon `ic_stat` (white on transparent, named in the manifest as
  Firebase's default notification icon) and the Android 12 splash screen in the light
  and the dark appearance. iOS gets the default, dark and tinted icons of iOS 18 in
  an `AppIcon.appiconset`. The web gets the favicons, the touch icon and the manifest
  icons with the maskable ones; the desktop gets `icon.ico` and `icon.icns`, the
  latter without `iconutil`.
- `native:build` draws those icons itself from `-icon`. `-icon-background` and
  `-icon-dark-background` say what the mark sits on (white and black when the picture
  has no ground of its own), and `-icon-dark` names a picture for the dark appearance;
  without one the mark is turned over when it is grey and would be lost on the dark
  ground. `native:icons` takes `-background`, `-dark-background`, `-dark` and `-output`.
- The SVG reader draws paths, basic shapes, groups, transforms and solid fills and
  strokes. It refuses gradients, filters, masks, text and references by name rather
  than draw something else.

### Fixed

- The Android adaptive icon used the whole picture as both its background and its
  foreground, drawn over all 108 units, so the mark was neither inside the safe zone
  nor centred under a circular or squircle mask, and the themed icon, the status bar
  icon and the splash screen had nothing of their own.

## [v0.83.1](https://github.com/arandu-io/aru/compare/v0.83.0...v0.83.1) - 2026-11-02

**Full Changelog**: https://github.com/arandu-io/aru/compare/v0.83.0...v0.83.1

## [v0.83.0](https://github.com/arandu-io/aru/compare/v0.82.1...v0.83.0) - 2026-11-02

### Added

- `native:build` packages an application whose Java needs libraries.
  `-android-lib` names `.aar` and `.jar` files, or directories of them; their
  classes go on the compiler's path and into the dex (in as many dex files as
  the methods need), their manifests are merged into the application's, their
  resources are compiled and the R classes their code reads are generated.
  Nothing is downloaded or carried by `aru`: the libraries and the terms they
  are used under are the project's.
- A package may carry Java and a manifest fragment in an `android` directory
  beside its Go; they are compiled and merged only when the package is in the
  application's graph. `-placeholder name=value` gives a value to the `${name}`
  such a manifest asks for, and `applicationId`, `pushChannelId` and
  `pushChannelName` have values of their own.
- `-google-services` takes the project's `google-services.json` (or the one
  beside `cmd/native`) and writes the string resources the Firebase libraries
  read their configuration from, for the client whose package is the
  application id.
- The `notifications` permission package: `POST_NOTIFICATIONS` on Android; on
  iOS the remote-notification background mode, and a provisioning profile with
  no `aps-environment` entitlement is refused before the application is signed.
- A library that asks for a newer Android than the packager's floor raises the
  floor.

### Fixed

- `native:build`: the manifest merge adds together the meta-data of a component
  that several libraries declare. Firebase finds its parts through one
  `ComponentDiscoveryService` that every library declares with its own registrar;
  the merge kept the first declaration, so the service the system started listed
  one registrar and `FirebaseMessaging.getInstance()` threw "Firebase Messaging
  component is not present".

**Full Changelog**: https://github.com/arandu-io/aru/compare/v0.82.1...v0.83.0

## [v0.82.1](https://github.com/arandu-io/aru/compare/v0.82.0...v0.82.1) - 2026-11-02

**Full Changelog**: https://github.com/arandu-io/aru/compare/v0.82.0...v0.82.1

## [v0.82.0](https://github.com/arandu-io/aru/compare/v0.81.3...v0.82.0) - 2026-10-31

**Full Changelog**: https://github.com/arandu-io/aru/compare/v0.81.3...v0.82.0

## v0.82.0 - 2026-10-31

### Fixed

- `native:build` for Android: the manifest declares `uiMode` among the
  configuration changes the activity handles itself, so the system changing
  between the light and the dark mode no longer destroys and recreates the
  activity -- the application keeps running and is told, which is what lets a
  window follow the system's scheme at run time.
- `native:build` for Android: the theme no longer paints the window white and
  the bars a fixed translucent black. The window background is the light or the
  dark palette's ground (`values` and `values-night`, with the release variants
  `values-v21`, `values-v23` and `values-night-v21`), so an application opened in
  the dark mode does not flash white before its first frame, and the system bars
  are transparent, painted by the application with its own background. A light
  device gets dark status icons from the first frame. The manifest and the theme
  resources are checked by tests.

## [v0.81.3](https://github.com/arandu-io/aru/compare/v0.81.2...v0.81.3) - 2026-10-26

**Full Changelog**: https://github.com/arandu-io/aru/compare/v0.81.2...v0.81.3

## [v0.81.2](https://github.com/arandu-io/aru/compare/v0.81.1...v0.81.2) - 2026-10-26

**Full Changelog**: https://github.com/arandu-io/aru/compare/v0.81.1...v0.81.2

## [v0.81.1](https://github.com/arandu-io/aru/compare/v0.81.0...v0.81.1) - 2026-10-25

**Full Changelog**: https://github.com/arandu-io/aru/compare/v0.81.0...v0.81.1

## [v0.81.0](https://github.com/arandu-io/aru/compare/v0.80.0...v0.81.0) - 2026-10-25

**Full Changelog**: https://github.com/arandu-io/aru/compare/v0.80.0...v0.81.0

## [v0.80.0](https://github.com/arandu-io/aru/compare/v0.79.0...v0.80.0) - 2026-10-25

**Full Changelog**: https://github.com/arandu-io/aru/compare/v0.79.0...v0.80.0

## [v0.79.0](https://github.com/arandu-io/aru/compare/v0.78.0...v0.79.0) - 2026-10-24

**Full Changelog**: https://github.com/arandu-io/aru/compare/v0.78.0...v0.79.0

## [v0.78.0](https://github.com/arandu-io/aru/compare/v0.77.0...v0.78.0) - 2026-10-23

**Full Changelog**: https://github.com/arandu-io/aru/compare/v0.77.0...v0.78.0

## [v0.77.0](https://github.com/arandu-io/aru/compare/v0.76.1...v0.77.0) - 2026-10-23

**Full Changelog**: https://github.com/arandu-io/aru/compare/v0.76.1...v0.77.0

## [v0.76.1](https://github.com/arandu-io/aru/compare/v0.76.0...v0.76.1) - 2026-10-22

**Full Changelog**: https://github.com/arandu-io/aru/compare/v0.76.0...v0.76.1

## [v0.76.0](https://github.com/arandu-io/aru/compare/v0.75.0...v0.76.0) - 2026-10-22

**Full Changelog**: https://github.com/arandu-io/aru/compare/v0.75.0...v0.76.0

## [v0.75.0](https://github.com/arandu-io/aru/compare/v0.74.0...v0.75.0) - 2026-10-21

**Full Changelog**: https://github.com/arandu-io/aru/compare/v0.74.0...v0.75.0

## [v0.74.0](https://github.com/arandu-io/aru/compare/v0.73.2...v0.74.0) - 2026-10-20

**Full Changelog**: https://github.com/arandu-io/aru/compare/v0.73.2...v0.74.0

## [v0.73.2](https://github.com/arandu-io/aru/compare/v0.73.1...v0.73.2) - 2026-10-15

**Full Changelog**: https://github.com/arandu-io/aru/compare/v0.73.1...v0.73.2

## [v0.73.1](https://github.com/arandu-io/aru/compare/v0.73.0...v0.73.1) - 2026-10-15

**Full Changelog**: https://github.com/arandu-io/aru/compare/v0.73.0...v0.73.1

## [v0.73.0](https://github.com/arandu-io/aru/compare/v0.72.2...v0.73.0) - 2026-10-15

**Full Changelog**: https://github.com/arandu-io/aru/compare/v0.72.2...v0.73.0

## [v0.72.2](https://github.com/arandu-io/aru/compare/v0.72.1...v0.72.2) - 2026-10-11

**Full Changelog**: https://github.com/arandu-io/aru/compare/v0.72.1...v0.72.2

## [v0.72.1](https://github.com/arandu-io/aru/compare/v0.72.0...v0.72.1) - 2026-10-10

**Full Changelog**: https://github.com/arandu-io/aru/compare/v0.72.0...v0.72.1

## [v0.72.0](https://github.com/arandu-io/aru/compare/v0.71.0...v0.72.0) - 2026-10-10

**Full Changelog**: https://github.com/arandu-io/aru/compare/v0.71.0...v0.72.0

## [v0.71.0](https://github.com/arandu-io/aru/compare/v0.70.0...v0.71.0) - 2026-10-10

**Full Changelog**: https://github.com/arandu-io/aru/compare/v0.70.0...v0.71.0

## [v0.70.0](https://github.com/arandu-io/aru/compare/v0.69.3...v0.70.0) - 2026-10-10

**Full Changelog**: https://github.com/arandu-io/aru/compare/v0.69.3...v0.70.0

## [v0.69.3](https://github.com/arandu-io/aru/compare/v0.69.2...v0.69.3) - 2026-10-09

**Full Changelog**: https://github.com/arandu-io/aru/compare/v0.69.2...v0.69.3

## [v0.69.2](https://github.com/arandu-io/aru/compare/v0.69.1...v0.69.2) - 2026-10-09

**Full Changelog**: https://github.com/arandu-io/aru/compare/v0.69.1...v0.69.2

## [v0.69.1](https://github.com/arandu-io/aru/compare/v0.69.0...v0.69.1) - 2026-10-09

**Full Changelog**: https://github.com/arandu-io/aru/compare/v0.69.0...v0.69.1

## [v0.69.0](https://github.com/arandu-io/aru/compare/v0.68.0...v0.69.0) - 2026-10-09

**Full Changelog**: https://github.com/arandu-io/aru/compare/v0.68.0...v0.69.0

## [v0.68.0](https://github.com/arandu-io/aru/compare/v0.67.0...v0.68.0) - 2026-10-09

**Full Changelog**: https://github.com/arandu-io/aru/compare/v0.67.0...v0.68.0

## [v0.67.0](https://github.com/arandu-io/aru/compare/v0.66.0...v0.67.0) - 2026-10-09

**Full Changelog**: https://github.com/arandu-io/aru/compare/v0.66.0...v0.67.0

## [v0.66.0](https://github.com/arandu-io/aru/compare/v0.65.0...v0.66.0) - 2026-10-09

**Full Changelog**: https://github.com/arandu-io/aru/compare/v0.65.0...v0.66.0

## [v0.65.0](https://github.com/arandu-io/aru/compare/v0.64.0...v0.65.0) - 2026-10-09

**Full Changelog**: https://github.com/arandu-io/aru/compare/v0.64.0...v0.65.0

## [v0.64.0](https://github.com/arandu-io/aru/compare/v0.63.0...v0.64.0) - 2026-10-09

**Full Changelog**: https://github.com/arandu-io/aru/compare/v0.63.0...v0.64.0

## [v0.63.0](https://github.com/arandu-io/aru/compare/v0.62.0...v0.63.0) - 2026-10-09

**Full Changelog**: https://github.com/arandu-io/aru/compare/v0.62.0...v0.63.0

## [v0.62.0](https://github.com/arandu-io/aru/compare/v0.61.0...v0.62.0) - 2026-10-09

**Full Changelog**: https://github.com/arandu-io/aru/compare/v0.61.0...v0.62.0

## [v0.61.0](https://github.com/arandu-io/aru/compare/v0.60.5...v0.61.0) - 2026-10-09

**Full Changelog**: https://github.com/arandu-io/aru/compare/v0.60.5...v0.61.0

## [v0.60.5](https://github.com/arandu-io/aru/compare/v0.60.4...v0.60.5) - 2026-10-09

**Full Changelog**: https://github.com/arandu-io/aru/compare/v0.60.4...v0.60.5

## [v0.60.4](https://github.com/arandu-io/aru/compare/v0.60.3...v0.60.4) - 2026-10-09

**Full Changelog**: https://github.com/arandu-io/aru/compare/v0.60.3...v0.60.4

## [v0.60.3](https://github.com/arandu-io/aru/compare/v0.60.2...v0.60.3) - 2026-10-04

**Full Changelog**: https://github.com/arandu-io/aru/compare/v0.60.2...v0.60.3

## [v0.60.2](https://github.com/arandu-io/aru/compare/v0.60.1...v0.60.2) - 2026-10-03

**Full Changelog**: https://github.com/arandu-io/aru/compare/v0.60.1...v0.60.2

## [v0.60.1](https://github.com/arandu-io/aru/compare/v0.60.0...v0.60.1) - 2026-10-03

**Full Changelog**: https://github.com/arandu-io/aru/compare/v0.60.0...v0.60.1

## [v0.60.0](https://github.com/arandu-io/aru/compare/v0.59.0...v0.60.0) - 2026-10-03

**Full Changelog**: https://github.com/arandu-io/aru/compare/v0.59.0...v0.60.0

## [v0.59.0](https://github.com/arandu-io/aru/compare/v0.58.4...v0.59.0) - 2026-10-03

**Full Changelog**: https://github.com/arandu-io/aru/compare/v0.58.4...v0.59.0

## [v0.58.4](https://github.com/arandu-io/aru/compare/v0.58.3...v0.58.4) - 2026-10-03

**Full Changelog**: https://github.com/arandu-io/aru/compare/v0.58.3...v0.58.4

## [v0.58.3](https://github.com/arandu-io/aru/compare/v0.58.2...v0.58.3) - 2026-10-03

**Full Changelog**: https://github.com/arandu-io/aru/compare/v0.58.2...v0.58.3

## [v0.58.2](https://github.com/arandu-io/aru/compare/v0.58.1...v0.58.2) - 2026-10-03

**Full Changelog**: https://github.com/arandu-io/aru/compare/v0.58.1...v0.58.2

## [v0.58.1](https://github.com/arandu-io/aru/compare/v0.58.0...v0.58.1) - 2026-10-02

**Full Changelog**: https://github.com/arandu-io/aru/compare/v0.58.0...v0.58.1

## [v0.58.0](https://github.com/arandu-io/aru/compare/v0.57.0...v0.58.0) - 2026-10-01

**Full Changelog**: https://github.com/arandu-io/aru/compare/v0.57.0...v0.58.0

## [v0.57.0](https://github.com/arandu-io/aru/compare/v0.56.2...v0.57.0) - 2026-10-01

## What's Changed
* build(deps): bump the gomod group across 1 directory with 2 updates by @dependabot[bot] in https://github.com/arandu-io/aru/pull/4

## New Contributors
* @dependabot[bot] made their first contribution in https://github.com/arandu-io/aru/pull/4

**Full Changelog**: https://github.com/arandu-io/aru/compare/v0.56.2...v0.57.0

## [v0.56.2](https://github.com/arandu-io/aru/compare/v0.56.1...v0.56.2) - 2026-09-18

**Full Changelog**: https://github.com/arandu-io/aru/compare/v0.56.1...v0.56.2

## [v0.56.1](https://github.com/arandu-io/aru/compare/v0.56.0...v0.56.1) - 2026-09-17

**Full Changelog**: https://github.com/arandu-io/aru/compare/v0.56.0...v0.56.1

## [v0.56.0](https://github.com/arandu-io/aru/compare/v0.55.0...v0.56.0) - 2026-09-14

**Full Changelog**: https://github.com/arandu-io/aru/compare/v0.55.0...v0.56.0

## [v0.55.0](https://github.com/arandu-io/aru/compare/v0.54.0...v0.55.0) - 2026-09-09

**Full Changelog**: https://github.com/arandu-io/aru/compare/v0.54.0...v0.55.0

## [v0.54.0](https://github.com/arandu-io/aru/compare/v0.53.0...v0.54.0) - 2026-09-09

**Full Changelog**: https://github.com/arandu-io/aru/compare/v0.53.0...v0.54.0

## [v0.53.0](https://github.com/arandu-io/aru/compare/v0.52.0...v0.53.0) - 2026-09-09

**Full Changelog**: https://github.com/arandu-io/aru/compare/v0.52.0...v0.53.0

## [v0.52.0](https://github.com/arandu-io/aru/compare/v0.51.0...v0.52.0) - 2026-09-09

**Full Changelog**: https://github.com/arandu-io/aru/compare/v0.51.0...v0.52.0

## [v0.51.0](https://github.com/arandu-io/aru/compare/v0.50.0...v0.51.0) - 2026-09-08

**Full Changelog**: https://github.com/arandu-io/aru/compare/v0.50.0...v0.51.0

## [v0.50.0](https://github.com/arandu-io/aru/compare/v0.49.0...v0.50.0) - 2026-09-08

**Full Changelog**: https://github.com/arandu-io/aru/compare/v0.49.0...v0.50.0

## [v0.49.0](https://github.com/arandu-io/aru/compare/v0.48.0...v0.49.0) - 2026-09-08

**Full Changelog**: https://github.com/arandu-io/aru/compare/v0.48.0...v0.49.0

## [v0.48.0](https://github.com/arandu-io/aru/compare/v0.47.0...v0.48.0) - 2026-09-08

**Full Changelog**: https://github.com/arandu-io/aru/compare/v0.47.0...v0.48.0

## [v0.47.0](https://github.com/arandu-io/aru/compare/v0.46.0...v0.47.0) - 2026-09-08

**Full Changelog**: https://github.com/arandu-io/aru/compare/v0.46.0...v0.47.0

## [v0.46.0](https://github.com/arandu-io/aru/compare/v0.45.0...v0.46.0) - 2026-09-08

**Full Changelog**: https://github.com/arandu-io/aru/compare/v0.45.0...v0.46.0

## [v0.45.0](https://github.com/arandu-io/aru/compare/v0.44.0...v0.45.0) - 2026-09-08

**Full Changelog**: https://github.com/arandu-io/aru/compare/v0.44.0...v0.45.0

## [v0.44.0](https://github.com/arandu-io/aru/compare/v0.43.0...v0.44.0) - 2026-09-07

**Full Changelog**: https://github.com/arandu-io/aru/compare/v0.43.0...v0.44.0

## [v0.43.0](https://github.com/arandu-io/aru/compare/v0.42.0...v0.43.0) - 2026-09-07

**Full Changelog**: https://github.com/arandu-io/aru/compare/v0.42.0...v0.43.0

## [v0.42.0](https://github.com/arandu-io/aru/compare/v0.41.0...v0.42.0) - 2026-09-06

**Full Changelog**: https://github.com/arandu-io/aru/compare/v0.41.0...v0.42.0

## [v0.41.0](https://github.com/arandu-io/aru/compare/v0.40.0...v0.41.0) - 2026-09-05

**Full Changelog**: https://github.com/arandu-io/aru/compare/v0.40.0...v0.41.0

## [v0.40.0](https://github.com/arandu-io/aru/compare/v0.39.0...v0.40.0) - 2026-09-05

**Full Changelog**: https://github.com/arandu-io/aru/compare/v0.39.0...v0.40.0

## [v0.38.0](https://github.com/arandu-io/aru/compare/v0.37.1...v0.38.0) - 2026-09-04

**Full Changelog**: https://github.com/arandu-io/aru/compare/v0.37.1...v0.38.0

## [v0.38.0](https://github.com/arandu-io/aru/compare/v0.37.1...v0.38.0) - 2026-09-03

**Full Changelog**: https://github.com/arandu-io/aru/compare/v0.37.1...v0.38.0

## [v0.37.1](https://github.com/arandu-io/aru/compare/v0.37.0...v0.37.1) - 2026-09-02

**Full Changelog**: https://github.com/arandu-io/aru/compare/v0.37.0...v0.37.1

## [v0.37.0](https://github.com/arandu-io/aru/compare/v0.36.0...v0.37.0) - 2026-09-01

**Full Changelog**: https://github.com/arandu-io/aru/compare/v0.36.0...v0.37.0

## [v0.36.0](https://github.com/arandu-io/aru/compare/v0.35.2...v0.36.0) - 2026-08-29

**Full Changelog**: https://github.com/arandu-io/aru/compare/v0.35.2...v0.36.0

## [v0.35.2](https://github.com/arandu-io/aru/compare/v0.35.1...v0.35.2) - 2026-08-29

**Full Changelog**: https://github.com/arandu-io/aru/compare/v0.35.1...v0.35.2

## [v0.35.1](https://github.com/arandu-io/aru/compare/v0.35.0...v0.35.1) - 2026-08-29

**Full Changelog**: https://github.com/arandu-io/aru/compare/v0.35.0...v0.35.1

## [v0.35.0](https://github.com/arandu-io/aru/compare/v0.34.0...v0.35.0) - 2026-08-29

**Full Changelog**: https://github.com/arandu-io/aru/compare/v0.34.0...v0.35.0

## [v0.34.0](https://github.com/arandu-io/aru/compare/v0.32.0...v0.34.0) - 2026-08-26

**Full Changelog**: https://github.com/arandu-io/aru/compare/v0.32.0...v0.34.0

## [v0.33.0](https://github.com/arandu-io/aru/compare/v0.32.0...v0.33.0) - 2026-08-23

**Full Changelog**: https://github.com/arandu-io/aru/compare/v0.32.0...v0.33.0

## [v0.32.0](https://github.com/arandu-io/aru/compare/v0.31.0...v0.32.0) - 2026-08-21

**Full Changelog**: https://github.com/arandu-io/aru/compare/v0.31.0...v0.32.0

## [v0.31.0](https://github.com/arandu-io/aru/compare/v0.30.0...v0.31.0) - 2026-08-21

**Full Changelog**: https://github.com/arandu-io/aru/compare/v0.30.0...v0.31.0

Written by the release, not by hand. `.github/workflows/release.yml` publishes
the release a tag names and prepends what it said, so the tag, the release and
this file cannot disagree.

Tags cut before that workflow existed have no release and are not listed here.
`git tag` is where they are.
