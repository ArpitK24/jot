package mcp

import (
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"
)

// MaxMemoryLen is the longest memory remember accepts, in characters. A
// memory is meant to be one lasting fact; anything longer is usually a
// transcript, a log or several memories at once, and recalls badly.
const MaxMemoryLen = 600

// secretPatterns match text that looks like a credential. jot stores
// memories as plain text in git, and every connected agent can read them,
// so these are refused outright rather than saved with a warning.
var secretPatterns = []struct {
	what string
	re   *regexp.Regexp
}{
	{"a private key", regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----`)},
	{"an AWS access key", regexp.MustCompile(`\b(AKIA|ASIA)[0-9A-Z]{16}\b`)},
	{"a GitHub token", regexp.MustCompile(`\b(gh[pousr]_[A-Za-z0-9]{30,}|github_pat_[A-Za-z0-9_]{40,})\b`)},
	{"an API key", regexp.MustCompile(`\b(sk|rk|pk)[-_](live|test|proj|ant|or)?[-_]?[A-Za-z0-9_-]{20,}`)},
	{"a Slack token", regexp.MustCompile(`\bxox[abposr]-[A-Za-z0-9-]{10,}`)},
	{"a Google API key", regexp.MustCompile(`\bAIza[0-9A-Za-z_-]{35}\b`)},
	{"a JSON web token", regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{10,}\.eyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}`)},
	{"a secret value", regexp.MustCompile(`(?i)\b(api[_-]?key|secret|access[_-]?token|auth[_-]?token|client[_-]?secret)\s*[:=]\s*["']?[A-Za-z0-9_\-./+]{16,}`)},
	{"a URL with a password in it", regexp.MustCompile(`[a-z][a-z0-9+.-]*://[^\s/:@]+:[^\s/@]+@`)},
}

// checkMemory says why text should not be saved as a memory, or returns
// nil. The errors are written for the agent: they say what to do instead.
func checkMemory(text string) error {
	t := strings.TrimSpace(text)
	if t == "" {
		return fmt.Errorf("Not saved: text is empty. Pass one short, self-contained statement, " +
			"e.g. \"Use pnpm, never npm, in JS projects.\"")
	}
	if what := secretIn(t); what != "" {
		return fmt.Errorf("Not saved: this looks like it contains %s. jot keeps memories as plain text in git, "+
			"readable by every connected agent, so it never stores credentials. Save where the secret lives instead, "+
			"e.g. \"The Stripe test key is in 1Password under 'Stripe dev'\", or leave it out.", what)
	}
	if n := utf8.RuneCountInString(t); n > MaxMemoryLen {
		return fmt.Errorf("Not saved: %d characters is too long for one memory (limit %d). "+
			"Keep only the lasting rule or fact in a sentence or two, and save separate facts as separate memories. "+
			"Don't save logs, code, transcripts or task notes.", n, MaxMemoryLen)
	}
	return nil
}

// passwordRe finds "password: X" style values; looksLikePassword then
// keeps only values that read like a credential, not like a policy
// ("password is required", "password: at least 12 characters").
var passwordRe = regexp.MustCompile(`(?i)\b(password|passwd|pwd|passphrase)\s*(is|[:=])\s*["'` + "`" + `]?([^\s"'` + "`" + `]+)`)

func looksLikePassword(v string) bool {
	if len(v) < 6 {
		return false
	}
	return strings.ContainsAny(v, "0123456789!@#$%^&*_+=?~")
}

func secretIn(text string) string {
	for _, p := range secretPatterns {
		if p.re.MatchString(text) {
			return p.what
		}
	}
	for _, m := range passwordRe.FindAllStringSubmatch(text, -1) {
		if looksLikePassword(m[3]) {
			return "a password"
		}
	}
	return ""
}

// memoryTips returns a gentle note for text that was saved but could be
// better, or "".
func memoryTips(text string) string {
	points := 0
	for _, line := range strings.Split(text, "\n") {
		l := strings.TrimSpace(line)
		if strings.HasPrefix(l, "- ") || strings.HasPrefix(l, "* ") || (len(l) > 2 && l[0] >= '0' && l[0] <= '9' && (l[1] == '.' || l[1] == ')')) {
			points++
		}
	}
	if points >= 3 {
		return fmt.Sprintf(" Tip: this holds %d separate points; one memory per fact recalls better next time.", points)
	}
	return ""
}
