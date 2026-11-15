Request: graph pkg_tc -> pkg_util

**Reason**

```text
netem needs the shared retry helper
```

**Record**

```text
pkg/tc/netem.go:12: graph: pkg_tc -> pkg_util is not an allowed dependency
  why: a new dependency between modules needs an owner
  balance: 9/10 (min 7)
  next: wait for an owner: #431
```

- Pull requests: #12, #15
