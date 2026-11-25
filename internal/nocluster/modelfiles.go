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

// Package nocluster is the theos binary's commands with no cluster: init,
// scaffold, protect, status, plan and the lead's, on the ports and on the
// model files of a project repository (docs/plans/no-cluster.md, decisions 2
// to 4, 9 and 12).
// It reads the model files as the API server would hold them: each object
// defaulted from its embedded CRD's schema by the API server's own
// defaulting package, checked by its own validators against the schema's
// create-time rules, then decoded strictly through api/v1beta1's types. It
// imports no controller-runtime, no client-go and no adapter: cmd/theos
// wires the adapters.
package nocluster

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"slices"
	"strings"
	"sync"

	apiextensions "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	structuralschema "k8s.io/apiextensions-apiserver/pkg/apiserver/schema"
	"k8s.io/apiextensions-apiserver/pkg/apiserver/schema/cel"
	"k8s.io/apiextensions-apiserver/pkg/apiserver/schema/defaulting"
	"k8s.io/apiextensions-apiserver/pkg/apiserver/schema/listtype"
	structuralpruning "k8s.io/apiextensions-apiserver/pkg/apiserver/schema/pruning"
	"k8s.io/apiextensions-apiserver/pkg/apiserver/validation"
	apivalidation "k8s.io/apimachinery/pkg/api/validation"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utiljson "k8s.io/apimachinery/pkg/util/json"
	k8svalidation "k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/apimachinery/pkg/util/validation/field"
	utilyaml "k8s.io/apimachinery/pkg/util/yaml"
	celconfig "k8s.io/apiserver/pkg/apis/cel"
	"sigs.k8s.io/yaml"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
	"github.com/JairJosafath/theos/config/crd/bases"
	"github.com/JairJosafath/theos/internal/model"
	"github.com/JairJosafath/theos/internal/modelfacts"
)

// The model files, under a project repository's root (decision 2).
const (
	// ModelFile is the story: the Project and the kinds a plan writes.
	ModelFile = "model/model.yaml"
	// SetupFile is what a person makes and no plan writes: the GitProvider,
	// the catalog entries, the Bundles, and the Crews and their Agents.
	SetupFile = "model/setup.yaml"
	// SettingsFile holds what no CRD does (decision 12); it may be absent.
	SettingsFile = "model/settings.yaml"
)

// storyKinds are the kinds model/model.yaml holds; setupKinds those
// model/setup.yaml holds.
var (
	storyKinds = []string{"Project", "Person", "SoftwareSystem", "Container", "DataStore", "Relationship"}
	setupKinds = []string{"GitProvider", "Framework", "Standard", "Compute", "Deployment", "Connector", "Domain",
		"Bundle", "Agent", "Crew"}
)

// Model is a project's model files as the commands read them.
type Model struct {
	// Project is the Project's name, every object's namespace.
	Project string
	// Objects are what a Container's scaffold reads; Objects.Secrets is
	// empty: the commands fill it with the credential they read.
	Objects    modelfacts.Objects
	People     []c4v1beta1.Person
	Systems    []c4v1beta1.SoftwareSystem
	Containers []c4v1beta1.Container
	// Crews and Agents are model/setup.yaml's (docs/plans/lead-runtime.md,
	// decision 6). Written holds, by "Crew <name>" and "Agent <name>", the
	// spec fields each document wrote, before defaulting: what a runtime
	// says it doesn't honour of them (model.NotHonoured).
	Crews    []c4v1beta1.Crew
	Agents   []c4v1beta1.Agent
	Written  map[string][]string
	Settings Settings
}

// Read reads the model files from fsys, a project repository's root.
func Read(fsys fs.FS) (*Model, error) {
	story, err := fs.ReadFile(fsys, ModelFile)
	if err != nil {
		return nil, err
	}
	setup, err := fs.ReadFile(fsys, SetupFile)
	if err != nil {
		return nil, err
	}
	settings, err := fs.ReadFile(fsys, SettingsFile)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	return Decode(story, setup, settings)
}

