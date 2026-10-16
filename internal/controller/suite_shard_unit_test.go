/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of C4 Factory, which is proprietary. No license is granted
to use, copy, modify or distribute it without the copyright holder's written
permission. See the LICENSE file.
*/

package controller

import (
	"fmt"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	"github.com/onsi/ginkgo/v2/types"
)

// The shards split the suite by top-level container, evenly, each container
// in exactly one shard, and skip what the preview would not run (#1283).
func TestShardFocus(t *testing.T) {
	spec := func(container, line int, state types.SpecState) SpecReport {
		return SpecReport{
			LeafNodeType:                types.NodeTypeIt,
			State:                       state,
			LeafNodeLocation:            types.CodeLocation{FileName: "/x/a_test.go", LineNumber: line},
			ContainerHierarchyLocations: []types.CodeLocation{{FileName: "/x/a_test.go", LineNumber: container}},
		}
	}
	var preview Report
	// Containers at lines 10, 20, 30 and 40 with 4, 3, 2 and 1 specs.
	for c, n := range map[int]int{10: 4, 20: 3, 30: 2, 40: 1} {
		for j := range n {
			preview.SpecReports = append(preview.SpecReports, spec(c, c+j+1, types.SpecStatePassed))
		}
	}
	preview.SpecReports = append(preview.SpecReports,
		spec(50, 51, types.SpecStateSkipped), spec(60, 61, types.SpecStatePending),
		SpecReport{LeafNodeType: types.NodeTypeBeforeSuite, State: types.SpecStatePassed})

	for _, tc := range []struct {
		n     int
		specs []int
		files [][]string
	}{
		{1, []int{10}, [][]string{{`^/x/a_test\.go$:10`, `^/x/a_test\.go$:20`, `^/x/a_test\.go$:30`, `^/x/a_test\.go$:40`}}},
		// 4 to the first, 3 to the second, 2 to the second, 1 to the first.
		{2, []int{5, 5}, [][]string{{`^/x/a_test\.go$:10`, `^/x/a_test\.go$:40`}, {`^/x/a_test\.go$:20`, `^/x/a_test\.go$:30`}}},
		// More shards than containers: the last focuses on nothing.
		{5, []int{4, 3, 2, 1, 0}, [][]string{{`^/x/a_test\.go$:10`}, {`^/x/a_test\.go$:20`}, {`^/x/a_test\.go$:30`}, {`^/x/a_test\.go$:40`}, nil}},
	} {
		for i := 1; i <= tc.n; i++ {
			files, specs := shardFocus(preview, i, tc.n)
			want := append([]string{"^$:0"}, tc.files[i-1]...)
			if specs != tc.specs[i-1] || fmt.Sprint(files) != fmt.Sprint(want) {
				t.Errorf("shardFocus(%d/%d) = %v, %d; want %v, %d", i, tc.n, files, specs, want, tc.specs[i-1])
			}
			if _, err := types.ParseFileFilters(files); err != nil {
				t.Errorf("shardFocus(%d/%d) filters don't parse: %v", i, tc.n, err)
			}
		}
	}
}
