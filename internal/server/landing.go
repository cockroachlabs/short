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

package server

import (
	"context"
	"html/template"
	"time"

	"github.com/cockroachlabs/short/internal/assets"
	"github.com/cockroachlabs/short/internal/db"
	"github.com/pkg/errors"
)

type cachedTemplate struct {
	*template.Template
	mTime time.Time
}

type templateData struct {
	Ctx context.Context
	// Show additional UI allowing the user to hijack a link.
	EnableForce bool
	// Link will be present for editing flow.
	Link  *db.Link
	Store *db.Store
	User  string
}

func (s *Server) template(path string) (*cachedTemplate, error) {
	asset, err := assets.Get(path)
	if err != nil {
		return nil, errors.Wrapf(err, "path %s:", path)
	}

	stat, err := asset.Stat()
	if err != nil {
		return nil, errors.Wrapf(err, "template %s:", path)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	found := s.mu.templates[path]
	if found == nil || found.mTime.Before(stat.ModTime()) {
		data := make([]byte, stat.Size())
		if _, err := asset.Read(data); err != nil {
			return nil, errors.Wrapf(err, "reading %s:", path)
		}

		if parsed, err := template.New(path).Parse(string(data)); err == nil {
			found = &cachedTemplate{
				mTime:    stat.ModTime(),
				Template: parsed,
			}
			s.mu.templates[path] = found
		} else {
			return nil, errors.Wrapf(err, "parsing %s:", path)
		}
	}
	return found, nil
}
