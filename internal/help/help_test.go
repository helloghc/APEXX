package help

import "testing"

func TestTopicsAreComplete(t *testing.T) {
	ts := Topics()
	if len(ts) < 20 {
		t.Fatalf("%d topics", len(ts))
	}
	for _, x := range ts {
		if x.Title == "" || x.Route == "" || x.Module == "" || x.Level == "" || len(x.Body) < 40 {
			t.Errorf("incomplete topic: %+v", x)
		}
	}
}
