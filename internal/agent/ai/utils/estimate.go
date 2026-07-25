package utils

const charsPerToken = 4

func EstimateTextTokens(text string) int {
	return EstimateTokensForChars(len(text))
}

func EstimateTokensForChars(chars int) int {
	if chars <= 0 {
		return 0
	}
	return (chars + charsPerToken - 1) / charsPerToken
}
