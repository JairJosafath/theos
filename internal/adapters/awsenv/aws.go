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
	"errors"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/pricing"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/aws/smithy-go"

	"github.com/JairJosafath/theos/internal/model"
	"github.com/JairJosafath/theos/internal/ports"
)

// Keys of the credential in the environment's Secret, as a cloudFormation
// DeployTarget's (T9).
const (
	accessKeyIDKey     = "accessKeyId"
	secretAccessKeyKey = "secretAccessKey"
	sessionTokenKey    = "sessionToken"
)

// EC2API is what the member calls of EC2: the machines, the security group
// that fences them, and a Mac's dedicated hosts.
type EC2API interface {
	RunInstances(ctx context.Context, in *ec2.RunInstancesInput, opts ...func(*ec2.Options)) (*ec2.RunInstancesOutput,
		error)
	DescribeInstances(ctx context.Context, in *ec2.DescribeInstancesInput,
		opts ...func(*ec2.Options)) (*ec2.DescribeInstancesOutput, error)
	TerminateInstances(ctx context.Context, in *ec2.TerminateInstancesInput,
		opts ...func(*ec2.Options)) (*ec2.TerminateInstancesOutput, error)
	ModifyInstanceMetadataOptions(ctx context.Context, in *ec2.ModifyInstanceMetadataOptionsInput,
		opts ...func(*ec2.Options)) (*ec2.ModifyInstanceMetadataOptionsOutput, error)
	DescribeSubnets(ctx context.Context, in *ec2.DescribeSubnetsInput,
		opts ...func(*ec2.Options)) (*ec2.DescribeSubnetsOutput, error)
	DescribeSecurityGroups(ctx context.Context, in *ec2.DescribeSecurityGroupsInput,
		opts ...func(*ec2.Options)) (*ec2.DescribeSecurityGroupsOutput, error)
	CreateSecurityGroup(ctx context.Context, in *ec2.CreateSecurityGroupInput,
		opts ...func(*ec2.Options)) (*ec2.CreateSecurityGroupOutput, error)
	DeleteSecurityGroup(ctx context.Context, in *ec2.DeleteSecurityGroupInput,
		opts ...func(*ec2.Options)) (*ec2.DeleteSecurityGroupOutput, error)
	AuthorizeSecurityGroupIngress(ctx context.Context, in *ec2.AuthorizeSecurityGroupIngressInput,
		opts ...func(*ec2.Options)) (*ec2.AuthorizeSecurityGroupIngressOutput, error)
	RevokeSecurityGroupIngress(ctx context.Context, in *ec2.RevokeSecurityGroupIngressInput,
		opts ...func(*ec2.Options)) (*ec2.RevokeSecurityGroupIngressOutput, error)
	AuthorizeSecurityGroupEgress(ctx context.Context, in *ec2.AuthorizeSecurityGroupEgressInput,
		opts ...func(*ec2.Options)) (*ec2.AuthorizeSecurityGroupEgressOutput, error)
	RevokeSecurityGroupEgress(ctx context.Context, in *ec2.RevokeSecurityGroupEgressInput,
		opts ...func(*ec2.Options)) (*ec2.RevokeSecurityGroupEgressOutput, error)
	// An EC2 Mac's dedicated hosts (mac.go).
	AllocateHosts(ctx context.Context, in *ec2.AllocateHostsInput, opts ...func(*ec2.Options)) (*ec2.AllocateHostsOutput,
		error)
	DescribeHosts(ctx context.Context, in *ec2.DescribeHostsInput, opts ...func(*ec2.Options)) (*ec2.DescribeHostsOutput,
		error)
	ReleaseHosts(ctx context.Context, in *ec2.ReleaseHostsInput, opts ...func(*ec2.Options)) (*ec2.ReleaseHostsOutput,
		error)
	CreateTags(ctx context.Context, in *ec2.CreateTagsInput, opts ...func(*ec2.Options)) (*ec2.CreateTagsOutput, error)
}

// STSAPI is what the member calls of STS: who the credential is.
type STSAPI interface {
	GetCallerIdentity(ctx context.Context, in *sts.GetCallerIdentityInput,
		opts ...func(*sts.Options)) (*sts.GetCallerIdentityOutput, error)
}

// PricingAPI is what the member calls of the AWS Price List: the on-demand
// price a lease's cost is counted at.
type PricingAPI interface {
	GetProducts(ctx context.Context, in *pricing.GetProductsInput,
		opts ...func(*pricing.Options)) (*pricing.GetProductsOutput, error)
}

// Clients are the AWS clients of one credential and region.
type Clients struct {
	EC2     EC2API
	STS     STSAPI
	Pricing PricingAPI
}

// NewClients builds the clients of cfg: AWSClients on AWS, fakes in tests.
type NewClients func(cfg aws.Config) Clients

