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
	"bytes"
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

	"github.com/cockroachlabs/short/internal/assets"
	"github.com/cockroachlabs/short/internal/db"
	"github.com/cockroachlabs/short/internal/hash"
	"github.com/pkg/errors"
	"github.com/spf13/pflag"
)

// Server handles the HTTP API and the UI.
type Server struct {
	bind      string
	conn      string
	fallback  string
	forceUser string
	store     *db.Store
}

// New constructs a Server that will be configured by the flag set.
func New(flags *pflag.FlagSet) *Server {
	s := &Server{}
	flags.StringVarP(&s.bind, "bind", "b", ":443", "the address to bind to")
	flags.StringVarP(&s.conn, "conn", "c", "", "the database connection string")
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

	mux := http.NewServeMux()
	mux.HandleFunc("/_/asset/", s.asset)
	mux.HandleFunc("/_/v1/publish", s.publish)
	mux.HandleFunc("/healthz", s.healthz)
	mux.HandleFunc("/p/", s.public)
	mux.HandleFunc("/", s.root)

	server := http.Server{
		Addr:    s.bind,
		Handler: mux,
		TLSConfig: &tls.Config{
			Certificates: []tls.Certificate{{
				Certificate: [][]byte{certBytes},
				PrivateKey:  priv,
			}},
		},
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

	if err := server.ListenAndServeTLS("" /* certfile */, "" /* keyfile */); err != nil && err != http.ErrServerClosed {
		return errors.Wrap(err, "failed to start server")
	}

	return nil
}

// asset serves static asset data (images, js, etc.)
func (s *Server) asset(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeStatus(w, http.StatusMethodNotAllowed)
		return
	}
	path := r.URL.Path[9:]
	if asset, ok := assets.Assets[path]; ok {
		// Use pre-computed content type.
		r.Header.Set(contentType, asset.ContentType)
		http.ServeContent(w, r, path, asset.MTime, bytes.NewReader(asset.Data))
	} else {
		writeStatus(w, http.StatusNotFound)
	}
}

// checkAuth ensures that the given request has IAP JWT data. Note that
// this function does not actually validate the token, we assume
// that the only direct access to this service is via IAP.
func (s *Server) checkAuth(w http.ResponseWriter, r *http.Request) (string, bool) {
	if email, ok := s.extractEmail(r); ok {
		return email, true
	}
	writeStatus(w, http.StatusForbidden)
	return "", false
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

func (s *Server) healthz(w http.ResponseWriter, req *http.Request) {
	if req.URL.Query().Get("ready") != "" {
		if err := s.store.Ping(req.Context()); err != nil {
			log.Printf("failed health check: %v", err)
			writeStatus(w, http.StatusInternalServerError)
			return
		}
	}

	writeStatus(w, http.StatusOK)
}

// public serves only public links at the /p/ prefix.
func (s *Server) public(w http.ResponseWriter, req *http.Request) {
	l, err := s.store.Get(req.Context(), req.URL.Path[3:])
	if err != nil {
		writeError(w, http.StatusInternalServerError, errors.Wrap(err, "get"))
		return
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

	writeRedirect(w, http.StatusTemporaryRedirect, l.URL)
}

func (s *Server) publish(w http.ResponseWriter, req *http.Request) {
	auth, ok := s.checkAuth(w, req)
	if !ok {
		return
	}
	if req.Method != "POST" {
		w.WriteHeader(http.StatusMethodNotAllowed)
	}

	var payload struct {
		Public bool
		Short  string
		URL    string
	}
	redirect := false

	if req.Header.Get(contentType) == "application/json" {
		if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
			writeError(w, http.StatusBadRequest, errors.Wrap(err, "unable to decode payload"))
			return
		}
	} else {
		if err := req.ParseForm(); err != nil {
			writeError(w, http.StatusBadRequest, errors.Wrap(err, "unable to decode form"))
			return
		}
		payload.Public = req.PostForm.Get("Public") == "true"
		payload.Short = req.PostForm.Get("Short")
		payload.URL = req.PostForm.Get("URL")
		redirect = true
	}

	if payload.Short == "" {
		payload.Short = hash.Hash(fmt.Sprintf("%s-%d", payload.URL, mathrand.Int()))
	}

	ctx := req.Context()
	if l, err := s.store.Publish(ctx, &db.Link{
		Author: auth,
		Public: payload.Public,
		Short:  payload.Short,
		URL:    payload.URL,
	}); err == nil {
		if redirect {
			writeRedirect(w, http.StatusFound, "/")
		} else {
			writeJSON(w, http.StatusOK, l)
		}
	} else if _, ok := err.(db.ValidationError); ok {
		writeError(w, http.StatusBadRequest, err)
	} else if err == db.ErrShortConflict {
		writeError(w, http.StatusBadRequest, err)
	} else {
		writeError(w, http.StatusInternalServerError, errors.Wrap(err, "unable to store data"))
	}
}

func (s *Server) root(w http.ResponseWriter, req *http.Request) {
	if _, ok := s.checkAuth(w, req); !ok {
		return
	}

	if req.URL.Path == "/" {
		s.landingPage(w, req)
		return
	}

	l, err := s.store.Get(req.Context(), req.URL.Path[1:])
	if err != nil {
		writeError(w, http.StatusInternalServerError, errors.Wrap(err, "get"))
		return
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
	writeRedirect(w, http.StatusTemporaryRedirect, l.URL)
}
