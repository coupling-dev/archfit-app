```text
pkg/tc/netem.go:12: graph: pkg_tc -> pkg_util is not an allowed dependency
  why: a new dependency between modules needs an owner
  balance: 9/10 (min 7)
  next: wait for an owner: #431
pkg/tc/second.go:12: graph: pkg_tc -> pkg_util is not an allowed dependency
  why: a new dependency between modules needs an owner
  balance: 9/10 (min 7)
  next: wait for an owner: #432
pkg/tc/netem.go:12: graph: pkg_tc -> pkg_util is not an allowed dependency
  why: a new dependency between modules needs an owner
  balance: 9/10 (min 7)
  next: wait for an owner: #431
archfit: waiting, requests #431 and #432
```
