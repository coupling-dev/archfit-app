## Archfit: waiting, request #431

`pkg/tc/netem.go:12` **graph** pkg_tc -> pkg_util is not an allowed dependency
- why: a new dependency between modules needs an owner
- balance: 9/10 (min 7)

**Author:** nothing to do. This check runs again when #431 is answered.
**Owner:** answer #431.