// Decode reads the three model files' contents; settings may be empty. Each
// object is read as the API server reads a create (crdSchema.create):
// defaulted from its CRD's schema and checked against that schema's
// create-time rules by the API server's own validators, then decoded
// strictly: an object the API server would refuse at create, an unknown
// field, a kind the file does not hold, a name another object of its kind
// has and a GitProvider whose provider no CI is known for are refused. Its
// namespace is the Project's name.
func Decode(story, setup, settings []byte) (*Model, error) {
	m := &Model{Written: map[string][]string{}}
	if err := decodeFile(m, ModelFile, story, storyKinds); err != nil {
		return nil, err
	}
	if m.Objects.Project == nil {
		return nil, fmt.Errorf("%s holds no Project: its name is the project's", ModelFile)
	}
	m.Project = m.Objects.Project.Name
	if err := decodeFile(m, SetupFile, setup, setupKinds); err != nil {
		return nil, err
	}
	for _, gp := range m.Objects.GitProviders {
		if _, err := model.HostCI(string(gp.Spec.Provider)); err != nil {
			return nil, fmt.Errorf("%s: %s: spec.provider is not a host the factory knows a CI system of: "+
				"it is one of %s", SetupFile, named(gitProviderKind, gp.Name), strings.Join(model.Hosts(), ", "))
		}
	}
	s, err := DecodeSettings(settings, m.Project)
	if err != nil {
		return nil, err
	}
	m.Settings = s
	if err := setNamespaces(m); err != nil {
		return nil, err
	}
	if err := checkCrews(m); err != nil {
		return nil, err
	}
	return m, nil
}

// decodeFile decodes each document of a file into m.
func decodeFile(m *Model, file string, data []byte, kinds []string) error {
	reader := utilyaml.NewYAMLReader(bufio.NewReader(bytes.NewReader(data)))
	for i := 1; ; i++ {
		doc, err := reader.Read()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("%s: %w", file, err)
		}
		if len(bytes.TrimSpace(stripComments(doc))) == 0 {
			continue
		}
		if err := decodeDoc(m, doc, kinds); err != nil {
			return fmt.Errorf("%s, document %d: %w", file, i, err)
		}
	}
}

// stripComments drops the lines that hold only a comment, so a document of
// comments alone reads as empty.
func stripComments(doc []byte) []byte {
	var out []byte
	for line := range bytes.Lines(doc) {
		if !bytes.HasPrefix(bytes.TrimSpace(line), []byte("#")) {
			out = append(out, line...)
		}
	}
	return out
}

// decodeDoc readies one document as the API server readies it at create
// (crdSchema.create) and decodes it strictly into m's list of its kind.
func decodeDoc(m *Model, doc []byte, kinds []string) error {
	obj, err := objectOf(doc)
	if err != nil {
		return err
	}
	apiVersion, _ := obj["apiVersion"].(string)
	kind, _ := obj["kind"].(string)
	if apiVersion != c4v1beta1.GroupVersion.String() {
		return fmt.Errorf("apiVersion is not %s: the model holds those objects only", c4v1beta1.GroupVersion)
	}
	if !slices.Contains(kinds, kind) {
		return fmt.Errorf("kind is a kind this file does not hold: it holds %s", strings.Join(kinds, ", "))
	}
	schemas, err := crdSchemas()
	if err != nil {
		return err
	}
	written := writtenFields(obj)
	if err := schemas[kind].create(obj); err != nil {
		meta, _ := obj["metadata"].(map[string]any)
		name, _ := meta["name"].(string)
		return fmt.Errorf("%s: %w", named(kind, name), err)
	}
	defaulted, err := json.Marshal(obj)
	if err != nil {
		return err
	}
	switch kind {
	case "Project":
		if m.Objects.Project != nil {
			return errors.New("a second Project: a model is one project's")
		}
		m.Objects.Project = &c4v1beta1.Project{}
		return strict(defaulted, m.Objects.Project)
	case "Person":
		return add(&m.People, defaulted)
	case "SoftwareSystem":
		return add(&m.Systems, defaulted)
	case "Container":
		return add(&m.Containers, defaulted)
	case "DataStore":
		return add(&m.Objects.DataStores, defaulted)
	case "Relationship":
		return add(&m.Objects.Relationships, defaulted)
	case "GitProvider":
		return add(&m.Objects.GitProviders, defaulted)
	case "Framework":
		return add(&m.Objects.Frameworks, defaulted)
	case "Standard":
		return add(&m.Objects.Standards, defaulted)
	case "Compute":
		return add(&m.Objects.Computes, defaulted)
	case "Deployment":
		return add(&m.Objects.Deployments, defaulted)
	case "Connector":
		return add(&m.Objects.Connectors, defaulted)
	case "Domain":
		return add(&m.Objects.Domains, defaulted)
	case "Agent":
		return addWritten(m, &m.Agents, kind, defaulted, written)
	case "Crew":
		return addWritten(m, &m.Crews, kind, defaulted, written)
	default: // Bundle
		return add(&m.Objects.Bundles, defaulted)
	}
}

