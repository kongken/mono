package application

import (
	"strings"
	"testing"

	"connectrpc.com/connect"
)

func TestSearchTermsBoundsAndLiteralSyntax(t *testing.T) {
	for _, tc := range []struct {
		query   string
		count   int
		invalid bool
	}{
		{" \t\n ", 0, false}, {"西湖\t徒步", 2, false}, {"100%_ 'OR' <script>", 3, false},
		{strings.Repeat("😀", 50), 1, false}, {strings.Repeat("😀", 51), 0, true},
		{"a b c d e f g h", 8, false}, {"a b c d e f g h i", 0, true}, {"\xff", 0, true},
	} {
		t.Run(tc.query, func(t *testing.T) {
			terms, err := searchTerms(tc.query)
			if tc.invalid {
				if connect.CodeOf(err) != connect.CodeInvalidArgument {
					t.Fatalf("expected invalid argument: %v", err)
				}
			} else if err != nil || len(terms) != tc.count {
				t.Fatalf("terms = %v, error = %v", terms, err)
			}
		})
	}
}
