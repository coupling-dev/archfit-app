# Engine v3.2.3 actual native arm64 capture

Engine source `c7490636480a8faedddf3bff1d0a678a7ab7987c`, published platform image
`sha256:54f6bf1ec99be6f4bdc21ca59e16d3dd65fb5b31a2c2af6c932ad556c89a220c` (not the OCI index). Actual capture ran Action PR6 source
`f047795cd4c83073ea73a8801614f25ce7d7437c` in native CI run `40329796416`.
Approved immutable Action merge `dd835da5025fd6e54707521434a41767c3ec4ddb` was independently tree-checked equal to the reviewed/native-tested capture source; App approval pins that merge, not the branch.

The unchanged shipped `tests/helpers.sh write_module` and `tests/engine-smoke.sh` recipe created a scratch
committed Go repository and executed prepare/checkout/run/report without an endpoint. The report/envelope pair
is `report/real-v323-arm64-blocked-no-baseline`. GitHub event/run fields are synthetic input, not a live
or independently verified upload. Raw files were copied from the actual CI export, not edited/relabelled.

The real `captured-baseline.json` is stored `archfit.baseline.v3` bytes, not a baseline CHECK STATE.
Reanchor used the retained historical synthetic stored input with one stale accepted entry, obsolete producer
semantics and an encapsulation reference. Actual reanchor removes that stale entry and N/A encapsulation while
preserving measured cycle zero. These are controlled test inputs, not a production baseline reset.

Same-platform old/new Action comparison has zero profile/config/classification/model/labels/rubric differences.
AMD64 and ARM64 settings hashes differ historically; this platform's profile is not substituted for the other.

## Original unmodified export SHA-256
- state: `c472d77d04cf03d912d1a3f8b41e7494c9a688da60d957a71d789e05f07c5127`
- envelope: `00dfe2d810115e0b325ea447fd26f4e9d5fd873e985511f764bfc219e7bcd184`
- captured baseline: `8dd2d79aefa9852cbf6bfbcd20b6b38e666d83986d66f464f9e142a2a260418e`
- reanchored baseline: `254496115e437d0f7d32f7273626a808596152556d40b0edfb24bb9ee848a88f`
- reanchor stdout: `9150a14a515ac840b0b6cd851a89dee49e65e4d80bcb8395c6e484f7d0fd6f38`

The App tests read report/baseline structure and exact bytes; they do not rerun the engine algorithm.
