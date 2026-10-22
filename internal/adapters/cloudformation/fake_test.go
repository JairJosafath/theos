package cloudformation

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	cfn "github.com/aws/aws-sdk-go-v2/service/cloudformation"
	"github.com/aws/aws-sdk-go-v2/service/cloudformation/types"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	ststypes "github.com/aws/aws-sdk-go-v2/service/sts/types"
	"github.com/aws/smithy-go"
)

// fakeAWS is an in-memory AWS of accounts, each with CloudFormation stacks
// and change sets, S3 objects, SSM parameters and the CDK bootstrap roles.
// A caller is found from the config's credentials: a user's access key, or
// role:<arn> for a role it assumed. An executed change set moves its stack
// to *_IN_PROGRESS; the stack finishes on its next read.
type fakeAWS struct {
	mu       sync.Mutex
	keys     map[string]*fakeAccount // access key → its user's account
	accounts map[string]*fakeAccount
	// bootstrapVersion is what a finished CDKToolkit writes to SSM.
	bootstrapVersion int
	// down fails every call before it reaches AWS.
	down bool
	seq  int
}

type fakeAccount struct {
	id string
	// mayBootstrap says the user may create CDKToolkit.
	mayBootstrap bool
	stacks       map[string]*fakeStack
	objects      map[string][]byte // bucket/key
	puts         []fakeCall
	params       map[string]string
	changeSets   []fakeChangeSetCall
	executions   []fakeExecution
	// fail rolls the next execution of a stack back; empty makes the next
	// change set of a stack change nothing.
	fail, empty map[string]bool
}

type fakeCall struct{ What, Caller string }

type fakeChangeSetCall struct {
	In     cfn.CreateChangeSetInput
	Caller string
}

type fakeExecution struct{ Stack, App, Token, Caller string }

type fakeStack struct {
	name, id, status, reason string
	created                  time.Time
	tags, params, outputs    map[string]string
	protected                bool
	// events are the stack-level events, newest last.
	events []types.StackEvent
	// next is the status the stack moves to on its next read, with token.
	next, nextReason, nextToken string
	prevTags, prevParams        map[string]string
	changeSets                  map[string]*fakeChangeSet
}

type fakeChangeSet struct {
	id, name   string
	in         cfn.CreateChangeSetInput
	status     types.ChangeSetStatus
	reason     string
	exec       types.ExecutionStatus
	willBeVoid bool
}

func newFakeAWS(bootstrapVersion int) *fakeAWS {
	return &fakeAWS{keys: map[string]*fakeAccount{}, accounts: map[string]*fakeAccount{},
		bootstrapVersion: bootstrapVersion}
}

// account adds an account whose user's access key is key.
func (f *fakeAWS) account(key string, mayBootstrap bool) *fakeAccount {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.seq++
	a := &fakeAccount{id: fmt.Sprintf("%012d", 100000000000+f.seq), mayBootstrap: mayBootstrap,
		stacks: map[string]*fakeStack{}, objects: map[string][]byte{}, params: map[string]string{},
		fail: map[string]bool{}, empty: map[string]bool{}}
	f.accounts[a.id] = a
	f.keys[key] = a
	return a
}

// bootstrap gives a the CDKToolkit stack and bootstrap version v.
func (f *fakeAWS) bootstrap(a *fakeAccount, v int, params map[string]string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.seq++
	a.stacks[toolkitStack] = &fakeStack{name: toolkitStack, id: "stack/CDKToolkit/" + strconv.Itoa(f.seq),
		status: "UPDATE_COMPLETE", created: time.Unix(int64(f.seq), 0), params: params, tags: map[string]string{},
		outputs: map[string]string{}, changeSets: map[string]*fakeChangeSet{}}
	a.params[defaultBootstrapParam] = strconv.Itoa(v)
}

func (f *fakeAWS) clients(cfg aws.Config) Clients {
	return Clients{CloudFormation: &fakeCFN{f, cfg}, S3: &fakeS3{f, cfg}, STS: &fakeSTS{f, cfg},
		SSM: &fakeSSM{f, cfg}}
}

