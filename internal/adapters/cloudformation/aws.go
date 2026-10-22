package cloudformation

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	"github.com/aws/aws-sdk-go-v2/credentials"
	cfn "github.com/aws/aws-sdk-go-v2/service/cloudformation"
	"github.com/aws/aws-sdk-go-v2/service/ecr"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/aws/aws-sdk-go-v2/service/sts"

	"github.com/JairJosafath/c4factory/internal/model"
	"github.com/JairJosafath/c4factory/internal/ports"
)

// Keys of the credential in the target's Secret.
const (
	accessKeyIDKey     = "accessKeyId"
	secretAccessKeyKey = "secretAccessKey"
	sessionTokenKey    = "sessionToken"
)

// CloudFormationAPI is what the member calls of CloudFormation.
type CloudFormationAPI interface {
	DescribeStacks(ctx context.Context, in *cfn.DescribeStacksInput,
		opts ...func(*cfn.Options)) (*cfn.DescribeStacksOutput, error)
	DescribeStackEvents(ctx context.Context, in *cfn.DescribeStackEventsInput,
		opts ...func(*cfn.Options)) (*cfn.DescribeStackEventsOutput, error)
	CreateChangeSet(ctx context.Context, in *cfn.CreateChangeSetInput,
		opts ...func(*cfn.Options)) (*cfn.CreateChangeSetOutput, error)
	DescribeChangeSet(ctx context.Context, in *cfn.DescribeChangeSetInput,
		opts ...func(*cfn.Options)) (*cfn.DescribeChangeSetOutput, error)
	ExecuteChangeSet(ctx context.Context, in *cfn.ExecuteChangeSetInput,
		opts ...func(*cfn.Options)) (*cfn.ExecuteChangeSetOutput, error)
	DeleteChangeSet(ctx context.Context, in *cfn.DeleteChangeSetInput,
		opts ...func(*cfn.Options)) (*cfn.DeleteChangeSetOutput, error)
	UpdateTerminationProtection(ctx context.Context, in *cfn.UpdateTerminationProtectionInput,
		opts ...func(*cfn.Options)) (*cfn.UpdateTerminationProtectionOutput, error)
}

// S3API is what the member calls of S3: it publishes assets.
type S3API interface {
	HeadObject(ctx context.Context, in *s3.HeadObjectInput, opts ...func(*s3.Options)) (*s3.HeadObjectOutput, error)
	PutObject(ctx context.Context, in *s3.PutObjectInput, opts ...func(*s3.Options)) (*s3.PutObjectOutput, error)
}

// ECRAPI is what the member calls of ECR: it copies the release's image to
// the bootstrap's repository for a stack that runs it.
type ECRAPI interface {
	GetAuthorizationToken(ctx context.Context, in *ecr.GetAuthorizationTokenInput,
		opts ...func(*ecr.Options)) (*ecr.GetAuthorizationTokenOutput, error)
	DescribeImages(ctx context.Context, in *ecr.DescribeImagesInput,
		opts ...func(*ecr.Options)) (*ecr.DescribeImagesOutput, error)
}

// STSAPI is what the member calls of STS: who the credential is, and the
// bootstrap roles it assumes.
type STSAPI interface {
	GetCallerIdentity(ctx context.Context, in *sts.GetCallerIdentityInput,
		opts ...func(*sts.Options)) (*sts.GetCallerIdentityOutput, error)
	AssumeRole(ctx context.Context, in *sts.AssumeRoleInput, opts ...func(*sts.Options)) (*sts.AssumeRoleOutput, error)
}

// SSMAPI is what the member calls of SSM: the bootstrap version.
type SSMAPI interface {
	GetParameter(ctx context.Context, in *ssm.GetParameterInput, opts ...func(*ssm.Options)) (*ssm.GetParameterOutput,
		error)
}

// Clients are the AWS clients of one credential and region.
type Clients struct {
	CloudFormation CloudFormationAPI
	S3             S3API
	ECR            ECRAPI
	STS            STSAPI
	SSM            SSMAPI
}

// NewClients builds the clients of cfg: AWSClients on AWS, fakes in tests.
type NewClients func(cfg aws.Config) Clients

// AWSClients are the SDK's clients of cfg.
func AWSClients(cfg aws.Config) Clients {
	return Clients{CloudFormation: cfn.NewFromConfig(cfg), S3: streamingS3{s3.NewFromConfig(cfg)},
		ECR: ecr.NewFromConfig(cfg), STS: sts.NewFromConfig(cfg), SSM: ssm.NewFromConfig(cfg)}
}

// streamingS3 puts an object from a body it can't seek, a release file
// streamed from the Git host: the payload is sent unsigned (over TLS), with
// its length, so the SDK needn't read it twice.
type streamingS3 struct{ *s3.Client }

func (c streamingS3) PutObject(ctx context.Context, in *s3.PutObjectInput,
	opts ...func(*s3.Options)) (*s3.PutObjectOutput, error) {
	opts = append(opts, s3.WithAPIOptions(v4.SwapComputePayloadSHA256ForUnsignedPayloadMiddleware))
	return c.Client.PutObject(ctx, in, opts...)
}