// pricingRegion is where the Price List API answers for every region's
// prices.
const pricingRegion = "us-east-1"

// AWSClients are the SDK's clients of cfg; the Price List's in
// pricingRegion.
func AWSClients(cfg aws.Config) Clients {
	prices := cfg.Copy()
	prices.Region = pricingRegion
	return Clients{EC2: ec2.NewFromConfig(cfg), STS: sts.NewFromConfig(cfg), Pricing: pricing.NewFromConfig(prices)}
}

// config is the aws.Config of env's own credential, in its region: no shared
// config file, environment or instance role is read.
func config(env ports.Environment) (aws.Config, error) {
	region := strings.TrimSpace(env.AWS.Region)
	if region == "" {
		return aws.Config{}, fmt.Errorf("test environment %s/%s names no region", env.Namespace, env.Name)
	}
	if missing := model.TestEnvMissingKeys(model.TestEnvMemberAWS, env.Secret); len(missing) > 0 {
		return aws.Config{}, &ports.Error{Kind: ports.ErrCredentialRefused,
			Cause: fmt.Errorf("the Secret of test environment %s/%s lacks %s", env.Namespace, env.Name,
				strings.Join(missing, " and "))}
	}
	return aws.Config{Region: region, Credentials: credentials.NewStaticCredentialsProvider(
		strings.TrimSpace(string(env.Secret[accessKeyIDKey])), strings.TrimSpace(string(env.Secret[secretAccessKeyKey])),
		strings.TrimSpace(string(env.Secret[sessionTokenKey])))}, nil
}

// session is an environment's credential at work.
type session struct {
	env     ports.Environment
	clients Clients
}

// whoAmI refuses a credential AWS doesn't take, and one of an account other
// than the environment's account setting, naming both.
func (s *session) whoAmI(ctx context.Context) error {
	out, err := s.clients.STS.GetCallerIdentity(ctx, &sts.GetCallerIdentityInput{})
	if err != nil {
		return portError(err)
	}
	if want, got := strings.TrimSpace(s.env.AWS.Account), aws.ToString(out.Account); want != "" && want != got {
		return &ports.Error{Kind: ports.ErrCredentialRefused,
			Cause: fmt.Errorf("the credential of test environment %s/%s is of AWS account %s, the environment "+
				"names account %s", s.env.Namespace, s.env.Name, got, want)}
	}
	return nil
}

// AWS error codes of a credential AWS refuses, or that may not do the call.
var refusedCodes = map[string]bool{
	"InvalidClientTokenId": true, "SignatureDoesNotMatch": true, "UnrecognizedClientException": true,
	"ExpiredToken": true, "ExpiredTokenException": true, "InvalidAccessKeyId": true, "AccessDenied": true,
	"AccessDeniedException": true, "AuthFailure": true, "UnauthorizedOperation": true, "OptInRequired": true,
}

// AWS error codes of a call to try again later.
var unreachableCodes = map[string]bool{
	"Throttling": true, "ThrottlingException": true, "RequestLimitExceeded": true, "TooManyRequestsException": true,
	"ServiceUnavailable": true, "Unavailable": true, "InternalFailure": true, "InternalError": true,
	"RequestTimeout": true,
}

// portError makes an AWS failure one of the port's errors where it is one,
// keeping AWS's: a refused credential or a denied call refuses the
// credential; throttling, a 5xx or a call that never reached AWS is
// unreachable; any other answer of AWS stays a plain error.
func portError(err error) error {
	if err == nil {
		return nil
	}
	if _, ok := errors.AsType[*ports.Error](err); ok {
		return err
	}
	api, ok := errors.AsType[smithy.APIError](err)
	if !ok {
		return &ports.Error{Kind: ports.ErrUnreachable, Cause: err}
	}
	switch code := api.ErrorCode(); {
	case refusedCodes[code]:
		return &ports.Error{Kind: ports.ErrCredentialRefused, Cause: err}
	case unreachableCodes[code], httpStatus(err) >= 500:
		return &ports.Error{Kind: ports.ErrUnreachable, Cause: err}
	}
	return err
}

// portKind reports whether err is one of the port's errors, which a lease
// returns as its error rather than as a Failed lease.
func portKind(err error) bool {
	return errors.Is(err, ports.ErrCredentialRefused) || errors.Is(err, ports.ErrUnreachable)
}

// httpStatus is the HTTP status of an AWS answer, 0 when there is none.
func httpStatus(err error) int {
	if r, ok := errors.AsType[interface {
		error
		HTTPStatusCode() int
	}](err); ok {
		return r.HTTPStatusCode()
	}
	return 0
}

// apiCode is the AWS error code of err, "" when AWS didn't answer.
func apiCode(err error) string {
	if api, ok := errors.AsType[smithy.APIError](err); ok {
		return api.ErrorCode()
	}
	return ""
}