func apiErr(code, msg string) error { return &smithy.GenericAPIError{Code: code, Message: msg} }

// caller is the account and ARN cfg's credentials are, locked.
func (f *fakeAWS) caller(cfg aws.Config) (*fakeAccount, string, error) {
	if f.down {
		return nil, "", errors.New("dial tcp: lookup sts.amazonaws.com: no such host")
	}
	creds, err := cfg.Credentials.Retrieve(context.Background())
	if err != nil {
		return nil, "", err
	}
	if role, ok := strings.CutPrefix(creds.AccessKeyID, "role:"); ok {
		parts := strings.Split(role, ":")
		if len(parts) < 6 || f.accounts[parts[4]] == nil {
			return nil, "", apiErr("InvalidClientTokenId", "no such role session")
		}
		return f.accounts[parts[4]], role, nil
	}
	a, ok := f.keys[creds.AccessKeyID]
	if !ok {
		return nil, "", apiErr("InvalidClientTokenId", "The security token included in the request is invalid.")
	}
	return a, "arn:aws:iam::" + a.id + ":user/deployer", nil
}

// bootstrapped says a's CDKToolkit stands, so its roles exist.
func (a *fakeAccount) bootstrapped() bool {
	st := a.stacks[toolkitStack]
	return st != nil && (st.status == "CREATE_COMPLETE" || st.status == "UPDATE_COMPLETE" ||
		st.status == "UPDATE_ROLLBACK_COMPLETE")
}

// read finishes what CloudFormation started on st.
func (f *fakeAWS) read(a *fakeAccount, st *fakeStack) {
	if st.next == "" {
		return
	}
	st.status, st.reason = st.next, st.nextReason
	if strings.HasPrefix(st.next, "UPDATE_ROLLBACK") {
		st.tags, st.params = st.prevTags, st.prevParams
	}
	st.events = append(st.events, stackEvent(st, st.next, st.nextToken))
	st.next, st.nextReason, st.nextToken = "", "", ""
	if st.name == toolkitStack && strings.HasSuffix(st.status, "_COMPLETE") && !strings.Contains(st.status, "ROLLBACK") {
		a.params[defaultBootstrapParam] = strconv.Itoa(f.bootstrapVersion)
	}
}

func stackEvent(st *fakeStack, status, token string) types.StackEvent {
	e := types.StackEvent{LogicalResourceId: aws.String(st.name), ResourceType: aws.String(stackResourceType),
		ResourceStatus: types.ResourceStatus(status), StackName: aws.String(st.name)}
	if token != "" {
		e.ClientRequestToken = aws.String(token)
	}
	return e
}

func (st *fakeStack) sdk() types.Stack {
	out := types.Stack{StackName: aws.String(st.name), StackId: aws.String(st.id),
		StackStatus: types.StackStatus(st.status), CreationTime: aws.Time(st.created),
		EnableTerminationProtection: aws.Bool(st.protected)}
	if st.reason != "" {
		out.StackStatusReason = aws.String(st.reason)
	}
	for _, k := range sortedKeys(st.tags) {
		out.Tags = append(out.Tags, types.Tag{Key: aws.String(k), Value: aws.String(st.tags[k])})
	}
	for _, k := range sortedKeys(st.params) {
		out.Parameters = append(out.Parameters, types.Parameter{ParameterKey: aws.String(k),
			ParameterValue: aws.String(st.params[k])})
	}
	for _, k := range sortedKeys(st.outputs) {
		out.Outputs = append(out.Outputs, types.Output{OutputKey: aws.String(k), OutputValue: aws.String(st.outputs[k])})
	}
	return out
}

func sortedKeys[V any](m map[string]V) []string { return slices.Sorted(maps.Keys(m)) }

type fakeCFN struct {
	f   *fakeAWS
	cfg aws.Config
}

