package openclaw

import (
	"path/filepath"
	"strings"
)

const Mode = "openclaw"

var toolClassMap = map[string]string{
	"exec":             "shell",
	"process":          "shell",
	"read":             "file",
	"write":            "file",
	"edit":             "file",
	"message":          "message",
	"memory_search":    "memory",
	"memory_get":       "memory",
	"web_fetch":        "http",
	"http":             "http",
	"sessions_spawn":   "agent",
	"sessions_send":    "agent",
	"sessions_list":    "agent",
	"sessions_history": "agent",
	"sessions_yield":   "agent",
	"agents_list":      "agent",
	"subagents":        "agent",
	"canvas":           "system",
	"tts":              "system",
	"session_status":   "system",
	"s3":               "aws",
	"sts":              "aws",
	"image":            "other",
	"pdf":              "other",
}

func DeriveModelFamily(provider, model string) string {
	p := strings.ToLower(provider)
	m := strings.ToLower(model)
	switch {
	case p == "amazon-bedrock" || p == "bedrock":
		return "bedrock"
	case p == "openclaw":
		return "openclaw"
	case strings.Contains(p, "anthropic") || strings.Contains(m, "claude") || strings.Contains(m, "anthropic"):
		return "anthropic"
	case strings.Contains(p, "openai") || strings.Contains(m, "gpt") || strings.HasPrefix(m, "o1") || strings.HasPrefix(m, "o3") || strings.HasPrefix(m, "o4"):
		return "openai"
	case strings.Contains(p, "gemini") || strings.Contains(m, "gemini"):
		return "gemini"
	default:
		return "unknown"
	}
}

func DeriveToolClass(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	if value, ok := toolClassMap[name]; ok {
		return value
	}
	if strings.Contains(name, "search") {
		return "search"
	}
	if strings.Contains(name, "http") || strings.Contains(name, "web") || strings.Contains(name, "fetch") {
		return "http"
	}
	if strings.Contains(name, "file") || strings.Contains(name, "read") || strings.Contains(name, "write") {
		return "file"
	}
	return "other"
}

func DeriveSessionType(path, cwd string, firstUserText string) string {
	lowerPath := strings.ToLower(filepath.ToSlash(path))
	lowerCWD := strings.ToLower(filepath.ToSlash(cwd))
	lowerText := strings.ToLower(firstUserText)
	switch {
	case strings.Contains(lowerText, "[subagent context]"), strings.Contains(lowerPath, "subagent"), strings.Contains(lowerPath, "/agents/sub"), strings.Contains(lowerCWD, "subagent"):
		return "subagent"
	case strings.Contains(lowerPath, "heartbeat"):
		return "heartbeat"
	case strings.Contains(lowerPath, "cron"):
		return "cron"
	case strings.Contains(lowerPath, "ephemeral"), strings.Contains(lowerCWD, "/tmp/"):
		return "ephemeral"
	case strings.Contains(lowerPath, "/agents/main/"), strings.Contains(lowerPath, "/main/"), strings.Contains(lowerCWD, "/workspace"):
		return "main"
	default:
		return "unknown"
	}
}

func DeriveErrorType(customType string) string {
	s := strings.ToLower(customType)
	switch {
	case strings.Contains(s, "prompt"):
		return "prompt"
	case strings.Contains(s, "auth"):
		return "auth"
	case strings.Contains(s, "quota"), strings.Contains(s, "rate"):
		return "quota"
	case strings.Contains(s, "config"):
		return "config"
	case strings.Contains(s, "transient"):
		return "transient"
	case strings.Contains(s, "permanent"):
		return "permanent"
	default:
		return "unknown"
	}
}
