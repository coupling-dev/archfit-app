/archfit request retry is the shared backoff every module calls

```text
pkg_tc -> pkg_util: coupling: pkg/util/retry is past the surface
  balance: 2/10 (min 7): intrusive, pkg_util is high
  surface: pkg/util (public), pkg/util/types (model)
  next: use the surface, or: archfit request -m "<why retry belongs to the surface>"
```
