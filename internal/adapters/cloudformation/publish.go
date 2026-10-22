package cloudformation

import (
	"cmp"
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/go-logr/logr"

	"github.com/JairJosafath/c4factory/internal/model"
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
