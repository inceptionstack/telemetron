package contract

var enumValues = map[string]map[string]struct{}{
	"outcome": {
		"success": {},
		"error":   {},
		"aborted": {},
		"timeout": {},
		"unknown": {},
	},
	"model.family": {
		"anthropic": {},
		"openai":    {},
		"bedrock":   {},
		"gemini":    {},
		"openclaw":  {},
		"unknown":   {},
	},
	"tool.class": {
		"shell":   {},
		"file":    {},
		"http":    {},
		"system":  {},
		"aws":     {},
		"message": {},
		"search":  {},
		"memory":  {},
		"agent":   {},
		"other":   {},
		"unknown": {},
	},
	"error.type": {
		"transient": {},
		"permanent": {},
		"config":    {},
		"auth":      {},
		"quota":     {},
		"prompt":    {},
		"unknown":   {},
	},
	"session.type": {
		"main":      {},
		"heartbeat": {},
		"cron":      {},
		"subagent":  {},
		"ephemeral": {},
		"unknown":   {},
	},
}

func NormalizeValue(key, value string) string {
	allowed, ok := enumValues[key]
	if !ok {
		return value
	}
	if _, ok := allowed[value]; ok {
		return value
	}
	return "unknown"
}

func NormalizeMetric(name string, attrs map[string]string) (map[string]string, bool) {
	if !AllowedMetric(name) {
		return nil, false
	}
	out := make(map[string]string, len(attrs))
	for key, value := range attrs {
		if _, ok := allowedMetricAttrs[name][key]; !ok {
			continue
		}
		out[key] = NormalizeValue(key, value)
	}
	return out, true
}
