package webfetch

import "testing"

func TestHTMLToText(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{
			name: "document with head noise",
			body: "<!DOCTYPE html><html><head><title>Docs</title>" +
				"<script>var x = 1;</script><style>p{color:red}</style></head>" +
				"<body><h1>Hello</h1><p>Some &amp; text</p><!-- a comment --></body></html>",
			want: "Docs\nHello\nSome & text",
		},
		{
			name: "inline tags keep words together",
			body: "so<b>me</b>thing",
			want: "something",
		},
		{
			name: "breaks and paragraphs separate lines",
			body: "<p>a<br>b</p><p>c</p>",
			want: "a\nb\nc",
		},
		{
			name: "quoted attribute containing gt",
			body: `<a href="a>b">x</a>`,
			want: "x",
		},
		{
			name: "entities unescape to text",
			body: "&lt;p&gt; stays text",
			want: "<p> stays text",
		},
		{
			name: "unclosed script drops the rest",
			body: "keep<script>never",
			want: "keep",
		},
		{
			name: "unclosed comment drops the rest",
			body: "keep<!-- never",
			want: "keep",
		},
		{
			name: "self-closing block tag",
			body: "a<br/>b",
			want: "a\nb",
		},
		{
			name: "closing tag with trailing space",
			body: "<div>x</div >y",
			want: "x\ny",
		},
		{
			name: "source indentation collapses",
			body: "<div>\n\t<p>hi</p>\n</div>",
			want: "hi",
		},
		{
			name: "list items line up",
			body: "<ul><li>one</li><li>two</li></ul>",
			want: "one\ntwo",
		},
		{
			name: "text without tags",
			body: "plain text",
			want: "plain text",
		},
		{
			name: "empty body",
			body: "",
			want: "",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := htmlToText(testCase.body); got != testCase.want {
				t.Errorf("htmlToText(%q)\n got: %q\nwant: %q", testCase.body, got, testCase.want)
			}
		})
	}
}
