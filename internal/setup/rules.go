package setup

import (
	"errors"
	"os"
	"strings"
)

// Some tools read a global instructions file into every session. jot adds a
// short marked block there, so the agent checks shared memory on its own,
// without the user having to ask. The MCP server's own instructions say the
// same, but not every client passes those to the model.

const (
	rulesStart = "<!-- jot:start -->"
	rulesEnd   = "<!-- jot:end -->"
)

// RulesBlock is the text jot keeps in a tool's instructions file.
const RulesBlock = rulesStart + `
## Shared memory (jot)

You have a jot MCP server: memory shared with the user's other AI tools.

- Before starting a task, call jot ` + "`recall`" + ` with a few words about the task, and follow the preferences and decisions it returns. Do this without being asked.
- When the user states a lasting preference, makes a decision, or corrects you, save it with jot ` + "`remember`" + ` as one short, self-contained fact. If it says a similar memory exists, ` + "`update`" + ` that one instead.
- Don't save secrets, one-off details, or anything the user asks you not to keep.
` + rulesEnd + "\n"

// rulesBlock returns the jot block in text, or "".
func rulesBlock(text string) string {
	i := strings.Index(text, rulesStart)
	if i < 0 {
		return ""
	}
	j := strings.Index(text[i:], rulesEnd)
	if j < 0 {
		return text[i:]
	}
	end := i + j + len(rulesEnd)
	if end < len(text) && text[end] == '\n' {
		end++
	}
	return text[i:end]
}

func removeRulesBlock(text string) string {
	blk := rulesBlock(text)
	if blk == "" {
		return text
	}
	i := strings.Index(text, blk)
	before, after := text[:i], text[i+len(blk):]
	if after == "" && strings.HasSuffix(before, "\n\n") {
		before = before[:len(before)-1] // the blank line jot added before its block
	}
	return before + after
}

// rulesCurrent reports whether c's instructions file has jot's block as
// it is now. A client without an instructions file always counts as current.
func rulesCurrent(c Client) (bool, error) {
	if c.Rules == "" {
		return true, nil
	}
	data, err := os.ReadFile(c.Rules)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return rulesBlock(string(data)) == RulesBlock, nil
}

func applyRules(c Client) error {
	if c.Rules == "" {
		return nil
	}
	data, err := os.ReadFile(c.Rules)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	text := string(data)
	if blk := rulesBlock(text); blk != "" {
		if blk == RulesBlock {
			return nil
		}
		text = strings.Replace(text, blk, RulesBlock, 1)
	} else {
		if text != "" && !strings.HasSuffix(text, "\n") {
			text += "\n"
		}
		if strings.TrimSpace(text) != "" {
			text += "\n"
		}
		text += RulesBlock
	}
	return write(c.Rules, data, []byte(text))
}

func removeRules(c Client) (bool, error) {
	if c.Rules == "" {
		return false, nil
	}
	data, err := os.ReadFile(c.Rules)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if rulesBlock(string(data)) == "" {
		return false, nil
	}
	return true, write(c.Rules, data, []byte(removeRulesBlock(string(data))))
}
