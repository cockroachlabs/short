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
	"runtime/pprof"

	"github.com/cockroachlabs/short/internal/server/response"
)

type contextKey int

// A typesafe key for the user associated with the context.
const authUser contextKey = iota

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

type canonicity int

const (
	// If the hostname associated with the incoming request does not match
	// Server.canonicalHost, introduce a redirect before proceeding.
	canonical canonicity = iota
	// Process the request, regardless of the hostname used.
	anyHost
)

func (s *Server) handler(
	authLevel authLevel, canon canonicity, fn func(req *http.Request) *response.Response,
) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		labels := pprof.Labels("http-path", req.URL.Path)
		pprof.Do(req.Context(), labels, func(ctx context.Context) {
			var resp *response.Response
			defer func() {
				if err := resp.Write(w); err != nil {
					log.Printf("could not write response: %v", err)
				}
			}()

			// Canonicalize the incoming hostname.
			if canon == canonical && s.canonicalHost != "" && req.Host != s.canonicalHost {
				req.URL.Host = s.canonicalHost
				if s.httpOnly {
					req.URL.Scheme = "http"
				} else {
					req.URL.Scheme = "https"
				}
				resp = response.Redirect(http.StatusPermanentRedirect, req.URL.String())
				return
			}

			switch authLevel {
			case authPublic:
				// No-op.
			case authRequired, authOptional:
				if auth, ok := s.extractEmail(req); ok {
					req = req.WithContext(context.WithValue(ctx, authUser, auth))
				} else if authLevel == authRequired {
					resp = response.Status(http.StatusUnauthorized)
					return
				}
			}

			resp = fn(req)
		})
	}
}
