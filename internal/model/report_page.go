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

package model

import (
	"fmt"
	"math"
	"path"
	"strings"
	"time"
)

// ReportDir is where a repository keeps its reports (#66): one page and one
// JSON document per period, named after it.
const ReportDir = "docs/results"

// ReportPaths is where the report of the period called name lives: its page
// and its JSON document, from the repository's root.
func ReportPaths(name string) (page, document string) {
	return path.Join(ReportDir, name+".md"), path.Join(ReportDir, name+".json")
}

// reportNone stands in a table for a number that has no value.
const reportNone = "–"

// Markdown is the report as the page a release publishes. It prints nothing
// the JSON doesn't hold, and the same report gives the same page, byte for
// byte.
func (r Report) Markdown() string {
	var b strings.Builder
	line := func(format string, args ...any) { fmt.Fprintf(&b, format+"\n", args...) }
	table := func(head string, align string, rows [][]string) {
		line("%s", head)
		line("%s", align)
		for _, row := range rows {
			line("| %s |", strings.Join(row, " | "))
		}
		line("")
	}

	_, document := ReportPaths(r.Name)
	line("# The factory's numbers: %s", r.Name)
	line("")
	period := "The Tasks that finished up to " + reportTime(r.To)
	if r.From != "" {
		period = "The Tasks that finished after " + reportTime(r.From) + ", up to " + reportTime(r.To)
	}
	line("%s, counted from what each Task records. The same numbers as JSON: [%s](%s).",
		period, path.Base(document), path.Base(document))
	line("")

	line("## What finished")
	line("")
	table("| Outcome | Tasks |", "| --- | ---: |", [][]string{
		{"Merged a pull request", fmt.Sprint(r.Merged)},
		{"Failed", fmt.Sprint(r.Failed)},
		{"Ended with no change needed", fmt.Sprint(r.NoChange)},
	})
	if len(r.FailedByReason) > 0 {
		line("The failed Tasks, by the reason each recorded:")
		line("")
		rows := make([][]string, 0, len(r.FailedByReason))
		for _, c := range r.FailedByReason {
			rows = append(rows, []string{reportCell(c.Key), fmt.Sprint(c.Tasks)})
		}
		table("| Reason | Tasks |", "| --- | ---: |", rows)
	}

	line("## Who wrote the code, and who let it in")
	line("")
	table("| Merged Tasks | Tasks | Share |", "| --- | ---: | ---: |", [][]string{
		{"With no commit by a person", reportOf(r.NoPersonCommit), reportPercent(r.NoPersonCommit.Share)},
		{"On a person's approval", reportOf(r.PersonApproved), reportPercent(r.PersonApproved.Share)},
		{"In their first round", reportOf(r.FirstRound), reportPercent(r.FirstRound.Share)},
	})
	line("The first row is of the merged Tasks whose commits were read from the Git host.")
	line("")

	line("## How long a merged Task took")
	line("")
	times := func(rows []ReportTime) [][]string {
		var out [][]string
		for _, t := range rows {
			if t.Tasks > 0 {
				out = append(out, []string{t.Label, fmt.Sprint(t.Tasks),
					reportDuration(t.MedianSeconds), reportDuration(t.P90Seconds)})
			}
		}
		return out
	}
	leads, stages := times(r.LeadTimes), times(r.StageTimes)
	if len(leads)+len(stages) == 0 {
		line("No merged Task has a timeline in this period.")
		line("")
	}
	if len(leads) > 0 {
		line("From its first Implementing step:")
		line("")
		table("| To | Tasks | Median | 90th percentile |", "| --- | ---: | ---: | ---: |", leads)
	}
	if len(stages) > 0 {
		line("In each stage, over the Tasks that were in it:")
		line("")
		table("| Stage | Tasks | Median | 90th percentile |", "| --- | ---: | ---: | ---: |", stages)
	}

	line("## What a merged Task cost")
	line("")
	line("The median is %s, over the merged Tasks whose agents reported a cost: %d of %d.",
		reportMoney(r.Cost.MedianMilliUSD), r.Cost.Tasks, r.Merged)
	line("")
	sizes := make([][]string, 0, len(r.CostBySize))
	for _, s := range r.CostBySize {
		sizes = append(sizes, []string{reportSizeLabel(s.Size), fmt.Sprint(s.Merged), fmt.Sprint(s.Cost.Tasks),
			reportMoney(s.Cost.MedianMilliUSD)})
	}
	table("| Size, in changed lines | Merged | Reported a cost | Median cost |", "| --- | ---: | ---: | ---: |", sizes)
	if r.WithoutSize > 0 {
		line("%d merged %s had no size read and %s in no row above.", r.WithoutSize,
			plural(r.WithoutSize, "Task", "Tasks"), plural(r.WithoutSize, "is", "are"))
		line("")
	}
	groups := func(title string, rows []ReportGroup) {
		if len(rows) == 0 {
			return
		}
		var out [][]string
		for _, g := range rows {
			out = append(out, []string{reportCell(g.Key), fmt.Sprint(g.Merged),
				reportOf(g.NoPersonCommit), fmt.Sprint(g.Cost.Tasks), reportMoney(g.Cost.MedianMilliUSD),
				reportDuration(g.ToMergeSeconds)})
		}
		table("| "+title+" | Merged | No commit by a person | Reported a cost | Median cost | Median time to merge |",
			"| --- | ---: | ---: | ---: | ---: | ---: |", out)
	}
	groups("Harness and model", r.ByConfig)
	groups("Agent", r.ByAgent)

	line("## What the Scorers found")
	line("")
	if len(r.Scorers) == 0 {
		line("No Scorer judged a Task in this period.")
		line("")
	}
	for _, s := range r.Scorers {
		line("### %s/%s", s.Namespace, s.Scorer)
		line("")
		line("%d %s on the criteria of generation %d. A rate is the passes out of the passes and fails; "+
			"an unclear verdict is said apart.", s.Scores, plural(s.Scores, "Score", "Scores"), s.Generation)
		line("")
		head, align := "| Criterion | All |", "| --- | ---: |"
		for _, c := range s.Configs {
			head, align = head+" "+reportCell(c)+" |", align+" ---: |"
		}
		var rows [][]string
		for _, c := range s.Criteria {
			row := []string{reportCell(c.Criterion), reportRate(c.All)}
			for _, v := range c.ByConfig {
				row = append(row, reportRate(v))
			}
			rows = append(rows, row)
		}
		table(head, align, rows)
	}

	line("## What these numbers don't show")
	line("")
	for _, s := range r.NotShown {
		line("- %s", s)
	}
	return b.String()
}

