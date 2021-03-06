package main

import (
	"bytes"
	"flag"
	"fmt"
	"math/rand"
	"net/http"
	"os"
	"regexp"
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
)

var (
	debug           = flag.Bool("debug", false, "increase the logging level to debug")
	maxMessageCount = flag.Int("max_msg", 1000, "maximum number of messages to retrieve from each channel")

	learning bool

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
	bot.Hear("^update$").MessageHandler(UpdateHandler)
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

	if learning {
		bot.Reply(evt, "Hold on, I'm still learning about your team!", slackbot.WithTyping)
		return
	}

	if reactor == nil {
		bot.Reply(evt, "I don't know anything about you yet!", slackbot.WithTyping)
		return
	}

	reaction := reactor.Reaction(evt.Text)

	log.Infoln("reacting to:", evt.Text, "with:", reaction)

	randPrefix := prefixes[rand.Intn(len(prefixes))]
	bot.Reply(evt, fmt.Sprintf("%s :%s:", randPrefix, reaction), slackbot.WithTyping)
}

// HelpHandler responds with help information for this channel
func HelpHandler(ctx context.Context, bot *slackbot.Bot, evt *slack.MessageEvent) {
	mut.RLock()
	defer mut.RUnlock()

	log.Infoln("Helping the user")

	buf := bytes.NewBuffer(nil)
	buf.WriteString(fmt.Sprintf("I guess reactions to messages based on up to the past %d seen in each channel.", *maxMessageCount))

	if reactor != nil {
		buf.WriteString(" I'm currently aware of the following reactions:\n")
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
	learning = true
	defer func() {
		learning = false
	}()

	log.Infoln("setting up the reactor...")

	channels, err := client.GetChannels(true)
	if err != nil {
		log.Errorln("error getting channels", err)
		return nil, nil, nil
	}

	var messages []*msg
	classMap := make(map[string]struct{})

ChannelLoop:
	for _, channel := range channels {

		var (
			latest string
			count  int
			page   int
		)

		for {
			history, err := client.GetChannelHistory(channel.ID, slack.HistoryParameters{
				Count:  100,
				Latest: latest,
			})
			if err != nil {
				log.Errorf("error getting channel history for '%s' :%v", channel.Name, err)
				continue ChannelLoop
			}
			latest = history.Latest
			count += len(history.Messages)
			page++

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

			if count > *maxMessageCount {
				break
			}
			if !history.HasMore {
				break
			}
		}

		log.Infof("Finished getting message history from the %s channel.", channel.Name)
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
	mut        sync.RWMutex
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
	r.mut.RLock()
	defer r.mut.RUnlock()

	works := makeDocument(text)
	scores, inx, _ := r.Classifier.LogScores(works)

	log.Debugln("scores: %v", scores)

	return string(r.Classifier.Classes[inx])
}

// Learn handles learning from a new message
func (r *Reactor) Learn(msg *msg) {
	r.msgs <- msg
}

func (r *Reactor) train(msg *msg) {
	r.mut.Lock()
	defer r.mut.Unlock()

	words := makeDocument(msg.Text)
	log.Debugf("training on message:\n    raw: '%s'\n    words: %s\n    reactions: %+v", msg.Text, words, msg.Reactions)

	for _, reaction := range msg.Reactions {
		for i := 0; i < reaction.Count; i++ {
			r.Classifier.Learn(words, bayesian.Class(reaction.Name))
		}
	}

}

// helpers for classification

var linkRegexp = regexp.MustCompile(`<.*\|(.*)>`)

func makeDocument(txt string) []string {
	words := strings.Fields(txt)

	for i := range words {
		// parse slack links
		matches := linkRegexp.FindStringSubmatch(words[i])
		if len(matches) > 1 {
			words[i] = matches[1]
		}

		// trim leading/trailing punctuation
		words[i] = strings.TrimLeft(words[i], `'"(`)
		words[i] = strings.TrimRight(words[i], `,;:.!?'")`)

		words[i] = strings.ToLower(words[i])
	}

	return words
}

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

func (r *reaction) String() string {
	return fmt.Sprintf("%s(%d)", r.Name, r.Count)
}
