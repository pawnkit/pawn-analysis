package analysis_test

import (
	"testing"

	analysis "github.com/pawnkit/pawn-analysis"
)

func TestPawnPlusTagAliasesAreCompatible(t *testing.T) {
	text := `#define ConstString String@Const
#define StringTags String
#define StringTag {StringTags}
#define ConstStringTags ConstString,StringTags
#define ConstStringTag {ConstStringTags}
native str_get(ConstStringTag:str, buffer[], size=sizeof buffer, start=0, end=cellmax);
new ConstString:commandName;
main() {
    new command[65];
    str_get(commandName, command);
}
`
	result := analysis.Analyze([]byte(text), analysis.Options{RetainExpanded: true})
	for _, diagnostic := range result.Diagnostics {
		if diagnostic.Code == "pawn-analysis:sema/tag-mismatch" {
			t.Fatalf("unexpected tag mismatch: %s", diagnostic.Message)
		}
	}
}
