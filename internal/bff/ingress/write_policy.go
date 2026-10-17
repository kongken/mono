package ingress

import (
	"strings"

	bfftripmapv1connect "github.com/kongken/mono/pkg/proto/bff/tripmap/v1/bfftripmapv1connect"
	"github.com/kongken/mono/pkg/proto/bff/v1/bffv1connect"
)

// readOnlyProcedures are browser RPCs that must not require CSRF/Origin checks.
// All other bff.* procedures are state-changing or session-establishing writes.
var readOnlyProcedures = map[string]bool{
	bfftripmapv1connect.StatusServiceGetStatusProcedure: true,

	bffv1connect.AuthServiceMeProcedure:                 true,
	bffv1connect.AuthServiceListSessionsProcedure:       true,
	bffv1connect.AuthServiceIssueCSRFTokenProcedure:     true,
	bffv1connect.AuthServiceListOAuthProvidersProcedure: true,

	bfftripmapv1connect.FootprintServiceGetChangesProcedure:    true,
	bfftripmapv1connect.FootprintServiceGetFootprintsProcedure: true,

	bfftripmapv1connect.SnapshotServiceListSnapshotsProcedure: true,
	bfftripmapv1connect.SnapshotServiceGetSnapshotProcedure:   true,

	bfftripmapv1connect.NotificationServiceGetNotificationSettingsProcedure: true,

	bfftripmapv1connect.FriendServiceGetCollaborationOverviewProcedure: true,
	bfftripmapv1connect.FriendServiceGetFriendFootprintsProcedure:      true,

	bfftripmapv1connect.SharedTripServiceListSharedTripMediaProcedure: true,
	bfftripmapv1connect.SharedTripServiceReadSharedTripMediaProcedure: true,

	bfftripmapv1connect.ItineraryServiceListItinerariesProcedure: true,
	bfftripmapv1connect.ItineraryServiceGetItineraryProcedure:    true,

	bfftripmapv1connect.MediaServiceListMediaProcedure: true,
	bfftripmapv1connect.MediaServiceReadMediaProcedure: true,

	bfftripmapv1connect.ShareServiceGetShareProcedure:     true,
	bfftripmapv1connect.ShareServicePreviewShareProcedure: true,

	bfftripmapv1connect.ReportServiceGetReportProcedure:   true,
	bfftripmapv1connect.SearchServiceSearchNotesProcedure: true,

	bfftripmapv1connect.RegionServiceGetRegionIndexProcedure:       true,
	bfftripmapv1connect.RegionServicePreviewRegionUpgradeProcedure: true,
}

// RequiresWriteProtection reports whether Origin/CSRF must be validated for procedure.
func RequiresWriteProtection(procedure string) bool {
	if !strings.HasPrefix(procedure, "/bff.") {
		return false
	}
	return !readOnlyProcedures[procedure]
}
