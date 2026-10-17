package itinerary

import (
	"context"
	"errors"

	"connectrpc.com/connect"
	"github.com/kongken/mono/internal/bff/session"
	"github.com/kongken/mono/internal/bff/upstream"
	bffv1 "github.com/kongken/mono/pkg/proto/bff/v1"
	tripmapv1 "github.com/kongken/mono/pkg/proto/tripmap/v1"
)

type Handler struct {
	client         tripmapv1.ItineraryServiceClient
	allowedOrigins []string
}

func New(client tripmapv1.ItineraryServiceClient, allowedOrigins []string) *Handler {
	return &Handler{client: client, allowedOrigins: allowedOrigins}
}

func (h *Handler) ListItineraries(ctx context.Context, req *connect.Request[bffv1.ListItinerariesRequest]) (*connect.Response[bffv1.ListItinerariesResponse], error) {
	ctx = upstream.WithTripmapHeaders(ctx, req.Header())
	out, err := h.client.ListItineraries(ctx, &tripmapv1.ListItinerariesRequest{})
	if err != nil {
		return nil, upstream.ToConnect("tripmap", err)
	}
	return connect.NewResponse(mapList(out)), nil
}

func (h *Handler) GetItinerary(ctx context.Context, req *connect.Request[bffv1.GetItineraryRequest]) (*connect.Response[bffv1.GetItineraryResponse], error) {
	ctx = upstream.WithTripmapHeaders(ctx, req.Header())
	out, err := h.client.GetItinerary(ctx, &tripmapv1.GetItineraryRequest{Id: req.Msg.GetId()})
	if err != nil {
		return nil, upstream.ToConnect("tripmap", err)
	}
	return connect.NewResponse(mapGet(out)), nil
}

func (h *Handler) SaveItinerary(ctx context.Context, req *connect.Request[bffv1.SaveItineraryRequest]) (*connect.Response[bffv1.SaveItineraryResponse], error) {
	if err := session.CheckWrite(req.Header(), h.allowedOrigins, req.Header().Get("Host")); err != nil {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("request rejected"))
	}
	ctx = upstream.WithTripmapHeaders(ctx, req.Header())
	in := req.Msg
	stops := make([]*tripmapv1.ItineraryStopInput, 0, len(in.GetStops()))
	for _, s := range in.GetStops() {
		stops = append(stops, &tripmapv1.ItineraryStopInput{
			Id: s.GetId(), RegionCode: s.GetRegionCode(), Date: s.GetDate(), Note: s.GetNote(),
			Transport: s.GetTransport(), TripId: s.GetTripId(),
		})
	}
	out, err := h.client.SaveItinerary(ctx, &tripmapv1.SaveItineraryRequest{
		Id: in.GetId(), Title: in.GetTitle(), Note: in.GetNote(), Color: in.GetColor(),
		Stops: stops, ExpectedVersion: in.GetExpectedVersion(), MutationId: in.GetMutationId(),
	})
	if err != nil {
		return nil, upstream.ToConnect("tripmap", err)
	}
	return connect.NewResponse(mapSave(out)), nil
}

func (h *Handler) DeleteItinerary(ctx context.Context, req *connect.Request[bffv1.DeleteItineraryRequest]) (*connect.Response[bffv1.DeleteItineraryResponse], error) {
	if err := session.CheckWrite(req.Header(), h.allowedOrigins, req.Header().Get("Host")); err != nil {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("request rejected"))
	}
	ctx = upstream.WithTripmapHeaders(ctx, req.Header())
	out, err := h.client.DeleteItinerary(ctx, &tripmapv1.DeleteItineraryRequest{
		Id: req.Msg.GetId(), ExpectedVersion: req.Msg.GetExpectedVersion(),
	})
	if err != nil {
		return nil, upstream.ToConnect("tripmap", err)
	}
	return connect.NewResponse(&bffv1.DeleteItineraryResponse{Deleted: out.GetDeleted()}), nil
}

func mapList(in *tripmapv1.ListItinerariesResponse) *bffv1.ListItinerariesResponse {
	out := &bffv1.ListItinerariesResponse{Policy: mapPolicy(in.GetPolicy())}
	for _, it := range in.GetItineraries() {
		out.Itineraries = append(out.Itineraries, mapInfo(it))
	}
	return out
}

func mapGet(in *tripmapv1.GetItineraryResponse) *bffv1.GetItineraryResponse {
	return &bffv1.GetItineraryResponse{Itinerary: mapInfo(in.GetItinerary()), Policy: mapPolicy(in.GetPolicy())}
}

func mapSave(in *tripmapv1.SaveItineraryResponse) *bffv1.SaveItineraryResponse {
	return &bffv1.SaveItineraryResponse{
		Itinerary: mapInfo(in.GetItinerary()), Policy: mapPolicy(in.GetPolicy()), Replayed: in.GetReplayed(),
	}
}

func mapPolicy(p *tripmapv1.ItineraryEstimationPolicy) *bffv1.ItineraryEstimationPolicy {
	if p == nil {
		return nil
	}
	return &bffv1.ItineraryEstimationPolicy{
		Provider: p.GetProvider(), ProviderLabel: p.GetProviderLabel(), ExternalApiEnabled: p.GetExternalApiEnabled(),
		CostPolicy: p.GetCostPolicy(), CacheStrategy: p.GetCacheStrategy(), Disclaimer: p.GetDisclaimer(),
	}
}

func mapInfo(in *tripmapv1.ItineraryInfo) *bffv1.ItineraryInfo {
	if in == nil {
		return nil
	}
	out := &bffv1.ItineraryInfo{
		Id: in.GetId(), Title: in.GetTitle(), Note: in.GetNote(), Color: in.GetColor(), Version: in.GetVersion(),
		TotalStraightDistanceKm: in.GetTotalStraightDistanceKm(), TotalEstimatedDistanceKm: in.GetTotalEstimatedDistanceKm(),
		TotalEstimatedDurationMinutes: in.GetTotalEstimatedDurationMinutes(),
		CityCount: in.GetCityCount(), ProvinceCount: in.GetProvinceCount(), FromCache: in.GetFromCache(),
		CreatedAt: in.GetCreatedAt(), UpdatedAt: in.GetUpdatedAt(),
	}
	for _, s := range in.GetStops() {
		out.Stops = append(out.Stops, &bffv1.ItineraryStop{
			Id: s.GetId(), Order: s.GetOrder(), RegionCode: s.GetRegionCode(), RegionName: s.GetRegionName(),
			ProvinceCode: s.GetProvinceCode(), ProvinceName: s.GetProvinceName(), Date: s.GetDate(), Note: s.GetNote(),
			Transport: s.GetTransport(), TripId: s.GetTripId(), Longitude: s.GetLongitude(), Latitude: s.GetLatitude(),
			WgsLongitude: s.GetWgsLongitude(), WgsLatitude: s.GetWgsLatitude(),
		})
	}
	for _, l := range in.GetLegs() {
		out.Legs = append(out.Legs, &bffv1.ItineraryLeg{
			FromOrder: l.GetFromOrder(), ToOrder: l.GetToOrder(), FromRegionCode: l.GetFromRegionCode(),
			FromRegionName: l.GetFromRegionName(), ToRegionCode: l.GetToRegionCode(), ToRegionName: l.GetToRegionName(),
			Transport: l.GetTransport(), StraightDistanceKm: l.GetStraightDistanceKm(),
			EstimatedDistanceKm: l.GetEstimatedDistanceKm(), EstimatedDurationMinutes: l.GetEstimatedDurationMinutes(),
			FromCache: l.GetFromCache(),
		})
	}
	return out
}
