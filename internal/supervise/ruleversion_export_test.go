package supervise

import "testing"

// SetRuleVersion gives rule id another version in the table until t ends.
func SetRuleVersion(t *testing.T, id, version string) {
	t.Helper()
	for i := range ruleTable {
		if ruleTable[i].id == id {
			was := ruleTable[i].version
			ruleTable[i].version = version
			t.Cleanup(func() { ruleTable[i].version = was })
			return
		}
	}
	t.Fatalf("no rule %s", id)
}
