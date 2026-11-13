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

package cloudformation

import (
	"errors"
	"fmt"
	"strings"

	"github.com/aws/smithy-go"

	"github.com/JairJosafath/theos/internal/ports"
)

// StackTakenError is a stack whose theos.io/app tag names another app:
// the member leaves it alone, and the deploy fails.
type StackTakenError struct {
	Stack, App, Owner string
}

func (e *StackTakenError) Error() string {
	return fmt.Sprintf("cloudformation: stack %s belongs to app %s, not %s; it is left alone", e.Stack, e.Owner, e.App)
}

// ErrStackTaken is what errors.Is finds in a StackTakenError.
var ErrStackTaken = errors.New("the stack belongs to another app")

// Is makes errors.Is(err, ErrStackTaken) true.
func (e *StackTakenError) Is(target error) bool { return target == ErrStackTaken }

// AWS error codes of a credential AWS refuses, or that may not do the call.
var refusedCodes = map[string]bool{
	"InvalidClientTokenId": true, "SignatureDoesNotMatch": true, "UnrecognizedClientException": true,
	"ExpiredToken": true, "ExpiredTokenException": true, "InvalidAccessKeyId": true, "AccessDenied": true,
	"AccessDeniedException": true, "AuthFailure": true,
}

// AWS error codes of a call to try again later.
var unreachableCodes = map[string]bool{
	"Throttling": true, "ThrottlingException": true, "RequestLimitExceeded": true, "TooManyRequestsException": true,
	"ServiceUnavailable": true, "InternalFailure": true, "InternalError": true, "RequestTimeout": true,
	"SlowDown": true,
}

// portError makes an AWS failure one of the port's errors where it is one,
// keeping AWS's: a refused credential or a denied call refuses the
// credential; throttling, a 5xx or a call that never reached AWS is
// unreachable; any other answer of AWS stays a plain error, the deploy's
// failure.
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

// stackMissing says err is DescribeStacks' answer for a stack that doesn't
// exist.
func stackMissing(err error) bool {
	api, ok := errors.AsType[smithy.APIError](err)
	return ok && api.ErrorCode() == "ValidationError" && strings.Contains(api.ErrorMessage(), "does not exist")
}
