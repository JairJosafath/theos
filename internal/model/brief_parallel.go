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

// BriefParallelDefault is how many issues a Brief's Crews work on at once when
// nothing says otherwise (plans/done/brief-parallel.md, decision 2). The
// agent-slot queue still caps how many agents run.
const BriefParallelDefault int32 = 3

// BriefParallel is how many issues a Brief's Crews work on at once: spec, the
// Brief's spec.parallel, wins; else status, the number a prompt gave; else
// BriefParallelDefault. Zero is unset.
func BriefParallel(spec, status int32) int32 {
	switch {
	case spec > 0:
		return spec
	case status > 0:
		return status
	}
	return BriefParallelDefault
}
