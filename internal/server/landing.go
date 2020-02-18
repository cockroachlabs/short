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
	"net/http"

	"github.com/cockroachlabs/short/internal/assets"
	"github.com/cockroachlabs/short/internal/server/response"
)

var landing *template.Template

func init() {
	var err error
	landing, err = template.New("landing").Parse(string(assets.Assets["landing.html"].Data))
	if err != nil {
		panic(err)
	}
}

func (s *Server) landingPage(ctx context.Context) *response.Response {
	auth := authFrom(ctx)
	links, _ := s.store.List(ctx, auth)
	totalLinks, totalClicks, err := s.store.Served(ctx)
	if err != nil {
		return response.Error(http.StatusInternalServerError, err)
	}

	data := map[string]interface{}{
		"TotalClicks": totalClicks,
		"TotalLinks":  totalLinks,
		"Links":       links,
		"User":        auth,
	}

	return response.Func(func(w http.ResponseWriter) error {
		w.Header().Set(contentType, "text/html; charset=UTF-8")
		return landing.Execute(w, data)
	})
}