func (c *fakeCFN) begin() (*fakeAccount, string, func(), error) {
	c.f.mu.Lock()
	a, who, err := c.f.caller(c.cfg)
	if err != nil {
		c.f.mu.Unlock()
		return nil, "", nil, err
	}
	return a, who, c.f.mu.Unlock, nil
}

// stack is the stack name or id names, live, read.
func (c *fakeCFN) stack(a *fakeAccount, name string) *fakeStack {
	for _, st := range a.stacks {
		if st.name == name || st.id == name {
			c.f.read(a, st)
			return st
		}
	}
	return nil
}

func notExist(name string) error {
	return apiErr("ValidationError", "Stack with id "+name+" does not exist")
}

func (c *fakeCFN) DescribeStacks(_ context.Context, in *cfn.DescribeStacksInput,
	_ ...func(*cfn.Options)) (*cfn.DescribeStacksOutput, error) {
	a, _, done, err := c.begin()
	if err != nil {
		return nil, err
	}
	defer done()
	if in.StackName != nil {
		st := c.stack(a, *in.StackName)
		if st == nil {
			return nil, notExist(*in.StackName)
		}
		return &cfn.DescribeStacksOutput{Stacks: []types.Stack{st.sdk()}}, nil
	}
	// Pages of two, by name, to exercise NextToken.
	sorted := sortedKeys(a.stacks)
	start, _ := strconv.Atoi(aws.ToString(in.NextToken))
	out := &cfn.DescribeStacksOutput{}
	for i := start; i < len(sorted) && i < start+2; i++ {
		st := a.stacks[sorted[i]]
		c.f.read(a, st)
		out.Stacks = append(out.Stacks, st.sdk())
	}
	if start+2 < len(sorted) {
		out.NextToken = aws.String(strconv.Itoa(start + 2))
	}
	return out, nil
}

func (c *fakeCFN) DescribeStackEvents(_ context.Context, in *cfn.DescribeStackEventsInput,
	_ ...func(*cfn.Options)) (*cfn.DescribeStackEventsOutput, error) {
	a, _, done, err := c.begin()
	if err != nil {
		return nil, err
	}
	defer done()
	st := c.stack(a, aws.ToString(in.StackName))
	if st == nil {
		return nil, notExist(aws.ToString(in.StackName))
	}
	out := &cfn.DescribeStackEventsOutput{}
	for _, e := range slices.Backward(st.events) {
		// A resource's event before each stack-level one, as CloudFormation
		// interleaves them.
		out.StackEvents = append(out.StackEvents, types.StackEvent{LogicalResourceId: aws.String("Handler"),
			ResourceType: aws.String("AWS::Lambda::Function"), ClientRequestToken: aws.String("other")},
			e)
	}
	return out, nil
}

// mayDeploy says who may change stack: the user for CDKToolkit when it may
// bootstrap, the account's deploy role for the others.
func mayDeploy(a *fakeAccount, who, stack string) bool {
	if stack == toolkitStack {
		return a.mayBootstrap && strings.HasSuffix(who, ":user/deployer")
	}
	return strings.Contains(who, ":role/cdk-hnb659fds-deploy-role-"+a.id+"-")
}

