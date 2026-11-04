/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of theos, which is proprietary. No license is granted
to use, copy, modify or distribute it without the copyright holder's written
permission. See the LICENSE file.
*/

package v1alpha1

import (
	"encoding/json"
	"fmt"
	"maps"
	"reflect"
	"slices"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	utiljson "k8s.io/apimachinery/pkg/util/json"
	"sigs.k8s.io/controller-runtime/pkg/conversion"

	"github.com/JairJosafath/theos/api/v1beta1"
)

// Every kind converts to and from its v1beta1 hub through one generic
// converter (docs/plans/done/api-versions.md, decision 3): an unstructured round
// trip, since the shapes match, with a per-kind hook for what differs. A
// field the target version has no home for rides in the annotation
// FieldsAnnotation of the version it came from, as JSON, and comes back when
// the object converts back to that version, so a round trip loses nothing
// (docs/design.md, "Compatibility").

// FieldsAnnotation is the annotation, formatted with the version a field came
// from, that carries the fields the other version has no home for.
const FieldsAnnotation = "theos.factory.io/%s-fields"

// A conversionHook moves what differs between the versions in an object's
// unstructured form, after the fields that rode in FieldsAnnotation are back.
type conversionHook func(obj map[string]any) error

// toHubHooks and fromHubHooks hold a kind's hooks, by kind. None yet:
// v1beta1 changes no shape. The change that adds a kind's first hook also
// marks its hub type +kubebuilder:metadata:annotations="theos.factory.io/conversion=webhook",
// which switches that CRD alone from strategy None to the webhook (#1830,
// docs/plans/done/api-versions.md, decision 3); a test in internal/controller
// keeps the mark and the schemas in step.
var toHubHooks, fromHubHooks = map[string]conversionHook{}, map[string]conversionHook{}

// HookedKinds names the kinds with a hook, sorted: each must carry the
// webhook mark, since strategy None runs no hook (#1830).
func HookedKinds() []string {
	kinds := map[string]struct{}{}
	for k := range toHubHooks {
		kinds[k] = struct{}{}
	}
	for k := range fromHubHooks {
		kinds[k] = struct{}{}
	}
	return slices.Sorted(maps.Keys(kinds))
}

func convertToHub(src runtime.Object, dst conversion.Hub) error {
	return convertObject(src, dst, GroupVersion.Version, v1beta1.GroupVersion, toHubHooks[kindOf(src)])
}

func convertFromHub(src conversion.Hub, dst runtime.Object) error {
	return convertObject(src, dst, v1beta1.GroupVersion.Version, GroupVersion, fromHubHooks[kindOf(dst)])
}

func kindOf(obj runtime.Object) string {
	return reflect.TypeOf(obj).Elem().Name()
}

// convertObject converts src, at version srcVersion, into dst, at dstGV: the
// fields of dstGV that rode on src come back, the hook moves what differs,
// and what dst has no home for rides on dst under srcVersion's annotation.
func convertObject(src, dst runtime.Object, srcVersion string, dstGV schema.GroupVersion, hook conversionHook) error {
	obj, err := runtime.DefaultUnstructuredConverter.ToUnstructured(src)
	if err != nil {
		return fmt.Errorf("convert %T: %w", src, err)
	}
	restoreFields(obj, dstGV.Version)
	obj["apiVersion"] = dstGV.String()
	if hook != nil {
		if err := hook(obj); err != nil {
			return fmt.Errorf("convert %T: %w", src, err)
		}
	}
	reflect.ValueOf(dst).Elem().SetZero()
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(obj, dst); err != nil {
		return fmt.Errorf("convert %T to %T: %w", src, dst, err)
	}
	placed, err := runtime.DefaultUnstructuredConverter.ToUnstructured(dst)
	if err != nil {
		return fmt.Errorf("convert %T to %T: %w", src, dst, err)
	}
	left := unplaced(obj, placed)
	if len(left) == 0 {
		return nil
	}
	data, err := json.Marshal(left)
	if err != nil {
		return fmt.Errorf("convert %T to %T: %w", src, dst, err)
	}
	accessor, err := meta.Accessor(dst)
	if err != nil {
		return err
	}
	annotations := accessor.GetAnnotations()
	if annotations == nil {
		annotations = map[string]string{}
	}
	annotations[fmt.Sprintf(FieldsAnnotation, srcVersion)] = string(data)
	accessor.SetAnnotations(annotations)
	return nil
}

