---
name: example-module
description: Wire the example module into an Arandu application. Use when the request mentions examples or the example module.
license: MIT
metadata:
  audience: app
---

# Wiring the example module

Register it in bootstrap/app.go, then run `aru migrate`: the module carries
its own migrations, and its tables do not exist until they run.
