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

// An operator upgrade that changes an agent bot's token scopes makes a new
// token beside the old one, so a sandbox already running keeps the token it
// started with (#519). The old one is revoked once no sandbox of the Container
// may still hold it.

// OldTokensUnused reports whether no sandbox may still hold a token made
// before since, the time the new token was made: each sandbox, by its
// creation time, started after it. A sandbox started in the same second may
// have read the old token, so it holds the old ones until it ends.
func OldTokensUnused(since time.Time, sandboxes []time.Time) bool {
	for _, started := range sandboxes {
		if !started.After(since) {
			return false
		}
	}
	return true
}
