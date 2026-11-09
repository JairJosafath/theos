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

package awsenv

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/devicefarm"
	dftypes "github.com/aws/aws-sdk-go-v2/service/devicefarm/types"
)

// fakeFarm is an in-memory AWS Device Farm on the fake AWS's accounts:
// projects and their tags, device pools, uploads that go up to pre-signed
// addresses on an httptest server and are processed on their next read,
// and runs a test finishes by hand with their jobs, problems and
// artifacts, whose files the same server serves.
type fakeFarm struct {
	aws    *fakeAWS
	server *httptest.Server

	mu       sync.Mutex
	seq      int
	projects map[string]*fakeProject // by ARN
	pools    map[string]bool
	uploads  []*fakeUpload
	runs     []*fakeFarmRun
	// files are the bodies the server keeps, by path: uploads put, and
	// artifacts to get.
	files    map[string][]byte
	stops    []string
	deleted  []string
	schedule []devicefarm.ScheduleRunInput
}

type fakeProject struct {
	account string
	tags    []dftypes.Tag
}

type fakeUpload struct {
	upload dftypes.Upload
	path   string
}

type fakeFarmRun struct {
	run       dftypes.Run
	jobs      []dftypes.Job
	problems  map[string][]dftypes.UniqueProblem
	artifacts map[dftypes.ArtifactCategory][]dftypes.Artifact
}

func newFakeFarm(f *fakeAWS) *fakeFarm {
	d := &fakeFarm{aws: f, projects: map[string]*fakeProject{}, pools: map[string]bool{}, files: map[string][]byte{}}
	d.server = httptest.NewServer(http.HandlerFunc(d.serve))
	return d
}

