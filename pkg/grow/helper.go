package grow

// ==== Helper Functions ====

// trunc 截断字符串到 maxLen。超出时保留首尾各 1/3，中间用 "..." 替代。
func trunc(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}

	segmentLen := max(1, maxLen/3)
	headStr := string(runes[:segmentLen])
	tailStr := string(runes[len(runes)-segmentLen:])
	return headStr + "..." + tailStr
}
