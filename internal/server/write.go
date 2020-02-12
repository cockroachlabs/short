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
	"encoding/json"
	"log"
	"net/http"
)

const (
	applicationJSON = "application/json"
	contentType     = "Content-Type"
	location        = "Location"
)

func writeError(w http.ResponseWriter, code int, err error) {
	writePlain(w, code, err.Error())
}

func writeJSON(w http.ResponseWriter, code int, data interface{}) {
	w.Header().Set(contentType, applicationJSON)
	w.WriteHeader(code)
	if err := json.NewEncoder(w).Encode(data); err != nil {
		log.Printf("could not write response: %v", err)
	}
}

func writePlain(w http.ResponseWriter, code int, data string) {
	writeJSON(w, code, struct{ Message string }{data})
}

func writeRedirect(w http.ResponseWriter, code int, loc string) {
	w.Header().Set(location, loc)
	writeJSON(w, code, struct{ Location string }{loc})
}

func writeStatus(w http.ResponseWriter, code int) {
	writePlain(w, code, http.StatusText(code))
}
