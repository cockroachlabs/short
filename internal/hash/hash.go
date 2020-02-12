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

// Package hash contains utility routines for synthesizing short links.
package hash

import (
	"crypto/sha256"
	"encoding/base32"
	"fmt"
)

// Eliminate characters like l, 1, o, 0 that are easy to misread.
var encoder = base32.NewEncoding("abcdefghijkmnpqrstuvwxyz23456789")

// Hash creates a short, unique, and communicable value from the given
// input value.
func Hash(s string) string {
	h := sha256.New()
	h.Write([]byte(s))
	bytes := h.Sum(nil)

	s = encoder.EncodeToString(bytes)
	s = fmt.Sprintf("%s-%s-%s", s[0:4], s[4:8], s[8:12])
	return s
}
