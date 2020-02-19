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

// Package server contains the bulk of the API and UI code.
package server

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"math/big"
	mathrand "math/rand"
	"net/http"
	"strings"
	"time"

	"path"

	"sync"

	"github.com/cockroachlabs/short/internal/assets"
	"github.com/cockroachlabs/short/internal/db"
	"github.com/cockroachlabs/short/internal/hash"
	"github.com/cockroachlabs/short/internal/server/response"
	"github.com/pkg/errors"
	"github.com/spf13/pflag"
)

const (
	applicationJSON = "application/json"
	contentType     = "Content-Type"
	textHTML        = "text/html; charset=UTF-8"
)

// Server handles the HTTP API and the UI.
type Server struct {
	bind      string
	conn      string
	fallback  string
	forceUser string
	httpOnly  bool
	store     *db.Store

	mu struct {
		sync.Mutex
		templates map[string]*cachedTemplate
	}
}

// New constructs a Server that will be configured by the flag set.
func New(flags *pflag.FlagSet) *Server {
	s := &Server{}
	s.mu.templates = make(map[string]*cachedTemplate)
	flags.StringVar(&assets.AssetPath, "assetPath", "", "for development use")
	flags.StringVarP(&s.bind, "bind", "b", ":443", "the address to bind to")
	flags.StringVarP(&s.conn, "conn", "c", "", "the database connection string")
	flags.BoolVar(&s.httpOnly, "httpOnly", false, "bind HTTP instead of HTTPS")
	flags.StringVar(&s.fallback, "fallback", "https://cockroachlabs.com", "the URL to send if not found")
	flags.StringVar(&s.forceUser, "forceUser", "", "for debugging use only")
	return s
}

// Run blocks until the context is cancelled.
func (s *Server) Run(ctx context.Context) error {
	if s.conn == "" {
		return errors.New("no database connection string provided")
	}
	var err error
	s.store, err = db.New(ctx, s.conn)
	if err != nil {
		return err
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/_/asset/", s.handler(authRequired, s.asset))
	mux.HandleFunc("/_/edit/", s.handler(authRequired, s.edit))
	mux.HandleFunc("/_/healthz", s.handler(authPublic, s.healthz))
	mux.HandleFunc("/_/v1/link/", s.handler(authRequired, s.crud))
	mux.HandleFunc("/_/v1/publish", s.handler(authRequired, s.publish))
	mux.HandleFunc("/p/", s.handler(authPublic, s.public))
	mux.HandleFunc("/", s.handler(authRequired, s.root))

	server := http.Server{
		Addr:    s.bind,
		Handler: mux,
	}
	listen := server.ListenAndServe

	if !s.httpOnly {
		// Loosely based on https://golang.org/src/crypto/tls/generate_cert.go
		priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			return errors.Wrap(err, "failed to generate private key")
		}

		now := time.Now().UTC()

		serialNumberLimit := new(big.Int).Lsh(big.NewInt(1), 128)
		serialNumber, err := rand.Int(rand.Reader, serialNumberLimit)
		if err != nil {
			return errors.Wrap(err, "failed to generate serial number")
		}

		cert := x509.Certificate{
			BasicConstraintsValid: true,
			DNSNames:              []string{"localhost"},
			ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
			KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
			NotBefore:             now,
			NotAfter:              now.AddDate(1, 0, 0),
			SerialNumber:          serialNumber,
			Subject: pkix.Name{
				Organization: []string{"Cockroach Labs"},
			},
		}

		certBytes, err := x509.CreateCertificate(rand.Reader, &cert, &cert, &priv.PublicKey, priv)
		if err != nil {
			return errors.Wrap(err, "failed to generate certificate")
		}

		server.TLSConfig = &tls.Config{
			Certificates: []tls.Certificate{{
				Certificate: [][]byte{certBytes},
				PrivateKey:  priv,
			}}}
		listen = func() error {
			return server.ListenAndServeTLS("" /* certfile */, "" /* keyfile */)
		}
	}

	go func() {
		<-ctx.Done()
		grace, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		if err := server.Shutdown(grace); err != nil {
			log.Printf("could not stop server: %v", err)
		}
		cancel()
	}()
	log.Printf("listening on %s", server.Addr)

	if err := listen(); err != nil && err != http.ErrServerClosed {
		return errors.Wrap(err, "failed to start server")
	}

	return nil
}

