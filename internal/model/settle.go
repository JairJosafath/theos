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

// DefaultScaffoldSettle is how long a new Container waits before its scaffold
// (#43): a model applied in one go creates a Container's Relationships after
// it (kubectl apply -f goes file by file, kustomize doesn't order custom
// resources), and the scaffold reads the Connectors from the Relationships
// there are then, once. The operator's --scaffold-settle sets it.
const DefaultScaffoldSettle = 10 * time.Second

// SettleLeft is how much longer a Container created at created waits, at now,
// before it is scaffolded: zero once settle has passed, and for a settle of
// zero or less.
func SettleLeft(created, now time.Time, settle time.Duration) time.Duration {
	if settle <= 0 {
		return 0
	}
	return max(created.Add(settle).Sub(now), 0)
}
