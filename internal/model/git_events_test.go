package model

import "testing"

func TestGitEventsURL(t *testing.T) {
	tests := []struct {
		base, namespace, name, want string
	}{
		{base: "http://theos-git-events.theos-system.svc:8082", namespace: "shop", name: "git",
			want: "http://theos-git-events.theos-system.svc:8082/git-events/shop/git"},
		{base: "https://factory.example/", namespace: "shop", name: "git",
			want: "https://factory.example/git-events/shop/git"},
		{base: "https://factory.example/theos", namespace: "a", name: "b",
			want: "https://factory.example/theos/git-events/a/b"},
	}
	for _, tt := range tests {
		if got := GitEventsURL(tt.base, tt.namespace, tt.name); got != tt.want {
			t.Errorf("GitEventsURL(%q, %q, %q) = %q, want %q", tt.base, tt.namespace, tt.name, got, tt.want)
		}
	}
}

func TestGitEventsSecretName(t *testing.T) {
	if got, want := GitEventsSecretName("git"), "git-git-events"; got != want {
		t.Errorf("GitEventsSecretName(git) = %q, want %q", got, want)
	}
}
