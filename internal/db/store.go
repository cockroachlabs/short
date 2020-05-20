// Copyright 2020 The Cockroach Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package db contains the storage layer for the application.
package db

import (
	"context"
	"database/sql"
	"runtime"

	"log"

	"github.com/lib/pq"
	"github.com/pkg/errors"
)

type contextKey int

// A typesafe key for storing a transaction in a context.
const txKey contextKey = iota

var (
	// ErrShortConflict means that a conflicting short-name was chosen.
	ErrShortConflict = errors.New("the requested short link name already exists")

	schema = []string{`
CREATE TABLE IF NOT EXISTS links (
  author     STRING      NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  comment    STRING      NOT NULL DEFAULT '',
  listed     BOOL        NOT NULL DEFAULT false,
  pub        BOOL        NOT NULL DEFAULT false,
  short      STRING      NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  url        STRING      NOT NULL,
  PRIMARY KEY (short), -- Use INSERT ON CONFLICT for updates
  INDEX (updated_at DESC, author) -- "Your links" data
)`, `
CREATE TABLE IF NOT EXISTS clicks (
  short      STRING      NOT NULL REFERENCES links(short) ON DELETE CASCADE,
  click_time TIMESTAMPTZ NOT NULL DEFAULT now(),
  uuid       UUID        NOT NULL DEFAULT gen_random_uuid(),
  PRIMARY KEY (short, click_time, uuid)
) INTERLEAVE IN PARENT links(short)`,
	}
)

// ETag is an opaque value used to track changes in the links table.
type ETag string

// EmptyETag will be returned when there is no link data.
const EmptyETag ETag = "<<empty>>"

// GlobalStats summarizes the total usage.
type GlobalStats struct {
	Clicks int
	Links  int
}

// PublishOpt can be passed to Store.Publish to alter the default behaviors.
type PublishOpt int

const (
	// AllowNewAuthor instructs Store.Publish to allow a link to be
	// taken over by an author other than the one that created it.
	AllowNewAuthor PublishOpt = iota
)

// Store provides access to the short-link data.
type Store struct {
	db *sql.DB

	click   *sql.Stmt // Record a click
	clicks  *sql.Stmt // Get click count for a link
	delete  *sql.Stmt // Delete a short link
	get     *sql.Stmt // Get a short link
	etag    *sql.Stmt // A modification marker for the links table
	list    *sql.Stmt // List all links for a user
	listAll *sql.Stmt // List all links, unordered
	listed  *sql.Stmt // All listed (internally-visible) links
	publish *sql.Stmt // Insert/update a link
	served  *sql.Stmt // Global statistics
}

// New creates a new Store.
func New(ctx context.Context, conn string) (*Store, error) {
	db, err := sql.Open("postgres", conn)
	if err != nil {
		return nil, err
	}

	for _, q := range schema {
		_, err = db.ExecContext(ctx, q)
		if err != nil {
			return nil, err
		}
	}

	s := &Store{db: db}

	if s.click, err = db.PrepareContext(ctx, `
INSERT INTO clicks (short)
VALUES ($1)
`); err != nil {
		return nil, err
	}

	if s.clicks, err = db.PrepareContext(ctx, `
SELECT COUNT(*) from clicks
WHERE short = $1
`); err != nil {
		return nil, err
	}

	if s.delete, err = db.PrepareContext(ctx, `
DELETE FROM links
WHERE short = $1 AND author = $2
`); err != nil {
		return nil, err
	}

	if s.etag, err = db.PrepareContext(ctx, `
SHOW EXPERIMENTAL_FINGERPRINTS FROM TABLE links
`); err != nil {
		return nil, err
	}

	if s.get, err = db.PrepareContext(ctx, `
SELECT author, comment, created_at, (SELECT COUNT(*) FROM clicks WHERE short=$1), listed, pub, short, updated_at, url
FROM links
WHERE short=$1
`); err != nil {
		return nil, err
	}

	if s.list, err = db.PrepareContext(ctx, `
SELECT author, comment, created_at, (SELECT COUNT(*) FROM clicks WHERE clicks.short=links.short), listed, pub, short, updated_at, url
FROM links
WHERE author=$1
ORDER BY updated_at DESC
`); err != nil {
		return nil, err
	}

	if s.listAll, err = db.PrepareContext(ctx, `
SELECT author, comment, created_at, (SELECT COUNT(*) FROM clicks WHERE clicks.short=links.short), listed, pub, short, updated_at, url
FROM links
`); err != nil {
		return nil, err
	}

	if s.listed, err = db.PrepareContext(ctx, `
SELECT * FROM
  (SELECT author, comment, created_at, (SELECT COUNT(*) FROM clicks WHERE clicks.short=links.short), listed, pub, short, updated_at, url
  FROM links
  WHERE listed
  ORDER BY updated_at DESC
  LIMIT $1)
ORDER BY short
`); err != nil {
		return nil, err
	}

	if s.publish, err = db.PrepareContext(ctx, `
INSERT INTO links (author, comment, created_at, listed, pub, short, updated_at, url)
VALUES ($1, $2, now(), $3, $4, $5, now(), $6)
ON CONFLICT (short) DO UPDATE
SET
  author = excluded.author,
  comment = excluded.comment,
  listed = excluded.listed,
  pub = excluded.pub,
  updated_at = now(),
  url = excluded.url
WHERE (links.author = excluded.author) OR $7 -- Allow "force-pushing" a link
RETURNING author, comment, created_at, (SELECT COUNT(*) FROM clicks WHERE short=$5), listed, pub, short, updated_at, url
`); err != nil {
		return nil, err
	}

	if s.served, err = db.PrepareContext(ctx, `
SELECT 
  (SELECT COUNT(*) FROM links),
  (SELECT COUNT(*) FROM clicks)
`); err != nil {
		return nil, err
	}

	return s, nil
}