// add decodes data strictly into a new item of list.
func add[T any](list *[]T, data []byte) error {
	var item T
	if err := strict(data, &item); err != nil {
		return err
	}
	*list = append(*list, item)
	return nil
}

// addWritten decodes data strictly into a new item of list, and records the
// spec fields its document wrote.
func addWritten[T any, P interface {
	*T
	GetName() string
}](m *Model, list *[]T, kind string, data []byte, written []string) error {
	if err := add(list, data); err != nil {
		return err
	}
	m.Written[kind+" "+P(&(*list)[len(*list)-1]).GetName()] = written
	return nil
}

// writtenFields are the fields of obj's spec as written, sorted; one written
// as null is left out, as the API server reads it.
func writtenFields(obj map[string]any) []string {
	spec, _ := obj["spec"].(map[string]any)
	var out []string
	for k, v := range spec {
		if v != nil {
			out = append(out, k)
		}
	}
	slices.Sort(out)
	return out
}

// strict decodes data into obj, refusing a field obj's type does not have.
func strict(data []byte, obj any) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	return dec.Decode(obj)
}

// objectOf is one YAML document as the API server reads a JSON body: a
// key given twice is refused, and a whole number is an int64, as defaulting,
// the validators and CEL see it on the server.
func objectOf(doc []byte) (map[string]any, error) {
	raw, err := yaml.YAMLToJSONStrict(doc)
	if err != nil {
		return nil, err
	}
	var obj map[string]any
	if err := utiljson.Unmarshal(raw, &obj); err != nil {
		return nil, err
	}
	return obj, nil
}

// named is how a refusal names an object: by its kind and name, or by its
// kind alone when the name is not one the API server takes, so a refusal
// never quotes a value a rule refused.
func named(kind, name string) string {
	if name == "" || len(k8svalidation.IsDNS1123Subdomain(name)) > 0 {
		return "a " + kind
	}
	return kind + " " + name
}

// setNamespaces puts every object in the Project's namespace, and refuses
// one that names another, that has no name, that sets
// metadata.resourceVersion or whose name another of its kind has; it checks
// each one's metadata as the API server does (validateMeta). A resourceVersion
// is refused whatever its value: a model file has no business carrying one.
// That is stricter than the API server, which refuses at create only a
// non-zero whole number.
func setNamespaces(m *Model) error {
	var err error
	seen := map[string]bool{}
	set := func(kind string, meta *metav1.ObjectMeta) {
		switch {
		case err != nil:
		case meta.Name == "":
			err = fmt.Errorf("a %s has no metadata.name", kind)
		case meta.Namespace != "" && meta.Namespace != m.Project:
			err = fmt.Errorf("%s: metadata.namespace is not the Project's name: a model's objects are in the "+
				"Project's namespace", named(kind, meta.Name))
		case meta.ResourceVersion != "":
			err = fmt.Errorf("%s: metadata.resourceVersion is set: the API server sets it, so a model leaves it out",
				named(kind, meta.Name))
		default:
			meta.Namespace = m.Project
			if verr := validateMeta(meta); verr != nil {
				err = fmt.Errorf("%s: %w", named(kind, meta.Name), verr)
				return
			}
			if seen[kind+"/"+meta.Name] {
				err = fmt.Errorf("%s: a second %s of this name: a name is used once per kind in a project",
					named(kind, meta.Name), kind)
			}
			seen[kind+"/"+meta.Name] = true
		}
	}
	o := &m.Objects
	set("Project", &o.Project.ObjectMeta)
	for i := range m.People {
		set("Person", &m.People[i].ObjectMeta)
	}
	for i := range m.Systems {
		set("SoftwareSystem", &m.Systems[i].ObjectMeta)
	}
	for i := range m.Containers {
		set("Container", &m.Containers[i].ObjectMeta)
	}
	for i := range o.DataStores {
		set("DataStore", &o.DataStores[i].ObjectMeta)
	}
	for i := range o.Relationships {
		set("Relationship", &o.Relationships[i].ObjectMeta)
	}
	for i := range o.GitProviders {
		set("GitProvider", &o.GitProviders[i].ObjectMeta)
	}
	for i := range o.Frameworks {
		set("Framework", &o.Frameworks[i].ObjectMeta)
	}
	for i := range o.Standards {
		set("Standard", &o.Standards[i].ObjectMeta)
	}
	for i := range o.Computes {
		set("Compute", &o.Computes[i].ObjectMeta)
	}
	for i := range o.Deployments {
		set("Deployment", &o.Deployments[i].ObjectMeta)
	}
	for i := range o.Connectors {
		set("Connector", &o.Connectors[i].ObjectMeta)
	}
	for i := range o.Domains {
		set("Domain", &o.Domains[i].ObjectMeta)
	}
	for i := range o.Bundles {
		set("Bundle", &o.Bundles[i].ObjectMeta)
	}
	for i := range m.Agents {
		set("Agent", &m.Agents[i].ObjectMeta)
	}
	for i := range m.Crews {
		set("Crew", &m.Crews[i].ObjectMeta)
	}
	return err
}