// asset serves static asset data (images, js, etc.)
func (s *Server) asset(r *http.Request) *response.Response {
	if r.Method != http.MethodGet {
		return response.Status(http.StatusMethodNotAllowed)
	}
	p := r.URL.Path[9:]
	if asset, _ := assets.Get(p); asset != nil {
		return response.Func(func(w http.ResponseWriter) error {
			// Use pre-computed content type.
			w.Header().Set(contentType, asset.ContentType())
			stat, err := asset.Stat()
			if err != nil {
				return err
			}
			http.ServeContent(w, r, p, stat.ModTime(), asset)
			return nil
		})
	}
	return response.Status(http.StatusNotFound)
}

// extractEmail looks for the IAP JWT data.
func (s *Server) extractEmail(r *http.Request) (string, bool) {
	if s.forceUser != "" {
		return s.forceUser, true
	}
	jwt := r.Header.Get("x-goog-iap-jwt-assertion")
	if jwt == "" {
		return "", false
	}
	// Split the JWT at the period tokens.
	parts := strings.Split(jwt, ".")
	if len(parts) != 3 {
		return "", false
	}

	// Base64 decode the JSON assertion data.
	data, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", false
	}
	var token struct {
		Email string
	}
	if err := json.Unmarshal(data, &token); err != nil {
		return "", false
	}
	if token.Email == "" {
		return "", false
	}
	return token.Email, true
}

func (s *Server) crud(req *http.Request) *response.Response {
	// Expecting the next element of the path to be a short link id
	dir, short := path.Split(req.URL.Path)
	if dir != "/_/v1/link/" {
		return response.Status(http.StatusNotFound)
	}

	switch req.Method {
	case http.MethodDelete:
		return s.crudDelete(req, short)
	case http.MethodGet:
		return s.crudGet(req, short)
	case http.MethodPost:
		return s.crudPost(req)
	default:
		return response.Status(http.StatusMethodNotAllowed)
	}
}

func (s *Server) crudDelete(req *http.Request, short string) *response.Response {
	author := authFrom(req.Context())
	ctx, tx, err := s.store.WithTransaction(req.Context())
	defer func() {
		if err != nil {
			tx.Rollback()
		}
	}()

	link, err := s.store.Get(ctx, short)
	if err != nil {
		return response.Error(http.StatusInternalServerError, err)
	}
	if link == nil {
		return response.Status(http.StatusNotFound)
	}
	if link.Author != author {
		return response.Status(http.StatusForbidden)
	}
	if err := s.store.Delete(ctx, short, author); err != nil {
		return response.Error(http.StatusInternalServerError, err)
	}
	if err := tx.Commit(); err != nil {
		return response.Error(http.StatusInternalServerError, err)
	}
	return response.JSON(http.StatusOK, link)
}

func (s *Server) crudGet(req *http.Request, short string) *response.Response {
	link, err := s.store.Get(req.Context(), short)
	if err != nil {
		return response.Error(http.StatusInternalServerError, err)
	}
	if link == nil {
		return response.Status(http.StatusNotFound)
	}
	return response.JSON(http.StatusOK, link)
}

func (s *Server) crudPost(req *http.Request) *response.Response {
	auth := authFrom(req.Context())

	var payload struct {
		Listed bool
		Public bool
		Short  string
		URL    string
	}

	if req.Header.Get(contentType) != applicationJSON {
		return response.Text(http.StatusBadRequest, "expecting "+applicationJSON)
	}
	if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
		return response.Error(http.StatusBadRequest, errors.Wrap(err, "unable to decode payload"))
	}

	if payload.Short == "" {
		payload.Listed = false
		payload.Short = hash.Hash(fmt.Sprintf("%s-%d", payload.URL, mathrand.Int()))
	}

	ctx := req.Context()
	if l, err := s.store.Publish(ctx, &db.Link{
		Author: auth,
		Listed: payload.Listed,
		Public: payload.Public,
		Short:  payload.Short,
		URL:    payload.URL,
	}); err == nil {
		return response.JSON(http.StatusCreated, l)
	} else if test := db.ValidationError(""); errors.As(err, &test) {
		return response.Error(http.StatusBadRequest, test)
	} else if test := db.ErrShortConflict; errors.As(err, &test) {
		return response.Error(http.StatusBadRequest, test)
	} else {
		return response.Error(http.StatusInternalServerError, errors.Wrap(err, "unable to store data"))
	}
}

func (s *Server) healthz(req *http.Request) *response.Response {
	if req.URL.Query().Get("ready") != "" {
		if err := s.store.Ping(req.Context()); err != nil {
			log.Printf("failed health check: %v", err)
			return response.Status(http.StatusInternalServerError)
		}
	}

	return response.Status(http.StatusOK)
}