// Click records a click on a short link.
func (s *Store) Click(ctx context.Context, short string) error {
	_, err := tx(ctx, s.click).ExecContext(ctx, normalize(short))
	return err
}

// Clicks returns the number of times that the link has been clicked.
func (s *Store) Clicks(ctx context.Context, short string) (int, error) {
	var count int
	row := tx(ctx, s.clicks).QueryRowContext(ctx, normalize(short))
	if err := row.Scan(&count); err != nil {
		return 0, err
	}
	return count, nil
}

// Delete removes the given short link if it is owned by author.
func (s *Store) Delete(ctx context.Context, short, author string) error {
	_, err := tx(ctx, s.delete).ExecContext(ctx, short, author)
	return err
}

// Get locates a short link in the database, or returns nil if one
// does not exist.
func (s *Store) Get(ctx context.Context, short string) (*Link, error) {
	return decode(tx(ctx, s.get).QueryRowContext(ctx, normalize(short)))
}

// ETag returns a last-modified value for the collection of links.
func (s *Store) ETag(ctx context.Context) (ETag, error) {
	rows, err := tx(ctx, s.etag).QueryContext(ctx)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		var hash *string
		if err := rows.Scan(&name, &hash); err != nil {
			return "", err
		}
		if name == "primary" {
			if hash == nil {
				return EmptyETag, nil
			}
			return ETag(*hash), nil
		}
	}
	return "", errors.New("no primary key fingerprint")
}

// List returns the Links that were created by the given author.
func (s *Store) List(ctx context.Context, author string) (<-chan *Link, error) {
	links := make(chan *Link, 1)

	go func() {
		defer close(links)

		rows, err := tx(ctx, s.list).QueryContext(ctx, author)
		if err != nil {
			log.Printf("listing for %s: %v", author, err)
			return
		}

		for rows.Next() {
			if link, err := decode(rows); err == nil {
				select {
				case links <- link:
				case <-ctx.Done():
					// Interrupted.
					break
				}
			} else {
				log.Printf("could not decode row for %s: %v", author, err)
				return
			}
		}
		if err := rows.Err(); err != nil {
			log.Printf("query failure for %s: %v", author, err)
		}
		_ = rows.Close()
	}()

	return links, nil
}

// ListAll returns all Links in the database.
func (s *Store) ListAll(ctx context.Context) (<-chan *Link, error) {
	links := make(chan *Link, 1)

	go func() {
		defer close(links)

		rows, err := tx(ctx, s.listAll).QueryContext(ctx)
		if err != nil {
			log.Printf("listing all links: %v", err)
			return
		}

		for rows.Next() {
			if link, err := decode(rows); err == nil {
				select {
				case links <- link:
				case <-ctx.Done():
					// Interrupted.
					break
				}
			} else {
				log.Printf("could not decode link row: %v", err)
				return
			}
		}
		if err := rows.Err(); err != nil {
			log.Printf("query failure for all links: %v", err)
		}
		_ = rows.Close()
	}()

	return links, nil
}

