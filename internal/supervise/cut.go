package supervise

// MaxFindingRefs bounds the records one finding cites, so its record fits a
// findings log line whatever the run did: with every identifier at the
// contract's longest and each of its bytes escaped in two, ten events and
// the rest of the record take about 50 KiB of the 64 KiB line.
const MaxFindingRefs = 10

// cut is refs when there are at most MaxFindingRefs of them, in their order.
// Otherwise it keeps MaxFindingRefs, the most doubtful first, each verdict's
// in their order, and counts the rest. The verdict is decided over all of
// refs before the cut, so a doubt is never cut out of it.
func cut(refs []ref) ([]ref, uint64) {
	if len(refs) <= MaxFindingRefs {
		return refs, 0
	}
	kept := make([]ref, 0, MaxFindingRefs)
	for strength := range rank(confirmed) + 1 {
		for _, r := range refs {
			if len(kept) == MaxFindingRefs {
				break
			}
			if rank(r.v) == strength {
				kept = append(kept, r)
			}
		}
	}
	return kept, uint64(len(refs)) - uint64(len(kept))
}
