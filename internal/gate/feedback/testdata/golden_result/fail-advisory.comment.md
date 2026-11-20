## Archfit: fail, 1 error

`pkg/chaos/netem/netem.go:8` **no-chaos-to-runtime** pkg_chaos must not depend on pkg_runtime
- why: A direct import ties a chaos action to one runtime.
- instead: Add a method to container.Client.

**Author:** fix the code and push.
