package animap

import "strings"

func ScoreTitle(query, title string) int {
	q := strings.ToLower(strings.TrimSpace(query))
	t := strings.ToLower(strings.TrimSpace(title))
	if q == t {
		return 100
	}
	if strings.Contains(t, q) || strings.Contains(q, t) {
		return 75
	}
	return 0
}
