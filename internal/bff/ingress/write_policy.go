package ingress

import (
	"strings"

	"github.com/kongken/mono/pkg/proto/bff/v1/bffv1connect"
)

// readOnlyProcedures are browser RPCs that must not require CSRF/Origin checks.
// All other bff.v1 procedures are state-changing or session-establishing writes.
var readOnlyProcedures = map[string]bool{
	bffv1connect.StatusServiceGetStatusProcedure: true,

	bffv1connect.AuthServiceMeProcedure:                 true,
	bffv1connect.AuthServiceListSessionsProcedure:       true,
	bffv1connect.AuthServiceIssueCSRFTokenProcedure:     true,
	bffv1connect.AuthServiceListOAuthProvidersProcedure: true,

	bffv1connect.FootprintServiceGetChangesProcedure:    true,
	bffv1connect.FootprintServiceGetFootprintsProcedure: true,

	bffv1connect.SnapshotServiceListSnapshotsProcedure: true,
	bffv1connect.SnapshotServiceGetSnapshotProcedure:   true,

	bffv1connect.NotificationServiceGetNotificationSettingsProcedure: true,

	bffv1connect.FriendServiceGetCollaborationOverviewProcedure: true,
	bffv1connect.FriendServiceGetFriendFootprintsProcedure:      true,

	bffv1connect.SharedTripServiceListSharedTripMediaProcedure: true,
	bffv1connect.SharedTripServiceReadSharedTripMediaProcedure: true,

	bffv1connect.ItineraryServiceListItinerariesProcedure: true,
	bffv1connect.ItineraryServiceGetItineraryProcedure:    true,

	bffv1connect.MediaServiceListMediaProcedure: true,
	bffv1connect.MediaServiceReadMediaProcedure: true,

	bffv1connect.ShareServiceGetShareProcedure:    true,
	bffv1connect.ShareServicePreviewShareProcedure: true,

	bffv1connect.ReportServiceGetReportProcedure: true,
	bffv1connect.SearchServiceSearchNotesProcedure: true,

	bffv1connect.RegionServiceGetRegionIndexProcedure:       true,
	bffv1connect.RegionServicePreviewRegionUpgradeProcedure: true,
}

// RequiresWriteProtection reports whether Origin/CSRF must be validated for procedure.
func RequiresWriteProtection(procedure string) bool {
	if !strings.HasPrefix(procedure, "/bff.v1.") {
		return false
	}
	return !readOnlyProcedures[procedure]
}
