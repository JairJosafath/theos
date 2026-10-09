// Command printversion prints buildinfo.Version, for buildinfo's ldflags test.
package main

import (
	"fmt"

	"github.com/JairJosafath/c4factory/internal/buildinfo"
)

func main() { fmt.Println(buildinfo.Version) }
