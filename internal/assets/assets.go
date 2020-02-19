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

// Package assets incorporates the given static assets into the compiled code.
package assets

import (
	"bytes"
	"io/ioutil"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/cockroachlabs/short/internal/minify"
)

//go:generate go run github.com/cockroachlabs/short/cmd/slurp -i .png -i .svg -i .html

// AssetPath is a developer flag to load asset data from disk instead of
// using a built-in map.
var AssetPath string

// An Asset represents a static file asset to serve to a client.
type Asset interface {
	http.File
	ContentType() string
}

// Get returns the requested asset, or os.ErrNotExist if it does not exist.
func Get(path string) (Asset, error) {
	if AssetPath != "" {
		file, err := os.Open(filepath.Join(AssetPath, path))
		if err != nil {
			return nil, err
		}
		stat, err := file.Stat()
		if err != nil {
			return nil, err
		}
		data, err := ioutil.ReadAll(file)
		if err != nil {
			return nil, err
		}

		typ := mime.TypeByExtension(filepath.Ext(path))
		if typ == "" {
			if _, err := file.Read(data); err != nil {
				return nil, err
			}
			typ = http.DetectContentType(data)
		}

		// We'll ignore any minification errors. The API is documented
		// to return the original data bytes if it didn't work.
		data, _ = minify.M.Bytes(typ, data)

		return &staticAsset{
			Reader:      bytes.NewReader(data),
			contentType: typ,
			name:        filepath.Base(path),
			mTime:       stat.ModTime(),
		}, nil
	}
	if found, ok := assets[path]; ok {
		return &staticAsset{
			Reader:      bytes.NewReader(found.Data),
			contentType: found.ContentType,
			name:        filepath.Base(path),
			mTime:       found.MTime,
		}, nil
	}
	return nil, os.ErrNotExist
}

type staticAsset struct {
	*bytes.Reader
	contentType string
	name        string
	mTime       time.Time
}

var (
	_ Asset       = &staticAsset{}
	_ os.FileInfo = &staticAsset{}
)

func (m *staticAsset) Close() error {
	m.Reader = nil
	return nil
}

func (m *staticAsset) IsDir() bool {
	return false
}

func (m *staticAsset) Mode() os.FileMode {
	return 0644
}

func (m *staticAsset) ModTime() time.Time {
	return m.mTime
}

func (m *staticAsset) Name() string {
	return m.name
}

func (m *staticAsset) Readdir(_ int) ([]os.FileInfo, error) {
	return nil, nil
}

func (m *staticAsset) Stat() (os.FileInfo, error) {
	return m, nil
}

func (m *staticAsset) ContentType() string {
	return m.contentType
}

func (m *staticAsset) Sys() interface{} {
	return m
}
