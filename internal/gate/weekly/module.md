# Recorded weekly counts

The optional `v4/weekly/<repository>` value is display-only and independent of the pull index and Gate decisions. The upload path writes accepted results on a best-effort basis after Gate publication. Failure to write does not change the verdict or publication. The existing pull-index schema and decoder remain unchanged, including during rollback.

Read returns unknown for absent or unreadable evidence. Available counts cover distinct recorded pull requests whose latest recorded accepted result is within the last seven days, including closed pull requests. Pass and owner-needed are subsets of checked; owner-needed requires an owner-request next step. Duplicate and older attempts do not renew the observation time. Entries are bounded; old or excess entries are discarded. Counts are not exhaustive: every display must say “Counts include checks Archfit recorded; some checks may be missing”. Never interpret absent evidence as zero.
