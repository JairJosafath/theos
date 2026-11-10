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

package controller

import (
	"context"
	"crypto/rand"
	"errors"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
	"github.com/JairJosafath/theos/internal/ports"
)

// minter is a host that mints agent credentials, each living for life (0:
// until revoked), and counts them.
type minter struct {
	now    time.Time
	life   time.Duration
	minted int
}

func (m *minter) access() (ports.Credentials, error) {
	m.minted++
	creds := ports.Credentials{Username: "bot", Token: rand.Text()}
	if m.life > 0 {
		creds.Expires = m.now.Add(m.life)
	}
	return creds, nil
}

// TestAgentCredentialOutlivesItsRun covers #81: a run starts with a
// credential that outlives it. A credential that never ends is minted once.
// One that ends is kept with when, and minted again when it would end
// before the run does. And a host whose credentials live shorter than the
// run may take says so after one minting, and mints no more for it.
func TestAgentCredentialOutlivesItsRun(t *testing.T) {
	scheme := briefScheme(t)
	ctx := context.Background()
	container := newContainer("apps", "orders-api", "webshop-core", c4v1beta1.ContainerTypeService)
	start := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	const name = "orders-api-agent-git"
	ensure := func(c client.Client, m *minter, at time.Time, outlive time.Duration) error {
		m.now = at
		_, err := ensureAgentSecret(ctx, c, c, container, name, nil, map[string]string{"scopes": "all"}, at, outlive, m.access)
		return err
	}
	kept := func(c client.Client) corev1.Secret {
		var s corev1.Secret
		if err := c.Get(ctx, key("apps", name), &s); err != nil {
			t.Fatal(err)
		}
		return s
	}

	t.Run("a credential that never ends is minted once, whatever a run takes", func(t *testing.T) {
		c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(container).Build()
		m := &minter{}
		for _, outlive := range []time.Duration{0, time.Hour, 4 * time.Hour} {
			if err := ensure(c, m, start.Add(outlive), outlive); err != nil {
				t.Fatal(err)
			}
		}
		if s := kept(c); m.minted != 1 || s.Data[agentSecretExpires] != nil || s.Data[agentSecretLifetime] != nil {
			t.Errorf("minted %d times, and the Secret says the token ends: want one token that doesn't", m.minted)
		}
	})

	t.Run("one that ends is kept while it outlives the run, and minted again when it would end first", func(t *testing.T) {
		c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(container).Build()
		m := &minter{life: time.Hour}
		run := 50 * time.Minute
		if err := ensure(c, m, start, run); err != nil {
			t.Fatal(err)
		}
		first := kept(c)
		if got := string(first.Data[agentSecretExpires]); got != "2026-11-10T13:00:00Z" || string(first.Data[agentSecretLifetime]) != "3600" {
			t.Errorf("the Secret says the token ends at %q after %q seconds, want 13:00 and 3600", got,
				first.Data[agentSecretLifetime])
		}
		// Nine minutes on, fifty-one are left: the same token.
		if err := ensure(c, m, start.Add(9*time.Minute), run); err != nil || m.minted != 1 {
			t.Fatalf("with 51 minutes left for a 50 minute run: minted %d times, %v; want the kept token", m.minted, err)
		}
		// The operator's own follow-up needs five minutes of it, long after.
		if err := ensure(c, m, start.Add(54*time.Minute), credentialFollowUp); err != nil || m.minted != 1 {
			t.Fatalf("with 6 minutes left for a 5 minute follow-up: minted %d times, %v; want the kept token", m.minted, err)
		}
		// Twenty minutes on, forty are left: a new one, and the old marked as such.
		if err := ensure(c, m, start.Add(20*time.Minute), run); err != nil || m.minted != 2 {
			t.Fatalf("with 40 minutes left for a 50 minute run: minted %d times, %v; want a new token", m.minted, err)
		}
		second := kept(c)
		if string(second.Data[secretKeyToken]) == string(first.Data[secretKeyToken]) ||
			string(second.Data[agentSecretExpires]) != "2026-11-10T13:20:00Z" ||
			second.Annotations[annotationOldTokensBefore] != "2026-11-10T12:20:00Z" {
			t.Errorf("the new token is not kept with its own end, or the old ones are not marked: ends %q, marked %q",
				second.Data[agentSecretExpires], second.Annotations[annotationOldTokensBefore])
		}
	})

	t.Run("a host whose credentials live shorter than the run says so, and mints once", func(t *testing.T) {
		c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(container).Build()
		m := &minter{life: time.Hour}
		agent := &c4v1beta1.Agent{ObjectMeta: objectMeta("apps", "claude"), Spec: c4v1beta1.AgentSpec{TimeoutMinutes: 90}}
		needs := runTimeout(agent) + credentialFollowUp
		var short *credentialTooShort
		for i := range 3 {
			err := ensure(c, m, start.Add(time.Duration(i)*time.Minute), needs)
			if !errors.As(err, &short) {
				t.Fatalf("a 90 minute run on a host whose credentials live an hour gave %v, want it too short", err)
			}
		}
		if m.minted != 1 {
			t.Errorf("minted %d times for a run no credential outlives, want once", m.minted)
		}
		message := tooShortForAgent(agent, short)
		for _, want := range []string{"Agent claude", "may take 90 minutes", "lives 60", "spec.timeoutMinutes to 55 or less"} {
			if !strings.Contains(message, want) {
				t.Errorf("the Task says %q, want it to say %q", message, want)
			}
		}
		// A run that fits takes the token that is there.
		if err := ensure(c, m, start.Add(3*time.Minute), 45*time.Minute+credentialFollowUp); err != nil || m.minted != 1 {
			t.Errorf("a 45 minute run with 57 minutes left: minted %d times, %v; want the kept token", m.minted, err)
		}
	})
}
