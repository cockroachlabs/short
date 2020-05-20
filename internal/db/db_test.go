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

package db

import (
	"context"
	"log"
	"net/http"
	"os"
	"testing"

	"github.com/cockroachlabs/short/internal/test"
	"github.com/stretchr/testify/assert"
)

var conn string

func TestMain(m *testing.M) {
	ctx, cancel := context.WithCancel(context.Background())

	c, stopped, err := test.StartDB(ctx)
	if err != nil {
		cancel()
		log.Fatal(err)
	}
	conn = c

	exit := m.Run()
	cancel()
	<-stopped
	os.Exit(exit)
}

func TestDB(t *testing.T) {
	ctx := context.Background()

	s, err := New(ctx, conn)
	if !assert.NoError(t, err) {
		return
	}
	assert.NotNil(t, s)

	ctx, tx, err := s.WithTransaction(ctx)
	if !assert.NoError(t, err) {
		return
	}

	var eTag ETag
	checkETag := func(t *testing.T) {
		t.Helper()
		t.Run("etag", func(t *testing.T) {
			a := assert.New(t)
			tag, err := s.ETag(ctx)
			a.NoError(err)
			a.NotEqual(eTag, tag)
			eTag = tag
		})
	}

	checkETag(t)
	assert.Equal(t, EmptyETag, eTag)

	t.Run("create link", func(t *testing.T) {
		a := assert.New(t)
		l1, err := s.Publish(ctx, &Link{
			Author:  "test",
			Comment: "Hello World!",
			Public:  false,
			Short:   "foobar",
			URL:     "https://example.com/foobar",
		})
		if !a.NoError(err) {
			return
		}
		a.False(l1.Public)
		a.Equal(l1.CreatedAt, l1.UpdatedAt)

		found, err := s.Get(ctx, "foobar")
		a.NoError(err)
		a.Equal(l1, found)
	})
	checkETag(t)

	t.Run("check updating", func(t *testing.T) {
		a := assert.New(t)

		l2, err := s.Publish(ctx, &Link{
			Author: "test",
			Listed: true,
			Public: true,
			Short:  "foobar",
			URL:    "https://example.com/foobar",
		})
		if !a.NoError(err) {
			return
		}
		a.True(l2.Listed)
		a.True(l2.Public)

		links, err := s.List(ctx, "test")
		a.NoError(err)
		link := <-links
		a.Equal(l2, link)
		_, ok := <-links
		a.False(ok)

		links, err = s.Listed(ctx, 10)
		a.NoError(err)
		link = <-links
		a.Equal(l2, link)
		_, ok = <-links
		a.False(ok)

		stats, err := s.Served(ctx)
		a.Equal(1, stats.Links)
		a.NoError(err)
	})
	checkETag(t)

	t.Run("check short aliases", func(t *testing.T) {
		a := assert.New(t)

		created, err := s.Publish(ctx, &Link{
			Author: "test",
			Short:  "foobar-alias",
			URL:    "https://example.com/foobar",
		})
		a.NoError(err)
		a.Equal(created.CreatedAt, created.UpdatedAt)

		original, err := s.Get(ctx, "foobar")
		a.NoError(err)
		a.NotNil(original)

		found, err := s.Get(ctx, "foobar-alias")
		a.NoError(err)
		a.NotNil(found)

		a.Equal(created, found)
		a.NotEqual(created, original)
	})

	t.Run("count clicks", func(t *testing.T) {
		a := assert.New(t)

		l, err := s.Get(ctx, "foobar")
		if !a.NoError(err) {
			return
		}

		a.NoError(s.Click(ctx, &Click{l, &http.Request{}}))
		a.NoError(s.Click(ctx, &Click{l, &http.Request{}}))
		count, err := s.Clicks(ctx, "foobar")
		a.NoError(err)
		a.Equal(2, count)
	})

	t.Run("totals", func(t *testing.T) {
		a := assert.New(t)

		stats, err := s.Served(ctx)
		a.Equal(2, stats.Links)
		a.Equal(2, stats.Clicks)
		a.NoError(err)
	})

	t.Run("list all", func(t *testing.T) {
		a := assert.New(t)

		ch, err := s.ListAll(ctx)
		if !a.NoError(err) {
			return
		}

		count := 0
		for range ch {
			count++
		}
		a.Equal(2, count)
	})
	checkETag(t)

	t.Run("delete", func(t *testing.T) {
		a := assert.New(t)

		a.NoError(s.Delete(ctx, "foobar", "test"))

		stats, err := s.Served(ctx)
		a.Equal(1, stats.Links)
		a.Equal(0, stats.Clicks)
		a.NoError(err)
	})
	checkETag(t)

	assert.NoError(t, tx.Commit())
}

func TestNoDuplicates(t *testing.T) {
	a := assert.New(t)
	ctx := context.Background()

	s, err := New(ctx, conn)
	if !a.NoError(err) {
		return
	}
	a.NotNil(s)

	_, err = s.Publish(context.Background(), &Link{
		Author: "one",
		Public: true,
		Short:  "foobar",
		URL:    "https://example.com/foobar",
	})
	a.NoError(err)

	l3, err := s.Publish(context.Background(), &Link{
		Author: "other",
		Public: true,
		Short:  "foobar",
		URL:    "https://example.com/foobar",
	})
	a.Equal(ErrShortConflict, err)
	a.Nil(l3)

	l4, err := s.Publish(context.Background(), &Link{
		Author: "other",
		Public: true,
		Short:  "foobar",
		URL:    "https://example.com/foobar",
	}, AllowNewAuthor)
	a.NoError(err)
	a.Equal("other", l4.Author)
}