// Listed returns CRL-visible links.
func (s *Store) Listed(ctx context.Context, limit int) (<-chan *Link, error) {
	links := make(chan *Link, 1)

	go func() {
		defer close(links)

		rows, err := tx(ctx, s.listed).QueryContext(ctx, limit)
		if err != nil {
			log.Printf("listed: %v", err)
			return
		}

		for rows.Next() {
			if link, err := decode(rows); err == nil {
				select {
				case links <- link:
				case <-ctx.Done():
					// Interrupted.
					return
				}
			} else {
				log.Printf("listed: could not decode row: %v", err)
				return
			}
		}
		if err := rows.Err(); err != nil {
			log.Printf("listed: query failure: %v", err)
		}
		_ = rows.Close()
	}()

	return links, nil
}

// Ping checks the database connection.
func (s *Store) Ping(ctx context.Context) error {
	return s.db.PingContext(ctx)
}

// Publish stores or updates the link in the database.  This function
// returns the latest value in the database.
func (s *Store) Publish(ctx context.Context, l *Link, opts ...PublishOpt) (_ *Link, err error) {
	var force bool
	for _, opt := range opts {
		switch opt {
		case AllowNewAuthor:
			force = true
		}
	}

	l, err = decode(tx(ctx, s.publish).QueryRowContext(
		ctx, l.Author, l.Comment, l.Listed, l.Public, normalize(l.Short), l.URL, force))
	if err != nil {
		return nil, err
	} else if l == nil {
		return nil, ErrShortConflict
	} else if err := l.Validate(); err != nil {
		return nil, err
	} else {
		return l, nil
	}
}

// Served returns global statistics.
func (s *Store) Served(ctx context.Context) (*GlobalStats, error) {
	ret := &GlobalStats{}
	if err := tx(ctx, s.served).QueryRowContext(ctx).Scan(&ret.Links, &ret.Clicks); err != nil {
		return nil, err
	}
	return ret, nil
}

// WithTransaction returns a new context that represents a database
// transaction. If the given context already contains a transaction,
// this method will return it. The transaction will be automatically
// rolled back if the
func (s *Store) WithTransaction(parent context.Context) (context.Context, *Tx, error) {
	if parent.Value(txKey) != nil {
		return parent, nil, nil
	}

	dbTx, err := s.db.BeginTx(parent, nil)
	if err != nil {
		return parent, nil, err
	}
	tx := &Tx{false, dbTx}
	// Ensure that transactions will eventually get cleaned up.
	runtime.SetFinalizer(tx, func(tx *Tx) {
		if !tx.closed {
			log.Printf("transaction open at finalization")
			tx.Rollback()
		}
	})
	ctx := context.WithValue(parent, txKey, tx)
	return ctx, tx, nil
}

// Tx is a handle to an underlying database transaction.
type Tx struct {
	closed bool
	tx     *sql.Tx
}

// Commit will commit the underlying database transaction. This method
// will return an error if the transaction has already been closed.
func (t *Tx) Commit() error {
	t.closed = true
	return t.tx.Commit()
}

// Rollback will abort the underlying database transaction. This method
// is a no-op if the transaction has already been committed.
func (t *Tx) Rollback() {
	if t.closed {
		return
	}
	t.closed = true
	_ = t.tx.Rollback()
}

func decode(data interface {
	Scan(args ...interface{}) error
}) (*Link, error) {
	l := &Link{}

	if err := data.Scan(
		&l.Author, &l.Comment, &l.CreatedAt, &l.Count, &l.Listed, &l.Public, &l.Short, &l.UpdatedAt, &l.URL,
	); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		if e, ok := err.(*pq.Error); ok {
			if e.Code == "23505" {
				return nil, ErrShortConflict
			}
		}
		return nil, err
	}
	return l, nil
}

// Attach the given statement to the transaction associated with the
// context, if one exists, and return the statement.
func tx(ctx context.Context, stmt *sql.Stmt) *sql.Stmt {
	if tx, ok := ctx.Value(txKey).(*Tx); ok {
		return tx.tx.Stmt(stmt)
	}
	return stmt
}
