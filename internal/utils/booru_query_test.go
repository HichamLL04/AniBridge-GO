package utils

import "testing"

func TestParseBooruQuery(t *testing.T) {
	q := ParseBooruQuery(`solo -"bad tag" blue~red`)
	if len(q.Include) != 1 || q.Include[0] != "solo" {
		t.Fatalf("include mismatch: %#v", q.Include)
	}
	if len(q.Exclude) != 1 || q.Exclude[0] != "bad tag" {
		t.Fatalf("exclude mismatch: %#v", q.Exclude)
	}
	if len(q.Or) != 2 {
		t.Fatalf("or mismatch: %#v", q.Or)
	}
}
