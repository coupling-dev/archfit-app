status: completed
conclusion: neutral
title: ❌ fail, 1 error, advisory

```text
pkg/chaos/netem/netem.go:8: no-chaos-to-runtime: pkg_chaos must not depend on pkg_runtime
  why: A direct import ties a chaos action to one runtime.
  instead: Add a method to container.Client.
  next: fix the code
archfit: fail, 1 error
Advisory: this check does not block the merge. To make it required, open the setup page: https://app.archfit.test/r/acme/shop
```

annotation pkg/chaos/netem/netem.go:8 failure
pkg/chaos/netem/netem.go:8: no-chaos-to-runtime: pkg_chaos must not depend on pkg_runtime
  why: A direct import ties a chaos action to one runtime.
  instead: Add a method to container.Client.
  next: fix the code