// reportTime writes an RFC 3339 time as a page says it: 2026-11-05 12:00 UTC.
func reportTime(rfc3339 string) string {
	t, err := time.Parse(time.RFC3339, rfc3339)
	if err != nil {
		return rfc3339
	}
	return t.UTC().Format("2006-01-02 15:04 UTC")
}

// reportCell makes s safe in a table cell: one line, no column of its own.
func reportCell(s string) string {
	return strings.ReplaceAll(strings.Join(strings.Fields(s), " "), "|", `\|`)
}

// reportOf writes a share's count: 6 of 8.
func reportOf(s ReportShare) string {
	return fmt.Sprintf("%d of %d", s.Tasks, s.Of)
}

// reportPercent writes a share in whole percent.
func reportPercent(share *float64) string {
	if share == nil {
		return reportNone
	}
	return fmt.Sprintf("%d%%", int(math.Round(*share*100)))
}

// reportMoney writes thousandths of a US dollar as dollars and cents.
func reportMoney(milliUSD *float64) string {
	if milliUSD == nil {
		return reportNone
	}
	return fmt.Sprintf("$%.2f", *milliUSD/1000)
}

// reportDuration writes seconds in its two largest units: 45s, 12m 30s,
// 3h 5m, 2d 4h.
func reportDuration(seconds *float64) string {
	if seconds == nil {
		return reportNone
	}
	s := int64(math.Round(*seconds))
	two := func(n int64, unit string, rest int64, small string) string {
		if rest == 0 {
			return fmt.Sprintf("%d%s", n, unit)
		}
		return fmt.Sprintf("%d%s %d%s", n, unit, rest, small)
	}
	switch {
	case s < 60:
		return fmt.Sprintf("%ds", s)
	case s < 3600:
		return two(s/60, "m", s%60, "s")
	case s < 86400:
		return two(s/3600, "h", s%3600/60, "m")
	}
	return two(s/86400, "d", s%86400/3600, "h")
}

// reportSizeLabel names a size with its bounds, as the UI's panel does.
func reportSizeLabel(size string) string {
	switch size {
	case SizeS:
		return "S (under 100)"
	case SizeM:
		return "M (100 to 499)"
	case SizeL:
		return "L (500 to 999)"
	case SizeXL:
		return "XL (1000 or more)"
	}
	return size
}

// reportRate writes a criterion's verdicts as the UI does: 75% (3 of 4), and
// how many were unclear.
func reportRate(v ReportVerdicts) string {
	shown := reportNone
	if v.Rate != nil {
		shown = fmt.Sprintf("%s (%d of %d)", reportPercent(v.Rate), v.Pass, v.Pass+v.Fail)
	}
	if v.Unclear > 0 {
		return fmt.Sprintf("%s, %d unclear", shown, v.Unclear)
	}
	return shown
}