// checkCrews refuses a Crew whose Container is not in model/model.yaml or
// one of whose Agents is not in model/setup.yaml, naming the field.
func checkCrews(m *Model) error {
	for _, c := range m.Crews {
		crew := named("Crew", c.Name)
		if modelfacts.Find(m.Containers, c.Spec.ContainerRef.Name) == nil {
			return fmt.Errorf("%s: %s: spec.containerRef names Container %s, which %s does not hold", SetupFile,
				crew, c.Spec.ContainerRef.Name, ModelFile)
		}
		for _, ref := range CrewAgentRefs(&c.Spec) {
			if modelfacts.Find(m.Agents, ref.Name) == nil {
				return fmt.Errorf("%s: %s: %s names Agent %s, which %s does not hold", SetupFile, crew, ref.Field,
					ref.Name, SetupFile)
			}
		}
	}
	return nil
}

// AgentRef is an Agent a Crew names, and the field that names it.
type AgentRef struct {
	Field, Name string
}

// CrewAgentRefs are the Agents a Crew names, by field, in the spec's order:
// its implementer and reviewer, its triager, spec writer and verifier, then
// its routes'.
func CrewAgentRefs(spec *c4v1beta1.CrewSpec) []AgentRef {
	refs := []AgentRef{{"spec.implementer", spec.Implementer.Name}, {"spec.reviewer", spec.Reviewer.Name}}
	add := func(path string, ref *c4v1beta1.LocalRef) {
		if ref != nil {
			refs = append(refs, AgentRef{path, ref.Name})
		}
	}
	add("spec.triager", spec.Triager)
	add("spec.specWriter", spec.SpecWriter)
	add("spec.verifier", spec.Verifier)
	for i, r := range spec.Routes {
		add(fmt.Sprintf("spec.routes[%d].implementer", i), r.Implementer)
		add(fmt.Sprintf("spec.routes[%d].reviewer", i), r.Reviewer)
		add(fmt.Sprintf("spec.routes[%d].verifier", i), r.Verifier)
	}
	return refs
}

// crdSchema is one embedded CRD's served version: its structural schema,
// which defaults an object, and the API server's own validators of it.
type crdSchema struct {
	structural *structuralschema.Structural
	// openAPI holds the schema's patterns, enums, formats, lengths and
	// bounds; cel its x-kubernetes-validations rules, nil when it has none.
	openAPI validation.SchemaValidator
	cel     *cel.Validator
	// statusSubresource is whether the kind has a status subresource,
	// whose status a create does not set.
	statusSubresource bool
}

// newCRDSchema is the crdSchema of a served version's schema; the
// GitProvider's provider enum is taken off its validators
// (hostsWithoutCluster).
func newCRDSchema(kind string, schema *apiextensions.JSONSchemaProps, statusSubresource bool) (crdSchema, error) {
	s, err := structuralschema.NewStructural(schema)
	if err != nil {
		return crdSchema{}, err
	}
	if kind == gitProviderKind {
		hostsWithoutCluster(schema)
	}
	openAPI, _, err := validation.NewSchemaValidator(schema)
	if err != nil {
		return crdSchema{}, err
	}
	return crdSchema{structural: s, openAPI: openAPI, cel: cel.NewValidator(s, true, celconfig.PerCallLimit),
		statusSubresource: statusSubresource}, nil
}

