// Package world adapts the browser's bff.tripmap.v1.WorldService (the world footprint
// document, docs/trip-map/world-spec.md section 9) to tripmap's internal
// tripmap.v1.WorldService. It adds nothing of its own: the session, CSRF and Origin
// checks are the BFF ingress's, the user comes from the session, and every rule about
// the document is the business service's.
package world

import (
	"context"

	"connectrpc.com/connect"
	"github.com/kongken/mono/internal/bff/upstream"
	bfftripmapv1 "github.com/kongken/mono/pkg/proto/bff/tripmap/v1"
	tripmapv1 "github.com/kongken/mono/pkg/proto/tripmap/v1"
)

type Handler struct {
	client tripmapv1.WorldServiceClient
}

func New(client tripmapv1.WorldServiceClient) *Handler {
	return &Handler{client: client}
}

func (h *Handler) GetWorld(ctx context.Context, req *connect.Request[bfftripmapv1.GetWorldRequest]) (*connect.Response[bfftripmapv1.GetWorldResponse], error) {
	ctx = upstream.WithTripmapHeaders(ctx, req.Header())
	out, err := h.client.GetWorld(ctx, &tripmapv1.GetWorldRequest{})
	if err != nil {
		return nil, upstream.ToConnect("tripmap", err)
	}
	res := connect.NewResponse(&bfftripmapv1.GetWorldResponse{Revision: out.GetRevision(), Document: toBrowser(out.GetDocument())})
	res.Header().Set("Cache-Control", "no-store")
	return res, nil
}

func (h *Handler) ReplaceWorld(ctx context.Context, req *connect.Request[bfftripmapv1.ReplaceWorldRequest]) (*connect.Response[bfftripmapv1.ReplaceWorldResponse], error) {
	ctx = upstream.WithTripmapHeaders(ctx, req.Header())
	in := req.Msg
	out, err := h.client.ReplaceWorld(ctx, &tripmapv1.ReplaceWorldRequest{
		BaseRevision: in.GetBaseRevision(), RequestId: in.GetRequestId(), Document: toInternal(in.GetDocument()),
	})
	if err != nil {
		return nil, upstream.ToConnect("tripmap", err)
	}
	res := connect.NewResponse(&bfftripmapv1.ReplaceWorldResponse{
		Applied: out.GetApplied(), Replayed: out.GetReplayed(), Revision: out.GetRevision(),
		Document: toBrowser(out.GetDocument()), CloudRevision: out.GetCloudRevision(),
	})
	res.Header().Set("Cache-Control", "no-store")
	return res, nil
}

// toInternal copies the browser's document field by field. A missing document stays
// missing so the business service rejects it as one.
func toInternal(in *bfftripmapv1.WorldDocument) *tripmapv1.WorldDocument {
	if in == nil {
		return nil
	}
	out := &tripmapv1.WorldDocument{Kind: in.GetKind(), Version: in.GetVersion()}
	if n := len(in.GetCountries()); n > 0 {
		out.Countries = make([]*tripmapv1.WorldCountry, 0, n)
	}
	for _, c := range in.GetCountries() {
		if c == nil {
			out.Countries = append(out.Countries, nil)
			continue
		}
		out.Countries = append(out.Countries, &tripmapv1.WorldCountry{Code: c.GetCode(), Color: c.GetColor()})
	}
	if n := len(in.GetPlaces()); n > 0 {
		out.Places = make([]*tripmapv1.WorldPlace, 0, n)
	}
	for _, p := range in.GetPlaces() {
		if p == nil {
			out.Places = append(out.Places, nil)
			continue
		}
		place := &tripmapv1.WorldPlace{
			Id: p.GetId(), Name: p.GetName(), Country: p.GetCountry(), LatE6: p.GetLatE6(), LngE6: p.GetLngE6(),
		}
		for _, t := range p.GetTrips() {
			if t == nil {
				place.Trips = append(place.Trips, nil)
				continue
			}
			place.Trips = append(place.Trips, &tripmapv1.WorldTrip{Id: t.GetId(), Date: t.GetDate(), Note: t.GetNote()})
		}
		out.Places = append(out.Places, place)
	}
	return out
}

func toBrowser(in *tripmapv1.WorldDocument) *bfftripmapv1.WorldDocument {
	if in == nil {
		return nil
	}
	out := &bfftripmapv1.WorldDocument{Kind: in.GetKind(), Version: in.GetVersion()}
	for _, c := range in.GetCountries() {
		out.Countries = append(out.Countries, &bfftripmapv1.WorldCountry{Code: c.GetCode(), Color: c.GetColor()})
	}
	for _, p := range in.GetPlaces() {
		place := &bfftripmapv1.WorldPlace{
			Id: p.GetId(), Name: p.GetName(), Country: p.GetCountry(), LatE6: p.GetLatE6(), LngE6: p.GetLngE6(),
		}
		for _, t := range p.GetTrips() {
			place.Trips = append(place.Trips, &bfftripmapv1.WorldTrip{Id: t.GetId(), Date: t.GetDate(), Note: t.GetNote()})
		}
		out.Places = append(out.Places, place)
	}
	return out
}