func (c *fakeCFN) CreateChangeSet(_ context.Context, in *cfn.CreateChangeSetInput,
	_ ...func(*cfn.Options)) (*cfn.CreateChangeSetOutput, error) {
	a, who, done, err := c.begin()
	if err != nil {
		return nil, err
	}
	defer done()
	name := aws.ToString(in.StackName)
	if !mayDeploy(a, who, name) {
		return nil, apiErr("AccessDenied", who+" is not authorized to perform cloudformation:CreateChangeSet")
	}
	a.changeSets = append(a.changeSets, fakeChangeSetCall{In: *in, Caller: who})
	st := c.stack(a, name)
	switch {
	case st == nil && in.ChangeSetType != types.ChangeSetTypeCreate:
		return nil, notExist(name)
	case st == nil:
		c.f.seq++
		st = &fakeStack{name: name, id: fmt.Sprintf("arn:aws:cloudformation:eu-west-1:%s:stack/%s/%d", a.id, name,
			c.f.seq), status: "REVIEW_IN_PROGRESS", created: time.Unix(int64(c.f.seq), 0), tags: map[string]string{},
			params: map[string]string{}, outputs: map[string]string{}, changeSets: map[string]*fakeChangeSet{}}
		st.events = append(st.events, stackEvent(st, "REVIEW_IN_PROGRESS", ""))
		a.stacks[name] = st
	case in.ChangeSetType == types.ChangeSetTypeCreate && st.status != "REVIEW_IN_PROGRESS":
		return nil, apiErr("ValidationError", "Stack ["+name+"] already exists and cannot be created again with the "+
			"changeSet ["+aws.ToString(in.ChangeSetName)+"].")
	}
	if _, ok := st.changeSets[aws.ToString(in.ChangeSetName)]; ok {
		return nil, apiErr("AlreadyExistsException", "ChangeSet "+aws.ToString(in.ChangeSetName)+" already exists")
	}
	c.f.seq++
	cs := &fakeChangeSet{id: fmt.Sprintf("arn:aws:cloudformation:eu-west-1:%s:changeSet/%s/%d", a.id,
		aws.ToString(in.ChangeSetName), c.f.seq), name: aws.ToString(in.ChangeSetName), in: *in,
		status: types.ChangeSetStatusCreatePending, exec: types.ExecutionStatusUnavailable, willBeVoid: a.empty[name]}
	delete(a.empty, name)
	st.changeSets[cs.name] = cs
	return &cfn.CreateChangeSetOutput{Id: aws.String(cs.id), StackId: aws.String(st.id)}, nil
}

func (c *fakeCFN) changeSet(a *fakeAccount, stack, name string) (*fakeStack, *fakeChangeSet) {
	st := c.stack(a, stack)
	if st == nil {
		return nil, nil
	}
	for _, cs := range st.changeSets {
		if cs.name == name || cs.id == name {
			return st, cs
		}
	}
	return st, nil
}

func (c *fakeCFN) DescribeChangeSet(_ context.Context, in *cfn.DescribeChangeSetInput,
	_ ...func(*cfn.Options)) (*cfn.DescribeChangeSetOutput, error) {
	a, _, done, err := c.begin()
	if err != nil {
		return nil, err
	}
	defer done()
	_, cs := c.changeSet(a, aws.ToString(in.StackName), aws.ToString(in.ChangeSetName))
	if cs == nil {
		return nil, apiErr("ChangeSetNotFound", "ChangeSet ["+aws.ToString(in.ChangeSetName)+"] does not exist")
	}
	out := &cfn.DescribeChangeSetOutput{ChangeSetId: aws.String(cs.id), ChangeSetName: aws.String(cs.name),
		Status: cs.status, ExecutionStatus: cs.exec}
	if cs.reason != "" {
		out.StatusReason = aws.String(cs.reason)
	}
	// It is ready on the read after the one that saw it pending.
	if cs.status == types.ChangeSetStatusCreatePending {
		if cs.willBeVoid {
			cs.status, cs.exec = types.ChangeSetStatusFailed, types.ExecutionStatusUnavailable
			cs.reason = "The submitted information didn't contain changes. Submit different information to create " +
				"a change set."
		} else {
			cs.status, cs.exec = types.ChangeSetStatusCreateComplete, types.ExecutionStatusAvailable
		}
	}
	return out, nil
}

