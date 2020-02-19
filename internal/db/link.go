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

package db

import (
	"net/url"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// Link is a shortened link.
type Link struct {
	Author    string    // Only the owner can update
	CreatedAt time.Time // Creation time
	Count     int       // Number of times clicked
	Listed    bool      // Appears on the front page
	Public    bool      // Accessible from outside
	Short     string    // The unique short-link value
	UpdatedAt time.Time // Last-updated time
	URL       string    // A well-formed URL
}

// Validate returns nil if the link is well-formed for storage.
func (l *Link) Validate() error {
	if l.Author == "" {
		return ValidationError("no Author")
	}
	if l.CreatedAt.IsZero() {
		return ValidationError("no CreatedAt")
	}
	if len(l.Short) < 3 {
		return ValidationError("no Short or too short")
	}
	for test := l.Short; test != ""; {
		r, s := utf8.DecodeRuneInString(test)
		switch {
		case r == '-',
			unicode.IsNumber(r),
			unicode.IsLetter(r) && unicode.IsLower(r):
			// OK.
		default:
			return ValidationError("the Short field must be lowercase letters or numbers")
		}
		test = test[s:]
	}
	if l.UpdatedAt.IsZero() {
		return ValidationError("no UpdatedAt")
	}
	if l.URL == "" {
		return ValidationError("no URL")
	}
	if _, err := url.Parse(l.URL); err != nil {
		return ValidationError("bad URL: " + err.Error())
	}
	return nil
}

// ValidationError is returned from Link.Validate().
type ValidationError string

func (v ValidationError) Error() string {
	return string(v)
}

// normalize a possibly mixed-case string into its canonical form.
func normalize(short string) string {
	return strings.ToLower(short)
}
