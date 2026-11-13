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

// #43: a Container waits out the settle window after its creation, so the
// Relationships applied with it count at the scaffold.
func TestSettleLeft(t *testing.T) {
	created := time.Date(2026, 11, 9, 5, 0, 0, 0, time.UTC)
	tests := []struct {
		name   string
		now    time.Time
		settle time.Duration
		want   time.Duration
	}{
		{"just created", created, 10 * time.Second, 10 * time.Second},
		{"inside the window", created.Add(4 * time.Second), 10 * time.Second, 6 * time.Second},
		{"at the window's end", created.Add(10 * time.Second), 10 * time.Second, 0},
		{"long after", created.Add(time.Hour), 10 * time.Second, 0},
		{"no window", created, 0, 0},
		{"a negative window is none", created, -time.Second, 0},
		{"a clock behind the creation waits the window and the skew", created.Add(-time.Second), 10 * time.Second,
			11 * time.Second},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := SettleLeft(created, tt.now, tt.settle); got != tt.want {
				t.Errorf("SettleLeft() = %v, want %v", got, tt.want)
			}
		})
	}
}
