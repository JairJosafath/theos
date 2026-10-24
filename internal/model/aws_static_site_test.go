package model

import (
	"slices"
	"strings"
	"testing"
)

// staticSitePlan is a planner's plan for a static site on AWS:
// aws-static-site-ts with aws-standard, and no Compute or Deployment
// (plans/aws-production.md, step 9).
const staticSitePlan = `apiVersion: c4.factory.io/v1beta1
kind: SoftwareSystem
metadata:
  name: bakery
spec:
  description: Tells people about the bakery
---
apiVersion: c4.factory.io/v1beta1
kind: Container
metadata:
  name: bakery-site
spec:
  softwareSystemRef: {name: bakery}
  description: The bakery's website, on S3 behind CloudFront
  type: static-site
  gitProviderRef: {name: gitea}
  frameworkRef: {name: aws-static-site-ts}
  standardRef: {name: aws-standard}
`

func TestAWSStandardSupportsTheStaticSiteFramework(t *testing.T) {
	var site, standard *CatalogEntry
	for _, e := range chartCatalog(t) {
		switch e.Side + " " + e.Name {
		case "Framework aws-static-site-ts":
			site = &e
		case "Standard aws-standard":
			standard = &e
		}
	}
	if site == nil || standard == nil {
		t.Fatalf("the chart lacks aws-static-site-ts (%v) or aws-standard (%v)", site != nil, standard != nil)
	}
	if !slices.Equal(site.SupportedTypes, []string{TypeStaticSite}) || !site.DeploysItself {
		t.Errorf("aws-static-site-ts supports %v, deploys itself %v; want only %q, deploying itself",
			site.SupportedTypes, site.DeploysItself, TypeStaticSite)
	}
	if !slices.Contains(standard.SupportedFrameworks, "aws-static-site-ts") ||
		!slices.Contains(standard.SupportedTypes, TypeStaticSite) {
		t.Errorf("aws-standard supports %v and Frameworks %v, want static-site and aws-static-site-ts among them",
			standard.SupportedTypes, standard.SupportedFrameworks)
	}
}

func TestPlannerGuideStatesTheStaticSiteOnAWSCue(t *testing.T) {
	guide := strings.Join(strings.Fields(plannerGuideText), " ")
	for _, rule := range []string{
		"An idea that asks for a website or web page on AWS, S3 or CloudFront is a `static-site`",
		"such as `aws-static-site-ts`",
	} {
		if !strings.Contains(guide, rule) {
			t.Errorf("PLANNER.md doesn't say %q", rule)
		}
	}
}

func TestStaticSitePlansOnAWSStaticSiteTS(t *testing.T) {
	current := CurrentModel{Namespace: "bakery", Catalog: chartCatalog(t), GitProvider: "gitea"}
	files := map[string]string{
		PlanChoicesFile: "- bakery-site is a website on CloudFront, so it is a static-site on aws-static-site-ts\n",
		PlanSummaryFile: "Planned the bakery's website on AWS.\n",
	}
	check := func(model string) []Refusal {
		files[PlanModelFile] = model
		plan, err := ParsePlan(files)
		if err != nil {
			t.Fatal(err)
		}
		return CheckPlan(plan, current)
	}
	if got := check(staticSitePlan); got != nil {
		t.Fatalf("the static site plan is refused: %v", got)
	}
	// aws-static-site-ts builds static sites only; it deploys itself, so a
	// Deployment is refused; and aws-standard fits no other static-site
	// Framework.
	for name, model := range map[string]string{
		"a service": strings.Replace(staticSitePlan, "type: static-site", "type: service", 1),
		"a Deployment": strings.Replace(staticSitePlan, "  standardRef: {name: aws-standard}\n",
			"  standardRef: {name: aws-standard}\n  deploymentRef: {name: k8s-manifests}\n", 1),
		"react-vite": strings.Replace(staticSitePlan, "frameworkRef: {name: aws-static-site-ts}",
			"frameworkRef: {name: react-vite}", 1),
	} {
		if check(model) == nil {
			t.Errorf("the static site plan with %s passes the plan check", name)
		}
	}
}
