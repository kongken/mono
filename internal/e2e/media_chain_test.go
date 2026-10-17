package e2e_test

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"testing"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	bffv1 "github.com/kongken/mono/pkg/proto/bff/v1"
)

func TestChainMediaLifecycleAndIsolation(t *testing.T) {
	blobs := newChainMediaBlobs()
	h := NewChainHarness(t, blobs)
	ctx := context.Background()

	a := h.FreshBrowser()
	if _, err := a.Register(ctx, "media_a", "password1", "A"); err != nil {
		t.Fatal(err)
	}
	csrf := a.refreshCSRF(ctx)
	syncReq := connect.NewRequest(&bffv1.SyncChangesRequest{
		MutationId: "m1", BaseRevision: 0,
		Mutations: []*bffv1.FootprintMutation{{
			Operation: &bffv1.FootprintMutation_UpsertCity{
				UpsertCity: &bffv1.Visit{Code: "330100", Name: "杭州市", Province: "330000", Color: "#4b937d"},
			},
		}},
	})
	a.writeHeaders(syncReq, csrf, a.Origin)
	if _, err := a.Foot.SyncChanges(ctx, syncReq); err != nil {
		t.Fatal(err)
	}

	data := photoPNG(t)
	uploadID := uuid.NewString()
	prep, err := a.PrepareUpload(ctx, &bffv1.PrepareUploadRequest{
		RegionCode: "330100", Filename: "西湖.png", ContentType: "image/png",
		SizeBytes: int64(len(data)), UploadId: uploadID,
	})
	if err != nil || prep.GetMedia().GetId() == "" {
		t.Fatalf("prepare: %+v err=%v", prep, err)
	}
	id := prep.GetMedia().GetId()
	m := h.TripmapDB.MediaObject.GetX(ctx, id)
	if err = blobs.Put(ctx, m.UploadKey, bytes.NewReader(data), int64(len(data)), "image/png"); err != nil {
		t.Fatal(err)
	}
	ready, err := a.CompleteUpload(ctx, id)
	if err != nil || ready.GetWidth() != 16 {
		t.Fatalf("complete: %+v err=%v", ready, err)
	}
	list, err := a.ListMedia(ctx, "330100", "")
	if err != nil || len(list.GetMedia()) != 1 {
		t.Fatalf("list: %+v err=%v", list, err)
	}
	read, err := a.ReadMedia(ctx, id, false)
	if err != nil || len(read.GetData()) == 0 {
		t.Fatalf("read: err=%v", err)
	}

	b := h.FreshBrowser()
	if _, err := b.Register(ctx, "media_b", "password1", "B"); err != nil {
		t.Fatal(err)
	}
	_, err = b.ReadMedia(ctx, id, false)
	if connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("cross-user read: %v", err)
	}

	if err := a.DeleteMedia(ctx, id); err != nil {
		t.Fatal(err)
	}
	list, err = a.ListMedia(ctx, "330100", "")
	if err != nil || len(list.GetMedia()) != 0 {
		t.Fatalf("after delete: %+v err=%v", list, err)
	}
}

func photoPNG(t *testing.T) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewRGBA(image.Rect(0, 0, 16, 8))); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func (b *Browser) PrepareUpload(ctx context.Context, msg *bffv1.PrepareUploadRequest) (*bffv1.PrepareUploadResponse, error) {
	csrf := b.refreshCSRF(ctx)
	req := connect.NewRequest(msg)
	b.writeHeaders(req, csrf, b.Origin)
	res, err := b.Media().PrepareUpload(ctx, req)
	if err != nil {
		return nil, err
	}
	return res.Msg, nil
}

func (b *Browser) CompleteUpload(ctx context.Context, id string) (*bffv1.MediaInfo, error) {
	csrf := b.refreshCSRF(ctx)
	req := connect.NewRequest(&bffv1.CompleteUploadRequest{Id: id})
	b.writeHeaders(req, csrf, b.Origin)
	res, err := b.Media().CompleteUpload(ctx, req)
	if err != nil {
		return nil, err
	}
	return res.Msg.GetMedia(), nil
}

func (b *Browser) ListMedia(ctx context.Context, region, trip string) (*bffv1.ListMediaResponse, error) {
	req := connect.NewRequest(&bffv1.ListMediaRequest{RegionCode: region, TripId: trip})
	b.withTripmapHeaders(req)
	res, err := b.Media().ListMedia(ctx, req)
	if err != nil {
		return nil, err
	}
	return res.Msg, nil
}

func (b *Browser) ReadMedia(ctx context.Context, id string, thumb bool) (*bffv1.ReadMediaResponse, error) {
	req := connect.NewRequest(&bffv1.ReadMediaRequest{Id: id, Thumbnail: thumb})
	b.withTripmapHeaders(req)
	res, err := b.Media().ReadMedia(ctx, req)
	if err != nil {
		return nil, err
	}
	return res.Msg, nil
}

func (b *Browser) DeleteMedia(ctx context.Context, id string) error {
	csrf := b.refreshCSRF(ctx)
	req := connect.NewRequest(&bffv1.DeleteMediaRequest{Id: id})
	b.writeHeaders(req, csrf, b.Origin)
	_, err := b.Media().DeleteMedia(ctx, req)
	return err
}
