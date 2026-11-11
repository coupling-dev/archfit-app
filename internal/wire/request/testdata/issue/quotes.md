Request: graph pkg_tc -> pkg_util

**Reason**

````text
the "retry" helper must stay in ```pkg/util```; @team said so, and --expires 2026-11-30 was too soon
````

**Record**

```text
pkg/tc/netem.go:12: graph: pkg_tc -> pkg_util is not an allowed dependency
  why: a new dependency between modules needs an owner
  balance: 9/10 (min 7)
  next: wait for an owner: #431
```

- Pull requests: #12
- Expires: 2026-12-10
