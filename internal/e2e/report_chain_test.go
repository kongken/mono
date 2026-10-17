package e2e_test

import (
	"context"
	"testing"

	"connectrpc.com/connect"
	bffv1 "github.com/kongken/mono/pkg/proto/bff/v1"
	"github.com/kongken/mono/pkg/proto/bff/v1/bffv1connect"
)

func TestChainReportAndSearchIsolation(t *testing.T) {
	h := NewChainHarness(t)
	ctx := context.Background()

	alice := h.FreshBrowser()
	if _, err := alice.Register(ctx, "report_alice", "password1", "Alice"); err != nil {
		t.Fatal(err)
	}
	bob := h.FreshBrowser()
	if _, err := bob.Register(ctx, "report_bob", "password1", "Bob"); err != nil {
		t.Fatal(err)
	}

	csrf := alice.refreshCSRF(ctx)
	syncReq := connect.NewRequest(&bffv1.SyncChangesRequest{
		MutationId: "report-seed", BaseRevision: 0,
		Mutations: []*bffv1.FootprintMutation{
			{Operation: &bffv1.FootprintMutation_UpsertCity{
				UpsertCity: &bffv1.Visit{Code: "330100", Name: "杭州市", Province: "330000", Color: "#4b937d"},
			}},
			{Operation: &bffv1.FootprintMutation_UpsertTrip{
				UpsertTrip: &bffv1.Trip{Id: "trip-hz", City: "330100", Date: "2026-04-12", Note: "春游西湖苏堤与龙井茶园漫步"},
			}},
		},
	})
	alice.writeHeaders(syncReq, csrf, alice.Origin)
	if _, err := alice.Foot.SyncChanges(ctx, syncReq); err != nil {
		t.Fatalf("alice sync: %v", err)
	}

	getReport := func(b *Browser) (*bffv1.GetReportResponse, error) {
		req := connect.NewRequest(&bffv1.GetReportRequest{Timezone: "Asia/Shanghai"})
		b.withTripmapHeaders(req)
		res, err := bffv1connect.NewReportServiceClient(b.HTTP, b.APIURL).GetReport(ctx, req)
		if err != nil {
			return nil, err
		}
		return res.Msg, nil
	}
	searchNotes := func(b *Browser, q string) (*bffv1.SearchNotesResponse, error) {
		req := connect.NewRequest(&bffv1.SearchNotesRequest{Query: q, Limit: 20})
		b.withTripmapHeaders(req)
		res, err := bffv1connect.NewSearchServiceClient(b.HTTP, b.APIURL).SearchNotes(ctx, req)
		if err != nil {
			return nil, err
		}
		return res.Msg, nil
	}

	aliceReport, err := getReport(alice)
	if err != nil {
		t.Fatalf("alice report: %v", err)
	}
	if aliceReport.GetOverview().GetVisitedCities() != 1 || aliceReport.GetOverview().GetTotalTrips() != 1 {
		t.Fatalf("alice overview: %+v", aliceReport.GetOverview())
	}

	bobReport, err := getReport(bob)
	if err != nil {
		t.Fatalf("bob report: %v", err)
	}
	if bobReport.GetOverview().GetVisitedCities() != 0 || bobReport.GetOverview().GetTotalTrips() != 0 {
		t.Fatalf("bob must not see alice stats: %+v", bobReport.GetOverview())
	}

	notes, err := searchNotes(alice, "西湖")
	if err != nil || len(notes.GetMatches()) != 1 || notes.GetMatches()[0].GetNote() == "" {
		t.Fatalf("alice search: %+v err=%v", notes, err)
	}
	bobNotes, err := searchNotes(bob, "西湖")
	if err != nil {
		t.Fatalf("bob search: %v", err)
	}
	if len(bobNotes.GetMatches()) != 0 {
		t.Fatalf("bob must not see alice notes: %+v", bobNotes.GetMatches())
	}
}
