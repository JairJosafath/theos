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
	"bytes"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
	"github.com/JairJosafath/theos/internal/adapters/gitlab/gitlabtest"
	"github.com/JairJosafath/theos/internal/model"
	"github.com/JairJosafath/theos/internal/oci"
	"github.com/JairJosafath/theos/internal/oci/ocitest"
	"github.com/JairJosafath/theos/internal/ports"
)

var _ = Describe("Container ships its image to a gitlab target (#1806)", func() {
	It("copies the published image to the project's registry by digest and reports it, and copies nothing to a project whose registry is off", func() {
		ns := newProjectNamespace()
		deployingContainer(ns)
		archive, digest := ocitest.TwoPlatforms(GinkgoT())
		_, err := oci.Push(ctx, oci.Registry{Host: registryFake.host}, ns+"/orders-api", "v0.1.0",
			bytes.NewReader(archive))
		Expect(err).NotTo(HaveOccurred())
		gitFake.SetImage(ns, "orders-api", "v0.1.0", ports.Image{Outcome: ports.ImagePublished,
			Digest: digest, Platforms: []string{"linux/amd64", "linux/arm64"}})
		withImage, without := "Acme/"+ns+"-Img", "acme/"+ns+"-plain"
		img := gitlabFake.AddProject(withImage, "glpat-"+ns+"-img")
		img.RegistryHost = gitlabRegistryFake.host
		plain := gitlabFake.AddProject(without, "glpat-"+ns+"-plain")
		plain.RegistryHost = gitlabRegistryFake.host
		plain.RegistryEnabled = false
		for name, project := range map[string]*gitlabtest.Project{"img": img, "plain": plain} {
			dt := newGitLabTarget(ns, name, name+"-gitlab", project.Path)
			mustCreate(newDeploySecret(ns, name+"-gitlab", map[string]string{"token": "glpat-" + ns + "-" + name}), dt)
			deployReady(dt, metav1.ConditionTrue, model.DeployTargetReady)
		}
		shipRelease(ns, "v0.1.0", true)
		setDeploy(ns, c4v1beta1.ContainerDeploy{Target: "img"}, c4v1beta1.ContainerDeploy{Target: "plain"})

		repo := "acme/" + ns + "-img/orders-api"
		want := gitlabRegistryFake.host + "/" + repo + "@" + digest
		Eventually(func(g Gomega) {
			for _, name := range []string{"img", "plain"} {
				d, cond := deployOfContainer(g, ns, "orders-api", name)
				g.Expect(d.Phase).To(Equal(model.DeployPhaseDeployed), d.Message)
				g.Expect(cond.Reason).To(Equal(model.DeployDeployed), cond.Message)
			}
			d, _ := deployOfContainer(g, ns, "orders-api", "img")
			g.Expect(d.Image).To(Equal(want))
			d, _ = deployOfContainer(g, ns, "orders-api", "plain")
			g.Expect(d.Image).To(BeEmpty())
		}).Should(Succeed())
		got, err := oci.Resolve(ctx, oci.Registry{Host: gitlabRegistryFake.host}, repo, "v0.1.0")
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(Equal(digest))
		rels := img.Releases()
		Expect(rels).To(HaveLen(1))
		Expect(rels[0].Description).To(ContainSubstring("Image: `" + want + "`"))
		rels = plain.Releases()
		Expect(rels).To(HaveLen(1))
		Expect(rels[0].Description).NotTo(ContainSubstring("Image:"))
	})
})
