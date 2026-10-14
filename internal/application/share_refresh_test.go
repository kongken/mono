package application_test

import (
	"bytes"
	"testing"
)

// A private note change must be acknowledged after the owner explicitly reviews
// and refreshes, even when the visitor-facing snapshot does not change.
func TestShareRefreshAcknowledgesChangesToHiddenFields(t *testing.T) {
	se := newShareEnv(t)
	token := se.register("share_hidden_refresh")
	revision := se.footprint(token)
	selected := settings("link", false, false, false, false)
	before := se.publish(token, selected)
	_, previous := se.data(tokenOf(before.Url))
	revision = se.mustSync2(token, "hidden-note-change", revision,
		upTrip("330100", "trip1", "2025-05-01", "这条修改仍是私有内容"))
	preview := se.preview(token, selected)
	if preview.FootprintRevision != revision {
		t.Fatal("preview is not based on the current footprints")
	}
	result, err := se.save(token, before.Version, selected, true, preview.FootprintRevision)
	if err != nil {
		t.Fatal(err)
	}
	if result.Share.PublishedRevision != revision || result.Share.CurrentRevision != revision {
		t.Fatalf("successful refresh is still reported as outdated: %+v", result.Share)
	}
	if result.Share.Version != before.Version || result.Share.Url != before.Url {
		t.Fatal("identical public content should keep its version and URL")
	}
	_, after := se.data(tokenOf(before.Url))
	if !bytes.Equal(previous.body, after.body) {
		t.Fatal("refreshing a hidden note changed the public document")
	}
	stored := se.get1(token)
	if stored.PublishedRevision != revision {
		t.Fatal("acknowledged source revision was not persisted")
	}
}