// restoreFields merges back into obj the spec and status fields that rode in
// version's annotation, and drops the annotation. Anyone who can edit the
// object can write the annotation, so it places nothing else, and one that
// isn't a JSON object is dropped rather than failing every read of the
// object at the other version.
func restoreFields(obj map[string]any, version string) {
	metadata, _ := obj["metadata"].(map[string]any)
	annotations, _ := metadata["annotations"].(map[string]any)
	key := fmt.Sprintf(FieldsAnnotation, version)
	data, ok := annotations[key].(string)
	if !ok {
		return
	}
	delete(annotations, key)
	if len(annotations) == 0 {
		delete(metadata, "annotations")
	}
	var fields map[string]any
	if utiljson.Unmarshal([]byte(data), &fields) != nil {
		return
	}
	for _, k := range []string{"spec", "status"} {
		if part, ok := fields[k].(map[string]any); ok {
			mergeFields(obj, map[string]any{k: part})
		}
	}
}

// mergeFields sets every field of from in into, merging maps.
func mergeFields(into, from map[string]any) {
	for k, v := range from {
		sub, isMap := v.(map[string]any)
		target, intoMap := into[k].(map[string]any)
		if isMap && intoMap {
			mergeFields(target, sub)
			continue
		}
		into[k] = v
	}
}

// unplaced returns the fields of want that got differs on or lacks: a list
// that differs is kept whole.
func unplaced(want, got map[string]any) map[string]any {
	left := map[string]any{}
	for k, v := range want {
		g, ok := got[k]
		wantMap, isMap := v.(map[string]any)
		gotMap, gotIsMap := g.(map[string]any)
		switch {
		case !ok && carriesNothing(v):
		case isMap && gotIsMap:
			if sub := unplaced(wantMap, gotMap); len(sub) > 0 {
				left[k] = sub
			}
		case !ok || !reflect.DeepEqual(v, g):
			left[k] = v
		}
	}
	return left
}

// carriesNothing reports whether v holds no field: null, or an empty list or
// map, or a map of such. The unstructured form writes `status: {}` for a
// status whose only list is empty but not nil, and drops it when nil.
func carriesNothing(v any) bool {
	switch v := v.(type) {
	case nil:
		return true
	case []any:
		return len(v) == 0
	case map[string]any:
		for _, sub := range v {
			if !carriesNothing(sub) {
				return false
			}
		}
		return true
	}
	return false
}

// ConvertTo converts this Agent to its hub.
func (src *Agent) ConvertTo(dst conversion.Hub) error { return convertToHub(src, dst) }

// ConvertFrom converts the hub to this Agent.
func (dst *Agent) ConvertFrom(src conversion.Hub) error { return convertFromHub(src, dst) }

// ConvertTo converts this Brief to its hub.
func (src *Brief) ConvertTo(dst conversion.Hub) error { return convertToHub(src, dst) }

// ConvertFrom converts the hub to this Brief.
func (dst *Brief) ConvertFrom(src conversion.Hub) error { return convertFromHub(src, dst) }

// ConvertTo converts this Bundle to its hub.
func (src *Bundle) ConvertTo(dst conversion.Hub) error { return convertToHub(src, dst) }

// ConvertFrom converts the hub to this Bundle.
func (dst *Bundle) ConvertFrom(src conversion.Hub) error { return convertFromHub(src, dst) }

// ConvertTo converts this Compute to its hub.
func (src *Compute) ConvertTo(dst conversion.Hub) error { return convertToHub(src, dst) }

// ConvertFrom converts the hub to this Compute.
func (dst *Compute) ConvertFrom(src conversion.Hub) error { return convertFromHub(src, dst) }

// ConvertTo converts this Connector to its hub.
func (src *Connector) ConvertTo(dst conversion.Hub) error { return convertToHub(src, dst) }

// ConvertFrom converts the hub to this Connector.
func (dst *Connector) ConvertFrom(src conversion.Hub) error { return convertFromHub(src, dst) }

// ConvertTo converts this Container to its hub.
func (src *Container) ConvertTo(dst conversion.Hub) error { return convertToHub(src, dst) }

// ConvertFrom converts the hub to this Container.
func (dst *Container) ConvertFrom(src conversion.Hub) error { return convertFromHub(src, dst) }

