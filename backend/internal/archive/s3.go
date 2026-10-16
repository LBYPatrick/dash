package archive

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
	smithyhttp "github.com/aws/smithy-go/transport/http"
)

// RemoteError deliberately excludes request URLs, provider bodies and wrapped
// transport errors, which may contain preauthorized upload URLs or credentials.
type RemoteError struct {
	Provider, Operation string
	Status              int
}

func (e RemoteError) Error() string {
	return fmt.Sprintf("%s %s failed (HTTP %d)", e.Provider, e.Operation, e.Status)
}
func remoteError(provider, operation string, err error) error {
	if err == nil {
		return nil
	}
	var response *smithyhttp.ResponseError
	status := 0
	if errors.As(err, &response) {
		status = response.HTTPStatusCode()
	}
	return RemoteError{provider, operation, status}
}
func apiCode(err error) string {
	var value smithy.APIError
	if errors.As(err, &value) {
		return value.ErrorCode()
	}
	return ""
}

type S3Store struct {
	client *s3.Client
	config Config
	source Source
}

func NewS3Store(config Config, source Source) (*S3Store, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}
	client := s3.NewFromConfig(aws.Config{Region: config.Region, Credentials: credentials.NewStaticCredentialsProvider(config.Secret.AccessKey, config.Secret.SecretKey, config.Secret.SessionToken), HTTPClient: &http.Client{Timeout: 60 * time.Second}, RetryMaxAttempts: 2, RequestChecksumCalculation: aws.RequestChecksumCalculationWhenRequired, ResponseChecksumValidation: aws.ResponseChecksumValidationWhenRequired}, func(options *s3.Options) {
		options.UsePathStyle = config.PathStyle
		if config.Endpoint != "" {
			options.BaseEndpoint = aws.String(config.Endpoint)
		}
	})
	return &S3Store{client: client, config: config, source: source}, nil
}
func (s *S3Store) Close() error          { return nil }
func (s *S3Store) key(key string) string { return path.Join(s.config.Prefix, key) }
func (s *S3Store) exists(ctx context.Context, key, hash string, size int64) (bool, error) {
	result, err := s.client.HeadObject(ctx, &s3.HeadObjectInput{Bucket: aws.String(s.config.Bucket), Key: aws.String(s.key(key))})
	if err != nil {
		code := apiCode(err)
		if code == "NotFound" || code == "NoSuchKey" || code == "404" {
			return false, nil
		}
		return false, remoteError("S3", "inspect", err)
	}
	if aws.ToInt64(result.ContentLength) != size || !strings.EqualFold(result.Metadata["dash-sha256"], hash) {
		return false, ErrConflict
	}
	return true, nil
}
func (s *S3Store) Step(ctx context.Context, a Asset) (Checkpoint, bool, error) {
	cp := a.Checkpoint
	if cp.UploadID == "" || cp.Offset >= a.Size {
		exists, err := s.exists(ctx, a.Key, a.SHA256, a.Size)
		if err != nil {
			return cp, false, err
		}
		if exists {
			return Checkpoint{Offset: a.Size}, true, nil
		}
	}
	source, err := s.source.Open(a.Path)
	if err != nil {
		return cp, false, ErrMissing
	}
	defer source.Close()
	if !unchanged(source, a) {
		return cp, false, ErrMissing
	}
	if a.Size < 5<<20 {
		_, err = s.client.PutObject(ctx, &s3.PutObjectInput{Bucket: aws.String(s.config.Bucket), Key: aws.String(s.key(a.Key)), Body: source, ContentLength: aws.Int64(a.Size), Metadata: map[string]string{"dash-sha256": a.SHA256}, IfNoneMatch: aws.String("*")})
		if apiCode(err) == "PreconditionFailed" {
			return cp, false, ErrConflict
		}
		if err != nil {
			return cp, false, remoteError("S3", "upload", err)
		}
		return Checkpoint{Offset: a.Size}, true, nil
	}
	if cp.UploadID == "" {
		result, err := s.client.CreateMultipartUpload(ctx, &s3.CreateMultipartUploadInput{Bucket: aws.String(s.config.Bucket), Key: aws.String(s.key(a.Key)), Metadata: map[string]string{"dash-sha256": a.SHA256}})
		if err != nil {
			return cp, false, remoteError("S3", "start upload", err)
		}
		if aws.ToString(result.UploadId) == "" {
			return cp, false, errors.New("S3 returned an empty upload id")
		}
		cp.UploadID = aws.ToString(result.UploadId)
		return cp, false, nil
	}
	if cp.Offset < a.Size {
		// SectionReader is seekable. The SDK never needs a part-sized RAM buffer.
		partSize := max(ChunkBytes, ((a.Size+9999)/10000+(1<<20)-1)/(1<<20)*(1<<20))
		count := min(partSize, a.Size-cp.Offset)
		number := int32(len(cp.Parts) + 1)
		result, err := s.client.UploadPart(ctx, &s3.UploadPartInput{Bucket: aws.String(s.config.Bucket), Key: aws.String(s.key(a.Key)), UploadId: aws.String(cp.UploadID), PartNumber: aws.Int32(number), Body: io.NewSectionReader(source.ReaderAt, cp.Offset, count), ContentLength: aws.Int64(count)})
		if apiCode(err) == "NoSuchUpload" {
			return Checkpoint{}, false, nil
		}
		if err != nil {
			return cp, false, remoteError("S3", "upload part", err)
		}
		if aws.ToString(result.ETag) == "" {
			return cp, false, errors.New("S3 returned an empty part receipt")
		}
		cp.Parts = append(cp.Parts, Part{number, aws.ToString(result.ETag)})
		cp.Offset += count
		return cp, false, nil
	}
	parts := make([]types.CompletedPart, 0, len(cp.Parts))
	for _, part := range cp.Parts {
		parts = append(parts, types.CompletedPart{ETag: aws.String(part.ETag), PartNumber: aws.Int32(part.Number)})
	}
	_, err = s.client.CompleteMultipartUpload(ctx, &s3.CompleteMultipartUploadInput{Bucket: aws.String(s.config.Bucket), Key: aws.String(s.key(a.Key)), UploadId: aws.String(cp.UploadID), MultipartUpload: &types.CompletedMultipartUpload{Parts: parts}, IfNoneMatch: aws.String("*")})
	if apiCode(err) == "NoSuchUpload" {
		return Checkpoint{}, false, nil
	}
	if apiCode(err) == "PreconditionFailed" {
		return cp, false, ErrConflict
	}
	if err != nil {
		return cp, false, remoteError("S3", "complete upload", err)
	}
	return Checkpoint{Offset: a.Size}, true, nil
}
func (s *S3Store) Put(ctx context.Context, key string, data []byte) error {
	hash := sha256.Sum256(data)
	digest := hex.EncodeToString(hash[:])
	exists, err := s.exists(ctx, key, digest, int64(len(data)))
	if err != nil || exists {
		return err
	}
	_, err = s.client.PutObject(ctx, &s3.PutObjectInput{Bucket: aws.String(s.config.Bucket), Key: aws.String(s.key(key)), Body: bytes.NewReader(data), ContentLength: aws.Int64(int64(len(data))), Metadata: map[string]string{"dash-sha256": digest}, IfNoneMatch: aws.String("*")})
	if apiCode(err) == "PreconditionFailed" {
		return ErrConflict
	}
	return remoteError("S3", "write manifest", err)
}
func (s *S3Store) Abort(ctx context.Context, a Asset) error {
	if a.Checkpoint.UploadID == "" {
		return nil
	}
	_, err := s.client.AbortMultipartUpload(ctx, &s3.AbortMultipartUploadInput{Bucket: aws.String(s.config.Bucket), Key: aws.String(s.key(a.Key)), UploadId: aws.String(a.Checkpoint.UploadID)})
	if apiCode(err) == "NoSuchUpload" {
		return nil
	}
	return remoteError("S3", "cancel upload", err)
}
