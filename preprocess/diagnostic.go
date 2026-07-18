package preprocess

import (
	"github.com/pawnkit/pawnkit-core/diagnostic"
	"github.com/pawnkit/pawnkit-core/source"
)

// Code is a stable, machine-readable preprocessor diagnostic identifier.
type Code string

const (
	CodeUnterminatedConditional Code = "preprocess/unterminated-conditional"
	CodeUnmatchedElseif         Code = "preprocess/unmatched-elseif"
	CodeUnmatchedElse           Code = "preprocess/unmatched-else"
	CodeUnmatchedEndif          Code = "preprocess/unmatched-endif"
	CodeConditionalDepthLimit   Code = "preprocess/conditional-depth-limit"
	CodeUnresolvableCondition   Code = "preprocess/unresolvable-condition"
	CodeUnknownDirective        Code = "preprocess/unknown-directive"
	CodeMalformedDefine         Code = "preprocess/malformed-define"
	CodeMacroArgumentMismatch   Code = "preprocess/macro-argument-mismatch"
	CodeUnterminatedInvocation  Code = "preprocess/unterminated-macro-invocation"
	CodeExpansionDepthLimit     Code = "preprocess/expansion-depth-limit"
	CodeOutputSizeLimit         Code = "preprocess/output-size-limit"
	CodeMalformedInclude        Code = "preprocess/malformed-include"
	CodeIncludeNotFound         Code = "preprocess/include-not-found"
	CodeIncludeCycle            Code = "preprocess/include-cycle"
	CodeIncludeDepthLimit       Code = "preprocess/include-depth-limit"
	CodeUserError               Code = "preprocess/user-error"
	CodeUserWarning             Code = "preprocess/user-warning"
	CodeAssertFailed            Code = "preprocess/assert-failed"
	CodeAssertUnknown           Code = "preprocess/assert-unknown"
)

// ByteRange is a half-open source byte range within one file (see
// Diagnostic.File).
type ByteRange struct {
	Start int
	End   int
}

// Diagnostic is a preprocessor-stage finding. File indexes into
// Result.Files; convert to pawnkit-core's shared format with [ToCore].
type Diagnostic struct {
	File     uint32
	Code     Code
	Severity diagnostic.Severity
	Message  string
	Range    ByteRange
}

// ToCore converts d into the shared diagnostic.Diagnostic interchange
// format. file is the caller's pawnkit-core FileID for d.File; callers
// processing multiple files (via #include splicing) look up Result.Files[d.File]
// in their own source.Registry to obtain it.
func (d Diagnostic) ToCore(file source.FileID) diagnostic.Diagnostic {
	return diagnostic.New(
		"pawn-analysis:"+string(d.Code),
		"pawn-analysis",
		d.Severity,
		d.Message,
		source.Span{File: file, Start: source.Offset(d.Range.Start), End: source.Offset(d.Range.End)},
	)
}