// ConvertTo converts this Crew to its hub.
func (src *Crew) ConvertTo(dst conversion.Hub) error { return convertToHub(src, dst) }

// ConvertFrom converts the hub to this Crew.
func (dst *Crew) ConvertFrom(src conversion.Hub) error { return convertFromHub(src, dst) }

// ConvertTo converts this DataStore to its hub.
func (src *DataStore) ConvertTo(dst conversion.Hub) error { return convertToHub(src, dst) }

// ConvertFrom converts the hub to this DataStore.
func (dst *DataStore) ConvertFrom(src conversion.Hub) error { return convertFromHub(src, dst) }

// ConvertTo converts this DeployTarget to its hub.
func (src *DeployTarget) ConvertTo(dst conversion.Hub) error { return convertToHub(src, dst) }

// ConvertFrom converts the hub to this DeployTarget.
func (dst *DeployTarget) ConvertFrom(src conversion.Hub) error { return convertFromHub(src, dst) }

// ConvertTo converts this Deployment to its hub.
func (src *Deployment) ConvertTo(dst conversion.Hub) error { return convertToHub(src, dst) }

// ConvertFrom converts the hub to this Deployment.
func (dst *Deployment) ConvertFrom(src conversion.Hub) error { return convertFromHub(src, dst) }

// ConvertTo converts this Domain to its hub.
func (src *Domain) ConvertTo(dst conversion.Hub) error { return convertToHub(src, dst) }

// ConvertFrom converts the hub to this Domain.
func (dst *Domain) ConvertFrom(src conversion.Hub) error { return convertFromHub(src, dst) }

// ConvertTo converts this Framework to its hub.
func (src *Framework) ConvertTo(dst conversion.Hub) error { return convertToHub(src, dst) }

// ConvertFrom converts the hub to this Framework.
func (dst *Framework) ConvertFrom(src conversion.Hub) error { return convertFromHub(src, dst) }

// ConvertTo converts this GitProvider to its hub.
func (src *GitProvider) ConvertTo(dst conversion.Hub) error { return convertToHub(src, dst) }

// ConvertFrom converts the hub to this GitProvider.
func (dst *GitProvider) ConvertFrom(src conversion.Hub) error { return convertFromHub(src, dst) }

// ConvertTo converts this Person to its hub.
func (src *Person) ConvertTo(dst conversion.Hub) error { return convertToHub(src, dst) }

// ConvertFrom converts the hub to this Person.
func (dst *Person) ConvertFrom(src conversion.Hub) error { return convertFromHub(src, dst) }

// ConvertTo converts this Project to its hub.
func (src *Project) ConvertTo(dst conversion.Hub) error { return convertToHub(src, dst) }

// ConvertFrom converts the hub to this Project.
func (dst *Project) ConvertFrom(src conversion.Hub) error { return convertFromHub(src, dst) }

// ConvertTo converts this Relationship to its hub.
func (src *Relationship) ConvertTo(dst conversion.Hub) error { return convertToHub(src, dst) }

// ConvertFrom converts the hub to this Relationship.
func (dst *Relationship) ConvertFrom(src conversion.Hub) error { return convertFromHub(src, dst) }

// ConvertTo converts this Session to its hub.
func (src *Session) ConvertTo(dst conversion.Hub) error { return convertToHub(src, dst) }

// ConvertFrom converts the hub to this Session.
func (dst *Session) ConvertFrom(src conversion.Hub) error { return convertFromHub(src, dst) }

// ConvertTo converts this SoftwareSystem to its hub.
func (src *SoftwareSystem) ConvertTo(dst conversion.Hub) error { return convertToHub(src, dst) }

// ConvertFrom converts the hub to this SoftwareSystem.
func (dst *SoftwareSystem) ConvertFrom(src conversion.Hub) error { return convertFromHub(src, dst) }

// ConvertTo converts this Standard to its hub.
func (src *Standard) ConvertTo(dst conversion.Hub) error { return convertToHub(src, dst) }

// ConvertFrom converts the hub to this Standard.
func (dst *Standard) ConvertFrom(src conversion.Hub) error { return convertFromHub(src, dst) }

// ConvertTo converts this Task to its hub.
func (src *Task) ConvertTo(dst conversion.Hub) error { return convertToHub(src, dst) }

// ConvertFrom converts the hub to this Task.
func (dst *Task) ConvertFrom(src conversion.Hub) error { return convertFromHub(src, dst) }
