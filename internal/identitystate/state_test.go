package identitystate

import "testing"

func TestOnlyExplicitTransitionsAreAllowed(t *testing.T) {
	states := []string{"draft", "submitted", "approved", "needs_information", "rejected", "withdrawn", "discarded", "", "unknown"}
	allowed := map[string][]string{"draft": {"submitted", "discarded", "withdrawn"}, "submitted": {"approved", "needs_information", "rejected", "withdrawn"}, "approved": {"withdrawn"}, "needs_information": {"withdrawn"}, "rejected": {"withdrawn"}}
	for _, from := range states {
		for _, to := range states {
			want := false
			for _, next := range allowed[from] {
				if next == to {
					want = true
				}
			}
			if got := Transition(from, to) == nil; got != want {
				t.Errorf("%s -> %s allowed=%t want=%t", from, to, got, want)
			}
		}
	}
}

func TestVerificationDoesNotDefaultUnknownState(t *testing.T) {
	for _, state := range []string{"", "unknown", " approved "} {
		if _, err := FromSession(state); err == nil {
			t.Errorf("accepted %q", state)
		}
	}
	got, err := FromSession("submitted")
	if err != nil || got.Status != "pending" || got.Substatus != "manual_review" {
		t.Fatalf("submitted=%+v,%v", got, err)
	}
}
