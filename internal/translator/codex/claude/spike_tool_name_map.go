package claude

func ClaudeToolNameMap(original []byte) map[string]string {
	return buildReverseMapFromClaudeOriginalToShort(original)
}
