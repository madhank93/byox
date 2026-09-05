package learn

import (
	"reflect"
	"testing"
)

func TestParseConcepts(t *testing.T) {
	cases := []struct {
		name string
		doc  string
		want []string
	}{
		{"tagged", "---\ntitle: x\nconcepts: [tcp, framing]\n---\n\nbody", []string{"tcp", "framing"}},
		{"single", "---\nconcepts: [udp]\n---\n\nbody", []string{"udp"}},
		{"untagged", "---\ntitle: x\n---\n\nbody", nil},
		{"no frontmatter", "just prose\n", nil},
		{"unterminated", "---\nconcepts: [a]\n", nil},
	}
	for _, c := range cases {
		if got := parseConcepts(c.doc); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: parseConcepts = %v, want %v", c.name, got, c.want)
		}
	}
}
