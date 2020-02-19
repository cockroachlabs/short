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

// Package minify contains a pre-configured minifier for optimizing
// resources.
package minify

import (
	"bytes"
	"image/png"
	"io"
	"io/ioutil"
	"regexp"

	"github.com/tdewolff/minify"
	"github.com/tdewolff/minify/css"
	"github.com/tdewolff/minify/html"
	"github.com/tdewolff/minify/js"
	"github.com/tdewolff/minify/json"
	"github.com/tdewolff/minify/svg"
	"github.com/tdewolff/minify/xml"
)

// M is pre-configured.
var M *minify.M

func init() {
	M = minify.New()
	M.AddFunc("text/css", css.Minify)
	M.AddFunc("text/html", html.Minify)
	M.AddFunc("image/png", pngMinify)
	M.AddFunc("image/svg+xml", svg.Minify)
	M.AddFuncRegexp(regexp.MustCompile("^(application|text)/(x-)?(java|ecma)script$"), js.Minify)
	M.AddFuncRegexp(regexp.MustCompile("[/+]json$"), json.Minify)
	M.AddFuncRegexp(regexp.MustCompile("[/+]xml$"), xml.Minify)
}

func pngMinify(_ *minify.M, writer io.Writer, reader io.Reader, _ map[string]string) error {
	inBytes, err := ioutil.ReadAll(reader)
	if err != nil {
		return err
	}
	img, err := png.Decode(bytes.NewReader(inBytes))
	if err != nil {
		return err
	}

	outBytes := &bytes.Buffer{}
	err = (&png.Encoder{CompressionLevel: png.BestCompression}).Encode(outBytes, img)
	if err != nil {
		return err
	}
	if outBytes.Len() < len(inBytes) {
		_, err = io.Copy(writer, outBytes)
	} else {
		_, err = writer.Write(inBytes)
	}
	return err
}
