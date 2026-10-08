package model

// TokenUsage is what agent sessions used, in tokens: of a call, a run, a Task,
// a Brief round or a Brief. LargestSession is the prompt of the biggest turn
// of any one session in it: how full a session got.
type TokenUsage struct {
	Input          int64 `json:"input,omitempty"`
	Output         int64 `json:"output,omitempty"`
	CacheRead      int64 `json:"cacheRead,omitempty"`
	CacheWrite     int64 `json:"cacheWrite,omitempty"`
	LargestSession int64 `json:"largestSession,omitempty"`
	// RunsWithout counts the runs whose agent didn't report their tokens; the
	// sums leave them out.
	RunsWithout int32 `json:"runsWithout,omitempty"`
	// SessionHandoffs counts the sessions that handed their work to a fresh one.
	SessionHandoffs int32 `json:"sessionHandoffs,omitempty"`
}

// Add adds o to t: the counts add up, the largest session is the larger one.
func (t *TokenUsage) Add(o TokenUsage) {
	t.Input += o.Input
	t.Output += o.Output
	t.CacheRead += o.CacheRead
	t.CacheWrite += o.CacheWrite
	t.LargestSession = max(t.LargestSession, o.LargestSession)
	t.RunsWithout += o.RunsWithout
	t.SessionHandoffs += o.SessionHandoffs
}

// Tokens is what a run adds to its Task's tokens: what its agent reported, or
// one run without tokens when the agent ran and didn't say. A sandbox that is
// gone adds nothing; whoever ended it counted it.
func (s RunState) Tokens() TokenUsage {
	switch {
	case !s.Started:
		return TokenUsage{}
	case s.Result != nil && s.Result.Tokens != nil:
		return *s.Result.Tokens
	default:
		return TokenUsage{RunsWithout: 1}
	}
}
