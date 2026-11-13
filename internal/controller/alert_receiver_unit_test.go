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
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
	corev1 "k8s.io/api/core/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
	"github.com/JairJosafath/theos/internal/model"
)

// alertUnitSource is a generic AlertSource shop/alerts checked by a token,
// in a fake API server, and that token.
func alertUnitSource(t *testing.T) (*AlertReceiver, string, client.Client) {
	t.Helper()
	token := rand.Text()
	src := &c4v1beta1.AlertSource{ObjectMeta: metav1.ObjectMeta{Namespace: "shop", Name: "alerts"},
		Spec: c4v1beta1.AlertSourceSpec{Type: c4v1beta1.AlertSourceGeneric,
			SecretRef: c4v1beta1.LocalRef{Name: "alerts-webhook"}}}
	secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: "shop", Name: "alerts-webhook"},
		Data: map[string][]byte{model.AlertSecretKeyToken: []byte(token)}}
	c := fake.NewClientBuilder().WithScheme(briefScheme(t)).WithObjects(src, secret).
		WithStatusSubresource(&c4v1beta1.AlertSource{}).Build()
	return &AlertReceiver{Client: c, APIReader: c}, token, c
}

var alertUnitKey = types.NamespacedName{Namespace: "shop", Name: "alerts"}

// postAlert delivers body to path on h with the bearer token, and returns
// the answer's status.
func postAlert(h http.Handler, path, token, body string) int {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w.Code
}

func TestAlertReceiverAnswers(t *testing.T) {
	a, token, _ := alertUnitSource(t)
	h := a.Handler()
	path := model.AlertsURL("", "shop", "alerts")
	unauthorized := testutil.ToFloat64(alertDeliveries.WithLabelValues(alertUnauthorized))
	for _, tc := range []struct {
		name, path, token, body string
		want                    int
	}{
		{"an AlertSource that isn't there", model.AlertsURL("", "shop", "other"), token, `{"title":"x"}`,
			http.StatusNotFound},
		{"no credential", path, "", `{"title":"x"}`, http.StatusUnauthorized},
		{"another token", path, rand.Text(), `{"title":"x"}`, http.StatusUnauthorized},
		{"a body that isn't the type's shape", path, token, `{"summary":"no title"}`, http.StatusBadRequest},
		{"a body past 1 MiB", path, token, `{"title":"` + strings.Repeat("x", maxAlertBody) + `"}`,
			http.StatusRequestEntityTooLarge},
		{"a signed delivery", path, token, `{"title":"Disk full"}`, http.StatusAccepted},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := postAlert(h, tc.path, tc.token, tc.body); got != tc.want {
				t.Errorf("status = %d, want %d", got, tc.want)
			}
		})
	}
	if got := testutil.ToFloat64(alertDeliveries.WithLabelValues(alertUnauthorized)) - unauthorized; got != 2 {
		t.Errorf("unauthorized deliveries counted = %v, want 2", got)
	}
	first, counts := a.peek(alertUnitKey)
	if first == nil || first.Name != "Disk full" || counts.Deliveries != 1 {
		t.Fatalf("inbox = %+v, %+v; want the one signed delivery", first, counts)
	}

	// The inbox holds at most maxInboxAlerts: past it the sender retries.
	for i := 1; i < maxInboxAlerts; i++ {
		if !a.put(alertUnitKey, []model.Alert{{Key: fmt.Sprint(i)}}, 0) {
			t.Fatalf("put %d refused", i)
		}
	}
	if got := postAlert(h, path, token, `{"title":"one too many"}`); got != http.StatusServiceUnavailable {
		t.Errorf("a delivery to a full inbox: status = %d, want 503", got)
	}
	a.mu.Lock()
	a.closed = true
	a.mu.Unlock()
	a.forget(alertUnitKey)
	if got := postAlert(h, path, token, `{"title":"Disk full"}`); got != http.StatusServiceUnavailable {
		t.Errorf("a delivery once the receiver stopped: status = %d, want 503", got)
	}
}

// drainerFunc drains with a func.
type drainerFunc func(context.Context, types.NamespacedName) error

func (f drainerFunc) DrainAlerts(ctx context.Context, key types.NamespacedName) error {
	return f(ctx, key)
}

