package sema

import (
	"testing"

	"github.com/pawnkit/pawn-analysis/symbol"
)

func TestOrderedParametersPreservesSparseIndexes(t *testing.T) {
	t.Parallel()

	ids, references := orderedParameters(map[symbol.ID]parameterFact{
		7: {index: 2, mutable: true},
	})
	if len(ids) != 3 || ids[2] != 7 || len(references) != 3 || !references[2] {
		t.Fatalf("parameters = %v, references = %v", ids, references)
	}
}
