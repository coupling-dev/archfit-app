```text
cmd/main.go:14: coupling: cmd -> pkg/tc/netem is past the surface
  why: new knowledge across a module boundary needs a balance of at least 7
  balance: 2/10 (min 7): intrusive, pkg_tc is high
  surface: pkg/tc (public)
  next: use the surface, or: archfit request -m "<why netem belongs to the surface>"
archfit: fail, 1 error
```
