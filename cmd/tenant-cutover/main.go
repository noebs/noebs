// tenant-cutover is an offline administrative utility, never an API/startup hook.
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/adonese/noebs/internal/tenantcutover"
	_ "github.com/jackc/pgx/v5/stdlib"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() error {
	env := flag.String("database-url-env", "", "name of environment variable containing administrative PostgreSQL URL")
	source := flag.String("source", "", "must be tenant-mojaloop")
	target := flag.String("target", "", "must be noebs")
	apply := flag.Bool("apply", false, "apply under full application downtime, after backup and manifest review")
	expect := flag.String("expected", "", "reviewed pre-cutover JSON manifest, required with --apply")
	report := flag.String("report", "", "write resulting JSON manifest to this new file (0600)")
	flag.Parse()
	if flag.NArg() != 0 || *env == "" || *source != tenantcutover.Source || *target != tenantcutover.Target || *report == "" || (*apply && *expect == "") || (!*apply && *expect != "") {
		return fmt.Errorf("require --database-url-env NAME --source tenant-mojaloop --target noebs --report NEW_FILE; apply additionally requires --apply --expected REVIEWED_FILE")
	}
	url := os.Getenv(*env)
	if url == "" {
		return fmt.Errorf("database URL environment variable is empty")
	}
	var expected *tenantcutover.Snapshot
	if *apply {
		f, err := os.Open(*expect)
		if err != nil {
			return err
		}
		defer f.Close()
		expected = &tenantcutover.Snapshot{}
		d := json.NewDecoder(f)
		d.DisallowUnknownFields()
		if err = d.Decode(expected); err != nil {
			return err
		}
	}
	// Reserve the report before mutating; never overwrite the pre-cutover proof.
	f, err := os.OpenFile(*report, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	db, err := sql.Open("pgx", url)
	if err != nil {
		return fmt.Errorf("open administrative database: invalid connection settings")
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	snapshot, err := tenantcutover.Run(ctx, db, *apply, expected)
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(f)
	encoder.SetIndent("", "  ")
	if err = encoder.Encode(snapshot); err != nil {
		return fmt.Errorf("operation finished but report write failed: %w", err)
	}
	if err = f.Sync(); err != nil {
		return err
	}
	fmt.Printf("database=%s applied=%t report=%s\n", snapshot.Database, *apply, *report)
	return nil
}
