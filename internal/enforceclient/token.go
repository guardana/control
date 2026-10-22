package enforceclient

// IsToken reports whether s is at most limit characters of [A-Za-z0-9._-],
// the alphabet of a request key. A token can break neither a header nor a
// line, so the receiver and the hook bridge hold to it what they read.
func IsToken(s string, limit int) bool {
	if len(s) > limit {
		return false
	}
	for i := 0; i < len(s); i++ {
		if c := s[i]; (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && (c < '0' || c > '9') && c != '.' && c != '_' && c != '-' {
			return false
		}
	}
	return true
}