func (c *fakeCFN) ExecuteChangeSet(_ context.Context, in *cfn.ExecuteChangeSetInput,
	_ ...func(*cfn.Options)) (*cfn.ExecuteChangeSetOutput, error) {
	a, who, done, err := c.begin()
	if err != nil {
		return nil, err
	}
	defer done()
	name := aws.ToString(in.StackName)
	if !mayDeploy(a, who, name) {
		return nil, apiErr("AccessDenied", who+" is not authorized to perform cloudformation:ExecuteChangeSet")
	}
	st, cs := c.changeSet(a, name, aws.ToString(in.ChangeSetName))
	if cs == nil {
		return nil, apiErr("ChangeSetNotFound", "ChangeSet ["+aws.ToString(in.ChangeSetName)+"] does not exist")
	}
	if cs.exec != types.ExecutionStatusAvailable {
		return nil, apiErr("InvalidChangeSetStatus", "ChangeSet is "+string(cs.exec))
	}
	token := aws.ToString(in.ClientRequestToken)
	tags := map[string]string{}
	for _, t := range cs.in.Tags {
		tags[aws.ToString(t.Key)] = aws.ToString(t.Value)
	}
	a.executions = append(a.executions, fakeExecution{Stack: name, App: tags["c4factory.io/app"], Token: token,
		Caller: who})
	st.prevTags, st.prevParams = st.tags, st.params
	st.tags, st.params = tags, map[string]string{}
	for _, p := range cs.in.Parameters {
		if aws.ToBool(p.UsePreviousValue) {
			st.params[aws.ToString(p.ParameterKey)] = st.prevParams[aws.ToString(p.ParameterKey)]
		} else {
			st.params[aws.ToString(p.ParameterKey)] = aws.ToString(p.ParameterValue)
		}
	}
	create := cs.in.ChangeSetType == types.ChangeSetTypeCreate
	st.status, st.next, st.nextToken = "UPDATE_IN_PROGRESS", "UPDATE_COMPLETE", token
	if create {
		st.status, st.next = "CREATE_IN_PROGRESS", "CREATE_COMPLETE"
	}
	switch {
	case a.fail[name] && create:
		st.next, st.nextReason = "ROLLBACK_COMPLETE", "The following resource(s) failed to create: [Handler]."
	case a.fail[name]:
		st.next, st.nextReason = "UPDATE_ROLLBACK_COMPLETE", "The following resource(s) failed to update: [Handler]."
	}
	delete(a.fail, name)
	if name != toolkitStack {
		st.outputs = map[string]string{"EntryPoint": name + ".example"}
	}
	st.events = append(st.events, stackEvent(st, st.status, token))
	// CloudFormation removes a stack's change sets once one is executed.
	st.changeSets = map[string]*fakeChangeSet{}
	return &cfn.ExecuteChangeSetOutput{}, nil
}

func (c *fakeCFN) DeleteChangeSet(_ context.Context, in *cfn.DeleteChangeSetInput,
	_ ...func(*cfn.Options)) (*cfn.DeleteChangeSetOutput, error) {
	a, _, done, err := c.begin()
	if err != nil {
		return nil, err
	}
	defer done()
	st, cs := c.changeSet(a, aws.ToString(in.StackName), aws.ToString(in.ChangeSetName))
	if cs == nil {
		return nil, apiErr("ChangeSetNotFound", "ChangeSet ["+aws.ToString(in.ChangeSetName)+"] does not exist")
	}
	delete(st.changeSets, cs.name)
	return &cfn.DeleteChangeSetOutput{}, nil
}

func (c *fakeCFN) UpdateTerminationProtection(_ context.Context, in *cfn.UpdateTerminationProtectionInput,
	_ ...func(*cfn.Options)) (*cfn.UpdateTerminationProtectionOutput, error) {
	a, who, done, err := c.begin()
	if err != nil {
		return nil, err
	}
	defer done()
	name := aws.ToString(in.StackName)
	if !mayDeploy(a, who, name) {
		return nil, apiErr("AccessDenied", who+" may not protect "+name)
	}
	st := c.stack(a, name)
	if st == nil {
		return nil, notExist(name)
	}
	st.protected = aws.ToBool(in.EnableTerminationProtection)
	return &cfn.UpdateTerminationProtectionOutput{}, nil
}

type fakeS3 struct {
	f   *fakeAWS
	cfg aws.Config
}

