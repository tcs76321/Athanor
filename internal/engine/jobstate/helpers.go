package jobstate

import "strconv"

func parseInt(v string) int {
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		return 0
	}
	return n
}

func itoa(n int) string { return strconv.Itoa(n) }
