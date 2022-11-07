/*
Copyright 2022 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package driver

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"sigs.k8s.io/tejolote/pkg/run"
)

func TestDirectorySnap(t *testing.T) {
	// Create a fixed time to make the times deterministic
	fixedTime := time.Date(1976, time.Month(2), 10, 23, 30, 30, 0, time.Local)

	// Create some files in the directory
	for _, tc := range []struct {
		prepare func(path string) error
		mutate  func(path string) error
		expect  []run.Artifact
	}{
		// Two empty directories. No error, no change
		{
			func(path string) error { return nil },
			func(path string) error { return nil },
			[]run.Artifact{},
		},
		// One file, unchanged at mutation time
		{
			func(path string) error {
				return os.WriteFile(filepath.Join(path, "test.txt"), []byte("test"), os.FileMode(0o644))
			},
			func(path string) error { return nil },
			[]run.Artifact{},
		},
		// One file, rewritten should be reported
		{
			func(path string) error {
				return os.WriteFile(filepath.Join(path, "test.txt"), []byte("test"), os.FileMode(0o644))
			},
			func(path string) error {
				filePath := filepath.Join(path, "test.txt")
				if err := os.WriteFile(
					filePath, []byte("test"), os.FileMode(0o644),
				); err != nil {
					return err
				}
				if err := os.Chtimes(filePath, fixedTime, fixedTime); err != nil {
					return err
				}
				return nil
			},
			[]run.Artifact{
				{
					Path:     "test.txt",
					Time:     fixedTime,
					Checksum: map[string]string{"SHA256": "9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08"},
				},
			},
		},
		// One file, with contents changed
		{
			func(path string) error {
				filePath := filepath.Join(path, "test.txt")
				if err := os.WriteFile(
					filePath, []byte("test"), os.FileMode(0o644),
				); err != nil {
					return err
				}
				if err := os.Chtimes(filePath, fixedTime, fixedTime); err != nil {
					return err
				}
				return nil
			},
			func(path string) error {
				filePath := filepath.Join(path, "test.txt")
				if err := os.WriteFile(
					filePath, []byte("test, but with a change!"), os.FileMode(0o644),
				); err != nil {
					return err
				}
				if err := os.Chtimes(filePath, fixedTime, fixedTime); err != nil {
					return err
				}
				return nil
			},
			[]run.Artifact{
				{
					Path:     "test.txt",
					Time:     fixedTime,
					Checksum: map[string]string{"SHA256": "76aad9c1d52e424d0dd6c6b8e07169d5d5f9001a06fe5343d4bfa13c804788f0"},
				},
			},
		},
	} {
		// Create a temp directory to operate in
		dir, err := os.MkdirTemp("", "")
		require.NoError(t, err)
		defer os.RemoveAll(dir)

		// Create the directory watcher
		sut := Directory{
			Path: dir,
		}

		require.NoError(t, tc.prepare(dir))

		snap1, err := sut.Snap()
		require.NoError(t, err, "creating first snapshot")

		require.NoError(t, tc.mutate(dir))

		snap2, err := sut.Snap()
		require.NoError(t, err, "creating mutated fs snapshot")

		delta := snap1.Delta(snap2)
		require.Equal(t, delta, tc.expect)
	}
}
