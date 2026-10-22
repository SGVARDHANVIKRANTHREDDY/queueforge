package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/SGVARDHANVIKRANTHREDDY/queueforge/internal/config"
	"github.com/SGVARDHANVIKRANTHREDDY/queueforge/internal/postgres"
	"github.com/google/uuid"
)

// operator identifies who is running the CLI, for the audit log.
func operator() string {
	for _, k := range []string{"QF_OPERATOR", "SUDO_USER", "USER", "USERNAME"} {
		if v := os.Getenv(k); v != "" {
			return v
		}
	}
	return "unknown"
}

// audit records an operator action. A failure to audit is reported loudly but does not undo the
// action (the action has already happened and must not be silently half-recorded).
func audit(ctx context.Context, db *postgres.DB, tenantID *uuid.UUID, action, target string) {
	err := postgres.NewAuditRepo(db).Record(ctx, postgres.AuditEntry{
		TenantID: tenantID, ActorType: "operator", ActorID: operator(), Action: action, TargetID: target,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "WARNING: action succeeded but could not be written to the audit log: %v\n", err)
	}
}

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Config error: %v\n", err)
		os.Exit(1)
	}

	ctx := context.Background()

	switch os.Args[1] {
	case "migrate":
		fmt.Println("Applying database migrations...")
		if err := postgres.RunMigrations(cfg.MigrationURL()); err != nil {
			fmt.Fprintf(os.Stderr, "Migration failed: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("Migrations applied successfully.")

	case "create-tenant":
		cmd := flag.NewFlagSet("create-tenant", flag.ExitOnError)
		name := cmd.String("name", "Default Tenant", "Tenant name")
		rate := cmd.Float64("rate", 60, "Enqueue rate per second")
		burst := cmd.Float64("burst", 120, "Burst capacity")
		pending := cmd.Int("pending", 10000, "Max pending jobs")
		concurrent := cmd.Int("concurrent", 4, "Max concurrent running jobs")
		schedules := cmd.Int("schedules", 100, "Max schedules")
		_ = cmd.Parse(os.Args[2:])

		db, err := postgres.NewDB(ctx, cfg)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Database connection error: %v\n", err)
			os.Exit(1)
		}
		defer db.Close()

		repo := postgres.NewTenantRepo(db)
		tenant, err := repo.CreateTenant(ctx, postgres.CreateTenantParams{
			Name:                 *name,
			EnqueueRatePerSecond: *rate,
			EnqueueBurstCapacity: *burst,
			MaxPendingJobs:       *pending,
			MaxConcurrentJobs:    *concurrent,
			MaxSchedules:         *schedules,
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to create tenant: %v\n", err)
			os.Exit(1)
		}

		audit(ctx, db, &tenant.ID, "create_tenant", tenant.ID.String())
		fmt.Printf("Tenant created successfully!\nID:   %s\nName: %s\n", tenant.ID, tenant.Name)

	case "create-api-key":
		cmd := flag.NewFlagSet("create-api-key", flag.ExitOnError)
		tenantIDStr := cmd.String("tenant-id", "", "Tenant UUID")
		name := cmd.String("name", "Default Key", "Key descriptive name")
		permsStr := cmd.String("permissions", "jobs:read,jobs:write,schedules:read,schedules:write,deadletters:read,deadletters:write,metrics:read", "Comma-separated permissions (add workers:read for full worker details)")
		expiresIn := cmd.Duration("expires-in", 0, "Key lifetime, e.g. 2160h for 90 days (0 = never expires; prefer expiring keys)")
		_ = cmd.Parse(os.Args[2:])

		if *tenantIDStr == "" {
			fmt.Fprintln(os.Stderr, "Error: --tenant-id is required")
			os.Exit(1)
		}
		tenantID, err := uuid.Parse(*tenantIDStr)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Invalid tenant UUID: %v\n", err)
			os.Exit(1)
		}

		db, err := postgres.NewDB(ctx, cfg)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Database connection error: %v\n", err)
			os.Exit(1)
		}
		defer db.Close()

		perms := strings.Split(*permsStr, ",")
		for i := range perms {
			perms[i] = strings.TrimSpace(perms[i])
		}

		repo := postgres.NewTenantRepo(db)
		var res *postgres.APIKeyCreationResult
		if *expiresIn > 0 {
			res, err = repo.CreateAPIKeyExpiringIn(ctx, tenantID, *name, perms, *expiresIn)
		} else {
			res, err = repo.CreateAPIKey(ctx, tenantID, *name, perms, nil)
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to create API key: %v\n", err)
			os.Exit(1)
		}
		audit(ctx, db, &tenantID, "create_api_key", res.APIKey.ID.String())

		fmt.Println("==================================================")
		fmt.Println("API Key created successfully!")
		fmt.Printf("Key ID:       %s\n", res.APIKey.ID)
		fmt.Printf("Tenant ID:    %s\n", res.APIKey.TenantID)
		fmt.Printf("Prefix:       %s\n", res.APIKey.DisplayPrefix)
		fmt.Printf("Permissions:  %s\n", strings.Join(res.APIKey.Permissions, ", "))
		if res.APIKey.ExpiresAt != nil {
			fmt.Printf("Expires:      %s (rotate before then: create the new key first, then revoke this one)\n", res.APIKey.ExpiresAt.Format("2006-01-02 15:04 MST"))
		}
		fmt.Printf("SECRET TOKEN: %s\n", res.RawSecret)
		fmt.Println("NOTE: The secret token will NEVER be shown again! Save it securely.")
		fmt.Println("==================================================")

	case "list-tenants":
		db, err := postgres.NewDB(ctx, cfg)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Database connection error: %v\n", err)
			os.Exit(1)
		}
		defer db.Close()

		repo := postgres.NewTenantRepo(db)
		tenants, err := repo.ListTenants(ctx)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to list tenants: %v\n", err)
			os.Exit(1)
		}

		fmt.Println("Tenants:")
		for _, t := range tenants {
			fmt.Printf("- %s | %s | %s | %s\n", t.ID, t.Status, t.Name, t.CreatedAt.Format("2006-01-02 15:04:05"))
		}

	case "revoke-api-key":
		cmd := flag.NewFlagSet("revoke-api-key", flag.ExitOnError)
		keyIDStr := cmd.String("key-id", "", "API Key UUID")
		_ = cmd.Parse(os.Args[2:])

		if *keyIDStr == "" {
			fmt.Fprintln(os.Stderr, "Error: --key-id is required")
			os.Exit(1)
		}
		keyID, err := uuid.Parse(*keyIDStr)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Invalid key UUID: %v\n", err)
			os.Exit(1)
		}

		db, err := postgres.NewDB(ctx, cfg)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Database connection error: %v\n", err)
			os.Exit(1)
		}
		defer db.Close()

		repo := postgres.NewTenantRepo(db)
		if err := repo.RevokeAPIKey(ctx, keyID); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to revoke API key: %v\n", err)
			os.Exit(1)
		}
		audit(ctx, db, nil, "revoke_api_key", keyID.String())
		fmt.Println("API key revoked successfully.")

	case "reconcile-usage":
		cmd := flag.NewFlagSet("reconcile-usage", flag.ExitOnError)
		tenantIDStr := cmd.String("tenant-id", "", "Tenant UUID")
		_ = cmd.Parse(os.Args[2:])

		if *tenantIDStr == "" {
			fmt.Fprintln(os.Stderr, "Error: --tenant-id is required")
			os.Exit(1)
		}
		tenantID, err := uuid.Parse(*tenantIDStr)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Invalid tenant UUID: %v\n", err)
			os.Exit(1)
		}

		db, err := postgres.NewDB(ctx, cfg)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Database connection error: %v\n", err)
			os.Exit(1)
		}
		defer db.Close()

		repo := postgres.NewTenantRepo(db)
		count, err := repo.ReconcileUsage(ctx, tenantID)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to reconcile usage: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Tenant %s usage reconciled. Nonterminal outstanding jobs: %d\n", tenantID, count)

	default:
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Println("QueueForge Administrative CLI")
	fmt.Println("Usage:")
	fmt.Println("  admin migrate")
	fmt.Println("  admin create-tenant [--name ...] [--rate ...] [--burst ...] [--pending ...] [--concurrent ...] [--schedules ...]")
	fmt.Println("  admin create-api-key --tenant-id <uuid> [--name ...] [--permissions ...]")
	fmt.Println("  admin list-tenants")
	fmt.Println("  admin revoke-api-key --key-id <uuid>")
	fmt.Println("  admin reconcile-usage --tenant-id <uuid>")
}
