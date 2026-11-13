/*
Copyright 2026 The theos Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package agentrunner

import (
	"context"
	"strings"

	"github.com/JairJosafath/theos/internal/model"
)

// Agent does the work in a checked-out repository and answers in text.
type Agent interface {
	Run(ctx context.Context, dir, prompt string) (string, error)
}

// Usage is what one agent call cost and the tokens it used, as the agent
// reported them.
type Usage struct {
	MilliUSD int64
	// Reported is false when the agent's output didn't say what the call cost.
	Reported bool
	// Tokens is nil when the agent's output didn't say what tokens the call used.
	Tokens *model.TokenUsage
	// Handoffs are the sizes at which the call's sessions handed off to fresh ones.
	Handoffs []int64
}

// UsageReporter is an Agent that says what each call cost and used. The calls
// of an Agent that isn't one leave the run's cost and tokens unreported.
type UsageReporter interface {
	RunReportingUsage(ctx context.Context, dir, prompt string) (string, Usage, error)
}

// usageMeter adds up what an Agent's calls in one run cost and used. One call
// that doesn't report its cost leaves the whole run's cost unreported, and
// one that doesn't report its tokens the run's tokens.
type usageMeter struct {
	agent            Agent
	milliUSD         int64
	unreported       bool
	tokens           model.TokenUsage
	tokensUnreported bool
	// handoffs are the sizes at which the calls' sessions handed off.
	handoffs []int64
}

func (m *usageMeter) Run(ctx context.Context, dir, prompt string) (string, error) {
	reporter, ok := m.agent.(UsageReporter)
	if !ok {
		m.unreported, m.tokensUnreported = true, true
		return m.agent.Run(ctx, dir, prompt)
	}
	answer, usage, err := reporter.RunReportingUsage(ctx, dir, prompt)
	m.milliUSD += usage.MilliUSD
	m.unreported = m.unreported || !usage.Reported
	m.handoffs = append(m.handoffs, usage.Handoffs...)
	if usage.Tokens != nil {
		m.tokens.Add(*usage.Tokens)
	} else {
		m.tokensUnreported = true
	}
	return answer, err
}

// total is what the run cost in thousandths of a US dollar, or nil when a call
// didn't report its cost. A run that called no agent cost nothing.
func (m *usageMeter) total() *int64 {
	if m.unreported {
		return nil
	}
	return &m.milliUSD
}

// totalTokens is what the run's calls used, or nil when a call didn't report
// its tokens. A run that called no agent used none.
func (m *usageMeter) totalTokens() *model.TokenUsage {
	if m.tokensUnreported {
		return nil
	}
	return &m.tokens
}

func tail(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return "…" + s[len(s)-n:]
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}
