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

// Package response contains utility functions for creating HTTP responses.
package response

import (
	"encoding/csv"
	"encoding/json"
	"net/http"
	"time"

	"fmt"

	"github.com/cockroachlabs/short/internal/db"
)

const (
	applicationJSON    = "application/json"
	contentDisposition = "Content-Disposition"
	contentType        = "Content-Type"
	location           = "Location"
)

// A Response represents the content that will populate an HTTP response.
type Response struct {
	json     interface{}
	fn       func(w http.ResponseWriter) error
	redirect string
	status   int
}

// ClickReport generates a tab-separated-value file.
func ClickReport(filename string, report <-chan *db.ClickReport) *Response {
	return Func(func(w http.ResponseWriter) error {
		w.Header().Add(contentType, "text/tab-separated-values; charset=UTF-8")
		w.Header().Add(contentDisposition, fmt.Sprintf("attachment; filename=%q", filename+".tsv"))
		w.WriteHeader(http.StatusOK)

		out := csv.NewWriter(w)
		out.Comma = '\t'
		out.Write([]string{
			"link",
			"time",
			"uuid",
			"destination",
		})

		for r := range report {
			out.Write([]string{
				r.Short,
				r.Time.UTC().Format(time.RFC3339),
				r.UUID.String(),
				r.Destination,
			})
		}

		out.Flush()
		return out.Error()
	})
}

// Error constructs a JSON-formatted error message.
func Error(status int, err error) *Response {
	return JSON(status, struct{ Error string }{err.Error()})
}

// Func will execute an arbitrary callback to handle response
// construction. This is useful for streaming data responses.
func Func(fn func(w http.ResponseWriter) error) *Response {
	return &Response{
		fn: fn,
	}
}

// JSON returns the given object as a JSON blob.
func JSON(status int, json interface{}) *Response {
	return &Response{
		status: status,
		json:   json,
	}
}

// Redirect will send the given location to the client.
func Redirect(status int, location string) *Response {
	return &Response{
		status:   status,
		redirect: location,
	}
}

// Status returns a trivial response.
func Status(status int) *Response {
	return Text(status, http.StatusText(status))
}

// Text returns the message as a JSON-formatted blob.
func Text(status int, text string) *Response {
	return JSON(status, struct{ Message string }{text})
}

// Write sends the response via the writer.
func (r *Response) Write(w http.ResponseWriter) error {
	if r.fn != nil {
		return r.fn(w)
	}

	if r.redirect != "" {
		w.Header().Set(location, r.redirect)
	}

	status := r.status
	if status == 0 {
		status = http.StatusOK
	}

	if r.json == nil {
		w.WriteHeader(status)
		return nil
	}
	w.Header().Set(contentType, applicationJSON)
	w.WriteHeader(status)

	return json.NewEncoder(w).Encode(r.json)
}
