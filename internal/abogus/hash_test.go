package abogus

import (
	"fmt"
	"strings"
	"testing"
)

func codesToStr(a []int) string {
	s := make([]string, len(a))
	for i, v := range a {
		s[i] = fmt.Sprint(v)
	}
	return "[" + strings.Join(s, ",") + "]"
}

func TestGetArr(t *testing.T) {
	cases := map[string]string{
		"getArr(hello)":         codesToStr(getArrStr("hello")),
		"getArr(getArr(hello))": codesToStr(getArr(getArrStr("hello"))),
		"getArr(empty)":         codesToStr(getArrStr("")),
	}
	want := map[string]string{
		"getArr(hello)":         "[190,203,191,170,230,84,139,139,240,207,202,213,162,113,131,205,27,230,9,59,28,206,204,195,3,217,198,29,10,100,82,104]",
		"getArr(getArr(hello))": "[150,155,23,143,169,154,220,31,124,158,158,217,198,87,21,236,65,227,121,72,64,125,2,61,57,240,188,40,169,189,89,170]",
		"getArr(empty)":         "[26,178,29,131,85,207,161,127,142,97,25,72,49,232,26,143,34,190,200,199,40,254,251,116,126,208,53,235,80,130,170,43]",
	}
	for k, got := range cases {
		if got != want[k] {
			t.Errorf("%s\n got %s\nwant %s", k, got, want[k])
		}
	}
}
