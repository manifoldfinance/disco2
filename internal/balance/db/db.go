// Package db provides database connectivity for the balance service
package db

import (
	"database/sql"
	"fmt"
	"log"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres" // PostgreSQL driver
	_ "github.com/golang-migrate/migrate/v4/source/file"       // File source
)

// Connect establishes a connection to the database
func Connect(dsn string) (*sql.DB, error) {
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to database: %w", err)
	}

	// Verify connection works
	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	return db, nil
}

// RunMigrations applies database migrations
func RunMigrations(dsn, migrationsPath string) error {
	m, err := migrate.New(
		migrationsPath, // Path to migration files
		dsn)            // Database connection string
	if err != nil {
		return fmt.Errorf("failed to create migrate instance: %w", err)
	}

	if err := m.Up(); err != nil && err != migrate.ErrNoChange {
		return fmt.Errorf("failed to run migrations: %w", err)
	}

	log.Println("Database migrations applied successfully")
	return nil
}