// serve is Device Farm's storage: a PUT to an upload's address, a GET of an
// artifact's.
func (d *fakeFarm) serve(w http.ResponseWriter, r *http.Request) {
	d.mu.Lock()
	defer d.mu.Unlock()
	switch r.Method {
	case http.MethodPut:
		for _, u := range d.uploads {
			if u.path == r.URL.Path && u.upload.Status == dftypes.UploadStatusInitialized {
				data, _ := io.ReadAll(r.Body)
				d.files[r.URL.Path] = data
				u.upload.Status = dftypes.UploadStatusProcessing
				return
			}
		}
		http.Error(w, "no such upload", http.StatusForbidden)
	case http.MethodGet:
		data, ok := d.files[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(data)
	}
}

// project adds a project and a device pool of account, and returns their
// ARNs.
func (d *fakeFarm) project(account string) (project, pool string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.seq++
	id := fmt.Sprintf("0b6f3c1e-0000-4000-8000-%012d", d.seq)
	project = "arn:aws:devicefarm:us-west-2:" + account + ":project:" + id
	pool = "arn:aws:devicefarm:us-west-2:" + account + ":devicepool:" + id + "/5e1f0a9d-0000-4000-8000-000000000001"
	d.projects[project] = &fakeProject{account: account}
	d.pools[pool] = true
	return project, pool
}

// dropPool deletes a device pool, as a person might.
func (d *fakeFarm) dropPool(pool string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	delete(d.pools, pool)
}

// leaseTags counts the lease tags of environment uid on project.
func (d *fakeFarm) leaseTags(project, uid string) int {
	d.mu.Lock()
	defer d.mu.Unlock()
	n := 0
	for _, t := range d.projects[project].tags {
		if strings.HasPrefix(aws.ToString(t.Key), leaseTagPrefix+uid+":") {
			n++
		}
	}
	return n
}

// uploaded is what was put to the upload named name.
func (d *fakeFarm) uploaded(name string) ([]byte, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, u := range d.uploads {
		if aws.ToString(u.upload.Name) == name {
			data, ok := d.files[u.path]
			return data, ok
		}
	}
	return nil, false
}

// refuseUploads makes every upload being processed fail, as one Device
// Farm can't read does.
func (d *fakeFarm) refuseUploads(message string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, u := range d.uploads {
		if u.upload.Status == dftypes.UploadStatusProcessing {
			u.upload.Status, u.upload.Message = dftypes.UploadStatusFailed, aws.String(message)
		}
	}
}

// running starts run name, as Device Farm does once its devices are ready.
func (d *fakeFarm) running(name string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if r := d.find(name); r != nil {
		r.run.Status = dftypes.ExecutionStatusRunning
	}
}

// finish ends run name with result, its counts, its metered minutes, its
// jobs, problems and artifacts, whose bodies the server serves.
func (d *fakeFarm) finish(name string, result dftypes.ExecutionResult, c dftypes.Counters, metered float64,
	jobs []dftypes.Job, problems map[string][]dftypes.UniqueProblem, artifacts map[string][]byte,
	types map[string]dftypes.ArtifactType) {
	d.mu.Lock()
	defer d.mu.Unlock()
	r := d.find(name)
	r.run.Status, r.run.Result, r.run.Counters = dftypes.ExecutionStatusCompleted, result, &c
	r.run.DeviceMinutes = &dftypes.DeviceMinutes{Metered: aws.Float64(metered), Total: aws.Float64(metered)}
	r.run.TotalJobs, r.run.CompletedJobs = aws.Int32(int32(len(jobs))), aws.Int32(int32(len(jobs)))
	base := strings.Replace(aws.ToString(r.run.Arn), ":run:", ":job:", 1)
	for i := range jobs {
		jobs[i].Arn = aws.String(fmt.Sprintf("%s/0000000%d", base, i))
	}
	r.jobs, r.problems = jobs, problems
	r.artifacts = map[dftypes.ArtifactCategory][]dftypes.Artifact{}
	names := slices.Sorted(func(yield func(string) bool) {
		for n := range artifacts {
			if !yield(n) {
				return
			}
		}
	})
	for i, n := range names {
		// "<job>/<name>.<ext>": the job's index, the artifact's name.
		job, file, _ := strings.Cut(n, "/")
		stem, ext, _ := strings.Cut(file, ".")
		path := "/artifacts/" + name + "/" + n
		d.files[path] = artifacts[n]
		arn := strings.Replace(aws.ToString(r.run.Arn), ":run:", ":artifact:", 1) + "/0000000" + job +
			fmt.Sprintf("/suite/test/%08d", i)
		category := dftypes.ArtifactCategoryFile
		if types[n] == dftypes.ArtifactTypeScreenshot {
			category = dftypes.ArtifactCategoryScreenshot
		}
		r.artifacts[category] = append(r.artifacts[category], dftypes.Artifact{Arn: aws.String(arn),
			Name: aws.String(stem), Extension: aws.String(ext), Type: types[n], Url: aws.String(d.server.URL + path)})
	}
}

// find is run name; d is locked.
func (d *fakeFarm) find(name string) *fakeFarmRun {
	for _, r := range d.runs {
		if aws.ToString(r.run.Name) == name {
			return r
		}
	}
	return nil
}

func (d *fakeFarm) clients(cfg aws.Config) DeviceFarmClients {
	return DeviceFarmClients{DeviceFarm: &fakeFarmAPI{d, cfg}, STS: &fakeSTS{d.aws, cfg}, HTTP: d.server.Client()}
}

type fakeFarmAPI struct {
	d   *fakeFarm
	cfg aws.Config
}

// begin checks the caller and locks the farm: its account, and a func that
// unlocks.
func (c *fakeFarmAPI) begin(call string) (string, func(), error) {
	c.d.aws.mu.Lock()
	account, err := c.d.aws.caller(c.cfg, call)
	c.d.aws.mu.Unlock()
	if err != nil {
		return "", nil, err
	}
	if c.cfg.Region != "us-west-2" {
		return "", nil, apiErr("UnrecognizedClientException", "Device Farm is in us-west-2 alone")
	}
	c.d.mu.Lock()
	return account, c.d.mu.Unlock, nil
}

func (c *fakeFarmAPI) projectOf(account, arn string) (*fakeProject, error) {
	p, ok := c.d.projects[arn]
	if !ok || p.account != account {
		return nil, apiErr("NotFoundException", "No project with ARN "+arn)
	}
	return p, nil
}

func (c *fakeFarmAPI) CreateUpload(_ context.Context, in *devicefarm.CreateUploadInput,
	_ ...func(*devicefarm.Options)) (*devicefarm.CreateUploadOutput, error) {
	account, done, err := c.begin("CreateUpload")
	if err != nil {
		return nil, err
	}
	defer done()
	if _, err := c.projectOf(account, aws.ToString(in.ProjectArn)); err != nil {
		return nil, err
	}
	if strings.Contains(aws.ToString(in.Name), "/") {
		return nil, apiErr("ArgumentException", "An upload's name has no /")
	}
	d := c.d
	d.seq++
	path := fmt.Sprintf("/uploads/%d", d.seq)
	u := &fakeUpload{path: path, upload: dftypes.Upload{
		Arn:  aws.String(strings.Replace(aws.ToString(in.ProjectArn), ":project:", ":upload:", 1) + fmt.Sprintf("/%08d", d.seq)),
		Name: in.Name, Type: in.Type, Status: dftypes.UploadStatusInitialized, Url: aws.String(d.server.URL + path)}}
	d.uploads = append(d.uploads, u)
	copied := u.upload
	return &devicefarm.CreateUploadOutput{Upload: &copied}, nil
}

// process moves a sent upload on: processed on its next read.
func (c *fakeFarmAPI) process(u *fakeUpload) dftypes.Upload {
	out := u.upload
	if u.upload.Status == dftypes.UploadStatusProcessing {
		u.upload.Status = dftypes.UploadStatusSucceeded
	}
	return out
}

func (c *fakeFarmAPI) GetUpload(_ context.Context, in *devicefarm.GetUploadInput,
	_ ...func(*devicefarm.Options)) (*devicefarm.GetUploadOutput, error) {
	_, done, err := c.begin("GetUpload")
	if err != nil {
		return nil, err
	}
	defer done()
	for _, u := range c.d.uploads {
		if aws.ToString(u.upload.Arn) == aws.ToString(in.Arn) {
			got := c.process(u)
			return &devicefarm.GetUploadOutput{Upload: &got}, nil
		}
	}
	return nil, apiErr("NotFoundException", "No upload")
}

func (c *fakeFarmAPI) ListUploads(_ context.Context, in *devicefarm.ListUploadsInput,
	_ ...func(*devicefarm.Options)) (*devicefarm.ListUploadsOutput, error) {
	_, done, err := c.begin("ListUploads")
	if err != nil {
		return nil, err
	}
	defer done()
	out := &devicefarm.ListUploadsOutput{}
	for _, u := range c.d.uploads {
		if strings.HasPrefix(aws.ToString(u.upload.Arn), strings.Replace(aws.ToString(in.Arn), ":project:", ":upload:", 1)) &&
			(in.Type == "" || in.Type == u.upload.Type) {
			out.Uploads = append(out.Uploads, c.process(u))
		}
	}
	return out, nil
}

func (c *fakeFarmAPI) DeleteUpload(_ context.Context, in *devicefarm.DeleteUploadInput,
	_ ...func(*devicefarm.Options)) (*devicefarm.DeleteUploadOutput, error) {
	_, done, err := c.begin("DeleteUpload")
	if err != nil {
		return nil, err
	}
	defer done()
	d := c.d
	d.uploads = slices.DeleteFunc(d.uploads, func(u *fakeUpload) bool {
		if aws.ToString(u.upload.Arn) == aws.ToString(in.Arn) {
			d.deleted = append(d.deleted, aws.ToString(u.upload.Name))
			return true
		}
		return false
	})
	return &devicefarm.DeleteUploadOutput{}, nil
}

func (c *fakeFarmAPI) ScheduleRun(_ context.Context, in *devicefarm.ScheduleRunInput,
	_ ...func(*devicefarm.Options)) (*devicefarm.ScheduleRunOutput, error) {
	account, done, err := c.begin("ScheduleRun")
	if err != nil {
		return nil, err
	}
	defer done()
	d := c.d
	if _, err := c.projectOf(account, aws.ToString(in.ProjectArn)); err != nil {
		return nil, err
	}
	if !d.pools[aws.ToString(in.DevicePoolArn)] {
		return nil, apiErr("NotFoundException", "No device pool")
	}
	for _, arn := range []string{aws.ToString(in.AppArn), aws.ToString(in.Test.TestPackageArn)} {
		if !slices.ContainsFunc(d.uploads, func(u *fakeUpload) bool {
			return aws.ToString(u.upload.Arn) == arn && u.upload.Status == dftypes.UploadStatusSucceeded
		}) {
			return nil, apiErr("ArgumentException", "Upload "+arn+" isn't processed")
		}
	}
	d.schedule = append(d.schedule, *in)
	d.seq++
	r := &fakeFarmRun{run: dftypes.Run{
		Arn:  aws.String(strings.Replace(aws.ToString(in.ProjectArn), ":project:", ":run:", 1) + fmt.Sprintf("/%08d", d.seq)),
		Name: in.Name, Status: dftypes.ExecutionStatusScheduling, Result: dftypes.ExecutionResultPending,
		Created: aws.Time(d.aws.now()), DevicePoolArn: in.DevicePoolArn, AppUpload: in.AppArn, Type: in.Test.Type}}
	d.runs = append(d.runs, r)
	copied := r.run
	return &devicefarm.ScheduleRunOutput{Run: &copied}, nil
}

func (c *fakeFarmAPI) ListRuns(_ context.Context, in *devicefarm.ListRunsInput,
	_ ...func(*devicefarm.Options)) (*devicefarm.ListRunsOutput, error) {
	account, done, err := c.begin("ListRuns")
	if err != nil {
		return nil, err
	}
	defer done()
	if _, err := c.projectOf(account, aws.ToString(in.Arn)); err != nil {
		return nil, err
	}
	prefix := strings.Replace(aws.ToString(in.Arn), ":project:", ":run:", 1) + "/"
	// One run a page, newest first, as a lister must page through.
	var runs []dftypes.Run
	for _, r := range slices.Backward(c.d.runs) {
		if strings.HasPrefix(aws.ToString(r.run.Arn), prefix) {
			runs = append(runs, r.run)
		}
	}
	start := 0
	if in.NextToken != nil {
		_, _ = fmt.Sscan(aws.ToString(in.NextToken), &start)
	}
	out := &devicefarm.ListRunsOutput{}
	if start < len(runs) {
		out.Runs = runs[start : start+1]
		if start+1 < len(runs) {
			out.NextToken = aws.String(fmt.Sprint(start + 1))
		}
	}
	return out, nil
}

func (c *fakeFarmAPI) StopRun(_ context.Context, in *devicefarm.StopRunInput,
	_ ...func(*devicefarm.Options)) (*devicefarm.StopRunOutput, error) {
	_, done, err := c.begin("StopRun")
	if err != nil {
		return nil, err
	}
	defer done()
	for _, r := range c.d.runs {
		if aws.ToString(r.run.Arn) == aws.ToString(in.Arn) {
			r.run.Status = dftypes.ExecutionStatusStopping
			c.d.stops = append(c.d.stops, aws.ToString(r.run.Name))
			copied := r.run
			return &devicefarm.StopRunOutput{Run: &copied}, nil
		}
	}
	return nil, apiErr("NotFoundException", "No run")
}

// runOf is the run ARN arn's resource path names; d is locked.
func (c *fakeFarmAPI) runOf(arn string) *fakeFarmRun {
	for _, r := range c.d.runs {
		if aws.ToString(r.run.Arn) == arn {
			return r
		}
	}
	return nil
}

func (c *fakeFarmAPI) ListJobs(_ context.Context, in *devicefarm.ListJobsInput,
	_ ...func(*devicefarm.Options)) (*devicefarm.ListJobsOutput, error) {
	_, done, err := c.begin("ListJobs")
	if err != nil {
		return nil, err
	}
	defer done()
	r := c.runOf(aws.ToString(in.Arn))
	if r == nil {
		return nil, apiErr("NotFoundException", "No run")
	}
	return &devicefarm.ListJobsOutput{Jobs: r.jobs}, nil
}

func (c *fakeFarmAPI) ListUniqueProblems(_ context.Context, in *devicefarm.ListUniqueProblemsInput,
	_ ...func(*devicefarm.Options)) (*devicefarm.ListUniqueProblemsOutput, error) {
	_, done, err := c.begin("ListUniqueProblems")
	if err != nil {
		return nil, err
	}
	defer done()
	r := c.runOf(aws.ToString(in.Arn))
	if r == nil {
		return nil, apiErr("NotFoundException", "No run")
	}
	return &devicefarm.ListUniqueProblemsOutput{UniqueProblems: r.problems}, nil
}

func (c *fakeFarmAPI) ListArtifacts(_ context.Context, in *devicefarm.ListArtifactsInput,
	_ ...func(*devicefarm.Options)) (*devicefarm.ListArtifactsOutput, error) {
	_, done, err := c.begin("ListArtifacts")
	if err != nil {
		return nil, err
	}
	defer done()
	r := c.runOf(aws.ToString(in.Arn))
	if r == nil {
		return nil, apiErr("NotFoundException", "No run")
	}
	return &devicefarm.ListArtifactsOutput{Artifacts: r.artifacts[in.Type]}, nil
}

func (c *fakeFarmAPI) GetDevicePool(_ context.Context, in *devicefarm.GetDevicePoolInput,
	_ ...func(*devicefarm.Options)) (*devicefarm.GetDevicePoolOutput, error) {
	_, done, err := c.begin("GetDevicePool")
	if err != nil {
		return nil, err
	}
	defer done()
	if !c.d.pools[aws.ToString(in.Arn)] {
		return nil, apiErr("NotFoundException", "No device pool with ARN "+aws.ToString(in.Arn))
	}
	return &devicefarm.GetDevicePoolOutput{DevicePool: &dftypes.DevicePool{Arn: in.Arn, Name: aws.String("Top phones")}}, nil
}

func (c *fakeFarmAPI) TagResource(_ context.Context, in *devicefarm.TagResourceInput,
	_ ...func(*devicefarm.Options)) (*devicefarm.TagResourceOutput, error) {
	account, done, err := c.begin("TagResource")
	if err != nil {
		return nil, err
	}
	defer done()
	p, err := c.projectOf(account, aws.ToString(in.ResourceARN))
	if err != nil {
		return nil, err
	}
	for _, t := range in.Tags {
		if len(aws.ToString(t.Key)) > 128 || len(aws.ToString(t.Value)) > 256 {
			return nil, apiErr("ArgumentException", "A tag's key is 128 characters at most, its value 256")
		}
		p.tags = slices.DeleteFunc(p.tags, func(x dftypes.Tag) bool { return aws.ToString(x.Key) == aws.ToString(t.Key) })
		if len(p.tags) == 50 {
			return nil, apiErr("TooManyTagsException", "A resource has 50 tags at most")
		}
		p.tags = append(p.tags, t)
	}
	return &devicefarm.TagResourceOutput{}, nil
}

func (c *fakeFarmAPI) UntagResource(_ context.Context, in *devicefarm.UntagResourceInput,
	_ ...func(*devicefarm.Options)) (*devicefarm.UntagResourceOutput, error) {
	account, done, err := c.begin("UntagResource")
	if err != nil {
		return nil, err
	}
	defer done()
	p, err := c.projectOf(account, aws.ToString(in.ResourceARN))
	if err != nil {
		return nil, err
	}
	p.tags = slices.DeleteFunc(p.tags, func(x dftypes.Tag) bool { return slices.Contains(in.TagKeys, aws.ToString(x.Key)) })
	return &devicefarm.UntagResourceOutput{}, nil
}

func (c *fakeFarmAPI) ListTagsForResource(_ context.Context, in *devicefarm.ListTagsForResourceInput,
	_ ...func(*devicefarm.Options)) (*devicefarm.ListTagsForResourceOutput, error) {
	account, done, err := c.begin("ListTagsForResource")
	if err != nil {
		return nil, err
	}
	defer done()
	p, err := c.projectOf(account, aws.ToString(in.ResourceARN))
	if err != nil {
		return nil, err
	}
	return &devicefarm.ListTagsForResourceOutput{Tags: slices.Clone(p.tags)}, nil
}