// takeInbox empties a's inbox of key, as the reconciler does, an alert at a
// time.
func takeInbox(a *AlertReceiver, key types.NamespacedName) {
	unlock := a.lock(key)
	defer unlock()
	for {
		q, counts := a.peek(key)
		if q == nil && counts.empty() {
			return
		}
		var seq uint64
		if q != nil {
			seq = q.seq
		}
		a.taken(key, seq, counts)
	}
}

func TestAlertReceiverStops(t *testing.T) {
	for _, tc := range []struct {
		name    string
		signal  bool
		fails   int // passes the drainer fails, -1 for all
		dropped float64
		drained bool
	}{
		{"on a signal it drains, through a Git host that fails at first", true, 3, 0, true},
		{"on a signal it drops what is left at the deadline", true, -1, 2, false},
		{"on leadership lost it drains nothing", false, 0, 2, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, _, _ := alertUnitSource(t)
			var calls atomic.Int32
			var drained atomic.Bool
			a.Drainer = drainerFunc(func(_ context.Context, key types.NamespacedName) error {
				if n := calls.Add(1); tc.fails < 0 || int(n) <= tc.fails {
					return errors.New("the Git host is down")
				}
				takeInbox(a, key)
				drained.Store(true)
				return nil
			})
			a.DrainTimeout = 2 * time.Second
			signal, stop := context.WithCancel(context.Background())
			defer stop()
			a.SignalContext = signal
			a.put(alertUnitKey, []model.Alert{{Key: "a"}, {Key: "b"}}, 0)
			before := testutil.ToFloat64(alertDeliveries.WithLabelValues(alertDropped))
			run, cancel := context.WithCancel(context.Background())
			if tc.signal {
				stop()
			}
			cancel()
			if err := a.Start(run); err != nil {
				t.Fatal(err)
			}
			if got := testutil.ToFloat64(alertDeliveries.WithLabelValues(alertDropped)) - before; got != tc.dropped {
				t.Errorf("dropped = %v, want %v", got, tc.dropped)
			}
			if q, _ := a.peek(alertUnitKey); q != nil {
				t.Errorf("the inbox still holds %+v", q)
			}
			if drained.Load() != tc.drained {
				t.Errorf("drained = %v after %d passes, want %v", drained.Load(), calls.Load(), tc.drained)
			}
			if !a.isClosed() {
				t.Error("the receiver takes deliveries after it stopped")
			}
		})
	}
}

// The inbox is shared by the handler's goroutines and the drains: under
// -race, deliveries and drains side by side lose no alert.
func TestAlertReceiverInboxRace(t *testing.T) {
	a, token, _ := alertUnitSource(t)
	h := a.Handler()
	path := model.AlertsURL("", "shop", "alerts")
	var taken atomic.Int64
	var wg sync.WaitGroup
	for i := range 20 {
		wg.Go(func() {
			if got := postAlert(h, path, token, fmt.Sprintf(`{"title":"alert %d"}`, i)); got != http.StatusAccepted {
				t.Errorf("status = %d", got)
			}
		})
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for taken.Load() < 20 {
			unlock := a.lock(alertUnitKey)
			if q, counts := a.peek(alertUnitKey); q != nil {
				a.taken(alertUnitKey, q.seq, counts)
				taken.Add(1)
			}
			unlock()
		}
	}()
	wg.Wait()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatalf("took %d of 20 alerts", taken.Load())
	}
}

// A build whose listener is off takes no deliveries: its sources say so.
func TestAlertSourceReceiverOff(t *testing.T) {
	_, _, c := alertUnitSource(t)
	r := &AlertSourceReconciler{Client: c, APIReader: c}
	if _, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: alertUnitKey}); err != nil {
		t.Fatal(err)
	}
	var src c4v1beta1.AlertSource
	if err := c.Get(context.Background(), alertUnitKey, &src); err != nil {
		t.Fatal(err)
	}
	cond := apimeta.FindStatusCondition(src.Status.Conditions, c4v1beta1.ConditionAlertSourceReady)
	if cond == nil || cond.Reason != c4v1beta1.ReasonAlertSourceReceiverOff || src.Status.URL != "" {
		t.Errorf("Ready = %+v, url %q; want ReceiverOff and no url", cond, src.Status.URL)
	}
}
