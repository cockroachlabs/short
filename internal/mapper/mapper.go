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

// Package mapper contains code which will convert an incoming URL
// request path into a URL to redirect the client to.
package mapper

import (
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/cockroachlabs/short/internal/db"
)

// subst looks for patterns like $(1) or $(param).
var subst = regexp.MustCompile(`\$\((\d+|\w+)\)`)

// Mapper implements the lookup logic. It supports a limited form of
// path-segment and query-parameter mapping.
type Mapper struct {
	mu struct {
		sync.RWMutex
		// Links, keyed by the initial path segment.
		data map[string]db.Link
	}
}

// New constructs an empty Mapper.
func New() *Mapper {
	m := &Mapper{}
	m.mu.data = make(map[string]db.Link)
	return m
}

// Get resolves an incoming request URL to a short link to redirect the
// caller to.
func (m *Mapper) Get(request *url.URL) (_ *db.Link, ok bool) {
	parts := strings.Split(request.Path, "/")
	// Delete empty segments.
	idx := 0
	for _, part := range parts {
		if part != "" {
			parts[idx] = part
			idx++
		}
	}
	parts = parts[:idx]

	if idx == 0 {
		return nil, false
	}

	m.mu.RLock()
	link, ok := m.mu.data[parts[0]]
	m.mu.RUnlock()

	if !ok {
		return nil, false
	}

	ok = true
	link.URL = subst.ReplaceAllStringFunc(link.URL, func(s string) string {
		// Throw away delimiters: $(foo) -> foo.
		s = s[2 : len(s)-1]

		if s == "" {
			ok = false
			return ""
		}

		if param := request.Query().Get(s); param != "" {
			return param
		}

		if idx, err := strconv.Atoi(s); err == nil {
			if idx >= 0 && idx < len(parts) {
				return parts[idx]
			}
		}

		ok = false
		return ""
	})
	return &link, ok
}

// SetLinks updates the internal state of the Mapper with a new
// collection of short links. This method does not require external
// synchronization.
func (m *Mapper) SetLinks(links []*db.Link) {
	nextMap := make(map[string]db.Link, len(links))
	for i := range links {
		nextMap[links[i].Short] = *links[i]
	}

	m.mu.Lock()
	m.mu.data = nextMap
	m.mu.Unlock()
}
