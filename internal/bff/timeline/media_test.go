package timeline

import (
	"connectrpc.com/connect"
	"context"
	"testing"

	bff "github.com/kongken/mono/pkg/proto/bff/timeline/v1"
	v1 "github.com/kongken/mono/pkg/proto/timeline/v1"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// stubMedia answers the five media calls with what a signed upload looks like, so the test is about the BFF's
// copying and nothing else.
type stubMedia struct{ prepared *v1.PrepareUploadResponse }

func (s *stubMedia) PrepareUpload(context.Context, *v1.PrepareUploadRequest, ...grpc.CallOption) (*v1.PrepareUploadResponse, error) {
	return s.prepared, nil
}
func (s *stubMedia) CompleteUpload(context.Context, *v1.CompleteUploadRequest, ...grpc.CallOption) (*v1.CompleteUploadResponse, error) {
	return &v1.CompleteUploadResponse{Media: s.prepared.Media}, nil
}
func (s *stubMedia) ListMedia(context.Context, *v1.ListMediaRequest, ...grpc.CallOption) (*v1.ListMediaResponse, error) {
	return &v1.ListMediaResponse{Media: []*v1.MediaInfo{s.prepared.Media}}, nil
}
func (s *stubMedia) ReadMedia(context.Context, *v1.ReadMediaRequest, ...grpc.CallOption) (*v1.ReadMediaResponse, error) {
	return &v1.ReadMediaResponse{Data: []byte("jpg"), ContentType: "image/jpeg", Filename: "photo.jpg"}, nil
}
func (s *stubMedia) GetMediaDownloadUrl(context.Context, *v1.GetMediaDownloadUrlRequest, ...grpc.CallOption) (*v1.GetMediaDownloadUrlResponse, error) {
	return &v1.GetMediaDownloadUrlResponse{Url: "https://objects.example.com/bucket/key?sig=1", ContentType: "image/jpeg", Filename: "photo.jpg", ExpiresAt: timestamppb.New(timestamppb.Now().AsTime().Add(300))}, nil
}
func (s *stubMedia) DeleteMedia(context.Context, *v1.DeleteMediaRequest, ...grpc.CallOption) (*v1.DeleteMediaResponse, error) {
	return &v1.DeleteMediaResponse{Receipt: &v1.MutationReceipt{MutationId: "m1", Operation: "media_delete", ResultRevision: 1}}, nil
}

func newHandler() *MediaHandler {
	created := timestamppb.New(timestamppb.Now().AsTime())
	return &MediaHandler{client: &stubMedia{prepared: &v1.PrepareUploadResponse{
		Media:     &v1.MediaInfo{MediaRef: "med_abc", EntryRef: "ent_abc", Filename: "photo.jpg", ContentType: "image/jpeg", SizeBytes: 12, Status: "pending", CreatedAt: created},
		UploadUrl: "https://objects.example.com/bucket/key?sig=1",
		Headers:   map[string]string{"Content-Type": "image/jpeg"},
		ExpiresAt: timestamppb.New(timestamppb.Now().AsTime().Add(600)),
	}}}
}

// The presigned PUT signs Content-Type: a BFF that hands the browser only the URL makes every upload fail with
// SignatureDoesNotMatch, so the headers and the expiry have to cross this boundary with it.
func TestPrepareUploadCarriesTheSignedHeadersAndExpiry(t *testing.T) {
	answer, err := newHandler().PrepareUpload(context.Background(), connect.NewRequest(&bff.PrepareUploadRequest{EntryRef: "ent_abc", Filename: "photo.jpg", ContentType: "image/jpeg", SizeBytes: 12, MutationId: "m1"}))
	if err != nil {
		t.Fatal(err)
	}
	if got := answer.Msg.Headers["Content-Type"]; got != "image/jpeg" {
		t.Fatalf("headers were dropped: %v", answer.Msg.Headers)
	}
	if answer.Msg.ExpiresAt == nil {
		t.Fatal("expires_at was dropped")
	}
	if answer.Msg.UploadUrl == "" {
		t.Fatal("upload_url was dropped")
	}
	if answer.Msg.Media.CreatedAt == nil {
		t.Fatal("created_at was dropped")
	}
}

func TestListAndCompleteCarryTheAttachmentAsIs(t *testing.T) {
	listed, err := newHandler().ListMedia(context.Background(), connect.NewRequest(&bff.ListMediaRequest{EntryRef: "ent_abc"}))
	if err != nil {
		t.Fatal(err)
	}
	if len(listed.Msg.Media) != 1 || listed.Msg.Media[0].Filename != "photo.jpg" || listed.Msg.Media[0].CreatedAt == nil {
		t.Fatalf("unexpected list: %+v", listed.Msg.Media)
	}
	completed, err := newHandler().CompleteUpload(context.Background(), connect.NewRequest(&bff.CompleteUploadRequest{MediaRef: "med_abc", MutationId: "m1"}))
	if err != nil {
		t.Fatal(err)
	}
	if completed.Msg.Media.MediaRef != "med_abc" || completed.Msg.Media.ContentType != "image/jpeg" {
		t.Fatalf("unexpected complete: %+v", completed.Msg.Media)
	}
}

func TestDeleteReturnsAReceipt(t *testing.T) {
	answer, err := newHandler().DeleteMedia(context.Background(), connect.NewRequest(&bff.DeleteMediaRequest{MediaRef: "med_abc", MutationId: "m1", ExpectedRevision: 1}))
	if err != nil {
		t.Fatal(err)
	}
	if answer.Msg.Receipt == nil || answer.Msg.Receipt.Operation != "media_delete" {
		t.Fatalf("unexpected receipt: %+v", answer.Msg.Receipt)
	}
}
