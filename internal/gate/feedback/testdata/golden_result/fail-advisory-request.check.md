status: completed
conclusion: neutral
title: ❌ fail, 1 error, advisory

```text
pkg/tc/netem.go:12: graph: pkg_tc -> pkg_util is not an allowed dependency
  why: a new dependency between modules needs an owner
  balance: 9/10 (min 7): contract, pkg_util is low
  next: wait for an owner: #431
archfit: fail, 1 error
Advisory: this check does not block the merge. To make it required, open the setup page: https://app.archfit.test/r/acme/shop
```

annotation pkg/tc/netem.go:12 failure
pkg/tc/netem.go:12: graph: pkg_tc -> pkg_util is not an allowed dependency
  why: a new dependency between modules needs an owner
  balance: 9/10 (min 7): contract, pkg_util is low
  next: wait for an owner: #431
