package main

import (
	"bytes"
	"flag"
	"fmt"
	"math/rand"
	"net/http"
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
	// number of goroutines performing learning on training data
	// the bayesian library doesn't seem to be threadsafe, so for now this stays at 1
	learnerCount = 1

	// the number of messages to retrieve from each channel
	messageCount = 100
)

var (
	debug = flag.Bool("debug", false, "increase the logging level to debug")

	reactor *Reactor
	mut     sync.RWMutex
)

func port() string {
	val := "8080"
	if p := os.Getenv("PORT"); p != "" {
		val = p
	}
	return ":" + val
}

func main() {
	flag.Parse()

	if *debug {
		log.SetLevel(log.DebugLevel)
		log.Debugln("Debug mode is on")
	}

	log.Infoln("get ready for reactions!")

	bot := slackbot.New(os.Getenv("SLACK_TOKEN"))

	go setup(bot.Client)

	// reintialize the reactor
	bot.Hear("^update&").MessageHandler(UpdateHandler)
	// display a help message to the user
	bot.Hear("^help$").MessageHandler(HelpHandler)
	// react to everything else
	bot.Hear(".*").MessageHandler(ReactionHandler)
	go bot.Run()

	// for heroku, we have a minimal http handler
	log.Errorln(http.ListenAndServe(port(), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("Reactor"))
	})))
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

// HelpHandler responds with help information for this channel
func HelpHandler(ctx context.Context, bot *slackbot.Bot, evt *slack.MessageEvent) {
	mut.RLock()
	defer mut.RUnlock()

	log.Infoln("Helping the user")

	buf := bytes.NewBuffer(nil)
	buf.WriteString(fmt.Sprintf("I guess reactions to messages based on the past %d seen in each channel.\n", messageCount))

	if reactor != nil {
		buf.WriteString(" It is currently aware of the following reactions:")
		i := 0
		for _, class := range reactor.Classifier.Classes {
			if i == 0 {
				buf.WriteString(" :")
			} else if i == len(reactor.Classifier.Classes)-1 {
				buf.WriteString(", and :")
			} else {
				buf.WriteString(", :")
			}
			buf.WriteString(string(class))
			buf.WriteString(":")
		}
	}

	bot.Reply(evt, buf.String(), slackbot.WithTyping)
}

// UpdateHandler examines existing messages to gather information
func UpdateHandler(ctx context.Context, bot *slackbot.Bot, evt *slack.MessageEvent) {
	mut.Lock()
	defer mut.Unlock()

	channels, messages, _ := setup(bot.Client)

	bot.Reply(evt, fmt.Sprintf("I've learned from %d messages in %d channels", len(messages), len(channels)), slackbot.WithTyping)
}

func setup(client *slack.Client) ([]slack.Channel, []*msg, []bayesian.Class) {
	log.Infoln("setting up the reactor...")

	channels, err := client.GetChannels(true)
	if err != nil {
		log.Errorln("error getting channels", err)
		return nil, nil, nil
	}

	var messages []*msg
	classMap := make(map[string]struct{})

	for _, channel := range channels {

		history, err := client.GetChannelHistory(channel.ID, slack.HistoryParameters{
			Count: messageCount,
		})
		if err != nil {
			log.Errorf("error getting channel history for '%s' :%v", channel.Name, err)
			continue
		}

		log.Debugf("retrieved %d messages from channel %s", len(history.Messages), channel.Name)

		for _, m := range history.Messages {
			// skip messages without reactions
			if len(m.Reactions) == 0 {
				continue
			}

			messages = append(messages, newMsg(&m))
			for _, r := range m.Reactions {
				classMap[r.Name] = struct{}{}
			}
		}
	}

	var classes []bayesian.Class
	for class := range classMap {
		classes = append(classes, bayesian.Class(class))
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
