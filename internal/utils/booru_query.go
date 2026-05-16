package utils

import "strings"

type BooruQuery struct {
	Include []string `json:"include"`
	Exclude []string `json:"exclude"`
	Or      []string `json:"or"`
}

func ParseBooruQuery(input string) BooruQuery {
	q := BooruQuery{}
	for _, tok := range splitQuery(input) {
		switch {
		case strings.HasPrefix(tok, "-") && len(tok) > 1:
			q.Exclude = append(q.Exclude, tok[1:])
		case strings.Contains(tok, "~"):
			q.Or = append(q.Or, strings.Split(tok, "~")...)
		default:
			q.Include = append(q.Include, tok)
		}
	}
	return q
}

func splitQuery(s string) []string {
	var out []string
	var b strings.Builder
	quoted := false
	for _, r := range s {
		switch r {
		case '"':
			quoted = !quoted
		case ' ', '\t', '\n':
			if quoted {
				b.WriteRune(r)
			} else if b.Len() > 0 {
				out = append(out, b.String())
				b.Reset()
			}
		default:
			b.WriteRune(r)
		}
	}
	if b.Len() > 0 {
		out = append(out, b.String())
	}
	return out
}
