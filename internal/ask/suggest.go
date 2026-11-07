package ask

import (
	"regexp"
	"strings"
)

// Statements worth keeping: a preference, a standing rule or a decision,
// said in the user's own words. Questions and requests are not.
var memoryWorthy = []*regexp.Regexp{
	regexp.MustCompile(`(?i)^(i|we)\s+(always|never|usually|prefer|like to|don'?t like|hate|love|only|avoid)\b`),
	regexp.MustCompile(`(?i)^(i|we)('ve|\s+have)?\s+(decided|chosen|chose|agreed|settled on|switched to|moved to)\b`),
	regexp.MustCompile(`(?i)^(i|we)('ll|\s+will)\s+(use|go with|stick with|keep|stop)\b`),
	regexp.MustCompile(`(?i)^(i|we)\s+(use|work in|work with|write|deploy|ship|run)\s+\S+`),
	regexp.MustCompile(`(?i)^(let'?s|lets)\s+(use|go with|stick with|keep|always|never)\b`),
	regexp.MustCompile(`(?i)^(always|never|please always|please never|don'?t ever)\s+\w+`),
	regexp.MustCompile(`(?i)^(from now on|going forward|in future|in the future),?\s+\w+`),
	regexp.MustCompile(`(?i)^(my|our)\s+[\w\s'-]{2,40}?\s+(is|are)\s+\S+`),
}

var sentenceEnd = regexp.MustCompile(`[.!?\n]+\s*`)

// Suggest returns the first sentence in text that states a preference,
// rule or decision, or "" when there is none. It never saves anything; the
// UI offers it and the user decides.
func Suggest(text string) string {
	idx := sentenceEnd.FindAllStringIndex(text, -1)
	start := 0
	var sentences []string
	for _, ix := range idx {
		sentences = append(sentences, text[start:ix[1]])
		start = ix[1]
	}
	if start < len(text) {
		sentences = append(sentences, text[start:])
	}
	for _, s := range sentences {
		s = strings.TrimSpace(s)
		if strings.HasSuffix(s, "?") || len(s) < 12 || len(s) > 220 {
			continue
		}
		body := strings.TrimRight(s, ".! ")
		for _, re := range memoryWorthy {
			if re.MatchString(body) {
				if !strings.HasSuffix(s, ".") && !strings.HasSuffix(s, "!") {
					s += "."
				}
				return strings.ToUpper(s[:1]) + s[1:]
			}
		}
	}
	return ""
}
