package feedback

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