func (c *fakeS3) HeadObject(_ context.Context, in *s3.HeadObjectInput,
	_ ...func(*s3.Options)) (*s3.HeadObjectOutput, error) {
	c.f.mu.Lock()
	defer c.f.mu.Unlock()
	a, _, err := c.f.caller(c.cfg)
	if err != nil {
		return nil, err
	}
	b, ok := a.objects[aws.ToString(in.Bucket)+"/"+aws.ToString(in.Key)]
	if !ok {
		return nil, apiErr("NotFound", "Not Found")
	}
	return &s3.HeadObjectOutput{ContentLength: aws.Int64(int64(len(b)))}, nil
}

func (c *fakeS3) PutObject(_ context.Context, in *s3.PutObjectInput,
	_ ...func(*s3.Options)) (*s3.PutObjectOutput, error) {
	body, err := io.ReadAll(in.Body)
	if err != nil {
		return nil, err
	}
	c.f.mu.Lock()
	defer c.f.mu.Unlock()
	a, who, err := c.f.caller(c.cfg)
	if err != nil {
		return nil, err
	}
	if !strings.Contains(who, ":role/cdk-hnb659fds-file-publishing-role-"+a.id+"-") {
		return nil, apiErr("AccessDenied", who+" may not put objects")
	}
	if in.ContentLength == nil || *in.ContentLength != int64(len(body)) {
		return nil, apiErr("IncompleteBody", "the body's length isn't ContentLength")
	}
	key := aws.ToString(in.Bucket) + "/" + aws.ToString(in.Key)
	a.objects[key] = body
	a.puts = append(a.puts, fakeCall{What: key, Caller: who})
	return &s3.PutObjectOutput{}, nil
}

type fakeSTS struct {
	f   *fakeAWS
	cfg aws.Config
}

func (c *fakeSTS) GetCallerIdentity(context.Context, *sts.GetCallerIdentityInput,
	...func(*sts.Options)) (*sts.GetCallerIdentityOutput, error) {
	c.f.mu.Lock()
	defer c.f.mu.Unlock()
	a, who, err := c.f.caller(c.cfg)
	if err != nil {
		return nil, err
	}
	return &sts.GetCallerIdentityOutput{Account: aws.String(a.id), Arn: aws.String(who),
		UserId: aws.String("AIDAFAKE")}, nil
}

func (c *fakeSTS) AssumeRole(_ context.Context, in *sts.AssumeRoleInput,
	_ ...func(*sts.Options)) (*sts.AssumeRoleOutput, error) {
	c.f.mu.Lock()
	defer c.f.mu.Unlock()
	a, who, err := c.f.caller(c.cfg)
	if err != nil {
		return nil, err
	}
	role := aws.ToString(in.RoleArn)
	session := aws.ToString(in.RoleSessionName)
	if !a.bootstrapped() || !strings.HasPrefix(role, "arn:aws:iam::"+a.id+":role/cdk-hnb659fds-") ||
		len(session) < 2 || len(session) > 64 {
		return nil, apiErr("AccessDenied", who+" is not authorized to perform sts:AssumeRole on "+role)
	}
	return &sts.AssumeRoleOutput{Credentials: &ststypes.Credentials{AccessKeyId: aws.String("role:" + role),
		SecretAccessKey: aws.String("secret"), SessionToken: aws.String(session),
		Expiration: aws.Time(time.Now().Add(time.Hour))}}, nil
}

type fakeSSM struct {
	f   *fakeAWS
	cfg aws.Config
}

func (c *fakeSSM) GetParameter(_ context.Context, in *ssm.GetParameterInput,
	_ ...func(*ssm.Options)) (*ssm.GetParameterOutput, error) {
	c.f.mu.Lock()
	defer c.f.mu.Unlock()
	a, _, err := c.f.caller(c.cfg)
	if err != nil {
		return nil, err
	}
	v, ok := a.params[aws.ToString(in.Name)]
	if !ok {
		return nil, apiErr("ParameterNotFound", "")
	}
	return &ssm.GetParameterOutput{Parameter: &ssmtypes.Parameter{Name: in.Name, Value: aws.String(v)}}, nil
}
