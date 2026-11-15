status: in_progress
conclusion: 
title: waiting, request #431

```text
pkg/tc/netem.go:12: graph: pkg_tc -> pkg_util is not an allowed dependency
  why: a new dependency between modules needs an owner
  balance: 9/10 (min 7)
  next: wait for an owner: #431
archfit: waiting, request #431
```

annotation pkg/tc/netem.go:12 failure
pkg/tc/netem.go:12: graph: pkg_tc -> pkg_util is not an allowed dependency
  why: a new dependency between modules needs an owner
  balance: 9/10 (min 7)
  next: wait for an owner: #431
