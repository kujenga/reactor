package main

import (
	"fmt"
	"math/rand"
	"os"
	"strings"
	"sync"

	"github.com/BeepBoopHQ/go-slackbot"
	log "github.com/Sirupsen/logrus"
	"github.com/jbrukh/bayesian"
	"github.com/nlopes/slack"
	"golang.org/x/net/context"
)

const (
	learnerCount = 10
)

var (
	reactor *Reactor
	mut     sync.RWMutex
)

func main() {
	log.SetLevel(log.DebugLevel)
	log.Infoln("get ready for reactions!")

	bot := slackbot.New(os.Getenv("SLACK_TOKEN"))

	go setup(bot.Client)

	// anything with "look around" causes us to intialize the reactor
	bot.Hear("(?i)look around(.*)").MessageHandler(LookAroundHandler)
	// react to everything else
	bot.Hear(".*").MessageHandler(ReactionHandler)
	bot.Run()
}

var prefixes = []string{
	"I'm guessing the reaction will be",
	"That looks like a",
	"That's sure to get a",
	"I bet that people will react with",
}

// ReactionHandler handles guessing reactions
func ReactionHandler(ctx context.Context, bot *slackbot.Bot, evt *slack.MessageEvent) {
	mut.RLock()
	defer mut.RUnlock()

	if reactor == nil {
		bot.Reply(evt, "I don't know anything about you yet!", slackbot.WithTyping)
		return
	}

	log.Infoln("reacting to:", evt.Text)

	reaction := reactor.Reaction(evt.Text)

	randPrefix := prefixes[rand.Intn(len(prefixes))]
	bot.Reply(evt, fmt.Sprintf("%s :%s:", randPrefix, reaction), slackbot.WithTyping)
}

// LookAroundHandler examines existing messages to gather information
func LookAroundHandler(ctx context.Context, bot *slackbot.Bot, evt *slack.MessageEvent) {
	mut.Lock()
	defer mut.Unlock()

	channels, messages, _ := setup(bot.Client)

	bot.Reply(evt, fmt.Sprintf("I've learned from %d messages in %d channels", len(messages), len(channels)), slackbot.WithTyping)
}

func setup(client *slack.Client) ([]slack.Channel, []*msg, []bayesian.Class) {
	log.Infoln("setting up the reactor")

	channels, err := client.GetChannels(true)
	if err != nil {
		log.Errorln("error getting channels", err)
		return nil, nil, nil
	}

	var messages []*msg
	var classes []bayesian.Class

	for _, channel := range channels {

		history, err := client.GetChannelHistory(channel.ID, slack.HistoryParameters{Oldest: ""})
		if err != nil {
			log.Errorln("error getting channel history", err)
			return nil, nil, nil
		}

		for _, m := range history.Messages {
			messages = append(messages, newMsg(&m))
			for _, r := range m.Reactions {
				classes = append(classes, bayesian.Class(r.Name))
			}
		}
	}

	log.Debugln("reaction classes:\n", classes)
	reactor = NewReactor(classes)

	for _, m := range messages {
		reactor.Learn(m)
	}

	log.Infof("learning on %d messages with %d different reactions in %d channels",
		len(messages), len(classes), len(channels))

	return channels, messages, classes
}

// Reactor handles reacting to slack messages
type Reactor struct {
	Classifier *bayesian.Classifier
	msgs       chan *msg
}

// NewReactor creates a new reactor object
func NewReactor(classes []bayesian.Class) *Reactor {
	r := &Reactor{
		Classifier: bayesian.NewClassifier(classes...),
		msgs:       make(chan *msg, 100),
	}

	for i := 0; i < learnerCount; i++ {
		go func() {
			for msg := range r.msgs {
				r.train(msg)
			}
		}()
	}

	return r
}

// Reaction guesses a reaction for the given text
func (r *Reactor) Reaction(text string) string {
	works := strings.Fields(text)
	scores, inx, _ := r.Classifier.LogScores(works)

	log.Debugln("scores: %v", scores)

	return string(r.Classifier.Classes[inx])
}

// Learn handles learning from a new message
func (r *Reactor) Learn(msg *msg) {
	r.msgs <- msg
}

func (r *Reactor) train(msg *msg) {
	words := strings.Fields(msg.Text)
	log.Debugf("training on message:\n    '%s'\n    reactions: %+v", msg.Text, msg.Reactions)

	for _, reaction := range msg.Reactions {
		for i := 0; i < reaction.Count; i++ {
			r.Classifier.Learn(words, bayesian.Class(reaction.Name))
		}
	}

}

// helper types for classification

type msg struct {
	Text      string
	Reactions []*reaction
}

func newMsg(m *slack.Message) *msg {
	var reactions []*reaction
	for _, r := range m.Reactions {
		reactions = append(reactions, newReaction(&r))
	}

	return &msg{
		Text:      m.Text,
		Reactions: reactions,
	}
}

type reaction struct {
	Name  string
	Count int
}

func newReaction(r *slack.ItemReaction) *reaction {
	return &reaction{
		Name:  r.Name,
		Count: r.Count,
	}
}
