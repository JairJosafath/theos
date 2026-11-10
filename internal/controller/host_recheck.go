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
	"time"

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	"sigs.k8s.io/controller-runtime/pkg/client"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
	"github.com/JairJosafath/theos/internal/model"
)

// The longer fallback poll (docs/plans/done/git-events.md, decision 9, #1222):
// while a GitProvider's deliveries arrive, a delivery reconciles what it
// concerns at once, so the polls that wait for its host stretch to
// model.EventsFallbackPoll. A reconcile reads its GitProvider once, from the
// cache, into its context with withHostEvents; each poll that waits for the
// host asks hostRecheck. Error retries and waits on anything but the host
// keep their intervals, and don't ask.

// hostEventsKey holds, in a reconcile's context, whether its GitProvider's
// Events condition is True.
type hostEventsKey struct{}

// withHostEvents returns ctx marked with whether the GitProvider
// namespace/provider receives deliveries: its Events condition is True
// (Receiving). A GitProvider that can't be read counts as not receiving, so
// everything polls as without events.
func withHostEvents(ctx context.Context, c client.Reader, namespace, provider string) context.Context {
	return context.WithValue(ctx, hostEventsKey{}, hostEvents(ctx, c, namespace, provider))
}

func hostEvents(ctx context.Context, c client.Reader, namespace, provider string) bool {
	if provider == "" {
		return false
	}
	var gp c4v1beta1.GitProvider
	if err := c.Get(ctx, client.ObjectKey{Namespace: namespace, Name: provider}, &gp); err != nil {
		return false
	}
	return apimeta.IsStatusConditionTrue(gp.Status.Conditions, c4v1beta1.ConditionGitProviderEvents)
}

// hostRecheck is when a poll that waits for the Git host looks again, d
// being its interval: model.HostRecheck with the events withHostEvents put
// in ctx.
func hostRecheck(ctx context.Context, d time.Duration) time.Duration {
	events, _ := ctx.Value(hostEventsKey{}).(bool)
	return model.HostRecheck(d, events)
}
