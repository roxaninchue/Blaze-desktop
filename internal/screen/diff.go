package screen

import (
	"fmt"
	"strings"
)

// Diff describes what changed between two encodings, line by line. ok is false
// when so much changed that the full text is the better answer.
func Diff(old, cur string) (string, bool) {
	a := strings.Split(strings.TrimRight(old, "\n"), "\n")
	b := strings.Split(strings.TrimRight(cur, "\n"), "\n")
	if len(a) > 1500 || len(b) > 1500 {
		return "", false
	}
	// LCS table (small inputs: a few hundred lines).
	n, m := len(a), len(b)
	dp := make([][]int32, n+1)
	for i := range dp {
		dp[i] = make([]int32, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if a[i] == b[j] {
				dp[i][j] = dp[i+1][j+1] + 1
			} else if dp[i+1][j] >= dp[i][j+1] {
				dp[i][j] = dp[i+1][j]
			} else {
				dp[i][j] = dp[i][j+1]
			}
		}
	}
	var out []string
	changed := 0
	i, j := 0, 0
	for i < n || j < m {
		switch {
		case i < n && j < m && a[i] == b[j]:
			i++
			j++
		case j < m && (i == n || dp[i][j+1] >= dp[i+1][j]):
			out = append(out, "+ "+strings.TrimLeft(b[j], " "))
			changed++
			j++
		default:
			out = append(out, "- "+strings.TrimLeft(a[i], " "))
			changed++
			i++
		}
	}
	if changed == 0 {
		return "", true
	}
	if changed*2 > m+1 || changed > 120 {
		return "", false
	}
	return fmt.Sprintf("%d lines changed:\n%s\n", changed, strings.Join(out, "\n")), true
}
