package preprocess_test

import (
	"strings"
	"testing"

	"github.com/pawnkit/pawn-analysis/preprocess"
)

func TestTextualMacroPatterns(t *testing.T) {
	tests := []struct {
		name   string
		source string
		want   string
	}{
		{"colon suffix", "#define FONT: Other:\nnative Example(FONT:font);", "native Example ( Other : font ) ;"},
		{"unmatched suffix", "#define FONT: Other:\nnew FONT;", "new FONT ;"},
		{"no match across lines", "#define FONT: Other:\nFONT\n: value", "FONT : value"},
		{"spaced invocation", "#define FONT: Other:\nnative Example(FONT :font);", "native Example ( Other : font ) ;"},
		{"adjacent prefix", "#define TAG(%0) t_%0\nnative Example(TAG(FONT):font);", "native Example ( t_FONT : font ) ;"},
		{"adjacent suffix", "#define NAME(%0) %0_suffix\nnew NAME(value);", "new value_suffix ;"},
		{"spaced replacement", "#define NAME(%0) prefix %0\nNAME(value)", "prefix value"},
		{"bracket pattern", "#define Object[%0] GetObject(%0)\nObject[one(1, 2)]", "GetObject ( one ( 1 , 2 ) )"},
		{"fixed delimiters", "#define COMMAND:%0[%1](%2) public cmd_%0_%1(%2)\nCOMMAND:open[admin](playerid)", "public cmd_open_admin ( playerid )"},
		{"nested rescan", "#define TAG(%0) t_%0\n#define FONT: TAG(FONT):\nnative Example(FONT:font);", "native Example ( t_FONT : font ) ;"},
		{"result rescanned", "#define NAME(%0) prefix_%0\n#define prefix_value 42\nnew value = NAME(value);", "new value = 42 ;"},
		{"nested capture", "#define Object[%0] GetObject(%0)\nObject[lookup[one(1, 2)]]", "GetObject ( lookup [ one ( 1 , 2 ) ] )"},
		{"quoted delimiter", "#define Object[%0] GetObject(%0)\nObject[\"[not a delimiter]\"]", "GetObject ( \"[not a delimiter]\" )"},
		{"empty capture", "#define NAME(%0) prefix_%0\nNAME()", "prefix_"},
		{"multi-token capture", "#define NAME(%0) prefix_%0\nNAME(one + two)", "prefix_one + two"},
		{"numeric paste", "#define NUMBER(%0) %0.5\nNUMBER(1)", "1.5"},
		{"packed string paste", "#define PACK(%0) !%0\nPACK(\"text\")", "!\"text\""},
		{"operator paste", "#define SIGN(%0) +%0\nSIGN(+value)", "++ value"},
		{"string preserved", "#define NAME(%0) prefix_%0 + \"%0\"\nNAME(value)", "prefix_value + \"%0\""},
		{"suffix after parentheses", "#define TAG(%0): t_%0:\nnative Example(TAG(FONT):font);", "native Example ( t_FONT : font ) ;"},
		{"missing bracket", "#define Object[%0] GetObject(%0)\nObject[value", "Object [ value"},
		{"repeated capture", "#define NAME:%0:%0; prefix_%0\nNAME:first:last;", "prefix_last"},
		{"recursive pattern", "#define NAME: NAME:\nNAME:value", "NAME : value"},
		{"nested unmatched suffix", "#define FONT: Other:\n#define CALL(%0) use(FONT, %0)\nCALL(1)", "use ( FONT , 1 )"},
		{"operator prefix consumes source", "#define HASH (_:KEY:VALUE\n#define KEY:VALUE<%0> 1)\nHASH<Next page> + 2", "( _ : 1 ) + 2"},
		{"alias consumes source arguments", "#define ALIAS Target\n#define Target(%0) ((%0) + 1)\nALIAS(2)", "( ( 2 ) + 1 )"},
		{"prefix consumes enclosing replacement", "#define ALIAS Target\n#define CALL ALIAS(2)\n#define Target(%0) ((%0) + 1)\nCALL", "( ( 2 ) + 1 )"},
		{"prefix consumes replacement and source", "#define ALIAS Target(\n#define Target(%0) ((%0) + 1)\nALIAS 2)", "( ( 2 ) + 1 )"},
		{"unmatched suffix leaves source in place", "#define ALIAS Name:\n#define Name:%0; prefix_%0\nALIAS value\nnext;", "Name : value next ;"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := preprocess.Run([]byte(test.source), preprocess.Options{})
			if len(result.Diagnostics) != 0 {
				t.Fatalf("diagnostics: %+v", result.Diagnostics)
			}
			if got := strings.TrimSpace(expandedText(t, result)); got != test.want {
				t.Fatalf("expanded = %q, want %q", got, test.want)
			}
		})
	}
}

