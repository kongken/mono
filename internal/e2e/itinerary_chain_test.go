package e2e_test

import (
	"context"
	"testing"

	"connectrpc.com/connect"
	bffv1 "github.com/kongken/mono/pkg/proto/bff/v1"
)

func TestChainItineraryCRUDAndIsolation(t *testing.T) {
	h := NewChainHarness(t)
	ctx := context.Background()

	alice := h.FreshBrowser()
	if _, err := alice.Register(ctx, "alice", "password1", "Alice"); err != nil {
		t.Fatal(err)
	}
	bob := h.FreshBrowser()
	if _, err := bob.Register(ctx, "bob", "password1", "Bob"); err != nil {
		t.Fatal(err)
	}

	saved, err := alice.SaveItinerary(ctx, &bffv1.SaveItineraryRequest{
		Id: "it-jiangnan", Title: "江南行", Note: "", Color: "#4b937d", ExpectedVersion: 0,
		Stops: []*bffv1.ItineraryStopInput{
			{Id: "s1", RegionCode: "310115", Date: "2026-05-01", Transport: "train"},
			{Id: "s2", RegionCode: "330100", Date: "2026-05-03", Transport: "train"},
		},
	})
	if err != nil || saved.GetItinerary().GetVersion() != 1 {
		t.Fatalf("save: %v %+v", err, saved)
	}
	if len(saved.GetItinerary().GetLegs()) != 1 {
		t.Fatalf("expected one leg, got %d", len(saved.GetItinerary().GetLegs()))
	}

	list, err := alice.ListItineraries(ctx)
	if err != nil || len(list.GetItineraries()) != 1 {
		t.Fatalf("list alice: %v %+v", err, list)
	}

	got, err := alice.GetItinerary(ctx, "it-jiangnan")
	if err != nil || got.GetItinerary().GetTitle() != "江南行" {
		t.Fatalf("get alice: %v %+v", err, got)
	}

	if _, err = bob.GetItinerary(ctx, "it-jiangnan"); err == nil || connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("bob get must be not_found: %v", err)
	}
	delBob, err := bob.DeleteItinerary(ctx, &bffv1.DeleteItineraryRequest{Id: "it-jiangnan"})
	if err != nil || delBob.GetDeleted() {
		t.Fatalf("bob delete other's itinerary: err=%v deleted=%v", err, delBob.GetDeleted())
	}

	req := connect.NewRequest(&bffv1.SaveItineraryRequest{
		Id: "it-evil", Title: "x", Color: "#000", ExpectedVersion: 0,
		Stops: []*bffv1.ItineraryStopInput{
			{Id: "a", RegionCode: "310115", Transport: "train"},
			{Id: "b", RegionCode: "330100", Transport: "train"},
		},
	})
	alice.withTripmapHeaders(req)
	if _, err = alice.Itin.SaveItinerary(ctx, req); err == nil || connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("save without csrf: %v", err)
	}

	del, err := alice.DeleteItinerary(ctx, &bffv1.DeleteItineraryRequest{Id: "it-jiangnan", ExpectedVersion: 1})
	if err != nil || !del.GetDeleted() {
		t.Fatalf("alice delete: %v %+v", err, del)
	}
	list, err = alice.ListItineraries(ctx)
	if err != nil || len(list.GetItineraries()) != 0 {
		t.Fatalf("list after delete: %v %+v", err, list)
	}
}
