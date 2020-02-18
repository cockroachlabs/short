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
	"log"
	"net/http"

	"github.com/cockroachlabs/short/internal/server/response"
)

type contextKey string

const authUser = contextKey("auth")

// authFrom extracts the currently-authorized user from the context.
func authFrom(ctx context.Context) string {
	auth, _ := ctx.Value(authUser).(string)
	return auth
}

type authLevel int

const (
	// Authorization must be present.
	authRequired authLevel = iota
	// Authorization may be present.
	authOptional
	// Authorization is absent or ignored.
	authPublic
)

func (s *Server) handler(authLevel authLevel, fn func(req *http.Request) *response.Response) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		var resp *response.Response

		switch authLevel {
		case authRequired, authOptional:
			if auth, ok := s.extractEmail(req); ok {
				req = req.WithContext(context.WithValue(req.Context(), authUser, auth))
			} else if authLevel == authRequired {
				resp = response.Status(http.StatusUnauthorized)
			}
		case authPublic:
			// No-op.
		}

		if resp == nil {
			resp = fn(req)
		}
		if err := resp.Write(w); err != nil {
			log.Printf("could not send response: %v", err)
		}
	}
}