// create readies obj as the API server readies a custom resource it is
// asked to create, in its order: a field written as null that is not
// nullable and has no default is read as left out, defaults are set, status
// is read strictly and dropped for a kind with a status subresource, then
// obj is validated.
func (s crdSchema) create(obj map[string]any) error {
	defaulting.PruneNonNullableNullsWithoutDefaults(obj, s.structural)
	defaulting.Default(obj, s.structural)
	if s.statusSubresource {
		// The API server reads the whole body strictly before it drops status.
		if unknown := structuralpruning.PruneWithOptions(map[string]any{"status": obj["status"]}, s.structural, true,
			structuralschema.UnknownFieldPathOptions{TrackUnknownFieldPaths: true}); len(unknown) > 0 {
			return fmt.Errorf("unknown field %q", unknown[0])
		}
		delete(obj, "status")
	}
	return s.validate(obj)
}

// validate refuses obj as the API server refuses a custom resource at
// create time (k8s.io/apiextensions-apiserver/pkg/registry/customresource,
// customResourceStrategy.Validate): the schema's OpenAPI rules, the
// list-type rules, then its CEL rules with no old object, so a rule on a
// change (oldSelf) is not run. metadata is checked with the namespace
// (validateMeta).
func (s crdSchema) validate(obj map[string]any) error {
	errs := validation.ValidateCustomResource(nil, obj, s.openAPI)
	errs = append(errs, listtype.ValidateListSetsAndMaps(nil, s.structural, obj)...)
	if len(errs) == 0 && s.cel != nil {
		celErrs, _ := s.cel.Validate(context.Background(), nil, s.structural, obj, nil,
			celconfig.RuntimeCELCostBudget)
		errs = append(errs, celErrs...)
	}
	return withoutValues(errs)
}

// withoutValues is errs naming each field and its rule but never the value.
// With the decoder's own refusals, which name the field and the rule too
// (named, decodeDoc, setNamespaces), no value a rule refused is printed: no
// part of a model file's url, user or password included.
func withoutValues(errs field.ErrorList) error {
	for _, e := range errs {
		e.BadValue = field.OmitValueType{}
	}
	return errs.ToAggregate()
}

// validateMeta refuses an object's metadata as the API server refuses a
// namespaced custom resource's: a name that is not a DNS subdomain, a
// namespace that is not a DNS label, labels and annotations Kubernetes does
// not take.
func validateMeta(meta *metav1.ObjectMeta) error {
	return withoutValues(apivalidation.ValidateObjectMeta(meta, true, apivalidation.NameIsDNSSubdomain,
		field.NewPath("metadata")))
}

const gitProviderKind = "GitProvider"

// hostsWithoutCluster takes the provider enum off a GitProvider's schema,
// the one create-time rule the decoder does not apply: the enum is the
// cluster's word on where it can host, and with no cluster the providers
// are those model.HostCI knows a CI of, which Decode asks instead
// (docs/plans/no-cluster.md, decisions 2 and 11).
func hostsWithoutCluster(gitProvider *apiextensions.JSONSchemaProps) {
	spec := gitProvider.Properties["spec"]
	provider := spec.Properties["provider"]
	provider.Enum = nil
	spec.Properties["provider"] = provider
	gitProvider.Properties["spec"] = spec
}

// crdSchemas are the embedded CRDs' served versions, by kind, read once.
var crdSchemas = sync.OnceValues(func() (map[string]crdSchema, error) {
	files, err := fs.Glob(bases.FS, "*.yaml")
	if err != nil {
		return nil, err
	}
	out := make(map[string]crdSchema, len(files))
	for _, file := range files {
		data, err := bases.FS.ReadFile(file)
		if err != nil {
			return nil, err
		}
		var crd apiextensionsv1.CustomResourceDefinition
		if err := yaml.Unmarshal(data, &crd); err != nil {
			return nil, fmt.Errorf("%s: %w", file, err)
		}
		for _, v := range crd.Spec.Versions {
			if v.Name != c4v1beta1.GroupVersion.Version || v.Schema == nil || v.Schema.OpenAPIV3Schema == nil {
				continue
			}
			var internal apiextensions.JSONSchemaProps
			if err := apiextensionsv1.Convert_v1_JSONSchemaProps_To_apiextensions_JSONSchemaProps(
				v.Schema.OpenAPIV3Schema, &internal, nil); err != nil {
				return nil, fmt.Errorf("%s: %w", file, err)
			}
			s, err := newCRDSchema(crd.Spec.Names.Kind, &internal,
				v.Subresources != nil && v.Subresources.Status != nil)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", file, err)
			}
			out[crd.Spec.Names.Kind] = s
		}
	}
	return out, nil
})
