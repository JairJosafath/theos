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
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
)

// linearSource is a request source on Linear team ENG.
func linearSource(name, team string) c4v1beta1.BriefRequestSource {
	return c4v1beta1.BriefRequestSource{Name: name,
		Linear:               &c4v1beta1.BriefLinearSource{Team: team, Label: "factory"},
		CredentialsSecretRef: c4v1beta1.LocalRef{Name: name + "-linear"}}
}

// jiraSource is a request source on Jira project project of acme's site.
func jiraSource(name, project string) c4v1beta1.BriefRequestSource {
	return c4v1beta1.BriefRequestSource{Name: name,
		Jira: &c4v1beta1.BriefJiraSource{Site: "https://acme.atlassian.net",
			CloudID: "00000000-0000-4000-8000-000000000000", Project: project, Label: "factory"},
		CredentialsSecretRef: c4v1beta1.LocalRef{Name: name + "-jira"}}
}

// Step 9 of docs/plans/requests-and-chat.md (#160): spec.requestSources and
// the rules of decisions 12, 17 and 20. Nothing reads a source yet.
var _ = Describe("Brief spec.requestSources", func() {
	var ns string
	BeforeEach(func() { ns = newProjectNamespace() })

	sourcing := func(name string, sources ...c4v1beta1.BriefRequestSource) *c4v1beta1.Brief {
		brief := newBrief(ns, name)
		brief.Spec.RequestSources = sources
		return brief
	}

	It("takes a Linear team and a Jira project, in Ask unless told", func() {
		jira := jiraSource("support", "SHOP")
		jira.Mode = "On"
		jira.Jira.Close = &c4v1beta1.BriefJiraClosing{
			Completed:  &c4v1beta1.BriefJiraOutcome{Status: "Shipped", Resolution: "Fixed"},
			NotPlanned: &c4v1beta1.BriefJiraOutcome{Resolution: "Won't Fix"}}
		linear := linearSource("product", "ENG")
		linear.Accepted, linear.Declined = []int32{412}, []int32{415}
		mustCreate(sourcing("both", linear, jira))
		brief := &c4v1beta1.Brief{}
		Expect(k8sClient.Get(ctx, key(ns, "both"), brief)).To(Succeed())
		Expect(brief.Spec.RequestSources).To(HaveLen(2))
		Expect(brief.Spec.RequestSources[0].Mode).To(Equal("Ask"))
		Expect(brief.Spec.RequestSources[1].Mode).To(Equal("On"))
		Expect(brief.Spec.RequestSources[1].Jira.Close.NotPlanned.Resolution).To(Equal("Won't Fix"))
	})

	It("refuses an entry with both linear and jira, or neither", func() {
		both := linearSource("product", "ENG")
		both.Jira = jiraSource("product", "SHOP").Jira
		Expect(k8sClient.Create(ctx, sourcing("both", both))).To(MatchError(ContainSubstring(
			"a request source names exactly one of linear and jira")))
		neither := linearSource("product", "ENG")
		neither.Linear = nil
		Expect(k8sClient.Create(ctx, sourcing("neither", neither))).To(MatchError(ContainSubstring(
			"a request source names exactly one of linear and jira")))
	})

	It("refuses a site that isn't https, or has a path", func() {
		for _, site := range []string{"http://acme.atlassian.net", "acme.atlassian.net",
			"https://acme.atlassian.net/jira", "ftp://acme.atlassian.net"} {
			source := jiraSource("support", "SHOP")
			source.Jira.Site = site
			Expect(k8sClient.Create(ctx, sourcing("site", source))).To(MatchError(ContainSubstring(
				"site must be an https address with no path")), site)
		}
	})

	It("refuses a jira entry without cloudID", func() {
		source := jiraSource("support", "SHOP")
		source.Jira.CloudID = ""
		Expect(k8sClient.Create(ctx, sourcing("cloud", source))).To(MatchError(ContainSubstring(
			"spec.requestSources[0].jira.cloudID")))
	})

	It("refuses one team twice, whatever its case", func() {
		Expect(k8sClient.Create(ctx, sourcing("team", linearSource("product", "ENG"),
			linearSource("design", "eng")))).To(MatchError(ContainSubstring(
			"at most one request source per Linear team: an item is read once")))
		mustCreate(sourcing("teams", linearSource("product", "ENG"), linearSource("design", "DES")))
	})

	It("refuses one site and project twice", func() {
		Expect(k8sClient.Create(ctx, sourcing("project", jiraSource("support", "SHOP"),
			jiraSource("shop", "shop")))).To(MatchError(ContainSubstring(
			"at most one request source per Jira site and project: an item is read once")))
		other := jiraSource("shop", "SHOP")
		other.Jira.Site = "https://other.atlassian.net"
		other.Jira.CloudID = "11111111-1111-4111-8111-111111111111"
		mustCreate(sourcing("projects", jiraSource("support", "SHOP"), jiraSource("ops", "OPS"), other))
	})

	It("refuses a number accepted and declined", func() {
		source := linearSource("product", "ENG")
		source.Accepted, source.Declined = []int32{412, 414}, []int32{414}
		Expect(k8sClient.Create(ctx, sourcing("both", source))).To(MatchError(ContainSubstring(
			"an item number can't be both accepted and declined")))
	})

	It("names each source once, by a DNS label, at most ten", func() {
		Expect(k8sClient.Create(ctx, sourcing("twice", linearSource("product", "ENG"),
			linearSource("product", "DES")))).To(MatchError(ContainSubstring("Duplicate value")))
		Expect(k8sClient.Create(ctx, sourcing("label", linearSource("Product_Team", "ENG")))).To(
			MatchError(ContainSubstring("name must be a DNS label")))
		eleven := make([]c4v1beta1.BriefRequestSource, 0, 11)
		for i := range 11 {
			eleven = append(eleven, linearSource(fmt.Sprintf("team-%d", i), fmt.Sprintf("T%d", i)))
		}
		Expect(k8sClient.Create(ctx, sourcing("eleven", eleven...))).To(MatchError(ContainSubstring(
			"spec.requestSources")))
		mustCreate(sourcing("ten", eleven[:10]...))
	})

	It("keeps a source's request by source and key, with no repository or author", func() {
		mustCreate(sourcing("cel-lens", linearSource("product", "ENG")))
		since := metav1.Now()
		Eventually(func(g Gomega) {
			brief := getBrief(g, ns)
			brief.Status.Requests = []c4v1beta1.BriefRequest{{Source: "product", Key: "ENG-412", Number: 412,
				Title: "Dark theme", Stage: "Waiting", Since: &since}}
			brief.Status.RequestSourcesSettled = []c4v1beta1.BriefRequestSourceSettled{
				{Source: "product", Numbers: []int32{400}}}
			g.Expect(k8sClient.Status().Update(ctx, brief)).To(Succeed())
		}).Should(Succeed())

		for _, bad := range []c4v1beta1.BriefRequest{
			{Repository: "acme/cel-lens", Source: "product", Key: "ENG-412", Number: 412, Title: "x", Stage: "Waiting"},
			{Source: "product", Number: 412, Title: "x", Stage: "Waiting"},
			{Number: 412, Title: "x", Stage: "Waiting"},
		} {
			// Eventually, so that a write of the operator's in between is a
			// conflict tried again, not the refusal looked for.
			Eventually(func(g Gomega) {
				brief := getBrief(g, ns)
				brief.Status.Requests = []c4v1beta1.BriefRequest{bad}
				g.Expect(k8sClient.Status().Update(ctx, brief)).To(MatchError(ContainSubstring(
					"a request names either its repository, or its source and key")), "%+v", bad)
			}).Should(Succeed())
		}
	})
})
