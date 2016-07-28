package main

import (
	"reflect"
	"testing"

	"github.com/jbrukh/bayesian"
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

func TestMakeDocument(t *testing.T) {
	type tc struct {
		in  string
		out []string
	}

	tcs := []tc{
		{
			in:  "",
			out: []string{},
		},
		{
			in:  "Testing tHiS text.",
			out: []string{"testing", "this", "text"},
		},
		{
			in:  "A \"link\" resides here: <https://wwww.meta.sc|Meta>",
			out: []string{"a", "link", "resides", "here", "meta"},
		},
		// TODO: add support for multi-word links
		// {
		// 	in:  "Multiword link: <https://wwww.meta.sc|Meta Search>",
		// 	out: []string{"multiword", "link", "meta search"},
		// },
	}

	for _, c := range tcs {
		result := makeDocument(c.in)

		if !reflect.DeepEqual(result, c.out) {
			t.Errorf("result was not equal to the expected output:\n\tresult (len %d): %+v\n\texpected (len %d): %+v",
				len(result), result, len(c.out), c.out)
		}
	}
}
