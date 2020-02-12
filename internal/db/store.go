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

	"github.com/lib/pq"
	"github.com/pkg/errors"
)

var (
	// ErrShortConflict means that a conflicting short-name was chosen.
	ErrShortConflict = errors.New("the requested server link name already exists")

	schema = []string{`
CREATE TABLE IF NOT EXISTS links (
  author     STRING      NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  pub        BOOL        NOT NULL DEFAULT false,
  short      STRING      NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  url        STRING      NOT NULL,
  PRIMARY KEY (short),
  UNIQUE INDEX (author, short, url) -- Use INSERT ON CONFLICT for updates
)`, `
CREATE TABLE IF NOT EXISTS clicks (
  short      STRING      NOT NULL REFERENCES links(short) ON DELETE CASCADE,
  click_time TIMESTAMPTZ NOT NULL DEFAULT now()
)`,
	}
)

// Store provides access to the short-link data.
type Store struct {
	db *sql.DB

	click   *sql.Stmt
	clicks  *sql.Stmt
	get     *sql.Stmt
	list    *sql.Stmt
	publish *sql.Stmt
	served  *sql.Stmt
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

	if s.get, err = db.PrepareContext(ctx, `
SELECT author, created_at, pub, short, updated_at, url
FROM links
WHERE short=$1
`); err != nil {
		return nil, err
	}

	if s.list, err = db.PrepareContext(ctx, `
SELECT author, created_at, pub, short, updated_at, url
FROM links
WHERE author=$1
ORDER BY updated_at DESC
`); err != nil {
		return nil, err
	}

	if s.publish, err = db.PrepareContext(ctx, `
INSERT INTO links (author, created_at, pub, short, updated_at, url)
VALUES ($1, now(), $2, $3, now(), $4)
ON CONFLICT (author, short, url) DO UPDATE
SET pub = excluded.pub, updated_at = now()
RETURNING author, created_at, pub, short, updated_at, url
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
	_, err := s.click.ExecContext(ctx, normalize(short))
	return err
}

// Clicks returns the number of times that the link has been clicked.
func (s *Store) Clicks(ctx context.Context, short string) (int, error) {
	var count int
	row := s.clicks.QueryRowContext(ctx, normalize(short))
	if err := row.Scan(&count); err != nil {
		return 0, err
	}
	return count, nil
}

// Get locates a short link in the database, or returns nil if one
// does not exist.
func (s *Store) Get(ctx context.Context, short string) (*Link, error) {
	return decode(s.get.QueryRowContext(ctx, normalize(short)))
}

// List returns the Links that were created by the given author.
func (s *Store) List(ctx context.Context, author string) (<-chan *Link, <-chan error) {
	links := make(chan *Link, 1)
	errs := make(chan error, 3)

	go func() {
		defer close(links)
		defer close(errs)

		rows, err := s.list.QueryContext(ctx, author)
		if err != nil {
			errs <- err
			return
		}

		for rows.Next() {
			var link *Link
			if link, err = decode(rows); err == nil {
				if link.Count, err = s.Clicks(ctx, link.Short); err == nil {
					links <- link
					continue
				}
			}
			errs <- err
			break
		}
		if err := rows.Err(); err != nil {
			errs <- err
		}
		if err := rows.Close(); err != nil {
			errs <- err
		}
	}()

	return links, errs
}

// Ping checks the database connection.
func (s *Store) Ping(ctx context.Context) error {
	return s.db.PingContext(ctx)
}

// Publish stores or updates the link in the database.  This function
// returns the latest value in the database.
func (s *Store) Publish(ctx context.Context, l *Link) (*Link, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	l, err = decode(tx.Stmt(s.publish).QueryRowContext(ctx, l.Author, l.Public, normalize(l.Short), l.URL))
	if err != nil {
		ignore(tx.Rollback())
		return nil, err
	} else if err := l.Validate(); err != nil {
		ignore(tx.Rollback())
		return nil, err
	} else {
		return l, tx.Commit()
	}
}

// Served returns global statistics.
func (s *Store) Served(ctx context.Context) (links, clicks int, _ error) {
	if err := s.served.QueryRow().Scan(&links, &clicks); err != nil {
		return 0, 0, err
	}
	return
}

func decode(data scannable) (*Link, error) {
	l := &Link{}

	if err := data.Scan(&l.Author, &l.CreatedAt, &l.Public, &l.Short, &l.UpdatedAt, &l.URL); err != nil {
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

type scannable interface {
	Scan(args ...interface{}) error
}

func ignore(_ error) {}
