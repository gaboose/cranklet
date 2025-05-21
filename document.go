package main

import (
	"database/sql"
	"fmt"
	"strings"
)

const (
	verticesTable = `CREATE TABLE IF NOT EXISTS vertices (
		id TEXT PRIMARY KEY,
		data TEXT
	)`
	edgesTable = `CREATE TABLE IF NOT EXISTS edges (
		from_id TEXT,
		to_id TEXT,
		FOREIGN KEY(from_id) REFERENCES vertices(id),
		FOREIGN KEY(to_id) REFERENCES vertices(id)
	)`
)

type Vertex struct {
	ID      string   `json:"id"`
	Data    string   `json:"data"`
	Parents []string `json:"parents,omitempty"`
}

type Document struct {
	subscribers map[*func(Vertex) bool]struct{}
	db          *sql.DB
}

func NewDocument(name string) (*Document, error) {
	db, err := ensureDB(name + ".sqlite3")
	if err != nil {
		return nil, err
	}

	return &Document{
		subscribers: make(map[*func(Vertex) bool]struct{}),
		db:          db,
	}, nil
}

func (d *Document) Close() {
	d.db.Close()
}

func (d *Document) Get(after []string) ([]Vertex, error) {
	placeholders := strings.TrimRight(strings.Repeat("?,", len(after)), ",")

	query := fmt.Sprintf(`WITH RECURSIVE
SubGraph(id, data) AS (
	SELECT id, data FROM vertices WHERE id IN (%s)
	UNION ALL
	SELECT v.id, v.data
	FROM edges e
	JOIN vertices v ON v.id = e.to_id
	JOIN subgraph sg ON sg.id = e.from_id
)
SELECT v.id, v.data
FROM vertices v
LEFT JOIN SubGraph sg ON v.id = sg.id
WHERE sg.id IS NULL`, placeholders)

	args := make([]interface{}, len(after))
	for i, id := range after {
		args[i] = id
	}

	rows, err := d.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []Vertex
	for rows.Next() {
		var op Vertex
		if err := rows.Scan(&op.ID, &op.Data); err != nil {
			return nil, err
		}
		result = append(result, op)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return result, nil
}

func (d *Document) Post(v Vertex) error {
	tx, err := d.db.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}

	_, err = tx.Exec("INSERT INTO vertices (id, data) VALUES (?, ?)", v.ID, v.Data)
	if err != nil {
		tx.Rollback()
		return fmt.Errorf("failed to insert vertex: %w", err)
	}

	for _, parent := range v.Parents {
		_, err = tx.Exec("INSERT INTO edges (from_id, to_id) VALUES (?, ?)", v.ID, parent)
		if err != nil {
			tx.Rollback()
			return fmt.Errorf("failed to insert edge: %w", err)
		}
	}

	if err = tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	for updateHandler := range d.subscribers {
		if !(*updateHandler)(v) {
			delete(d.subscribers, updateHandler)
		}
	}

	return nil
}

func (d *Document) Subscribe(handler *func(Vertex) bool) {
	d.subscribers[handler] = struct{}{}
}

func (d *Document) Unsubscribe(handler *func(Vertex) bool) {
	delete(d.subscribers, handler)
}

func ensureDB(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		return nil, err
	}

	if _, err := db.Exec(verticesTable); err != nil {
		return nil, err
	}

	if _, err := db.Exec(edgesTable); err != nil {
		return nil, err
	}

	return db, nil
}