// config is an aws.Config of region and static credentials: no shared
// config file, environment or instance role is read.
func config(region string, creds aws.Credentials) aws.Config {
	return aws.Config{Region: region,
		Credentials: credentials.NewStaticCredentialsProvider(creds.AccessKeyID, creds.SecretAccessKey,
			creds.SessionToken),
		// A streamed body can't be read twice for a checksum.
		RequestChecksumCalculation: aws.RequestChecksumCalculationWhenRequired}
}

// targetConfig is the config of the target's own credential, in its region.
func targetConfig(t ports.Target) (aws.Config, error) {
	region := strings.TrimSpace(t.Settings[model.CloudFormationSettingRegion])
	if region == "" {
		return aws.Config{}, fmt.Errorf("deploy target %s names no region", t.Name)
	}
	key := strings.TrimSpace(string(t.Secret[accessKeyIDKey]))
	secret := strings.TrimSpace(string(t.Secret[secretAccessKeyKey]))
	if key == "" || secret == "" {
		return aws.Config{}, &ports.Error{Kind: ports.ErrCredentialRefused,
			Cause: fmt.Errorf("the Secret of deploy target %s lacks %s or %s", t.Name, accessKeyIDKey,
				secretAccessKeyKey)}
	}
	return config(region, aws.Credentials{AccessKeyID: key, SecretAccessKey: secret,
		SessionToken: strings.TrimSpace(string(t.Secret[sessionTokenKey]))}), nil
}

var sessionNameUnsafe = regexp.MustCompile(`[^\w+=,.@-]`)

// sessionName is the role session name of target: c4factory-<namespace>-<name>,
// in STS's characters and length.
func sessionName(target string) string {
	n := sessionNameUnsafe.ReplaceAllString("c4factory-"+strings.ReplaceAll(target, "/", "-"), "-")
	if len(n) > 64 {
		n = n[:64]
	}
	return n
}

// identity is who a target's credential is.
type identity struct {
	Account, Arn, Partition string
}

// session is a target's credential at work: its config, clients and
// identity, and the roles it assumed, by ARN.
type session struct {
	target  ports.Target
	cfg     aws.Config
	clients Clients
	id      identity
	newC    NewClients
	roles   map[string]Clients
}

// whoAmI reads the credential's identity and refuses an account other than
// the target's account setting.
func (s *session) whoAmI(ctx context.Context) error {
	out, err := s.clients.STS.GetCallerIdentity(ctx, &sts.GetCallerIdentityInput{})
	if err != nil {
		return portError(err)
	}
	s.id = identity{Account: aws.ToString(out.Account), Arn: aws.ToString(out.Arn)}
	if parts := strings.SplitN(s.id.Arn, ":", 3); len(parts) == 3 && parts[1] != "" {
		s.id.Partition = parts[1]
	} else {
		s.id.Partition = "aws"
	}
	if want := strings.TrimSpace(s.target.Settings[model.CloudFormationSettingAccount]); want != "" &&
		want != s.id.Account {
		return &ports.Error{Kind: ports.ErrCredentialRefused,
			Cause: fmt.Errorf("the credential of deploy target %s is of AWS account %s, the target names account %s",
				s.target.Name, s.id.Account, want)}
	}
	return nil
}

// assume returns the clients of role, assumed once per session.
func (s *session) assume(ctx context.Context, role, region string) (Clients, error) {
	key := role + "@" + region
	if c, ok := s.roles[key]; ok {
		return c, nil
	}
	out, err := s.clients.STS.AssumeRole(ctx, &sts.AssumeRoleInput{RoleArn: aws.String(role),
		RoleSessionName: aws.String(sessionName(s.target.Name))})
	if err != nil {
		return Clients{}, portError(fmt.Errorf("assuming role %s: %w", role, err))
	}
	if out.Credentials == nil {
		return Clients{}, fmt.Errorf("assuming role %s: STS returned no credentials", role)
	}
	c := s.newC(config(region, aws.Credentials{AccessKeyID: aws.ToString(out.Credentials.AccessKeyId),
		SecretAccessKey: aws.ToString(out.Credentials.SecretAccessKey),
		SessionToken:    aws.ToString(out.Credentials.SessionToken)}))
	if s.roles == nil {
		s.roles = map[string]Clients{}
	}
	s.roles[key] = c
	return c, nil
}

// env is where an assembly's stack ships: the credential's partition and
// account, else the stack's own, and the target's region (Ship refuses a
// stack of another region).
func (s *session) env(stack model.CDKStack) model.CDKEnv {
	e := model.CDKEnv{Partition: s.id.Partition, Account: s.id.Account, Region: s.cfg.Region}
	if stack.Account != "" {
		e.Account = stack.Account
	}
	return e
}

// in returns the credential's clients in region.
func (s *session) in(region string) Clients {
	if region == s.cfg.Region {
		return s.clients
	}
	cfg := s.cfg.Copy()
	cfg.Region = region
	return s.newC(cfg)
}
