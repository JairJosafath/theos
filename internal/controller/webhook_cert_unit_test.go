/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of C4 Factory, which is proprietary. No license is granted
to use, copy, modify or distribute it without the copyright holder's written
permission. See the LICENSE file.
*/

package controller

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	c4v1alpha1 "github.com/JairJosafath/c4factory/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/yaml"
)

var webhookCertStart = time.Date(2026, 10, 22, 0, 0, 0, 0, time.UTC)

func webhookCertScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(s); err != nil {
		t.Fatal(err)
	}
	if err := apiextensionsv1.AddToScheme(s); err != nil {
		t.Fatal(err)
	}
	return s
}

func newWebhookCert(c client.Client, now *time.Time) *WebhookCert {
	return &WebhookCert{Client: c, Namespace: "c4-factory-system", Secret: "c4-webhook-cert",
		Service: "c4-webhook-service", CRDs: []string{}, Now: func() time.Time { return *now }}
}

func readWebhookSecret(t *testing.T, c client.Client) map[string][]byte {
	t.Helper()
	var s corev1.Secret
	if err := c.Get(context.Background(), client.ObjectKey{Namespace: "c4-factory-system",
		Name: "c4-webhook-cert"}, &s); err != nil {
		t.Fatal(err)
	}
	return s.Data
}

// served returns the leaf certificate the webhook server would present.
func served(t *testing.T, w *WebhookCert) *x509.Certificate {
	t.Helper()
	pair, err := w.GetCertificate(&tls.ClientHelloInfo{})
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	return cert
}

