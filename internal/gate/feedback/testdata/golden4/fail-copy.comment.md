## Archfit: fail, 1 error

`pkg/tc/interface.go:12` **duplication** this literal already lives in pkg/util/util.go:15
- why: two copies must change together and nothing checks that they do
- next: `use pkg_util, or run: archfit request -m "<why a copy is needed>" --expires 2026-12-10`

**Author:** reply `/archfit request <why you need it>`. Do not copy the code.
**Owner:** nothing to do until a request exists.
