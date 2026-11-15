package proposals

// editEpoch includes the stale edits this action voided after reading cur.
// A concurrent push invalidation increments it again; the atomic pin then fails
// instead of blessing a file computed from an approval that disappeared.
func editEpoch(cur *proposal) uint64 {
	if cur == nil {
		return 0
	}
	epoch := cur.rec.EditEpoch
	for _, a := range cur.stale {
		if a.edit != nil {
			epoch++
		}
	}
	return epoch
}
