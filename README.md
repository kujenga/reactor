# Reactor [![CircleCI](https://circleci.com/gh/kujenga/reactor.svg?style=svg)](https://circleci.com/gh/kujenga/reactor)

<img src="img/nuclear.jpg" width="100" height="100" />

A Slack bot that guesses crowd reactions to your messages.

[![Deploy](https://www.herokucdn.com/deploy/button.svg)](https://heroku.com/deploy?template=https://github.com/kujenga/reactor/tree/master)

## Usage

In Slack, **@reactor** responds to direct messages with a guess at what the most popular reaction to it will be. It guesses based on recent interactions in public channels.

A few special commands:

- Typing `help` responds with some simple information about the bot.
- Typing `update` rebuilds the classifier based on the latest messages.

## Getting Started

To run this bot locally, retrieve the repository with:

```bash
go get github.com/kujenga/reactor
```

Optionally, if you have [`glide`](https://glide.sh) on your system you can install the pinned dependencies with `glide install`.

For information on how to get setup a Slack bot, check out the instructions for the Boston Golang [Slack lab](https://github.com/bostongolang/golang-lab-slack).

Once you have an API key, setup your shell with the following command, substituting in your token:

```bash
export SLACK_TOKEN="<my_slack_token>"
```

Then, you can run the bot with:

```bash
go run main.go
```

---

Special thanks to [**@doykle**](https://github.com/doykle) for the idea, and the [jbrukh/bayesian](github.com/jbrukh/bayesian) library for making it easy to setup a bayesian classifier.
