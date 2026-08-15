package golem_test

import (
	"testing"

	"github.com/abubakarsiddik31/golem"
)

func TestStagesCarryStableIdentifiers(t *testing.T) {
	t.Parallel()

	tests := map[golem.Stage]string{
		golem.StageModel:  "model",
		golem.StageDecode: "decode",
		golem.StageTool:   "tool",
		golem.StageLoop:   "loop",
	}
	for stage, want := range tests {
		if string(stage) != want {
			t.Fatalf("stage %q = %q, want %q", stage, string(stage), want)
		}
	}
}
