package jsonx

func AsString(value any) string {
	s, _ := value.(string)
	return s
}

func FirstText(value any) string {
	items, _ := value.([]any)
	for _, item := range items {
		block, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if text, ok := block["text"].(string); ok {
			return text
		}
	}
	return ""
}
