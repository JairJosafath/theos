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
