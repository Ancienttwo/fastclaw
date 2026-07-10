package main

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/fastclaw-ai/fastclaw/internal/store"
	"github.com/fastclaw-ai/fastclaw/internal/users"
)

func apikeyCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "apikey",
		Short: "Manage API keys (create, list, delete, rotate)",
	}
	cmd.AddCommand(apikeyCreateCmd())
	cmd.AddCommand(apikeyEnsureCmd())
	cmd.AddCommand(apikeyListCmd())
	cmd.AddCommand(apikeyDeleteCmd())
	cmd.AddCommand(apikeyRotateCmd())
	return cmd
}

func apikeyEnsureCmd() *cobra.Command {
	var name, owner, tokenEnv string
	cmd := &cobra.Command{
		Use:   "ensure",
		Short: "Create or update a named admin API key from an environment secret",
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := openStoreFromEnv()
			if err != nil {
				return err
			}
			defer st.Close()
			owner, err = resolveAPIKeyOwner(context.Background(), st, owner)
			if err != nil {
				return err
			}
			token := os.Getenv(tokenEnv)
			if token == "" {
				return fmt.Errorf("environment variable %s is empty", tokenEnv)
			}
			keys, err := users.NewAPIKeys(st)
			if err != nil {
				return err
			}
			rec, created, err := keys.EnsureAdminToken(context.Background(), owner, name, token)
			if err != nil {
				return err
			}
			verb := "updated"
			if created {
				verb = "created"
			}
			fmt.Printf("%s admin apikey id=%s name=%s owner=%s\n", verb, rec.ID, name, owner)
			return nil
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "key name (required)")
	cmd.Flags().StringVar(&owner, "owner", "", "owner user ID (defaults to first super_admin)")
	cmd.Flags().StringVar(&tokenEnv, "token-env", "", "environment variable containing an fc_ token (required)")
	cmd.MarkFlagRequired("name")
	cmd.MarkFlagRequired("token-env")
	return cmd
}

func resolveAPIKeyOwner(ctx context.Context, st store.Store, owner string) (string, error) {
	if owner != "" {
		return owner, nil
	}
	accounts, err := users.NewAccounts(st)
	if err != nil {
		return "", err
	}
	list, err := accounts.List(ctx)
	if err != nil {
		return "", err
	}
	for _, user := range list {
		if user.Role == users.RoleSuperAdmin {
			return user.ID, nil
		}
	}
	return "", fmt.Errorf("no super_admin found; use --owner to specify user ID")
}

func apikeyCreateCmd() *cobra.Command {
	var name, keyType, owner string
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a new API key",
		Long: `Create a new API key for the specified owner (or the first super_admin).

Key types:
  admin  — full platform access (only super_admin should own these)
  user   — scoped to owner's resources; supports X-Fastclaw-End-User for app_user provisioning
  agent  — locked to explicit agent list (requires --agents)`,
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := openStoreFromEnv()
			if err != nil {
				return err
			}
			defer st.Close()

			if owner == "" {
				accts, err := users.NewAccounts(st)
				if err != nil {
					return err
				}
				list, err := accts.List(context.Background())
				if err != nil {
					return err
				}
				for _, u := range list {
					if u.Role == users.RoleSuperAdmin {
						owner = u.ID
						break
					}
				}
				if owner == "" {
					return fmt.Errorf("no super_admin found; use --owner to specify user ID")
				}
			}

			ak, err := users.NewAPIKeys(st)
			if err != nil {
				return err
			}
			rec, token, err := ak.Create(context.Background(), owner, name, keyType, nil)
			if err != nil {
				return err
			}
			fmt.Printf("created apikey id=%s name=%s type=%s owner=%s\n", rec.ID, name, keyType, owner)
			fmt.Printf("token: %s\n", token)
			fmt.Println("(save this token now — it won't be shown again)")
			return nil
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "key name (required)")
	cmd.Flags().StringVar(&keyType, "type", "user", "'admin', 'user', or 'agent'")
	cmd.Flags().StringVar(&owner, "owner", "", "owner user ID (defaults to first super_admin)")
	cmd.MarkFlagRequired("name")
	return cmd
}

func apikeyListCmd() *cobra.Command {
	var owner string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List API keys for a user",
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := openStoreFromEnv()
			if err != nil {
				return err
			}
			defer st.Close()

			if owner == "" {
				accts, err := users.NewAccounts(st)
				if err != nil {
					return err
				}
				list, err := accts.List(context.Background())
				if err != nil {
					return err
				}
				for _, u := range list {
					if u.Role == users.RoleSuperAdmin {
						owner = u.ID
						break
					}
				}
				if owner == "" {
					return fmt.Errorf("no super_admin found; use --owner to specify user ID")
				}
			}

			ak, err := users.NewAPIKeys(st)
			if err != nil {
				return err
			}
			keys, err := ak.List(context.Background(), owner)
			if err != nil {
				return err
			}
			if len(keys) == 0 {
				fmt.Println("no API keys found")
				return nil
			}
			fmt.Printf("%-36s %-20s %-10s %-10s %s\n", "ID", "NAME", "PREFIX", "TYPE", "CREATED")
			for _, k := range keys {
				fmt.Printf("%-36s %-20s %-10s %-10s %s\n",
					k.ID, k.Name, k.Key, k.Type, k.CreatedAt.Format("2006-01-02"))
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&owner, "owner", "", "owner user ID (defaults to first super_admin)")
	return cmd
}

func apikeyDeleteCmd() *cobra.Command {
	var id string
	cmd := &cobra.Command{
		Use:   "delete",
		Short: "Delete an API key by ID",
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := openStoreFromEnv()
			if err != nil {
				return err
			}
			defer st.Close()
			ak, err := users.NewAPIKeys(st)
			if err != nil {
				return err
			}
			if err := ak.Delete(context.Background(), id); err != nil {
				return err
			}
			fmt.Printf("deleted apikey %s\n", id)
			return nil
		},
	}
	cmd.Flags().StringVar(&id, "id", "", "apikey ID (required)")
	cmd.MarkFlagRequired("id")
	return cmd
}

func apikeyRotateCmd() *cobra.Command {
	var id string
	cmd := &cobra.Command{
		Use:   "rotate",
		Short: "Rotate an API key (issue new token, invalidate old)",
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := openStoreFromEnv()
			if err != nil {
				return err
			}
			defer st.Close()
			ak, err := users.NewAPIKeys(st)
			if err != nil {
				return err
			}
			token, err := ak.Rotate(context.Background(), id)
			if err != nil {
				return err
			}
			fmt.Printf("rotated apikey %s\n", id)
			fmt.Printf("new token: %s\n", token)
			fmt.Println("(save this token now — it won't be shown again)")
			return nil
		},
	}
	cmd.Flags().StringVar(&id, "id", "", "apikey ID (required)")
	cmd.MarkFlagRequired("id")
	return cmd
}
