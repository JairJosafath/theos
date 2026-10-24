/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of C4 Factory, which is proprietary. No license is granted
to use, copy, modify or distribute it without the copyright holder's written
permission. See the LICENSE file.
*/

package v1alpha1

import (
	"fmt"
	"math/rand"
	"reflect"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/api/apitesting/fuzzer"
	"k8s.io/apimachinery/pkg/api/equality"
	metafuzzer "k8s.io/apimachinery/pkg/apis/meta/fuzzer"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"sigs.k8s.io/controller-runtime/pkg/conversion"

	"github.com/JairJosafath/c4factory/api/v1beta1"
)

// roundTrips is how many fuzzed objects of each kind go each way.
const roundTrips = 200

// TestConversionRoundTrip converts fuzzed objects of every kind to the hub and
// back, and the other way, and wants them back unchanged (#1615).
func TestConversionRoundTrip(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := v1beta1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	filler := fuzzer.FuzzerFor(metafuzzer.Funcs, rand.NewSource(1615), serializer.NewCodecFactory(scheme))

	kinds := 0
	for kind, typ := range scheme.KnownTypes(GroupVersion) {
		if strings.HasSuffix(kind, "List") || typ.PkgPath() != reflect.TypeFor[Project]().PkgPath() {
			continue
		}
		kinds++
		t.Run(kind, func(t *testing.T) {
			spoke, hub := newObject[conversion.Convertible](t, scheme, GroupVersion.WithKind(kind)),
				newObject[conversion.Hub](t, scheme, v1beta1.GroupVersion.WithKind(kind))
			for i := range roundTrips {
				// From the spoke, through the hub, back.
				src := newObject[conversion.Convertible](t, scheme, GroupVersion.WithKind(kind))
				fill(filler, src, GroupVersion.WithKind(kind))
				if err := src.ConvertTo(hub); err != nil {
					t.Fatalf("object %d: to the hub: %v", i, err)
				}
				if err := spoke.ConvertFrom(hub); err != nil {
					t.Fatalf("object %d: from the hub: %v", i, err)
				}
				if !equality.Semantic.DeepEqual(src, spoke) {
					t.Fatalf("object %d: v1alpha1 changed on the way through v1beta1:\nwant %+v\ngot  %+v", i, src, spoke)
				}

				// From the hub, through the spoke, back.
				srcHub := newObject[conversion.Hub](t, scheme, v1beta1.GroupVersion.WithKind(kind))
				fill(filler, srcHub, v1beta1.GroupVersion.WithKind(kind))
				if err := spoke.ConvertFrom(srcHub); err != nil {
					t.Fatalf("object %d: from the hub: %v", i, err)
				}
				if err := spoke.ConvertTo(hub); err != nil {
					t.Fatalf("object %d: to the hub: %v", i, err)
				}
				if !equality.Semantic.DeepEqual(srcHub, hub) {
					t.Fatalf("object %d: v1beta1 changed on the way through v1alpha1:\nwant %+v\ngot  %+v", i, srcHub, hub)
				}
			}
		})
	}
	if kinds != 20 {
		t.Errorf("round-tripped %d kinds, want all 20", kinds)
	}
}

func newObject[T any](t *testing.T, scheme *runtime.Scheme, gvk schema.GroupVersionKind) T {
	t.Helper()
	obj, err := scheme.New(gvk)
	if err != nil {
		t.Fatal(err)
	}
	typed, ok := obj.(T)
	if !ok {
		t.Fatalf("%s is a %T, not a %s", gvk, obj, reflect.TypeFor[T]())
	}
	return typed
}

// fill fuzzes obj, which converts with its apiVersion and kind set.
func fill(filler interface{ Fill(any) }, obj runtime.Object, gvk schema.GroupVersionKind) {
	filler.Fill(obj)
	obj.GetObjectKind().SetGroupVersionKind(gvk)
}

// The fields annotation carries what the other version has no home for, and
// gives it back on the way back (#1615).
type wideSpec struct {
	Name  string `json:"name,omitempty"`
	Extra string `json:"extra,omitempty"`
}

type wide struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              wideSpec `json:"spec,omitempty"`
}

type narrowSpec struct {
	Name string `json:"name,omitempty"`
}

type narrow struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              narrowSpec `json:"spec,omitempty"`
}

func (in *wide) DeepCopyObject() runtime.Object   { out := *in; return &out }
func (in *narrow) DeepCopyObject() runtime.Object { out := *in; return &out }

func TestConversionFieldsAnnotation(t *testing.T) {
	old := schema.GroupVersion{Group: GroupVersion.Group, Version: "vold"}
	next := schema.GroupVersion{Group: GroupVersion.Group, Version: "vnext"}
	src := &wide{
		TypeMeta:   metav1.TypeMeta{APIVersion: old.String(), Kind: "Thing"},
		ObjectMeta: metav1.ObjectMeta{Name: "a", Annotations: map[string]string{"keep": "me"}},
		Spec:       wideSpec{Name: "n", Extra: "only in vold"},
	}

	dst := &narrow{}
	if err := convertObject(src, dst, old.Version, next, nil); err != nil {
		t.Fatal(err)
	}
	key := fmt.Sprintf(FieldsAnnotation, old.Version)
	if got, want := dst.Annotations[key], `{"spec":{"extra":"only in vold"}}`; got != want {
		t.Errorf("annotation %s = %q, want %q", key, got, want)
	}
	if dst.Spec.Name != "n" || dst.APIVersion != next.String() {
		t.Errorf("converted to %+v, want spec.name n at %s", dst, next)
	}

	back := &wide{}
	if err := convertObject(dst, back, next.Version, old, nil); err != nil {
		t.Fatal(err)
	}
	if !equality.Semantic.DeepEqual(src, back) {
		t.Errorf("round trip through the narrow version:\nwant %+v\ngot  %+v", src, back)
	}
}

// An annotation a user wrote by hand places nothing outside spec and status,
// and one that isn't JSON is dropped, so reads at the other version still work.
func TestConversionFieldsAnnotationByHand(t *testing.T) {
	old := schema.GroupVersion{Group: GroupVersion.Group, Version: "vold"}
	next := schema.GroupVersion{Group: GroupVersion.Group, Version: "vnext"}
	key := fmt.Sprintf(FieldsAnnotation, old.Version)
	for _, tc := range []struct{ name, value string }{
		{"not JSON", "x"},
		{"metadata", `{"metadata":{"name":"y"},"kind":"Other"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := &narrow{
				TypeMeta:   metav1.TypeMeta{APIVersion: next.String(), Kind: "Thing"},
				ObjectMeta: metav1.ObjectMeta{Name: "a", Annotations: map[string]string{key: tc.value}},
				Spec:       narrowSpec{Name: "n"},
			}
			dst := &wide{}
			if err := convertObject(src, dst, next.Version, old, nil); err != nil {
				t.Fatal(err)
			}
			if dst.Name != "a" || dst.Kind != "Thing" || dst.Spec.Name != "n" || len(dst.Annotations) != 0 {
				t.Errorf("converted to %+v, want Thing a, spec.name n and no annotation", dst)
			}
		})
	}
}
