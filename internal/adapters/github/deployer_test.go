package github

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/JairJosafath/c4factory/internal/model"
	"github.com/JairJosafath/c4factory/internal/ports"
)

// #1598: a source the Git host streams without a file the commit needs is
// the host's side, a SourceError that portError passes through, not GitHub
// unreachable.
func TestUploadBlobsFilesGone(t *testing.T) {
	s := ports.Shipment{Version: "v1.0.0",
		ReadSource: func(context.Context, func(string, int64, io.Reader) error) error { return nil }}
	err := portError(uploadBlobs(context.Background(), nil, s,
		[]model.SourceFile{{Path: "main.go", Size: 1, Blob: "abc"}}))
	if _, ok := errors.AsType[*SourceError](err); !ok {
		t.Fatalf("uploadBlobs() = %v, want a SourceError", err)
	}
	if _, ok := errors.AsType[*ports.Error](err); ok {
		t.Fatalf("uploadBlobs() = %v, want no ports.Error", err)
	}
}

// #1605: images go to the registry of the target's GitHub Enterprise Server,
// GHCR without url, and the Deployer's own registry, a test's, over both.
func TestRegistryOf(t *testing.T) {
	ghe := ports.Target{Settings: map[string]string{model.GitHubSettingURL: "https://ghe.example.com"}}
	if got := New(nil).registryOf(ghe); got != "containers.ghe.example.com" {
		t.Errorf("registryOf(ghe) = %q", got)
	}
	if got := New(nil).registryOf(ports.Target{}); got != model.GHCRHost {
		t.Errorf("registryOf(github.com) = %q", got)
	}
	if got := New(nil, WithImageRegistry("127.0.0.1:5000")).registryOf(ghe); got != "127.0.0.1:5000" {
		t.Errorf("registryOf(ghe) with a registry = %q", got)
	}
}
