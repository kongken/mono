package catalog

import (
	"context"
	"testing"

	"connectrpc.com/connect"
	bffmetrov1 "github.com/kongken/mono/pkg/proto/bff/metro/v1"
	metrov1 "github.com/kongken/mono/pkg/proto/metro/v1"
	"google.golang.org/grpc"
)

type discoveryClient struct{ metrov1.CatalogServiceClient }

func (discoveryClient) GetCity(context.Context, *metrov1.GetCityRequest, ...grpc.CallOption) (*metrov1.GetCityResponse, error) {
	return &metrov1.GetCityResponse{City: &metrov1.City{Slug: "shanghai", Region: "east"}, Lines: []*metrov1.Line{{Code: "sh-1", VehicleType: "a", StationNames: []string{"富锦路", "人民广场", "莘庄"}}}}, nil
}

func TestPublicAdapterPreservesDiscoveryFields(t *testing.T) {
	res, err := New(discoveryClient{}).GetCity(context.Background(), connect.NewRequest(&bffmetrov1.GetCityRequest{Slug: "shanghai"}))
	if err != nil {
		t.Fatal(err)
	}
	if res.Msg.City.Region != "east" || res.Msg.Lines[0].VehicleType != "a" || len(res.Msg.Lines[0].StationNames) != 3 || res.Msg.Lines[0].StationNames[1] != "人民广场" {
		t.Fatalf("browser lost discovery metadata: %v", res.Msg)
	}
}
