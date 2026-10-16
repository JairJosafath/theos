package agentrunner

import (
	"context"
	"fmt"
	"image"
	_ "image/gif"  // decodes GIF sizes
	_ "image/jpeg" // decodes JPEG sizes
	_ "image/png"  // decodes PNG sizes
	"os"
	"path/filepath"
	"slices"
	"strings"

	_ "golang.org/x/image/webp" // decodes WebP sizes
)

// imageExtensions are the files the reviewer is shown as images.
var imageExtensions = []string{".png", ".jpg", ".jpeg", ".gif", ".webp", ".svg"}

// ChangedImage is an image the change adds or changes, as it is on the branch.
type ChangedImage struct {
	Path  string
	Bytes int64
	// Width and Height are its size in pixels; zero for an SVG or an image
	// that can't be decoded.
	Width, Height int
	// Unreadable says the file could not be decoded as its extension says.
	Unreadable bool
}

// String is how the review prompt lists the image.
func (i ChangedImage) String() string {
	switch {
	case i.Unreadable:
		return fmt.Sprintf("%s: %d bytes, not a readable image", i.Path, i.Bytes)
	case i.Width > 0 || i.Height > 0:
		return fmt.Sprintf("%s: %d×%d pixels, %d bytes", i.Path, i.Width, i.Height, i.Bytes)
	default:
		return fmt.Sprintf("%s: %d bytes", i.Path, i.Bytes)
	}
}

func isImage(path string) bool {
	return slices.Contains(imageExtensions, strings.ToLower(filepath.Ext(path)))
}

// changedImages lists the images the work branch adds or changes since it forked
// from the base, with their sizes as checked out.
func (r *repo) changedImages(ctx context.Context, base string) ([]ChangedImage, error) {
	out, err := r.git(ctx, r.dir, "diff", "--name-only", "--no-renames", "--diff-filter=AM", "-z",
		"origin/"+base+"...HEAD")
	if err != nil {
		return nil, err
	}
	var images []ChangedImage
	for path := range strings.SplitSeq(out, "\x00") {
		if path == "" || !isImage(path) {
			continue
		}
		img, err := imageAt(filepath.Join(r.dir, path))
		if err != nil {
			return nil, err
		}
		img.Path = path
		images = append(images, img)
	}
	return images, nil
}

// imageAt reads the size of the image file at path. A symbolic link is not
// followed: it is reported as unreadable.
func imageAt(path string) (ChangedImage, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return ChangedImage{}, err
	}
	img := ChangedImage{Bytes: info.Size()}
	if !info.Mode().IsRegular() {
		img.Unreadable = true
		return img, nil
	}
	if strings.EqualFold(filepath.Ext(path), ".svg") {
		return img, nil
	}
	f, err := os.Open(path) //nolint:gosec // a changed file of the checkout
	if err != nil {
		return ChangedImage{}, err
	}
	defer func() { _ = f.Close() }()
	cfg, _, err := image.DecodeConfig(f)
	if err != nil {
		img.Unreadable = true
		return img, nil
	}
	img.Width, img.Height = cfg.Width, cfg.Height
	return img, nil
}
