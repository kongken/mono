package ingress

import (
	"strings"

	"github.com/kongken/mono/pkg/proto/bff/metro/v1/bffmetrov1connect"
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
	bffv1connect.AuthServiceResolveNavigationProcedure:  true,
	bffv1connect.AuthServiceGetAccountDeletionProcedure: true,

	bffv1connect.AccessTokenServiceListAccessTokensProcedure: true,

	// User management reads: nothing is changed, so no CSRF token is needed. They are
	// not exempt from the admin perimeter, which demands the console's exact Origin.
	bffv1connect.UserAdminServiceListUsersProcedure: true,
	bffv1connect.UserAdminServiceGetUserProcedure:   true,
	// The account audit is a read too: same perimeter, session age and role, no CSRF.
	bffv1connect.UserAdminServiceListSecurityAuditProcedure: true,

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

	bffmetrov1connect.CatalogServiceListCitiesProcedure:             true,
	bffmetrov1connect.CatalogServiceGetCityProcedure:                true,
	bffmetrov1connect.CatalogServiceListLinesProcedure:              true,
	bffmetrov1connect.CatalogServiceListRollingStockTopicsProcedure: true,
	bffmetrov1connect.CatalogServiceGetRollingStockTopicProcedure:   true,

	bffmetrov1connect.RiderServiceGetPublicProfileProcedure: true,
	bffmetrov1connect.RiderServiceGetMyProfileProcedure:     true,
	bffmetrov1connect.RiderServiceListMyLinesProcedure:      true,

	// Administrator reads. They still need the metro-admin role.
	bffmetrov1connect.AdminServiceListAdminCitiesProcedure:             true,
	bffmetrov1connect.AdminServiceListAdminRollingStocksProcedure:      true,
	bffmetrov1connect.AdminServiceListAdminLinesProcedure:              true,
	bffmetrov1connect.AdminServiceListAdminRollingStockTopicsProcedure: true,
	bffmetrov1connect.AdminServiceExportCatalogProcedure:               true,
	bffmetrov1connect.AdminServiceListAuditLogsProcedure:               true,
}

// RequiresWriteProtection reports whether Origin/CSRF must be validated for procedure.
func RequiresWriteProtection(procedure string) bool {
	if !strings.HasPrefix(procedure, "/bff.") {
		return false
	}
	return !readOnlyProcedures[procedure]
}
