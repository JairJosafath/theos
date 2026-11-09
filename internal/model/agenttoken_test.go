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
	"testing"
	"time"
)

func TestOldTokensUnused(t *testing.T) {
	since := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name      string
		sandboxes []time.Time
		want      bool
	}{
		{"no sandbox", nil, true},
		{"all started after", []time.Time{since.Add(time.Second), since.Add(time.Hour)}, true},
		{"one started before", []time.Time{since.Add(time.Hour), since.Add(-time.Minute)}, false},
		{"same second", []time.Time{since}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := OldTokensUnused(since, tt.sandboxes); got != tt.want {
				t.Errorf("OldTokensUnused() = %v, want %v", got, tt.want)
			}
		})
	}
}
