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

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/JairJosafath/theos/internal/adapters/hostrun"
	"github.com/JairJosafath/theos/internal/buildinfo"
	"github.com/JairJosafath/theos/internal/model"
	"github.com/JairJosafath/theos/internal/nocluster"
	"github.com/JairJosafath/theos/internal/ports"
)

// The lead runtime's commands and theos mcp (docs/plans/lead-runtime.md,
// decision 8): the same work on the command line and as MCP tools over
// stdio. There is no merge tool: a person merges (#292).

// leadEnv adds the lead runtime to env: hostrun on each Container's host,
// and the clone of a claimed Task's branch under the project repository.
func leadEnv(env nocluster.Env, d deps, dir string) nocluster.Env {
	env.Runtime = func(host nocluster.LeadHost, container string, rules model.ClaimRules) ports.Runtime {
		return hostrun.New(host, container, rules, d.now)
	}
	env.Clone = func(ctx context.Context, repo ports.Repository, branch, work string) error {
		if !filepath.IsLocal(filepath.FromSlash(work)) {
			return fmt.Errorf("%s is not a folder of the project repository", work)
		}
		full := filepath.Join(dir, filepath.FromSlash(work))
		if _, err := os.Stat(full); err == nil { //nolint:gosec // work is local, checked above
			return nil // cloned at an earlier claim
		}
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil { //nolint:gosec // the project's own folder
			return err
		}
		// The folder ignores itself, so no clone lands in the project's commits.
		ignore := filepath.Join(dir, nocluster.WorkDir, ".gitignore")
		if _, err := os.Stat(ignore); errors.Is(err, fs.ErrNotExist) {
			if err := os.WriteFile(ignore, []byte("*\n"), 0o644); err != nil { //nolint:gosec // a .gitignore
				return err
			}
		}
		// git asks its own credential helper: theos hands git no credential.
		_, err := d.runner.Run(ctx, "", "git", "clone", "--quiet", "--branch", branch, "--single-branch",
			repo.CloneURL, full)
		return err
	}
	return env
}

// The tools' inputs.
type (
	nextIn   struct{}
	statusIn struct{}
	claimIn  struct {
		Task    string `json:"task" jsonschema:"the Task, as theos_next names it"`
		Session string `json:"session" jsonschema:"your session's name: one word, the same in every call"`
	}
	doneIn struct {
		Task    string `json:"task" jsonschema:"the Task, as theos_next names it"`
		Session string `json:"session" jsonschema:"your session's name, as you claimed with"`
		Commit  string `json:"commit,omitempty" jsonschema:"an implementer's: the commit pushed to the Task's branch"`
		Verdict string `json:"verdict,omitempty" jsonschema:"a reviewer's: approve or changes"`
		Summary string `json:"summary" jsonschema:"what you did, or why your verdict"`
	}
	statusOut struct {
		Status string `json:"status"`
	}
)

// leadTools are theos mcp's tools, each reading the model files afresh.
type leadTools struct {
	env nocluster.Env
	dir string
}

func (t leadTools) model() (*nocluster.Model, error) { return nocluster.Read(os.DirFS(t.dir)) }

// result is out as a tool's result; an error is the tool's, said to the
// caller, never a protocol error.
//
//nolint:unparam // the SDK's handler returns an error too
func result[O any](out O, err error) (*mcp.CallToolResult, O, error) {
	if err != nil {
		var zero O
		return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}}}, zero,
			nil
	}
	return nil, out, nil
}

func (t leadTools) next(ctx context.Context, _ *mcp.CallToolRequest, _ nextIn) (*mcp.CallToolResult,
	nocluster.NextResult, error) {
	m, err := t.model()
	if err != nil {
		return result(nocluster.NextResult{}, err)
	}
	return result(nocluster.Next(ctx, t.env, m))
}

func (t leadTools) claim(ctx context.Context, _ *mcp.CallToolRequest, in claimIn) (*mcp.CallToolResult,
	nocluster.ClaimResult, error) {
	m, err := t.model()
	if err != nil {
		return result(nocluster.ClaimResult{}, err)
	}
	return result(nocluster.Claim(ctx, t.env, m, in.Task, in.Session))
}

func (t leadTools) heartbeat(ctx context.Context, _ *mcp.CallToolRequest, in claimIn) (*mcp.CallToolResult,
	nocluster.ClaimResult, error) {
	m, err := t.model()
	if err != nil {
		return result(nocluster.ClaimResult{}, err)
	}
	return result(nocluster.Heartbeat(ctx, t.env, m, in.Task, in.Session))
}

