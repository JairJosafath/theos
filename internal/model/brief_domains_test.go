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

import (
	"reflect"
	"slices"
	"strings"
	"testing"
)

// #1760: a plan's SoftwareSystem names its Domains in domainRefs, from the
// catalog's installed Domains; happyModel's shop system exists, plants is new.
func TestCheckPlanDomains(t *testing.T) {
	const plants = "---\napiVersion: theos.factory.io/v1beta1\nkind: SoftwareSystem\nmetadata:\n  name: plants\nspec:\n" +
		"  description: Sells plants\n"
	for _, tc := range []struct {
		name string
		// created is plants's spec after its description, "" for no plants;
		// shop is a line added to shop's spec.
		created, shop string
		// before are shop's Domains in the namespace.
		before  []string
		refused []string // what each refusal says, in order
	}{
		{name: "an installed Domain", created: "  domainRefs: [webshop]\n"},
		{name: "two installed Domains", created: "  domainRefs: [webshop, catalog]\n"},
		{name: "none"},
		{name: "not installed", created: "  domainRefs: [webshop, blog]\n",
			refused: []string{"SoftwareSystem/plants: domainRefs names \"blog\", which is not an installed Domain of " +
				"model/catalog.yaml; name only its `domains`, or none"}},
		{name: "External", created: "  location: External\n  domainRefs: [webshop]\n",
			refused: []string{"SoftwareSystem/plants: an External system names no domainRefs: the factory builds nothing there"}},
		{name: "twice", created: "  domainRefs: [webshop, webshop]\n",
			refused: []string{"SoftwareSystem/plants: domainRefs names \"webshop\" twice; name each Domain once"}},
		// The CRD's MaxItems=5 is in specLimits; TestSpecLimitsMatchTheCRDs holds them together.
		{name: "an existing system keeps its Domains", shop: "  domainRefs: [webshop]\n", before: []string{"webshop"}},
		{name: "an existing system keeps them in any order", shop: "  domainRefs: [webshop, catalog]\n",
			before: []string{"catalog", "webshop"}},
		{name: "added to an existing system", shop: "  domainRefs: [webshop]\n",
			refused: []string{"SoftwareSystem/shop: domainRefs can't change (from [] to [webshop]): a system's Domains " +
				"are picked when it is made; a later Domain is a prompt for a new system, or a person's edit"}},
		{name: "dropped from an existing system", before: []string{"webshop"},
			refused: []string{"SoftwareSystem/shop: domainRefs can't change (from [webshop] to []): a system's Domains " +
				"are picked when it is made; a later Domain is a prompt for a new system, or a person's edit"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			files := happyFiles()
			files[PlanModelFile] = strings.Replace(files[PlanModelFile], "  needs: [orders]\n", "  needs: [orders]\n"+tc.shop, 1)
			if tc.created != "" {
				files[PlanModelFile] += plants + tc.created
			}
			plan, err := ParsePlan(files)
			if err != nil {
				t.Fatal(err)
			}
			current := happyCurrent()
			current.Objects[1].Domains = tc.before
			current.Catalog = append(current.Catalog, CatalogEntry{Side: SideDomain, Name: "webshop"},
				CatalogEntry{Side: SideDomain, Name: "catalog"})
			var got []string
			for _, r := range CheckPlan(plan, current) {
				if strings.Contains(r.String(), "domainRefs") {
					got = append(got, r.String())
				}
			}
			if strings.Join(got, "\n") != strings.Join(tc.refused, "\n") {
				t.Errorf("refusals\n got %q\nwant %q", got, tc.refused)
			}
			// An unchanged system stays unchanged: the copy of the model is
			// not a change, the dedicated check catches one.
			if _, changed := PlanDiff(plan, current); tc.shop == "" && tc.before == nil && len(changed) > 0 {
				t.Errorf("changed %v, want nothing changed", changed)
			}
		})
	}
}

func TestParsePlanDomains(t *testing.T) {
	files := happyFiles()
	files[PlanModelFile] = strings.Replace(files[PlanModelFile], "  needs: [orders]\n",
		"  needs: [orders]\n  domainRefs: [webshop, catalog]\n", 1)
	plan, err := ParsePlan(files)
	if err != nil {
		t.Fatal(err)
	}
	o, _ := findObject(plan.Objects, Ref{KindSoftwareSystem, "shop"})
	if got := strings.Join(o.Domains, ","); got != "webshop,catalog" {
		t.Errorf("shop's Domains = %q, want webshop,catalog", got)
	}
}

// #1760: a Domain gone between the plan check and apply is missing from the
// catalog; an existing system's Domains aren't asked for.
func TestMissingCatalogDomains(t *testing.T) {
	files := happyFiles()
	files[PlanModelFile] = strings.Replace(files[PlanModelFile], "  needs: [orders]\n",
		"  needs: [orders]\n  domainRefs: [webshop, catalog]\n", 1)
	plan, err := ParsePlan(files)
	if err != nil {
		t.Fatal(err)
	}
	current := happyCurrent()
	current.Catalog = append(current.Catalog, CatalogEntry{Side: SideDomain, Name: "webshop"})
	if got := MissingCatalog(plan, current); got != nil {
		t.Errorf("MissingCatalog with shop existing = %q, want none", got)
	}
	current.Objects = slices.DeleteFunc(current.Objects, func(o PlanObject) bool { return o.Kind == KindSoftwareSystem })
	if got, want := MissingCatalog(plan, current), []string{"Domain catalog"}; !reflect.DeepEqual(got, want) {
		t.Errorf("MissingCatalog with shop new = %q, want %q", got, want)
	}
}