// #1614: the operator issues its webhook certificate into the Secret when
// there is none, reuses it while it has over 30 days left, and re-issues it
// under the same CA when it doesn't.
func TestWebhookCertIssuesReusesAndReissues(t *testing.T) {
	ctx := context.Background()
	now := webhookCertStart
	c := fake.NewClientBuilder().WithScheme(webhookCertScheme(t)).Build()
	w := newWebhookCert(c, &now)

	// Issue.
	if _, err := w.GetCertificate(nil); err == nil {
		t.Fatal("a certificate is served before Ensure")
	}
	if err := w.Ensure(ctx); err != nil {
		t.Fatal(err)
	}
	issued := readWebhookSecret(t, c)
	cert := served(t, w)
	if err := cert.VerifyHostname("c4-webhook-service.c4-factory-system.svc"); err != nil {
		t.Errorf("issued for the wrong host: %v", err)
	}
	if got := cert.NotAfter.Sub(now); got < 364*24*time.Hour || got > 366*24*time.Hour {
		t.Errorf("valid for %v, want a year", got)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(issued[webhookCAKey]) {
		t.Fatal("no CA in the Secret")
	}
	if _, err := cert.Verify(x509.VerifyOptions{Roots: roots, DNSName: w.Host(), CurrentTime: now}); err != nil {
		t.Errorf("the CA doesn't verify the serving certificate: %v", err)
	}

	// Reuse, a second replica or a restart 300 days on.
	now = webhookCertStart.Add(300 * 24 * time.Hour)
	other := newWebhookCert(c, &now)
	if err := other.Ensure(ctx); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(readWebhookSecret(t, c)[corev1.TLSCertKey], issued[corev1.TLSCertKey]) {
		t.Error("the certificate was re-issued with 65 days left")
	}
	if !served(t, other).Equal(cert) {
		t.Error("the second replica serves another certificate")
	}

	// Re-issue with 20 days left, under the same CA.
	now = webhookCertStart.Add(345 * 24 * time.Hour)
	if err := w.Ensure(ctx); err != nil {
		t.Fatal(err)
	}
	reissued := readWebhookSecret(t, c)
	if bytes.Equal(reissued[corev1.TLSCertKey], issued[corev1.TLSCertKey]) {
		t.Fatal("the certificate was not re-issued with 20 days left")
	}
	if !bytes.Equal(reissued[webhookCAKey], issued[webhookCAKey]) {
		t.Error("re-issuing the certificate changed the CA")
	}
	renewed := served(t, w)
	if renewed.Equal(cert) || renewed.NotAfter.Sub(now) < 364*24*time.Hour {
		t.Errorf("serves a certificate valid until %v", renewed.NotAfter)
	}
	if _, err := renewed.Verify(x509.VerifyOptions{Roots: roots, DNSName: w.Host(), CurrentTime: now}); err != nil {
		t.Errorf("the old CA doesn't verify the re-issued certificate: %v", err)
	}

	// The other replica's recheck serves what this one wrote.
	if err := other.Ensure(ctx); err != nil {
		t.Fatal(err)
	}
	if !served(t, other).Equal(renewed) {
		t.Error("the second replica still serves the old certificate after its recheck")
	}
}

// #1614: a certificate for another Service is re-issued, and a replica that
// loses the race to create the Secret serves the winner's.
func TestWebhookCertReissuesForAnotherHostAndTakesTheFirstReplicas(t *testing.T) {
	ctx := context.Background()
	now := webhookCertStart
	c := fake.NewClientBuilder().WithScheme(webhookCertScheme(t)).Build()
	w := newWebhookCert(c, &now)
	if err := w.Ensure(ctx); err != nil {
		t.Fatal(err)
	}
	w.Service = "renamed"
	if err := w.Ensure(ctx); err != nil {
		t.Fatal(err)
	}
	if err := served(t, w).VerifyHostname("renamed.c4-factory-system.svc"); err != nil {
		t.Errorf("not re-issued for the renamed Service: %v", err)
	}

	first := readWebhookSecret(t, c)
	raced := false
	racing := fake.NewClientBuilder().WithScheme(webhookCertScheme(t)).
		WithInterceptorFuncs(interceptor.Funcs{Create: func(ctx context.Context, cl client.WithWatch,
			obj client.Object, opts ...client.CreateOption) error {
			if s, ok := obj.(*corev1.Secret); ok && !raced {
				raced = true
				winner := s.DeepCopy()
				winner.Data = first
				if err := cl.Create(ctx, winner); err != nil {
					return err
				}
			}
			return cl.Create(ctx, obj, opts...)
		}}).Build()
	loser := newWebhookCert(racing, &now)
	loser.Service = "renamed"
	if err := loser.Ensure(ctx); err != nil {
		t.Fatal(err)
	}
	if !raced || !bytes.Equal(readWebhookSecret(t, racing)[corev1.TLSCertKey], first[corev1.TLSCertKey]) {
		t.Error("the replica that lost the create replaced the winner's certificate")
	}
}

// #1614: the CA goes into every CRD that converts by webhook; one at
// strategy None is left alone, since the API server refuses a webhook stanza
// beside it.
func TestWebhookCertPatchesTheCAIntoWebhookCRDs(t *testing.T) {
	ctx := context.Background()
	now := webhookCertStart
	webhookCRD := &apiextensionsv1.CustomResourceDefinition{}
	webhookCRD.Name = "crews.c4.factory.io"
	webhookCRD.Spec.Conversion = &apiextensionsv1.CustomResourceConversion{
		Strategy: apiextensionsv1.WebhookConverter,
		Webhook: &apiextensionsv1.WebhookConversion{ConversionReviewVersions: []string{"v1"},
			ClientConfig: &apiextensionsv1.WebhookClientConfig{Service: &apiextensionsv1.ServiceReference{
				Namespace: "c4-factory-system", Name: "c4-webhook-service"}}},
	}
	noneCRD := &apiextensionsv1.CustomResourceDefinition{}
	noneCRD.Name = "tasks.c4.factory.io"
	noneCRD.Spec.Conversion = &apiextensionsv1.CustomResourceConversion{Strategy: apiextensionsv1.NoneConverter}
	c := fake.NewClientBuilder().WithScheme(webhookCertScheme(t)).WithObjects(webhookCRD, noneCRD).Build()
	w := newWebhookCert(c, &now)
	w.CRDs = []string{webhookCRD.Name, noneCRD.Name}
	if err := w.Ensure(ctx); err != nil {
		t.Fatal(err)
	}
	ca := readWebhookSecret(t, c)[webhookCAKey]
	var got apiextensionsv1.CustomResourceDefinition
	if err := c.Get(ctx, client.ObjectKey{Name: webhookCRD.Name}, &got); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got.Spec.Conversion.Webhook.ClientConfig.CABundle, ca) {
		t.Error("the webhook CRD's caBundle is not the CA")
	}
	if err := c.Get(ctx, client.ObjectKey{Name: noneCRD.Name}, &got); err != nil {
		t.Fatal(err)
	}
	if got.Spec.Conversion.Webhook != nil {
		t.Error("a webhook stanza was written beside strategy None")
	}
}

