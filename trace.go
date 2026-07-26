package analysis

import (
	"context"
	"sync"
	"time"
)

// Stage identifies one analysis step.
type Stage string

const (
	StagePreprocess      Stage = "preprocess"
	StageParseOriginal   Stage = "parser.original"
	StageSymbolsOriginal Stage = "symbols.original"
	StageParseExpanded   Stage = "parser.expanded"
	StageSymbolsExpanded Stage = "symbols.expanded"
	StageSemanticNames   Stage = "semantic.names"
	StageSemanticTags    Stage = "semantic.tags"
	StageSemanticStates  Stage = "semantic.states"
	StageSemanticOrder   Stage = "semantic.constants"
	StageSemanticCFG     Stage = "semantic.cfg"
)

// TraceEvent records one completed stage.
type TraceEvent struct {
	Stage     Stage
	Duration  time.Duration
	Reused    int
	Cancelled bool
}

type stageTrace struct {
	emit    func(TraceEvent)
	stage   Stage
	started time.Time
}

func beginStage(emit func(TraceEvent), stage Stage) stageTrace {
	if emit == nil {
		return stageTrace{}
	}
	return stageTrace{emit: emit, stage: stage, started: time.Now()}
}

func serializeTrace(emit func(TraceEvent)) func(TraceEvent) {
	if emit == nil {
		return nil
	}
	var mu sync.Mutex
	return func(event TraceEvent) {
		mu.Lock()
		defer mu.Unlock()
		emit(event)
	}
}

func (trace stageTrace) end(ctx context.Context, reused int) {
	if trace.emit == nil {
		return
	}
	trace.emit(TraceEvent{
		Stage: trace.stage, Duration: time.Since(trace.started), Reused: reused,
		Cancelled: ctx.Err() != nil,
	})
}
