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

import "time"

// DefaultStaleAfter is how long a Session may go without a heartbeat before the
// operator marks it Stale. The client sends one every 30 seconds.
const DefaultStaleAfter = 2 * time.Minute

// SessionFreshness says whether a Session is stale at now, and if not, how long
// until it would be. A Session that never sent a heartbeat counts from its creation.
func SessionFreshness(lastHeartbeat, created, now time.Time, staleAfter time.Duration) (stale bool, remaining time.Duration) {
	last := lastHeartbeat
	if last.IsZero() {
		last = created
	}
	remaining = last.Add(staleAfter).Sub(now)
	if remaining <= 0 {
		return true, 0
	}
	return false, remaining
}
