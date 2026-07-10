package main

import (
	"bytes"
	"context"
	cryptorand "crypto/rand"
	"fmt"
	"io"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/fastclaw-ai/fastclaw/internal/config"
	"github.com/fastclaw-ai/fastclaw/internal/workspace"
)

func workspaceSmokeCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "workspace-smoke",
		Short:  "Verify configured workspace object storage",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			var cfg config.Config
			config.LoadEnv().ApplyToConfig(&cfg)
			homeDir, err := config.HomeDir()
			if err != nil {
				return err
			}
			st, err := workspace.Factory{
				Type:         cfg.ObjectStore.Type,
				LocalDir:     cfg.ObjectStore.Local.Root,
				AccountID:    cfg.ObjectStore.AccountID,
				AliyunIntern: cfg.ObjectStore.AliyunIntern,
				S3: workspace.S3Config{
					Endpoint:  cfg.ObjectStore.S3.Endpoint,
					Region:    cfg.ObjectStore.S3.Region,
					Bucket:    cfg.ObjectStore.S3.Bucket,
					Prefix:    cfg.ObjectStore.S3.Prefix,
					AccessKey: cfg.ObjectStore.S3.AccessKey,
					SecretKey: cfg.ObjectStore.S3.SecretKey,
					UseSSL:    cfg.ObjectStore.S3.UseSSL,
				},
			}.New(filepath.Join(homeDir, "workspaces"))
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
			defer cancel()
			if err := runWorkspaceSmoke(ctx, st); err != nil {
				return err
			}
			fmt.Println("workspace smoke ok")
			return nil
		},
	}
}

func runWorkspaceSmoke(ctx context.Context, st workspace.Store) error {
	var nonce [16]byte
	if _, err := cryptorand.Read(nonce[:]); err != nil {
		return fmt.Errorf("workspace smoke nonce: %w", err)
	}
	const agentID = "__fastclaw_startup_smoke__"
	path := fmt.Sprintf("readiness/%x.bin", nonce[:])
	payload := []byte("fastclaw-workspace-readiness")
	if err := st.Put(ctx, agentID, "", "", path, bytes.NewReader(payload), int64(len(payload)), "application/octet-stream"); err != nil {
		return fmt.Errorf("workspace smoke put: %w", err)
	}
	cleanupRequired := true
	defer func() {
		if !cleanupRequired {
			return
		}
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = st.Delete(cleanupCtx, agentID, "", "", path)
	}()
	rc, err := st.Get(ctx, agentID, "", "", path)
	if err != nil {
		return fmt.Errorf("workspace smoke get: %w", err)
	}
	got, readErr := io.ReadAll(rc)
	closeErr := rc.Close()
	if readErr != nil {
		return fmt.Errorf("workspace smoke read: %w", readErr)
	}
	if closeErr != nil {
		return fmt.Errorf("workspace smoke close: %w", closeErr)
	}
	if !bytes.Equal(got, payload) {
		return fmt.Errorf("workspace smoke payload mismatch")
	}
	if err := st.Delete(ctx, agentID, "", "", path); err != nil {
		return fmt.Errorf("workspace smoke delete: %w", err)
	}
	cleanupRequired = false
	return nil
}
