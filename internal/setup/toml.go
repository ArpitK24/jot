package setup

import "strings"

// isJotHeader reports whether line opens a [mcp_servers.jot] table or one
// of its sub-tables such as [mcp_servers.jot.env].
func isJotHeader(line string) bool {
	l := strings.TrimSpace(line)
	if !strings.HasPrefix(l, "[") || strings.HasPrefix(l, "[[") {
		return false
	}
	l = strings.TrimSuffix(strings.TrimPrefix(l, "["), "]")
	l = strings.ReplaceAll(strings.ReplaceAll(l, " ", ""), `"`, "")
	return l == "mcp_servers."+Name || strings.HasPrefix(l, "mcp_servers."+Name+".")
}

func isHeader(line string) bool {
	return strings.HasPrefix(strings.TrimSpace(line), "[")
}

// splitTOML returns the jot tables and everything else, in order.
func splitTOML(text string) (jot, rest string) {
	var in bool
	var j, r strings.Builder
	for _, line := range strings.SplitAfter(text, "\n") {
		if isHeader(line) {
			in = isJotHeader(line)
		}
		if in {
			j.WriteString(line)
		} else {
			r.WriteString(line)
		}
	}
	return j.String(), r.String()
}

func tomlBlock(text string) string { j, _ := splitTOML(text); return j }

func removeTOMLBlock(text string) string {
	_, r := splitTOML(text)
	for strings.Contains(r, "\n\n\n") {
		r = strings.ReplaceAll(r, "\n\n\n", "\n\n")
	}
	return r
}

func tomlQuote(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return `"` + s + `"`
}
