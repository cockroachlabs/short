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

// Package test contains a utility function for starting a single-node
// CockroachDB cluster.
package test

import (
	"context"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/pkg/errors"
)

// StartDB executes a cockroach binary from $PATH and returns a
// connection string that uses a unix domain socket. The server will be
// terminated when the context is cancelled.
func StartDB(ctx context.Context) (string, <-chan struct{}, error) {
	dir, err := ioutil.TempDir("", "server")
	if err != nil {
		return "", nil, err
	}
	log.Printf("socket and cockroach logs in %s", dir)
	sock := filepath.Join(dir, ".s.PGSQL.13013")

	cmd := exec.CommandContext(ctx, "cockroach", "start",
		"--http-addr=127.0.0.1:0",
		"--insecure",
		"--listen-addr=127.0.0.1:0",
		"--socket="+sock,
		"--store=type=mem,size=1GiB",
	)

	// Put output somewhere useful.
	stdout, err := os.OpenFile(filepath.Join(dir, "cockroach-stdout"), os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return "", nil, err
	}
	cmd.Stdout = stdout

	stderr, err := os.OpenFile(filepath.Join(dir, "cockroach-stderr"), os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return "", nil, err
	}
	cmd.Stderr = stderr

	// Fire up the binary.
	if err := cmd.Start(); err != nil {
		return "", nil, errors.Wrap(err, "cockroach: ")
	}

	// Provide a signal when the process has shut down.
	stop := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(stop)
	}()

	// Wait for cockroach to set up the socket.
	for {
		if _, err := os.Stat(sock); err == nil {
			break
		} else if !os.IsNotExist(err) {
			return "", stop, err
		}
		select {
		case <-ctx.Done():
			return "", stop, ctx.Err()
		case <-time.NewTimer(1 * time.Second).C:
		}
	}

	return fmt.Sprintf("user=root host=%s port=13013 sslmode=disable", dir), stop, err
}