// #1830: a CRD converts by the operator's webhook only while its kind's
// versions differ in shape; the mark is the annotation
// c4.factory.io/conversion: webhook on the base, from a +kubebuilder:metadata
// marker on the kind's hub type. A kind whose versions' schemas match carries
// no mark, and one whose schemas differ carries it: the change that first
// makes a shape differ switches the kind to the webhook.
func TestConversionMarkFollowsTheShapes(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("..", "..", "config", "crd", "bases", "c4.factory.io_*.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		var crd apiextensionsv1.CustomResourceDefinition
		if err := yaml.Unmarshal(raw, &crd); err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		if len(crd.Spec.Versions) < 2 {
			t.Errorf("%s: serves %d version(s), want v1alpha1 and v1beta1", crd.Name, len(crd.Spec.Versions))
			continue
		}
		differs := false
		for _, v := range crd.Spec.Versions[1:] {
			if !equality.Semantic.DeepEqual(shapeOf(t, v.Schema), shapeOf(t, crd.Spec.Versions[0].Schema)) {
				differs = true
			}
		}
		hooked := slices.Contains(c4v1alpha1.HookedKinds(), crd.Spec.Names.Kind)
		marked := crd.Annotations["c4.factory.io/conversion"] == "webhook"
		switch {
		case differs && !marked:
			t.Errorf("%s: its versions differ in shape but its hub type lacks the marker "+
				"+kubebuilder:metadata:annotations=\"c4.factory.io/conversion=webhook\" (#1830)", crd.Name)
		case hooked && !marked:
			t.Errorf("%s: has a conversion hook, which strategy None never runs, but its hub type lacks the marker "+
				"+kubebuilder:metadata:annotations=\"c4.factory.io/conversion=webhook\" (#1830)", crd.Name)
		case marked && !differs && !hooked:
			t.Errorf("%s: marked c4.factory.io/conversion: webhook, but its versions share a shape and no hook: "+
				"drop the marker, strategy None serves them (#1830)", crd.Name)
		}
	}
}

// shapeOf is a version's schema with every description dropped: a doc
// comment that differs between the versions is no difference in shape. A
// field named description (Crew's, Bundle's) is one, and stays (#1830).
func shapeOf(t *testing.T, s *apiextensionsv1.CustomResourceValidation) map[string]any {
	t.Helper()
	raw, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	// fields says v is a properties map, whose keys name fields, not a schema.
	var strip func(v any, fields bool)
	strip = func(v any, fields bool) {
		switch v := v.(type) {
		case map[string]any:
			if !fields {
				delete(v, "description")
			}
			for k, c := range v {
				strip(c, !fields && (k == "properties" || k == "patternProperties"))
			}
		case []any:
			for _, c := range v {
				strip(c, false)
			}
		}
	}
	strip(m, false)
	return m
}

// #1830: a field named description is part of the shape; only the doc
// comments are not.
func TestShapeOfKeepsAFieldNamedDescription(t *testing.T) {
	schema := func(fieldType, doc string) *apiextensionsv1.CustomResourceValidation {
		return &apiextensionsv1.CustomResourceValidation{OpenAPIV3Schema: &apiextensionsv1.JSONSchemaProps{
			Type: "object", Description: doc,
			Properties: map[string]apiextensionsv1.JSONSchemaProps{
				"description": {Type: fieldType, Description: doc},
			},
		}}
	}
	if !equality.Semantic.DeepEqual(shapeOf(t, schema("string", "one")), shapeOf(t, schema("string", "two"))) {
		t.Error("schemas that differ only in their doc comments differ in shape")
	}
	if equality.Semantic.DeepEqual(shapeOf(t, schema("string", "one")), shapeOf(t, schema("integer", "one"))) {
		t.Error("a field named description changed type and the shapes still compare equal")
	}
	fields, _ := shapeOf(t, schema("string", ""))["openAPIV3Schema"].(map[string]any)["properties"].(map[string]any)
	if _, ok := fields["description"]; !ok {
		t.Error("the field named description was stripped as a doc comment")
	}
}

// #1614: WebhookCRDs and the RBAC marker that lets the operator patch them
// name every CRD in config/crd/bases.
func TestWebhookCRDsAreEveryCRD(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("..", "..", "config", "crd", "bases", "c4.factory.io_*.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	want := make([]string, 0, len(files))
	for _, f := range files {
		plural := strings.TrimSuffix(strings.TrimPrefix(filepath.Base(f), "c4.factory.io_"), ".yaml")
		want = append(want, plural+".c4.factory.io")
	}
	if !slices.Equal(want, slices.Sorted(slices.Values(WebhookCRDs))) {
		t.Errorf("WebhookCRDs = %v, want %v", WebhookCRDs, want)
	}
	src, err := os.ReadFile("webhook_cert.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(src), "resourceNames="+strings.Join(want, ";")+"\n") {
		t.Error("the customresourcedefinitions RBAC marker doesn't name exactly WebhookCRDs")
	}
}
