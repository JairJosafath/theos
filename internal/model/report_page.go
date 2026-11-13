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
	"math/big"
	"path"
	"regexp"
	"strings"
	"time"
)

// ReportDir is where a repository keeps its reports unless it says another
// place (#66): one page and one JSON document per period, named after it.
const ReportDir = "docs/results"

// reportName is what a report may be called: a release's tag, or a word like
// one. The name is two files' and goes into the page's heading and its link
// to the JSON, so it holds no path separator, no space and nothing a path or
// a link reads as more than a name.
var reportName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+-]*$`)

// CheckReportName is the error of a name no report can have, and nil for
// one it can: "v0.5.0", not "", "a/b" or "../../README".
func CheckReportName(name string) error {
	if !reportName.MatchString(name) {
		return fmt.Errorf("a report can't be called %q: a name is letters, digits and . _ + -, and begins with a "+
			"letter or a digit", name)
	}
	return nil
}

// ReportPaths is where the report of the period called name lives under dir,
// a repository's directory for its reports (ReportDir when dir is empty): its
// page and its JSON document. The directory is the caller's own to choose and
// is taken as given; a name that could leave it is an error.
func ReportPaths(dir, name string) (page, document string, err error) {
	if err := CheckReportName(name); err != nil {
		return "", "", err
	}
	if dir == "" {
		dir = ReportDir
	}
	return path.Join(dir, name+".md"), path.Join(dir, name+".json"), nil
}

// reportNone stands in a table for a number that has no value.
const reportNone = "–"

// Markdown is the report as the page a release publishes. It prints nothing
// the JSON doesn't hold, and the same report gives the same page, byte for
// byte. The page links its JSON document by file name, so the two lie in one
// directory, whichever it is. A report whose name is none (CheckReportName)
// has no page. Text that comes from the cluster (a reason, a harness and
// model, an Agent, a Scorer and its criteria) is printed as text
// (reportCell), whatever it holds.
func (r Report) Markdown() (string, error) {
	if err := CheckReportName(r.Name); err != nil {
		return "", err
	}
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

	document := r.Name + ".json"
	line("# The factory's numbers: %s", reportCell(r.Name))
	line("")
	period := "The Tasks that finished up to " + reportTime(r.To)
	if r.From != "" {
		period = "The Tasks that finished after " + reportTime(r.From) + ", up to " + reportTime(r.To)
	}
	line("%s, counted from what each Task records. The same numbers as JSON: [%s](%s).",
		period, reportCell(document), document)
	line("")

	line("## What finished")
	line("")
	table("| Outcome | Tasks |", "| --- | ---: |", [][]string{
		{"Merged a pull request", fmt.Sprint(r.Merged)},
		{"Failed", fmt.Sprint(r.Failed)},
		{"Ended with no change needed", fmt.Sprint(r.NoChange)},
	})
	if r.Merged > 0 {
		line("The JSON names each merged Task and its pull request, under `mergedTasks`, so a number here can be " +
			"held against the pull requests it counts.")
		line("")
	}
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
		line("### %s/%s", reportCell(s.Namespace), reportCell(s.Scorer))
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
	return b.String(), nil
}

// reportTime writes an RFC 3339 time as a page says it: 2026-11-05 12:00 UTC.
func reportTime(rfc3339 string) string {
	t, err := time.Parse(time.RFC3339, rfc3339)
	if err != nil {
		return rfc3339
	}
	return t.UTC().Format("2006-01-02 15:04 UTC")
}

// reportEscapes takes from a text what Markdown reads in it, in one pass, so
// no escape is escaped again. A backslash goes before the backslash, so the
// text's own can't undo the escape after it ("a\|b"), and before a pipe, a
// backtick, an asterisk, an underscore, a tilde and both square brackets.
// The backtick is one of them because a code span takes backslashes as they
// are, and would show the escapes inside it; with it there, the emphasis and
// link characters cost nothing more, and a criterion that says
// [see here](https://...) prints as it was typed and links nowhere. An
// ampersand and a less-than sign become their HTML entities, which every
// Markdown renderer prints as the character: no tag and no entity of the
// text's own reaches the page. A dollar sign goes into a span: GitHub reads
// what lies between two of them as mathematics, so a criterion that names
// $HOME and $PATH would print as a formula, and the span is what its
// documentation gives for a dollar sign outside a formula ("Writing
// mathematical expressions"; a backslash is its escape inside one). The
// span's own less-than sign is the replacer's and is not escaped again.
var reportEscapes = strings.NewReplacer(
	`\`, `\\`, "|", `\|`, "`", "\\`", "*", `\*`, "_", `\_`, "~", `\~`, "[", `\[`, "]", `\]`, "$", `<span>$</span>`,
	"&", "&amp;", "<", "&lt;",
)

// reportCell makes text that came from the cluster safe on the page, in a
// table cell or a heading: one line, no column of its own, and printed as
// the characters it is. A reason, a harness and model, an Agent's name and a
// Scorer's criteria are what someone typed into a resource; none of it is
// the page's markup. A bare address is left as it is: GitHub links it, to
// the address it shows.
func reportCell(s string) string {
	return reportEscapes.Replace(strings.Join(strings.Fields(s), " "))
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

// reportMoney writes thousandths of a US dollar as the UI's formatCost does
// (ui/src/model/brief.ts): dollars and the nearest cent, half a cent going
// up, and "<$0.01" for a cost under one cent, which is not nothing. The
// nearest cent is to the number of dollars as the machine holds it, as
// JavaScript's toFixed takes it: $1.125 is held exactly and prints $1.13,
// $1.115 is held a hair under and prints $1.11. fmt's %.2f rounds an exact
// half to the even cent, $1.12, and the page would differ from the UI by a
// cent. The cases both sides must print are in the fixture's money.json.
// A cost under zero, or one that is no number (NaN, an infinity), is no cost
// and prints as none: no Task's agents report one, the arithmetic below has
// no cents for it, and "$-1.-49" on a published page would pass for a number.
func reportMoney(milliUSD *float64) string {
	if milliUSD == nil || math.IsNaN(*milliUSD) || math.IsInf(*milliUSD, 0) || *milliUSD < 0 {
		return reportNone
	}
	if *milliUSD > 0 && *milliUSD < 10 {
		// No tag begins with "<$", so the sign needs no entity.
		return "<$0.01"
	}
	// Exact: a float64 times a hundred, plus a half, fits in 128 bits.
	usd := new(big.Float).SetPrec(128).SetFloat64(*milliUSD / 1000)
	cents, _ := usd.Mul(usd, big.NewFloat(100)).Add(usd, big.NewFloat(0.5)).Int(nil)
	dollars, rest := new(big.Int).QuoRem(cents, big.NewInt(100), new(big.Int))
	return fmt.Sprintf("$%d.%02d", dollars, rest)
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
