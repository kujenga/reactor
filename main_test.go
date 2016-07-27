package main

import (
	"github.com/jbrukh/bayesian"
	"testing"
)

func TestReactor(t *testing.T) {
	classes := []bayesian.Class{
		"a",
		"b",
	}

	r := NewReactor(classes)

	msgs := []*msg{
		&msg{Text: "words all together", Reactions: []*reaction{{Name: "a", Count: 2}}},
		&msg{Text: "this is a second string", Reactions: []*reaction{{Name: "b", Count: 1}}},
	}

	for _, m := range msgs {
		r.Learn(m)
	}

	reaction := r.Reaction("words all together")

	if reaction != "a" {
		t.Error("reaction should equal the expected valud for this simple case")
	}
}