// public serves only public links at the /p/ prefix.
func (s *Server) public(req *http.Request) *response.Response {
	l, err := s.store.Get(req.Context(), req.URL.Path[3:])
	if err != nil {
		return response.Error(http.StatusInternalServerError, errors.Wrap(err, "get"))
	}

	if l == nil || !l.Public {
		l = &db.Link{URL: s.fallback}
	} else {
		// Don't hold up caller for our metrics.
		go func() {
			if err := s.store.Click(context.Background(), l.Short); err != nil {
				log.Printf("dropped click for %s: %v", l.Short, err)
			}
		}()
	}

	return response.Redirect(http.StatusTemporaryRedirect, l.URL)
}

func (s *Server) publish(req *http.Request) *response.Response {
	auth := authFrom(req.Context())
	if req.Method != "POST" {
		return response.Status(http.StatusMethodNotAllowed)
	}

	if err := req.ParseForm(); err != nil {
		return response.Error(http.StatusBadRequest, errors.Wrap(err, "unable to decode form"))
	}

	originalShort := req.PostForm.Get("OriginalShort")
	link := &db.Link{
		Author: auth,
		Listed: req.PostForm.Get("Listed") == "true",
		Public: req.PostForm.Get("Public") == "true",
		Short:  req.PostForm.Get("Short"),
		URL:    req.PostForm.Get("URL"),
	}

	if link.Short == "" {
		link.Listed = false
		link.Short = hash.Hash(fmt.Sprintf("%s-%d", link.URL, mathrand.Int()))
	}

	ctx, tx, err := s.store.WithTransaction(req.Context())
	if err != nil {
		return response.Error(http.StatusInternalServerError, err)
	}
	// Rollback is a no-op if already committed.
	defer tx.Rollback()

	if originalShort != link.Short {
		if err := s.store.Delete(ctx, originalShort, auth); err != nil {
			return response.Error(http.StatusInternalServerError, err)
		}
	}

	if _, err := s.store.Publish(ctx, link); err == nil {
		if err := tx.Commit(); err != nil {
			return response.Error(http.StatusInternalServerError, err)
		}
		return response.Redirect(http.StatusTemporaryRedirect, "/")
	} else if test := db.ValidationError(""); errors.As(err, &test) {
		return response.Error(http.StatusBadRequest, test)
	} else if test := db.ErrShortConflict; errors.As(err, &test) {
		return response.Error(http.StatusBadRequest, test)
	} else {
		return response.Error(http.StatusInternalServerError, errors.Wrap(err, "unable to store data"))
	}
}

func (s *Server) edit(req *http.Request) *response.Response {
	ctx := req.Context()
	short := path.Base(req.URL.Path)

	tmpl, err := s.template("edit.html")
	if err != nil {
		return response.Error(http.StatusInternalServerError, err)
	}

	link, err := s.store.Get(ctx, short)
	if err != nil {
		return response.Error(http.StatusInternalServerError, err)
	}
	if link == nil {
		return response.Status(http.StatusNotFound)
	}

	data := &templateData{
		Ctx:   ctx,
		Link:  link,
		Store: s.store,
		User:  authFrom(ctx),
	}

	return response.Func(func(w http.ResponseWriter) error {
		w.Header().Set(contentType, textHTML)
		return tmpl.Execute(w, data)
	})
}

func (s *Server) root(req *http.Request) *response.Response {
	if req.URL.Path == "/" {
		ctx := req.Context()
		tmpl, err := s.template("landing.html")
		if err != nil {
			return response.Error(http.StatusInternalServerError, err)
		}

		data := &templateData{
			Ctx:   ctx,
			Store: s.store,
			User:  authFrom(ctx),
		}

		return response.Func(func(w http.ResponseWriter) error {
			w.Header().Set(contentType, textHTML)
			return tmpl.Execute(w, data)
		})
	}

	l, err := s.store.Get(req.Context(), req.URL.Path[1:])
	if err != nil {
		return response.Error(http.StatusInternalServerError, errors.Wrap(err, "get"))
	}

	if l == nil {
		l = &db.Link{URL: s.fallback}
	} else {
		// Don't hold up caller for our metrics.
		go func() {
			if err := s.store.Click(context.Background(), l.Short); err != nil {
				log.Printf("dropped click for %s: %v", l.Short, err)
			}
		}()
	}

	// Use a 307 here to allow request forwarding.
	return response.Redirect(http.StatusTemporaryRedirect, l.URL)
}
