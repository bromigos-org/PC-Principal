package discordevent

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type s3Request struct {
	method string
	path   string
	acl    string
	body   string
}

func TestS3AttachmentStore_creates_bucket_when_missing_before_upload(t *testing.T) {
	// Given
	requests := make([]s3Request, 0)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, recordS3Request(t, r))
		switch {
		case r.Method == http.MethodHead && r.URL.Path == "/pc-principal-discord-media":
			w.WriteHeader(http.StatusNotFound)
		case r.Method == http.MethodPut && r.URL.Path == "/pc-principal-discord-media":
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodPut && r.URL.Path == "/pc-principal-discord-media/guilds/guild-1/file.pdf":
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusTeapot)
		}
	}))
	t.Cleanup(server.Close)
	store := newTestS3AttachmentStore(t, server.URL)

	// When
	_, err := store.StoreAttachment(context.Background(), AttachmentObject{
		Bucket:      "pc-principal-discord-media",
		Key:         "guilds/guild-1/file.pdf",
		ContentType: "application/pdf",
		Size:        4,
		SHA256:      "checksum",
		Body:        []byte("body"),
	})

	// Then
	if err != nil {
		t.Fatalf("expected bucket provisioning and upload to succeed: %v", err)
	}
	want := []s3Request{
		{method: http.MethodHead, path: "/pc-principal-discord-media"},
		{method: http.MethodPut, path: "/pc-principal-discord-media"},
		{method: http.MethodPut, path: "/pc-principal-discord-media/guilds/guild-1/file.pdf", body: "body"},
	}
	assertS3Requests(t, requests, want)
}

func TestS3AttachmentStore_skips_bucket_create_when_bucket_exists(t *testing.T) {
	// Given
	requests := make([]s3Request, 0)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, recordS3Request(t, r))
		switch {
		case r.Method == http.MethodHead && r.URL.Path == "/pc-principal-discord-media":
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodPut && r.URL.Path == "/pc-principal-discord-media/guilds/guild-1/file.pdf":
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusTeapot)
		}
	}))
	t.Cleanup(server.Close)
	store := newTestS3AttachmentStore(t, server.URL)

	// When
	_, err := store.StoreAttachment(context.Background(), AttachmentObject{
		Bucket:      "pc-principal-discord-media",
		Key:         "guilds/guild-1/file.pdf",
		ContentType: "application/pdf",
		Size:        4,
		SHA256:      "checksum",
		Body:        []byte("body"),
	})

	// Then
	if err != nil {
		t.Fatalf("expected existing bucket upload to succeed: %v", err)
	}
	want := []s3Request{
		{method: http.MethodHead, path: "/pc-principal-discord-media"},
		{method: http.MethodPut, path: "/pc-principal-discord-media/guilds/guild-1/file.pdf", body: "body"},
	}
	assertS3Requests(t, requests, want)
}

func TestS3AttachmentStore_returns_error_when_bucket_provisioning_fails(t *testing.T) {
	// Given
	requests := make([]s3Request, 0)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, recordS3Request(t, r))
		switch {
		case r.Method == http.MethodHead && r.URL.Path == "/pc-principal-discord-media":
			w.WriteHeader(http.StatusNotFound)
		case r.Method == http.MethodPut && r.URL.Path == "/pc-principal-discord-media":
			w.WriteHeader(http.StatusInternalServerError)
		default:
			w.WriteHeader(http.StatusTeapot)
		}
	}))
	t.Cleanup(server.Close)
	store := newTestS3AttachmentStore(t, server.URL)

	// When
	_, err := store.StoreAttachment(context.Background(), AttachmentObject{
		Bucket:      "pc-principal-discord-media",
		Key:         "guilds/guild-1/file.pdf",
		ContentType: "application/pdf",
		Size:        4,
		SHA256:      "checksum",
		Body:        []byte("body"),
	})

	// Then
	if err == nil {
		t.Fatalf("expected bucket provisioning error")
	}
	if len(requests) < 2 {
		t.Fatalf("expected head bucket and create bucket attempts, got %#v", requests)
	}
	if requests[0].method != http.MethodHead || requests[0].path != "/pc-principal-discord-media" {
		t.Fatalf("expected first request to head bucket, got %#v", requests[0])
	}
	for index := 1; index < len(requests); index++ {
		if requests[index].method != http.MethodPut || requests[index].path != "/pc-principal-discord-media" {
			t.Fatalf("expected create bucket retry without object upload at request %d, got %#v", index, requests[index])
		}
	}
}

func TestS3AttachmentStore_uploads_when_bucket_is_created_by_another_client(t *testing.T) {
	// Given
	requests := make([]s3Request, 0)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, recordS3Request(t, r))
		switch {
		case r.Method == http.MethodHead && r.URL.Path == "/pc-principal-discord-media":
			w.WriteHeader(http.StatusNotFound)
		case r.Method == http.MethodPut && r.URL.Path == "/pc-principal-discord-media":
			w.WriteHeader(http.StatusConflict)
			_, _ = w.Write([]byte(`<Error><Code>BucketAlreadyOwnedByYou</Code><Message>created elsewhere</Message></Error>`))
		case r.Method == http.MethodPut && r.URL.Path == "/pc-principal-discord-media/guilds/guild-1/file.pdf":
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusTeapot)
		}
	}))
	t.Cleanup(server.Close)
	store := newTestS3AttachmentStore(t, server.URL)

	// When
	_, err := store.StoreAttachment(context.Background(), AttachmentObject{
		Bucket:      "pc-principal-discord-media",
		Key:         "guilds/guild-1/file.pdf",
		ContentType: "application/pdf",
		Size:        4,
		SHA256:      "checksum",
		Body:        []byte("body"),
	})

	// Then
	if err != nil {
		t.Fatalf("expected upload after bucket already exists race: %v", err)
	}
	want := []s3Request{
		{method: http.MethodHead, path: "/pc-principal-discord-media"},
		{method: http.MethodPut, path: "/pc-principal-discord-media"},
		{method: http.MethodPut, path: "/pc-principal-discord-media/guilds/guild-1/file.pdf", body: "body"},
	}
	assertS3Requests(t, requests, want)
}

func newTestS3AttachmentStore(t *testing.T, endpoint string) *S3AttachmentStore {
	t.Helper()
	store, err := NewS3AttachmentStore(context.Background(), S3StoreConfig{
		Endpoint:        endpoint,
		Region:          "us-east-1",
		AccessKeyID:     "test-access-key",
		SecretAccessKey: "test-secret-key",
		Provider:        "rustfs",
		UsePathStyle:    true,
	})
	if err != nil {
		t.Fatalf("new test s3 store: %v", err)
	}
	return store
}

func recordS3Request(t *testing.T, r *http.Request) s3Request {
	t.Helper()
	body, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatalf("read request body: %v", err)
	}
	return s3Request{method: r.Method, path: r.URL.Path, acl: r.Header.Get("X-Amz-Acl"), body: strings.TrimSpace(string(body))}
}

func assertS3Requests(t *testing.T, got []s3Request, want []s3Request) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("expected %d s3 requests, got %d: %#v", len(want), len(got), got)
	}
	for index := range want {
		if got[index].method != want[index].method || got[index].path != want[index].path || got[index].body != want[index].body {
			t.Fatalf("request %d mismatch: want %#v got %#v", index, want[index], got[index])
		}
		if got[index].acl != "" {
			t.Fatalf("request %d unexpectedly set public ACL header %q", index, got[index].acl)
		}
	}
}
