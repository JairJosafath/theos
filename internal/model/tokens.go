package model

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
)

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
// Its handoffs count whether or not its tokens were reported.
func (s RunState) Tokens() TokenUsage {
	var t TokenUsage
	switch {
	case !s.Started:
		return t
	case s.Result != nil && s.Result.Tokens != nil:
		t = *s.Result.Tokens
	default:
		t.RunsWithout = 1
	}
	if s.Result != nil {
		t.SessionHandoffs = int32(len(s.Result.SessionHandoffs))
	}
	return t
}

// HandoffNote says when a run's sessions handed off, for the Task's
// SessionHandedOff Event: "Round 2's implement run handed off at 134k tokens."
func HandoffNote(round int, role string, sizes []int64) string {
	at := make([]string, len(sizes))
	for i, size := range sizes {
		at[i] = TokenCount(size)
	}
	times := ""
	if len(sizes) > 1 {
		times = fmt.Sprintf(" %d times,", len(sizes))
	}
	return fmt.Sprintf("Round %d's %s run handed off%s at %s tokens.", round, orRun(role), times, joinAnd(at))
}

// TokenCount writes a number of tokens short: 950, 134k, 1.4M.
func TokenCount(n int64) string {
	switch {
	case n >= 1_000_000:
		return strconv.FormatFloat(float64(n)/1_000_000, 'f', 1, 64) + "M"
	case n >= 1000:
		return strconv.FormatInt((n+500)/1000, 10) + "k"
	default:
		return strconv.FormatInt(n, 10)
	}
}

func orRun(role string) string {
	if role == "" {
		return "agent"
	}
	return role
}

// MaxRunRecords is how many agent runs a Task keeps a record of, the newest,
// and MaxRecordedHandoffs how many handoff sizes each record keeps, the first.
const (
	MaxRunRecords       = 64
	MaxRecordedHandoffs = 8
)

// RunRecord is what a Task keeps of one agent run when it ends: its round and
// role, what it cost and used, and the sizes at which its sessions handed off.
type RunRecord struct {
	Round   int
	Role    string
	Sandbox string
	EndedAt time.Time
	// CostMilliUSD is what the run cost when CostReported.
	CostMilliUSD int64
	CostReported bool
	// Tokens is what the run adds to its Task's tokens (RunState.Tokens).
	Tokens          TokenUsage
	SessionHandoffs []int64
}

// Record is the record of a run of sandbox in round that ends at, and false
// for a sandbox that is gone: whoever ended it recorded it. The role is the one
// c4agent reported, else the one in the sandbox's name.
func (s RunState) Record(round int, sandbox string, at time.Time) (RunRecord, bool) {
	if !s.Started {
		return RunRecord{}, false
	}
	rec := RunRecord{Round: round, Role: SandboxRole(sandbox), Sandbox: sandbox, EndedAt: at, Tokens: s.Tokens()}
	rec.CostMilliUSD, _ = s.Cost()
	if s.Result != nil {
		rec.CostReported = s.Result.CostReported
		if s.Result.Role != "" {
			rec.Role = s.Result.Role
		}
		rec.SessionHandoffs = slices.Clone(s.Result.SessionHandoffs[:min(len(s.Result.SessionHandoffs), MaxRecordedHandoffs)])
	}
	return rec, true
}

// SandboxRole is the role in a sandbox's name (SandboxName), or "" when the
// name has none.
func SandboxRole(sandbox string) string {
	for _, role := range []string{RoleImplementer, RoleReviewer, RolePlanner} {
		i := strings.LastIndex(sandbox, "-"+role)
		if i < 0 {
			continue
		}
		rest := sandbox[i+1+len(role):]
		if j := strings.LastIndex(sandbox[:i], "-r"); j >= 0 && isDigits(sandbox[j+2:i]) &&
			(rest == "" || strings.HasPrefix(rest, "-a") || strings.HasPrefix(rest, "-x")) {
			return role
		}
	}
	return ""
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}
