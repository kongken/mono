package media

import (
	"bytes"
	"context"
	"testing"

	"connectrpc.com/connect"
	bff "github.com/kongken/mono/pkg/proto/bff/tripmap/v1"
	tripmap "github.com/kongken/mono/pkg/proto/tripmap/v1"
	"google.golang.org/grpc"
)

type fakeMediaClient struct {
	prepare *tripmap.PrepareUploadRequest
	delete  *tripmap.DeleteMediaRequest
}

func (f *fakeMediaClient) PrepareUpload(_ context.Context, req *tripmap.PrepareUploadRequest, _ ...grpc.CallOption) (*tripmap.PrepareUploadResponse, error) {
	f.prepare = req
	return &tripmap.PrepareUploadResponse{UploadUrl: "https://upload", Headers: map[string]string{"x-test": "yes"}}, nil
}
func (f *fakeMediaClient) DeleteMedia(_ context.Context, req *tripmap.DeleteMediaRequest, _ ...grpc.CallOption) (*tripmap.DeleteMediaResponse, error) {
	f.delete = req
	return &tripmap.DeleteMediaResponse{Receipt: &tripmap.MediaMutationReceipt{
		MutationId: "mut_1", MediaId: "42", MediaRef: "med_opaque", EntryRef: "ent_opaque",
		ResultState: "deleting", ResultRevision: 9, ResultSnapshot: []byte(`{"id":"42"}`), SnapshotVersion: 3, Replayed: true,
	}}, nil
}
func (f *fakeMediaClient) CompleteUpload(context.Context, *tripmap.CompleteUploadRequest, ...grpc.CallOption) (*tripmap.CompleteUploadResponse, error) {
	return &tripmap.CompleteUploadResponse{}, nil
}
func (f *fakeMediaClient) ListMedia(context.Context, *tripmap.ListMediaRequest, ...grpc.CallOption) (*tripmap.ListMediaResponse, error) {
	return &tripmap.ListMediaResponse{}, nil
}
func (f *fakeMediaClient) ReadMedia(context.Context, *tripmap.ReadMediaRequest, ...grpc.CallOption) (*tripmap.ReadMediaResponse, error) {
	return &tripmap.ReadMediaResponse{}, nil
}

func TestHandlerForwardsPrepareUploadAndDeleteReceipt(t *testing.T) {
	fake := &fakeMediaClient{}
	h := New(fake)
	prepare, err := h.PrepareUpload(context.Background(), connect.NewRequest(&bff.PrepareUploadRequest{
		RegionCode: "US", Filename: "photo.jpg", ContentType: "image/jpeg", SizeBytes: 12,
		UploadId: "up_1", TripId: "trip_1", EntryRef: "ent_opaque",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if fake.prepare.GetEntryRef() != "ent_opaque" || fake.prepare.GetUploadId() != "up_1" || prepare.Msg.GetUploadUrl() != "https://upload" {
		t.Fatalf("prepare fields were not forwarded: request=%v response=%v", fake.prepare, prepare.Msg)
	}

	expected := int64(8)
	deleted, err := h.DeleteMedia(context.Background(), connect.NewRequest(&bff.DeleteMediaRequest{
		MediaRef: "med_opaque", MutationId: "mut_1", ExpectedRevision: &expected,
	}))
	if err != nil {
		t.Fatal(err)
	}
	if fake.delete.GetMediaRef() != "med_opaque" || fake.delete.GetMutationId() != "mut_1" || fake.delete.GetExpectedRevision() != expected {
		t.Fatalf("delete fields were not forwarded: %v", fake.delete)
	}
	r := deleted.Msg.GetReceipt()
	if r == nil || r.GetMediaRef() != "med_opaque" || r.GetEntryRef() != "ent_opaque" || r.GetResultRevision() != 9 || r.GetSnapshotVersion() != 3 || !bytes.Equal(r.GetResultSnapshot(), []byte(`{"id":"42"}`)) || !r.GetReplayed() {
		t.Fatalf("receipt fields were not preserved: %v", r)
	}
}
