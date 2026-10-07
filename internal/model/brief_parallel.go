package model

// BriefParallelDefault is how many issues a Brief's Crews work on at once when
// nothing says otherwise (plans/brief-parallel.md, decision 2). The agent-slot
// queue still caps how many agents run.
const BriefParallelDefault int32 = 3

// BriefParallel is how many issues a Brief's Crews work on at once: spec, the
// Brief's spec.parallel, wins; else status, the number a prompt gave; else
// BriefParallelDefault. Zero is unset.
func BriefParallel(spec, status int32) int32 {
	switch {
	case spec > 0:
		return spec
	case status > 0:
		return status
	}
	return BriefParallelDefault
}