func TestTextualSubstitutionPreservesOrigins(t *testing.T) {
	for _, test := range []struct {
		body, want, parent string
	}{
		{"prefix_%0", "prefix_value", "prefix_"},
		{"%0_suffix", "value_suffix", "value"},
	} {
		t.Run(test.body, func(t *testing.T) {
			definition := "#define NAME(%0) " + test.body + "\n"
			result := preprocess.Run([]byte("#include <api>\nnew x = NAME(value);"), preprocess.Options{
				Resolver: preprocess.MapResolver{"api": []byte(definition)},
			})
			for _, item := range result.ExpandedTokens {
				if item.Text(result.ExpandedSource) != test.want {
					continue
				}
				if item.Origin == nil || item.Origin.Parent == nil {
					t.Fatal("missing invocation or spelling origin")
				}
				span := item.Origin.Span
				if got := string(result.Files[span.File].Content[span.Start.Offset:span.End.Offset]); got != "NAME(value)" {
					t.Fatalf("invocation = %q", got)
				}
				span = item.Origin.Parent.Span
				if got := string(result.Files[span.File].Content[span.Start.Offset:span.End.Offset]); got != test.parent {
					t.Fatalf("spelling = %q, want %q", got, test.parent)
				}
				return
			}
			t.Fatalf("missing expanded token %q", test.want)
		})
	}
}

func TestPatternInvocationRange(t *testing.T) {
	source := "#define Object[%0] GetObject(%0)\nObject[one(1, 2)]"
	result := preprocess.Run([]byte(source), preprocess.Options{})
	if len(result.MacroInvocations) != 1 {
		t.Fatalf("invocations = %+v", result.MacroInvocations)
	}
	span := result.MacroInvocations[0].Range
	if got := source[span.Start:span.End]; got != "Object[one(1, 2)]" {
		t.Fatalf("invocation = %q", got)
	}
}

func TestNestedPatternInvocationCoversConsumedSource(t *testing.T) {
	const invocation = "HASH<Next page>"
	const source = "#define HASH (_:KEY:VALUE\n#define KEY:VALUE<%0> 1)\n" + invocation
	result := preprocess.Run([]byte(source), preprocess.Options{})
	for _, item := range result.MacroInvocations {
		if item.File == 0 && source[item.Range.Start:item.Range.End] == invocation {
			return
		}
	}
	t.Fatalf("no invocation covers %q: %+v", invocation, result.MacroInvocations)
}

func TestParenthesisPrefixDoesNotConsumeFollowingDirectives(t *testing.T) {
	const source = `#if 0
#define Read( OldRead(1,
#else
#define Read( ReadImpl(2,
#endif
Read(3)
`
	result := preprocess.Run([]byte(source), preprocess.Options{})
	if len(result.Diagnostics) != 0 {
		t.Fatalf("diagnostics: %+v", result.Diagnostics)
	}
	if got := strings.TrimSpace(expandedText(t, result)); got != "ReadImpl ( 2 , 3 )" {
		t.Fatalf("expanded = %q", got)
	}
}

func TestConditionalMacroExpansionPreservesDefinedOperand(t *testing.T) {
	const source = `#define MARKER unknown
#define ENABLED(%0) (defined MARKER && !defined DISABLE_%0)
#define DISABLE_other 1
#define SUM 1 + 2
#if ENABLED(test) && !ENABLED(other) && 2 * SUM == 4
new enabled;
#else
new disabled;
#endif
`
	result := preprocess.Run([]byte(source), preprocess.Options{})
	if len(result.Diagnostics) != 0 {
		t.Fatalf("diagnostics: %+v", result.Diagnostics)
	}
	if got := strings.TrimSpace(expandedText(t, result)); got != "new enabled ;" {
		t.Fatalf("expanded = %q", got)
	}
}
