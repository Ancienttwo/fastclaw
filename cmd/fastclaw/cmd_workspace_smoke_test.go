package main

import (
	"context"
	"testing"

	"github.com/fastclaw-ai/fastclaw/internal/workspace"
)

func TestRunWorkspaceSmokeWritesReadsAndCleansUp(t *testing.T) {
	st := workspace.NewLocalFS(t.TempDir())
	if err := runWorkspaceSmoke(context.Background(), st); err != nil {
		t.Fatalf("runWorkspaceSmoke: %v", err)
	}
	objects, err := st.List(context.Background(), "__fastclaw_startup_smoke__", "", "")
	if err != nil {
		t.Fatalf("list smoke prefix: %v", err)
	}
	if len(objects) != 0 {
		t.Fatalf("smoke left %d objects behind", len(objects))
	}
}

func TestWorkspaceSmokeCommandIsHidden(t *testing.T) {
	cmd := workspaceSmokeCmd()
	if !cmd.Hidden {
		t.Fatal("workspace-smoke must remain an internal command")
	}
}
