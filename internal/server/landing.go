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
	"html/template"
	"log"
	"net/http"

	"github.com/cockroachlabs/short/internal/assets"
)

var landing *template.Template

func init() {
	var err error
	landing, err = template.New("landing").Parse(string(assets.Assets["landing.html"].Data))
	if err != nil {
		panic(err)
	}
}

func (s *Server) landingPage(w http.ResponseWriter, req *http.Request) {
	auth, ok := s.checkAuth(w, req)
	if !ok {
		return
	}

	links, _ := s.store.List(req.Context(), auth)
	totalLinks, totalClicks, err := s.store.Served(req.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	data := map[string]interface{}{
		"TotalClicks": totalClicks,
		"TotalLinks":  totalLinks,
		"Links":       links,
		"User":        auth,
	}

	w.Header().Set(contentType, "text/html; charset=UTF-8")
	w.WriteHeader(http.StatusOK)
	if err := landing.Execute(w, data); err != nil {
		log.Printf("%v", err)
	}
}
