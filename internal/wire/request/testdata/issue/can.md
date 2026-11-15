Request: graph pkg_tc -> pkg_util

**Reason**

```text
netem needs the shared retry helper
```

**Record** (as reported: no pull request confirms these numbers yet)

```text
pkg_tc -> pkg_util: graph: not an allowed dependency
  balance: 9/10 (min 7)
  next: archfit request -m "<why you need it>"
```
