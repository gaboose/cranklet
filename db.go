package main

import (
	"database/sql"
	"fmt"

	"github.com/google/uuid"
)

const documentsTable = `CREATE TABLE IF NOT EXISTS documents (
		name TEXT PRIMARY KEY,
		filename TEXT
)`

type DB struct {
	inner *sql.DB
}

func NewDB(path string) (*DB, error) {
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		return nil, err
	}

	if _, err := db.Exec(documentsTable); err != nil {
		return nil, err
	}

	return &DB{inner: db}, err
}

// DocumentPath gets or generates a filename for the document name.
func (db *DB) DocumentFilename(name string) (string, error) {
	selectQuery := `SELECT filename FROM documents WHERE name = ?`
	insertQuery := `INSERT INTO documents (name, filename) VALUES (?, ?)`

	tx, err := db.inner.Begin()
	if err != nil {
		return "", fmt.Errorf("failed to begin transaction: %w", err)
	}

	var filename string

	row := tx.QueryRow(selectQuery, name)
	err = row.Scan(&filename)
	if err == sql.ErrNoRows {
		filename = uuid.New().String() + ".sqlite3"
		_, err := tx.Exec(insertQuery, name, filename)
		if err != nil {
			tx.Rollback()
			return "", fmt.Errorf("failed to insert document filename: %w", err)
		}
	} else if err != nil {
		tx.Rollback()
		return "", fmt.Errorf("failed to query document filename: %w", err)
	}

	if err = tx.Commit(); err != nil {
		return "", fmt.Errorf("failed to commit transaction: %w", err)
	}

	return filename, nil
}
