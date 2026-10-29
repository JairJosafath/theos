package cloudformation

import (
	"cmp"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ecr"
	ecrtypes "github.com/aws/aws-sdk-go-v2/service/ecr/types"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/go-logr/logr"

	"github.com/JairJosafath/c4factory/internal/model"
	"github.com/JairJosafath/c4factory/internal/oci"
	"github.com/JairJosafath/c4factory/internal/ports"
)

// publish puts each file asset of stack, its template among them, where
// the asset manifest says, as the file-publishing role it names, when S3
// lacks it. Assets are named by their content's hash, so one that is there
// is the same.
func publish(ctx context.Context, ses *session, fs files, stack model.CDKStack, env model.CDKEnv) error {
	for _, a := range stack.Assets {
		if a.Packaging != "" && a.Packaging != "file" {
			return fmt.Errorf("asset %s of stack %s is packaged %q; the release holds an assembly's assets "+
				"packaged, so the factory ships only file assets", a.Path, stack.StackName, a.Packaging)
		}
		f, err := fs.get(a.Path)
		if err != nil {
			return err
		}
		for _, dst := range a.Destinations {
			region := cmp.Or(env.Substitute(dst.Region), env.Region)
			c := ses.in(region)
			if dst.AssumeRoleArn != "" {
				if c, err = ses.assume(ctx, env.Substitute(dst.AssumeRoleArn), region); err != nil {
					return err
				}
			}
			bucket, key := env.Substitute(dst.BucketName), env.Substitute(dst.ObjectKey)
			there, err := objectExists(ctx, c.S3, bucket, key)
			if err != nil {
				return err
			}
			if there {
				continue
			}
			if err := put(ctx, c.S3, f, bucket, key); err != nil {
				return err
			}
			logr.FromContextOrDiscard(ctx).Info("Published asset", "file", a.Path, "bucket", bucket, "key", key)
		}
	}
	return nil
}

// objectExists asks S3 whether bucket holds key.
func objectExists(ctx context.Context, c S3API, bucket, key string) (bool, error) {
	_, err := c.HeadObject(ctx, &s3.HeadObjectInput{Bucket: aws.String(bucket), Key: aws.String(key)})
	if err == nil {
		return true, nil
	}
	if code := apiCode(err); code == "NotFound" || code == "NoSuchKey" || httpStatus(err) == 404 {
		return false, nil
	}
	return false, portError(fmt.Errorf("reading s3://%s/%s: %w", bucket, key, err))
}

// put streams the release file f to bucket's key.
func put(ctx context.Context, c S3API, f ports.File, bucket, key string) error {
	body, size, err := f.Open(ctx)
	if err != nil {
		return fmt.Errorf("opening release file %s: %w", f.Name, err)
	}
	defer func() { _ = body.Close() }()
	if size < 0 {
		size = f.Size
	}
	if _, err := c.PutObject(ctx, &s3.PutObjectInput{Bucket: aws.String(bucket), Key: aws.String(key), Body: body,
		ContentLength: aws.Int64(size)}); err != nil {
		return portError(fmt.Errorf("publishing %s to s3://%s/%s: %w", f.Name, bucket, key, err))
	}
	return nil
}

// publishImage copies s's image to cp, the bootstrap's ECR repository, as
// the image-publishing role, when ECR lacks its digest. The copy is tagged
// the digest's hex, so a tag that is there names the same image. ECR or the
// registry refusing the role refuses the credential, and any other failure
// to write the copy is unreachable; a failure to read the image keeps the
// caller's error.
func publishImage(ctx context.Context, ses *session, s ports.Shipment, cp model.CDKImageCopy, region string) error {
	c, err := ses.assume(ctx, cp.Role, region)
	if err != nil {
		return err
	}
	there, err := imageExists(ctx, c.ECR, cp)
	if err != nil || there {
		return err
	}
	login, err := c.ECR.GetAuthorizationToken(ctx, &ecr.GetAuthorizationTokenInput{})
	if err != nil {
		return portError(fmt.Errorf("logging in to ECR %s: %w", cp.Host, err))
	}
	if len(login.AuthorizationData) == 0 {
		return fmt.Errorf("logging in to ECR %s: ECR returned no token", cp.Host)
	}
	raw, err := base64.StdEncoding.DecodeString(aws.ToString(login.AuthorizationData[0].AuthorizationToken))
	user, password, ok := strings.Cut(string(raw), ":")
	if err != nil || !ok {
		return fmt.Errorf("logging in to ECR %s: the token is not base64 user:password", cp.Host)
	}
	err = s.CopyImage(ctx, ports.ImageRegistry{Host: cp.Host, Username: user, Password: password}, cp.Repository, cp.Tag)
	switch {
	case err == nil:
		logr.FromContextOrDiscard(ctx).Info("Copied the image", "image", s.Image, "copy", cp.URI)
		return nil
	case errors.Is(err, oci.ErrSource):
		return err
	case oci.Refused(err):
		return &ports.Error{Kind: ports.ErrCredentialRefused, Cause: fmt.Errorf("copying the image to %s: %w",
			cp.URI, err)}
	}
	return &ports.Error{Kind: ports.ErrUnreachable, Cause: fmt.Errorf("copying the image to %s: %w", cp.URI, err)}
}

// imageExists asks ECR whether cp's repository holds its digest.
func imageExists(ctx context.Context, c ECRAPI, cp model.CDKImageCopy) (bool, error) {
	_, err := c.DescribeImages(ctx, &ecr.DescribeImagesInput{RepositoryName: aws.String(cp.Repository),
		ImageIds: []ecrtypes.ImageIdentifier{{ImageDigest: aws.String(cp.Digest)}}})
	if err == nil {
		return true, nil
	}
	if apiCode(err) == "ImageNotFoundException" {
		return false, nil
	}
	return false, portError(fmt.Errorf("reading %s: %w", cp.URI, err))
}
