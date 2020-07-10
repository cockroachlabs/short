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

package mapper

import (
	"net/url"
	"testing"

	"github.com/cockroachlabs/short/internal/db"
	"github.com/stretchr/testify/assert"
)

func TestMapper(t *testing.T) {

	const host = "https://example.com/"
	links := []*db.Link{
		{
			Short: "example",
			URL:   host,
		},
		{
			Short: "path",
			URL:   host + "success/$(0)/$(1)/suffix",
		},
		{
			Short: "param",
			URL:   host + "success/$(p)/$(q)/suffix",
		},
		{
			Short: "fallback",
			URL: host + "success/fallback/$(1)||" +
				host + "success/fallback/$(q)||" +
				host + "success/fallback/other",
		},
	}
	m := New()
	m.SetLinks(links)

	tcs := []struct {
		request string
		match   string
		noMatch bool
	}{
		// Basic, simple matches.
		{
			request: "example",
			match:   host,
		},
		{
			request: "example/",
			match:   host,
		},
		{
			request: "/example",
			match:   host,
		},
		{
			request: "/example/",
			match:   host,
		},
		{
			request: "https://short.example.com/example",
			match:   host,
		},
		{
			request: "https://short.example.com/example/",
			match:   host,
		},

		// Test for simple mismatch.
		{
			request: "/nope",
			noMatch: true,
		},
		{
			request: "/nope/",
			noMatch: true,
		},
		{
			request: "https://short.example.com/nope",
			noMatch: true,
		},
		{
			request: "https://short.example.com/nope/",
			noMatch: true,
		},

		// Test path substitution.
		{
			request: "/path",
			noMatch: true,
		},
		{
			request: "/path/",
			noMatch: true,
		},
		{
			request: "/path/foo",
			match:   host + "success/path/foo/suffix",
		},
		{
			request: "/path/foo/",
			match:   host + "success/path/foo/suffix",
		},
		{
			request: "/path/foo/bar",
			match:   host + "success/path/foo/suffix",
		},
		{
			request: "/path/foo/bar/",
			match:   host + "success/path/foo/suffix",
		},

		// Test query parameter substitution.
		{
			request: "/param",
			noMatch: true,
		},
		{
			request: "/param?p=foo",
			noMatch: true,
		},
		{
			request: "/param?p=foo&q=bar",
			match:   host + "success/foo/bar/suffix",
		},
		{
			request: "/param?p=foo&q=bar&extra=ignored",
			match:   host + "success/foo/bar/suffix",
		},

		// Test fallback
		{
			request: "/fallback/foo",
			match:   host + "success/fallback/foo",
		},
		{
			request: "/fallback?q=bar",
			match:   host + "success/fallback/bar",
		},
		{
			request: "/fallback/?q=bar",
			match:   host + "success/fallback/bar",
		},
		{
			request: "/fallback?x=bar",
			match:   host + "success/fallback/other",
		},
		{
			request: "/fallback/",
			match:   host + "success/fallback/other",
		},
		{
			request: "/fallback",
			match:   host + "success/fallback/other",
		},
	}

	for _, tc := range tcs {
		t.Run(tc.request, func(t *testing.T) {
			a := assert.New(t)
			req, err := url.Parse(tc.request)
			if !a.NoError(err) {
				return
			}

			found, ok := m.Get(req)
			if tc.noMatch {
				a.False(ok, "match")
			} else {
				a.True(ok, "match")
				if a.NotNil(found, "matched, but returned nothing") {
					a.Equal(tc.match, found.URL, "url mismatch")
				}
			}
		})
	}
}
