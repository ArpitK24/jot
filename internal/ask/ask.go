package ask

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/ArpitK24/jot/internal/chat"
	"github.com/ArpitK24/jot/internal/dedup"
	"github.com/ArpitK24/jot/internal/embed"
	"github.com/ArpitK24/jot/internal/store"
)

// Session answers questions against a set of notes and saves the chats.
type Session struct {
	Chats    *Chats
	Notes    func() []*store.Memory // every note Ask may recall
	Index    *embed.Index           // semantic matching; nil means keyword only
	Registry *chat.Registry
	Now      func() time.Time
}

// Options for one question.
type Options struct {
	Model    string // model ref; empty means the registry's default
	NoRecall bool   // send the conversation without notes or earlier chats

	OnRecall func(Recalled, string) // what was found, and the model about to answer
	OnDelta  func(string)           // each piece of the reply
}

// Reply is the result of a question.
type Reply struct {
	Turn    *Turn    // the assistant turn, nil when the model wrote nothing
	Recall  Recalled // notes and exchanges given to the model
	Model   string   // model ref that answered
	Suggest string   // one memory-worthy statement from the question, or ""
	Stopped bool     // the caller cancelled mid-reply
}

func (s *Session) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// Send adds text as the user's turn (unless it is empty, which asks for a
// new reply to the last question), recalls, streams the reply and saves
// the conversation. Cancelling ctx stops the reply; what was written so far
// is kept and marked stopped, and Send returns no error.
func (s *Session) Send(ctx context.Context, c *Conversation, text string, opt Options) (*Reply, error) {
	text = strings.TrimSpace(text)
	if text != "" {
		c.Turns = append(c.Turns, Turn{Role: User, At: s.now().UTC().Truncate(time.Second), Content: text})
	}
	question := ""
	for i := len(c.Turns) - 1; i >= 0; i-- {
		if c.Turns[i].Role == User {
			question = c.Turns[i].Content
			break
		}
	}
	if question == "" {
		return nil, errors.New("nothing to answer: send a message first")
	}
	// A retry replaces a reply that failed or was stopped.
	if text == "" && len(c.Turns) > 0 && c.Turns[len(c.Turns)-1].Role == Assistant {
		c.Turns = c.Turns[:len(c.Turns)-1]
	}

	ref := opt.Model
	if ref == "" {
		var err error
		if ref, err = s.Registry.DefaultModel(ctx, nil); err != nil {
			s.save(c)
			return nil, err
		}
	}
	prov, m, err := s.Registry.Resolve(ref)
	if err != nil {
		s.save(c)
		return nil, err
	}
	ref = m.String()

	reply := &Reply{Model: ref}
	var mems []*store.Memory
	if s.Notes != nil {
		mems = s.Notes()
	}
	if !opt.NoRecall {
		chats, _ := s.Chats.List()
		// The question alone first: adding the previous one pulls in notes
		// about that. A follow-up that finds nothing on its own ("and on
		// Windows?") gets the previous question as context.
		reply.Recall = Recall(mems, chats, c.ID, question, s.Index)
		if len(reply.Recall.Notes) == 0 {
			if q := Query(c, question); q != question {
				reply.Recall = Recall(mems, chats, c.ID, q, s.Index)
			}
		}
	}
	if opt.OnRecall != nil {
		opt.OnRecall(reply.Recall, ref)
	}

	onDelta := opt.OnDelta
	if onDelta == nil {
		onDelta = func(string) {}
	}
	out, err := prov.Stream(ctx, chat.Request{Model: m.Model, Messages: Messages(c, reply.Recall, s.now())}, onDelta)
	reply.Stopped = errors.Is(err, context.Canceled)
	if err == nil && strings.TrimSpace(out) == "" {
		err = errors.New(m.Model + " returned an empty reply; try again or pick another model")
	}
	if strings.TrimSpace(out) != "" {
		t := Turn{Role: Assistant, Model: ref, At: s.now().UTC().Truncate(time.Second), Content: strings.TrimSpace(out), Stopped: reply.Stopped || err != nil}
		for _, n := range reply.Recall.Notes {
			t.Notes = append(t.Notes, n.ID)
		}
		for _, e := range reply.Recall.Past {
			t.Chats = append(t.Chats, e.ChatID)
		}
		c.Turns = append(c.Turns, t)
		reply.Turn = &c.Turns[len(c.Turns)-1]
	}
	if serr := s.save(c); serr != nil && err == nil {
		err = serr
	}
	if reply.Stopped {
		return reply, nil
	}
	if err != nil {
		return reply, err
	}
	if text != "" {
		reply.Suggest = s.suggestion(text, mems)
	}
	return reply, nil
}

func (s *Session) save(c *Conversation) error {
	if len(c.Turns) == 0 {
		return nil
	}
	return s.Chats.Save(c, s.now())
}

// suggestion offers the question's memory-worthy statement, unless a note
// already says it.
func (s *Session) suggestion(text string, mems []*store.Memory) string {
	sug := Suggest(text)
	if sug == "" {
		return ""
	}
	if sim, _ := dedup.SimilarWith(mems, sug, 0, s.Index); len(sim) > 0 {
		return ""
	}
	return sug
}
