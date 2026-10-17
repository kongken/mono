package e2e_test

import (
	"context"
	"testing"

	"connectrpc.com/connect"
	bfftripmapv1 "github.com/kongken/mono/pkg/proto/bff/tripmap/v1"
)

func TestChainFriendLifecycleAndFootprintVisibility(t *testing.T) {
	h := NewChainHarness(t)
	ctx := context.Background()

	a := h.FreshBrowser()
	aID, err := a.Register(ctx, "friend_alice", "password1", "Alice")
	if err != nil {
		t.Fatal(err)
	}
	b := h.FreshBrowser()
	bID, err := b.Register(ctx, "friend_bob", "password1", "Bob")
	if err != nil {
		t.Fatal(err)
	}

	ovA, err := a.GetCollaborationOverview(ctx)
	if err != nil || ovA.GetMyFriendCode() == "" {
		t.Fatalf("alice overview: %v %+v", err, ovA)
	}
	ovB, err := b.GetCollaborationOverview(ctx)
	if err != nil || ovB.GetMyFriendCode() == "" || ovA.GetMyFriendCode() == ovB.GetMyFriendCode() {
		t.Fatalf("bob overview: %v", err)
	}

	csrf := a.refreshCSRF(ctx)
	sendReq := connect.NewRequest(&bfftripmapv1.SendFriendRequestRequest{Target: "friend_bob"})
	a.writeHeaders(sendReq, csrf, a.Origin)
	sendRes, err := a.Friends.SendFriendRequest(ctx, sendReq)
	if err != nil {
		t.Fatalf("send friend request: %v", err)
	}
	frID := sendRes.Msg.GetFriendship().GetId()
	if sendRes.Msg.GetFriendship().GetStatus() != "pending" {
		t.Fatalf("expected pending, got %+v", sendRes.Msg.GetFriendship())
	}

	_, err = a.GetFriendFootprints(ctx, bID)
	if err == nil || connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("pending friend footprints: %v", err)
	}

	csrfB := b.refreshCSRF(ctx)
	declineReq := connect.NewRequest(&bfftripmapv1.RespondFriendRequestRequest{FriendshipId: frID, Accept: false})
	b.writeHeaders(declineReq, csrfB, b.Origin)
	if _, err = b.Friends.RespondFriendRequest(ctx, declineReq); err != nil {
		t.Fatal(err)
	}

	csrf = a.refreshCSRF(ctx)
	sendReq = connect.NewRequest(&bfftripmapv1.SendFriendRequestRequest{Target: ovB.GetMyFriendCode()})
	a.writeHeaders(sendReq, csrf, a.Origin)
	sendRes, err = a.Friends.SendFriendRequest(ctx, sendReq)
	if err != nil {
		t.Fatal(err)
	}
	frID = sendRes.Msg.GetFriendship().GetId()

	csrfB = b.refreshCSRF(ctx)
	acceptReq := connect.NewRequest(&bfftripmapv1.RespondFriendRequestRequest{FriendshipId: frID, Accept: true})
	b.writeHeaders(acceptReq, csrfB, b.Origin)
	if _, err = b.Friends.RespondFriendRequest(ctx, acceptReq); err != nil {
		t.Fatal(err)
	}

	if _, err = h.TripmapDB.CityVisit.Create().
		SetUserID(bID).
		SetRegionCode("330100").SetRegionName("杭州市").SetProvinceCode("330000").SetColor("#4b937d").
		Save(ctx); err != nil {
		t.Fatal(err)
	}

	fp, err := a.GetFriendFootprints(ctx, bID)
	if err != nil || fp.GetContentJson() == "" {
		t.Fatalf("friend footprints after accept: %v", err)
	}

	csrf = a.refreshCSRF(ctx)
	rmReq := connect.NewRequest(&bfftripmapv1.RemoveFriendRequest{FriendshipId: frID})
	a.writeHeaders(rmReq, csrf, a.Origin)
	if _, err = a.Friends.RemoveFriend(ctx, rmReq); err != nil {
		t.Fatal(err)
	}
	_, err = a.GetFriendFootprints(ctx, bID)
	if err == nil || connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("footprints after remove: %v", err)
	}
	_ = aID
}
