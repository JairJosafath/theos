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

// Package engine is the one copy of the loops both runtimes run
// (docs/plans/lead-runtime.md, decision 2): a tick reads the facts through
// the ports, calls the business rules' decision and carries out its step
// through the ports. The operator calls a tick from a reconcile; the lead
// runtime from a theos command. Each tick starts from what the store and
// the Git host hold, so a runtime that stops loses nothing.
//
// The engine imports internal/model and internal/ports alone: no
// Kubernetes, no controller-runtime, no net/http, no API types
// (TestImports).
package engine

import "time"

// HostTimeout bounds each of a tick's reads of the Git host, and its writes.
const HostTimeout = time.Minute

// HostError is a tick's failure to read the Git host. The runtime waits
// and ticks again; any other error is the store's.
type HostError struct {
	Err error
}

func (e *HostError) Error() string { return e.Err.Error() }

func (e *HostError) Unwrap() error { return e.Err }
