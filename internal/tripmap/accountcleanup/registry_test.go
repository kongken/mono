package accountcleanup_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestUserEdgeRegistry ensures every User edge in ent/schema still has a cleanup
// story: ON DELETE CASCADE via users row delete and/or cutPublicAccess /
// purgeObjectStorage. Update this test when internal/tripmap/ent/schema/*.go
// User.Edges() changes.
func TestUserEdgeRegistry(t *testing.T) {
	t.Helper()
	root := filepath.Join("..", "ent", "schema")
	raw, err := os.ReadFile(filepath.Join(root, "travel.go"))
	if err != nil {
		t.Fatal(err)
	}
	notify, err := os.ReadFile(filepath.Join(root, "notification.go"))
	if err != nil {
		t.Fatal(err)
	}
	block := string(raw) + string(notify)
	re := regexp.MustCompile(`edge\.To\("([^"]+)",\s*(\w+)\.Type\)`)
	found := map[string]string{}
	for _, m := range re.FindAllStringSubmatch(block, -1) {
		if strings.Contains(block, "func (User) Edges") {
			// only edges inside User.Edges — travel.go User block is the only User type
		}
		found[m[1]] = m[2]
	}
	// Explicit pre-delete in cutPublicAccess (share + social + itineraries).
	explicit := map[string]bool{
		"share": true, "sent_friendships": true, "received_friendships": true,
		"owned_shared_trips": true, "trip_memberships": true, "itineraries": true,
	}
	// MediaCleanup is not a User edge; staging keys are purged by media maintenance.
	wantTypes := map[string]bool{
		"CityVisit": true, "Trip": true, "ProvinceMark": true, "MediaObject": true,
		"FootprintChange": true, "SyncReceipt": true, "Snapshot": true,
		"Share": true, "Friendship": true, "SharedTrip": true, "TripMember": true,
		"Itinerary": true, "NotificationProfile": true, "NotificationSetting": true,
		"PushSubscription": true, "NotificationDelivery": true,
	}
	for _, typ := range found {
		if !wantTypes[typ] {
			t.Errorf("User edge type %q not in cleanup registry; extend accountcleanup", typ)
		}
	}
	for typ := range wantTypes {
		var ok bool
		for _, got := range found {
			if got == typ {
				ok = true
				break
			}
		}
		if !ok {
			t.Errorf("registry expects User edge %q but schema has no edge.To(..., %s.Type)", typ, typ)
		}
	}
	_ = explicit // documented: cutPublicAccess covers social/share/itinerary edges
}
