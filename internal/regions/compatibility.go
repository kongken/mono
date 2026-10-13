package regions

// CanonicalProvince preserves source IDs while correcting legacy island ownership.
// Only the two known records formerly assigned to Taiwan are migrated; unrelated
// invalid province values must still fail the caller's region validation.
func CanonicalProvince(code, province string) string {
	if province == "710000" && (code == "tw-09020" || code == "tw-09007") {
		return "350000"
	}
	return province
}
