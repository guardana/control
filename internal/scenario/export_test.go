package scenario

// ForgetReferences drops what the reader keeps for substitution, so a test
// can compare a read scenario with one written out by hand.
func ForgetReferences(s *Scenario) {
	for _, st := range s.Steps {
		if st.Call != nil {
			st.Call.refs = nil
		}
	}
}
