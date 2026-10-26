# Engine v3.2.3 actual native amd64 capture

Engine source `c7490636480a8faedddf3bff1d0a678a7ab7987c`, published platform image
`sha256:87323e8d80d519531338ff7a36ce750d337ecf6560e48493bdeb10b864e7b772` (not the OCI index). Actual capture ran Action PR6 source
`f047795cd4c83073ea73a8801614f25ce7d7437c` in native CI run `40329796416`.
Approved immutable Action merge `dd835da5025fd6e54707521434a41767c3ec4ddb` was independently tree-checked equal to the reviewed/native-tested capture source; App approval pins that merge, not the branch.

The unchanged shipped `tests/helpers.sh write_module` and `tests/engine-smoke.sh` recipe created a scratch
committed Go repository and executed prepare/checkout/run/report without an endpoint. The report/envelope pair
is `report/real-v323-amd64-blocked-no-baseline`. GitHub event/run fields are synthetic input, not a live
or independently verified upload. Raw files were copied from the actual CI export, not edited/relabelled.

The real `captured-baseline.json` is stored `archfit.baseline.v3` bytes, not a baseline CHECK STATE.
Reanchor used the retained historical synthetic stored input with one stale accepted entry, obsolete producer
semantics and an encapsulation reference. Actual reanchor removes that stale entry and N/A encapsulation while
preserving measured cycle zero. These are controlled test inputs, not a production baseline reset.

Same-platform old/new Action comparison has zero profile/config/classification/model/labels/rubric differences.
AMD64 and ARM64 settings hashes differ historically; this platform's profile is not substituted for the other.

## Original unmodified export SHA-256
- state: `f2d2aaa419a304b6ca92d83ad1a16d75cca43caabbb97706ec7408162e6fbb01`
- envelope: `5fb5f3e6ac694e70fb7376a8596b76ceb96f9b09fdf4ccff035a809608c7507e`
- captured baseline: `69bf7bd1c7107cd63b61a6cad9f3c5d559be9e0c3cb056b30d3410632411e339`
- reanchored baseline: `44de87108c683538876b88dbfbd7aaf8e18ad4a37ae64016db9fb3fd788c318b`
- reanchor stdout: `9150a14a515ac840b0b6cd851a89dee49e65e4d80bcb8395c6e484f7d0fd6f38`

The App tests read report/baseline structure and exact bytes; they do not rerun the engine algorithm.
