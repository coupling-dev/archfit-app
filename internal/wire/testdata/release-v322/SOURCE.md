# Engine v3.2.2 synthetic native ARM capture

Producer: engine revision `90c0ca240143329d404eb4eb116b096f607f7dc5`, image
`sha256:e97a200a7c26907e4d30982b06e61ac7cc27bffd1d46002ad53bbf6b6594c479`,
Action commit `e0a1ef9473d68b426e9c56b2da5986bcee2fc769`.

The Action's `tests/helpers.sh write_module` created a scratch committed Go repository. Its
prepare/checkout/run/report stages emitted the unmodified `report/real-v322-blocked-no-baseline`
report/envelope pair in the shared corpus. The report was emitted anew from the released image;
its byte identity with the v3.2.0 capture reflects unchanged facts/schema, not relabeling.
GitHub event/run metadata is synthetic; these files do not establish an authenticated upload.

`captured-baseline.json` is the subsequent baseline capture. The reanchor started from a synthetic
stale reference with obsolete scip/ast-grep semantics, one stale accepted entry and an encapsulation
metric. `reanchored-baseline.json` and `reanchor-report.txt` are unmodified reanchor outputs: the
stale entry and N/A encapsulation reference are absent; measured cycle zero remains.
The App tests read these files structurally; they do not execute or validate the engine's reanchor algorithm.

`TestV322CaptureBytesAreUnmodified` records the qualification artifact SHA-256 values for all five files.
