package copilot

import (
	"strings"
	"testing"
)

func TestThinkFilter(t *testing.T) {
	cases := []struct {
		chunks []string
		want   string
	}{
		{[]string{"plain answer"}, "plain answer"},
		{[]string{"<think>hidden</think>shown"}, "shown"},
		{[]string{"<th", "ink>hid", "den</thi", "nk>sho", "wn"}, "shown"},
		{[]string{"a < b and ", "x <thing> y"}, "a < b and x <thing> y"},
		{[]string{"before<think>mid</think>after"}, "beforeafter"},
		{[]string{"<think>never closed"}, ""},
	}
	for _, c := range cases {
		f := &thinkFilter{}
		var got strings.Builder
		for _, ch := range c.chunks {
			got.WriteString(f.Feed(ch))
		}
		got.WriteString(f.Flush())
		if got.String() != c.want {
			t.Errorf("%q: got %q want %q", c.chunks, got.String(), c.want)
		}
	}
}

func TestRelayStripsThinkingAndLeadingSpace(t *testing.T) {
	sse := "data: {\"choices\":[{\"delta\":{\"content\":\"<think>plan\"}}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{\"content\":\"</think>\\n\\nHello\"}}]}\n\n" +
		": keep-alive\n\n" +
		"data: {\"choices\":[{\"delta\":{\"content\":\" world\"}}]}\n\n" +
		"data: [DONE]\n\n"
	var out strings.Builder
	if err := relay(strings.NewReader(sse), func(s string) { out.WriteString(s) }); err != nil {
		t.Fatal(err)
	}
	if out.String() != "Hello world" {
		t.Errorf("got %q", out.String())
	}
}

func TestExplainCode(t *testing.T) {
	m, _ := explainCode("Xid 79", "")
	if !strings.Contains(m, "PCIe bus") {
		t.Errorf("Xid 79 not explained: %q", m)
	}
	m, _ = explainCode("Health: Alarm", "HBM multi-bit ECC error")
	if !strings.Contains(m, "could not correct") {
		t.Errorf("ECC not explained: %q", m)
	}
}