func (t leadTools) release(ctx context.Context, _ *mcp.CallToolRequest, in claimIn) (*mcp.CallToolResult,
	nocluster.ClaimResult, error) {
	m, err := t.model()
	if err != nil {
		return result(nocluster.ClaimResult{}, err)
	}
	return result(nocluster.Release(ctx, t.env, m, in.Task, in.Session))
}

func (t leadTools) done(ctx context.Context, _ *mcp.CallToolRequest, in doneIn) (*mcp.CallToolResult,
	nocluster.ClaimResult, error) {
	m, err := t.model()
	if err != nil {
		return result(nocluster.ClaimResult{}, err)
	}
	return result(nocluster.Done(ctx, t.env, m, in.Task, in.Session, in.Commit, in.Verdict, in.Summary))
}

func (t leadTools) status(ctx context.Context, _ *mcp.CallToolRequest, _ statusIn) (*mcp.CallToolResult,
	statusOut, error) {
	m, err := t.model()
	if err != nil {
		return result(statusOut{}, err)
	}
	var b bytesWriter
	env := t.env
	env.Out, env.Project = &b, os.DirFS(t.dir)
	err = nocluster.Status(ctx, env, m)
	return result(statusOut{Status: string(b)}, err)
}

// bytesWriter is a writer of a byte slice.
type bytesWriter []byte

func (b *bytesWriter) Write(p []byte) (int, error) {
	*b = append(*b, p...)
	return len(p), nil
}

// ToolNames are theos mcp's tools: no merge among them.
var ToolNames = []string{"theos_next", "theos_claim", "theos_heartbeat", "theos_release", "theos_done", "theos_status"}

// mcpServer is theos mcp's server on the project repository dir.
func mcpServer(env nocluster.Env, dir string) *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: "theos", Version: buildinfo.Version}, nil)
	t := leadTools{env: env, dir: dir}
	mcp.AddTool(s, &mcp.Tool{Name: ToolNames[0], Description: "The lead's: runs the Crews' tick and each Task's, " +
		"and returns the open offers, each with the subagent to start and its work order, and every Task at work."},
		t.next)
	mcp.AddTool(s, &mcp.Tool{Name: ToolNames[1], Description: "A role's first call: claims an offered Task for " +
		"your session, and clones its branch into work/<task>. Work only when it says held."}, t.claim)
	mcp.AddTool(s, &mcp.Tool{Name: ToolNames[2], Description: "Renews your claim when under half its lease is " +
		"left. Call it when half an hour passed without a theos call."}, t.heartbeat)
	mcp.AddTool(s, &mcp.Tool{Name: ToolNames[3], Description: "Gives your claim up, for another session to " +
		"take."}, t.release)
	mcp.AddTool(s, &mcp.Tool{Name: ToolNames[4], Description: "Ends your turn: an implementer's pushed commit " +
		"and summary, or a reviewer's verdict (approve or changes) and why."}, t.done)
	mcp.AddTool(s, &mcp.Tool{Name: ToolNames[5], Description: "What theos status prints: each Container, what " +
		"this runtime does not honour, the lead's files and the losses."}, t.status)
	return s
}

// serveMCP serves theos mcp over stdin and stdout until the client goes.
func serveMCP(ctx context.Context, env nocluster.Env, d deps, dir string) error {
	err := mcpServer(env, dir).Run(ctx, &mcp.IOTransport{Reader: io.NopCloser(d.stdin), Writer: nopWriteCloser{d.stdout}})
	if errors.Is(err, io.EOF) || errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}

type nopWriteCloser struct{ io.Writer }

func (nopWriteCloser) Close() error { return nil }

// leadCommand runs a lead command on the command line, printing its result
// as JSON.
func leadCommand(ctx context.Context, cmd string, env nocluster.Env, out io.Writer, dir string, in doneIn) error {
	m, err := nocluster.Read(os.DirFS(dir))
	if err != nil {
		return err
	}
	var res any
	switch cmd {
	case "next":
		res, err = nocluster.Next(ctx, env, m)
	case "claim":
		res, err = nocluster.Claim(ctx, env, m, in.Task, in.Session)
	case "heartbeat":
		res, err = nocluster.Heartbeat(ctx, env, m, in.Task, in.Session)
	case "release":
		res, err = nocluster.Release(ctx, env, m, in.Task, in.Session)
	default: // done
		res, err = nocluster.Done(ctx, env, m, in.Task, in.Session, in.Commit, in.Verdict, in.Summary)
	}
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(res, "", "  ")
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(out, "%s\n", data)
	return err
}
