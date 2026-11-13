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

import "testing"

// TestChatTaskNewsMergedChange: the chat says a Task merged a change by the
// rule the metrics and the report use (#210). A Task that ended
// DoneByStandardChange keeps the pull request of its round 1, which was
// closed, and the message names the Standard change.
func TestChatTaskNewsMergedChange(t *testing.T) {
	base := ChatTaskView{Name: "api-9", Kind: "Implement", Phase: "Merged", Round: 2, Issue: 9, PullRequest: 12}
	cases := map[string]struct {
		edit func(*ChatTaskView)
		want string // "" for no merged line
	}{
		"merged pull request": {func(*ChatTaskView) {}, "Merged pull request #12 for issue #9"},
		"#210 done by a Standard change, its pull request closed": {func(v *ChatTaskView) {
			v.DoneByStandardChange, v.StandardChange = true, 41
		}, "Standard change #41 merged and left issue #9 nothing to do"},
		"#210 done by a Standard change with no record": {func(v *ChatTaskView) { v.DoneByStandardChange = true }, ""},
		"#210 nothing to do": {func(v *ChatTaskView) { v.NoChange = true }, ""},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			v := base
			c.edit(&v)
			n, ok := newsByEvent(ChatTaskNews(v))[ChatEventMerged]
			if c.want == "" {
				if ok {
					t.Fatalf("merged line %q, want none", n.Line)
				}
				return
			}
			if !ok || n.Line != c.want || n.Post.Title != c.want {
				t.Fatalf("news = %+v, want line %q", n, c.want)
			}
		})
	}
	v := base
	v.DoneByStandardChange, v.StandardChange = true, 41
	want := "Done: Standard change #41 merged and left the issue nothing to do."
	if got := newsByEvent(ChatTaskNews(v))[ChatEventChangeWaiting].Post.Status; got != want {
		t.Errorf("thread status = %q, want %q", got, want)
	}
}
