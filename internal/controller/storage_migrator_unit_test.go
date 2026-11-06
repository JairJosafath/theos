/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of theos, which is proprietary. No license is granted
to use, copy, modify or distribute it without the copyright holder's written
permission. See the LICENSE file.
*/

package controller

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"testing"
	"time"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

// #1616, #1884: a CRD that converts by the webhook has its objects rewritten
// only once the webhook serves, since each rewrite converts through it; one at
// strategy None converts in the API server and never waits. The envtest spec
// rewrites a CRD at strategy None against a real API server; with every kind
// at v1beta1 alone (#1963), envtest has no conversion webhook to wait on.
func TestStorageMigratorWaitsForTheWebhookOnlyWhereItConverts(t *testing.T) {
	for strategy, wantAsked := range map[apiextensionsv1.ConversionStrategyType]int{
		apiextensionsv1.WebhookConverter: 3, apiextensionsv1.NoneConverter: 0,
	} {
		t.Run(string(strategy), func(t *testing.T) {
			ctx := context.Background()
			crd := &apiextensionsv1.CustomResourceDefinition{
				ObjectMeta: metav1.ObjectMeta{Name: "people.theos.factory.io"},
				Spec: apiextensionsv1.CustomResourceDefinitionSpec{
					Group: "theos.factory.io",
					Names: apiextensionsv1.CustomResourceDefinitionNames{Kind: "Person", ListKind: "PersonList",
						Plural: "people"},
					Scope: apiextensionsv1.NamespaceScoped,
					Versions: []apiextensionsv1.CustomResourceDefinitionVersion{
						{Name: "v1beta1", Served: true}, {Name: "v1", Served: true, Storage: true}},
					Conversion: &apiextensionsv1.CustomResourceConversion{Strategy: strategy},
				},
				Status: apiextensionsv1.CustomResourceDefinitionStatus{StoredVersions: []string{"v1beta1", "v1"}},
			}
			person := &unstructured.Unstructured{}
			person.SetAPIVersion("theos.factory.io/v1")
			person.SetKind("Person")
			person.SetNamespace("shop")
			person.SetName("ada")

			asked, rewritten := 0, 0
			c := fake.NewClientBuilder().WithScheme(webhookCertScheme(t)).WithObjects(crd, person).
				WithStatusSubresource(crd).
				WithInterceptorFuncs(interceptor.Funcs{Patch: func(ctx context.Context, cl client.WithWatch,
					obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
					if strategy == apiextensionsv1.WebhookConverter && asked < wantAsked {
						t.Errorf("%s rewritten before the webhook served", obj.GetName())
					}
					rewritten++
					return cl.Patch(ctx, obj, patch, opts...)
				}}).Build()
			m := &StorageMigrator{Client: c, CRDs: []string{crd.Name}, ReadyPoll: time.Millisecond,
				Ready: func(*http.Request) error {
					if asked++; asked < 3 {
						return errors.New("not yet")
					}
					return nil
				}}
			if err := m.Migrate(ctx); err != nil {
				t.Fatal(err)
			}
			if asked != wantAsked {
				t.Errorf("asked the webhook %d time(s), want %d", asked, wantAsked)
			}
			if rewritten != 1 {
				t.Errorf("rewrote %d object(s), want 1", rewritten)
			}
			var got apiextensionsv1.CustomResourceDefinition
			if err := c.Get(ctx, client.ObjectKey{Name: crd.Name}, &got); err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(got.Status.StoredVersions, []string{"v1"}) {
				t.Errorf("storedVersions = %v, want [v1]", got.Status.StoredVersions)
			}
		})
	}
}
