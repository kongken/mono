package application_test

import (
	"context"
	"testing"

	"connectrpc.com/connect"
	tripmapv1 "github.com/kongken/mono/pkg/proto/tripmap/v1"
)

func TestDeleteMediaMutationReceiptReplayAndConflict(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	userID := e.register("media_mutation_race")
	e.store.Blobs = &mediaMemory{files: map[string][]byte{}}
	m := e.store.DB.MediaObject.Create().
		SetID("media-race-1").SetPublicRef("med_0123456789abcdef0123456789").
		SetEntryPublicRef("ent_0123456789abcdef0123456789").SetUserID(userID).
		SetRegionCode("330100").SetFilename("race.png").SetContentType("image/png").
		SetSizeBytes(1).SetRevision(1).SetObjectKey("race/object").SaveX(ctx)
	first, err := e.media.DeleteMedia(ctx, bearer(userID, &tripmapv1.DeleteMediaRequest{
		MediaRef: m.PublicRef, MutationId: "mutation-race-1",
	}))
	if err != nil || first.Msg.GetReceipt() == nil || first.Msg.GetReceipt().GetReplayed() {
		t.Fatalf("first deletion = %v, err=%v", first, err)
	}
	replay, err := e.media.DeleteMedia(ctx, bearer(userID, &tripmapv1.DeleteMediaRequest{
		MediaRef: m.PublicRef, MutationId: "mutation-race-1",
	}))
	if err != nil || replay.Msg.GetReceipt() == nil || !replay.Msg.GetReceipt().GetReplayed() || replay.Msg.GetReceipt().GetResultRevision() != first.Msg.GetReceipt().GetResultRevision() {
		t.Fatalf("shared receipt replay = %v, err=%v", replay, err)
	}
	_, err = e.media.DeleteMedia(ctx, bearer(userID, &tripmapv1.DeleteMediaRequest{
		MediaRef: m.PublicRef, MutationId: "mutation-race-1", ExpectedRevision: func() *int64 { v := int64(99); return &v }(),
	}))
	wantCode(t, err, connect.CodeAlreadyExists)
}
