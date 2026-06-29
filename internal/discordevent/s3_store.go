package discordevent

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"
	smithyhttp "github.com/aws/smithy-go/transport/http"
)

type S3StoreConfig struct {
	Endpoint        string
	Region          string
	AccessKeyID     string
	SecretAccessKey string
	Provider        string
	UsePathStyle    bool
}

type S3AttachmentStore struct {
	client   *s3.Client
	endpoint string
	provider string
}

func NewS3AttachmentStore(ctx context.Context, storeConfig S3StoreConfig) (*S3AttachmentStore, error) {
	region := storeConfig.Region
	if region == "" {
		region = "us-east-1"
	}
	provider := storeConfig.Provider
	if provider == "" {
		provider = "s3"
	}
	options := []func(*config.LoadOptions) error{
		config.WithRegion(region),
		config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(storeConfig.AccessKeyID, storeConfig.SecretAccessKey, "")),
	}
	awsConfig, err := config.LoadDefaultConfig(ctx, options...)
	if err != nil {
		return nil, fmt.Errorf("load s3 config: %w", err)
	}
	client := s3.NewFromConfig(awsConfig, func(options *s3.Options) {
		options.UsePathStyle = storeConfig.UsePathStyle
		if storeConfig.Endpoint != "" {
			options.BaseEndpoint = aws.String(storeConfig.Endpoint)
		}
	})
	return &S3AttachmentStore{client: client, endpoint: storeConfig.Endpoint, provider: provider}, nil
}

func (s *S3AttachmentStore) StoreAttachment(ctx context.Context, object AttachmentObject) (AttachmentObjectPointer, error) {
	if err := s.ensureBucket(ctx, object.Bucket); err != nil {
		return AttachmentObjectPointer{}, fmt.Errorf("ensure bucket: %w", err)
	}
	_, err := s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(object.Bucket),
		Key:         aws.String(object.Key),
		Body:        bytes.NewReader(object.Body),
		ContentType: aws.String(object.ContentType),
	})
	if err != nil {
		return AttachmentObjectPointer{}, fmt.Errorf("put object: %w", err)
	}
	return AttachmentObjectPointer{
		Bucket:      object.Bucket,
		Key:         object.Key,
		Provider:    s.provider,
		Endpoint:    s.endpoint,
		ContentType: object.ContentType,
		Size:        object.Size,
		SHA256:      object.SHA256,
	}, nil
}

func (s *S3AttachmentStore) ensureBucket(ctx context.Context, bucket string) error {
	_, err := s.client.HeadBucket(ctx, &s3.HeadBucketInput{Bucket: aws.String(bucket)})
	if err == nil {
		return nil
	}
	if !isBucketMissing(err) {
		return fmt.Errorf("head bucket: %w", err)
	}
	if _, err := s.client.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: aws.String(bucket)}); err != nil {
		if isBucketAlreadyAvailable(err) {
			return nil
		}
		return fmt.Errorf("create bucket: %w", err)
	}
	return nil
}

func isBucketMissing(err error) bool {
	var responseErr *smithyhttp.ResponseError
	if errors.As(err, &responseErr) && responseErr.HTTPStatusCode() == http.StatusNotFound {
		return true
	}
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		switch apiErr.ErrorCode() {
		case "NotFound", "NotFoundException", "NoSuchBucket":
			return true
		}
	}
	return false
}

func isBucketAlreadyAvailable(err error) bool {
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		switch apiErr.ErrorCode() {
		case "BucketAlreadyOwnedByYou", "BucketAlreadyExists":
			return true
		}
	}
	return false
}
