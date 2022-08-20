/*
Copyright 2022 Adolfo García Veytia

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
	"context"
	"fmt"
	"io"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"cloud.google.com/go/storage"
	"google.golang.org/api/iterator"

	"github.com/puerco/tejolote/pkg/store/snapshot"
	"github.com/sirupsen/logrus"
)

func NewGCS(specURL string) (*GCS, error) {
	u, err := url.Parse(specURL)
	if err != nil {
		return nil, fmt.Errorf("parsing SpecURL %s: %w", specURL, err)
	}

	ctx := context.Background()
	client, err := storage.NewClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("creating storage client: %w", err)
	}

	tmpdir, err := os.MkdirTemp("", "tejolote-gcs")
	if err != nil {
		return nil, fmt.Errorf("creating temporary directory")
	}
	logrus.Infof("GCS driver init: Bucket: %s Path: %s", u.Hostname(), u.Path)
	return &GCS{
		Bucket:  u.Hostname(),
		Path:    u.Path,
		WorkDir: tmpdir,
		client:  client,
	}, nil
}

type GCS struct {
	Bucket  string
	Path    string
	WorkDir string
	client  *storage.Client
}

// syncGCSPrefix synchs a prefix in the bucket (a directory) and
// calls itself recursively for internal prefixes
func (gcs *GCS) syncGCSPrefix(ctx context.Context, prefix string, seen map[string]struct{}) error {
	logrus.Infof("Synching prefix %s", prefix)
	it := gcs.client.Bucket(gcs.Bucket).Objects(ctx, &storage.Query{
		Delimiter: "/",
		Prefix:    strings.TrimPrefix(prefix, "/"),
	})
	seen[prefix] = struct{}{}
	filesToSync := []string{}
	for {
		attrs, err := it.Next()
		if err == iterator.Done {
			logrus.Infof("Done listing %s", gcs.Bucket)
			break
		}
		if err != nil {
			log.Fatal(err)
		}

		// If name is empty, then it is a new prefix, lets index it:
		if _, ok := seen[attrs.Prefix]; !ok && attrs.Name == "" {
			gcs.syncGCSPrefix(ctx, attrs.Prefix, seen)
			continue
		}

		// The other is the marker file
		// If name is empty, then it is a new prefix, lets index it:
		if strings.HasSuffix(attrs.Name, "/") {
			trimmed := strings.TrimSuffix(attrs.Name, "/")
			if _, ok := seen[trimmed]; !ok {
				gcs.syncGCSPrefix(ctx, trimmed, seen)
				continue
			}
		}

		// GCS marks "directories" by creating a zero length text file.
		// If we did not catch it before as a directory, then
		// we need to skip these or the fs sync will not work. It may
		// be worth saving these and synching them if there is not a
		// directory with the same name.
		if attrs.Name != "" && attrs.Size > 0 && attrs.ContentType == "text/plain" {
			continue
		}

		// If there is a name, it is a file
		if attrs.Name != "" {
			logrus.Infof("%+v", attrs)
			// TODO: Check file md5 to see if it needs sync
			filesToSync = append(filesToSync, attrs.Prefix+attrs.Name)
		}
	}

	// TODO: Paralellize copies here
	for _, filename := range filesToSync {
		if err := gcs.syncGSFile(ctx, filename); err != nil {
			return fmt.Errorf("synching file: %w", err)
		}
	}
	return nil
}

// syncGSFile copies a file from the bucket to local workdir
func (gcs *GCS) syncGSFile(ctx context.Context, filePath string) error {
	logrus.Infof("Copying file from bucket: %s", filePath)
	localpath := filepath.Join(gcs.WorkDir, filePath)
	// Ensure the directory exists
	os.MkdirAll(filepath.Dir(localpath), os.FileMode(0o755))

	// Open the local file
	f, err := os.OpenFile(localpath, os.O_RDWR|os.O_CREATE, 0644)
	if err != nil {
		return fmt.Errorf("opening localfile: %w", err)
	}
	defer f.Close()

	// Create the reader to copy data
	rc, err := gcs.client.Bucket(gcs.Bucket).Object(filePath).NewReader(ctx)
	if err != nil {
		return fmt.Errorf("creating bucket reader: %w", err)
	}

	// Copy the file
	if _, err := io.Copy(f, rc); err != nil {
		return fmt.Errorf("copying data: %w", err)
	}
	if err := rc.Close(); err != nil {
		return fmt.Errorf("closing bucket reader")
	}
	return nil
}

// Snap takes a snapshot of the directory
func (gcs *GCS) Snap() (*snapshot.Snapshot, error) {
	if gcs.Path == "" {
		return nil, fmt.Errorf("gcs store has no path defined")
	}

	if gcs.Bucket == "" {
		return nil, fmt.Errorf("gcs store has no bucket defined")
	}

	if err := gcs.syncGCSPrefix(
		context.Background(), strings.TrimPrefix(gcs.Path, "/"), map[string]struct{}{},
	); err != nil {
		return nil, fmt.Errorf("synching bucket: %w", err)
	}

	// To snapshot the directory, we reuse the directory
	// store and use its artifacts
	dir, err := NewDirectory(fmt.Sprintf("file://%s", gcs.WorkDir))
	if err != nil {
		return nil, fmt.Errorf("creating temp directory store: %w", err)
	}
	snapDir, err := dir.Snap()
	if err != nil {
		return nil, fmt.Errorf("snapshotting work directory: %w", err)
	}
	snap := snapshot.Snapshot{}

	for _, a := range *snapDir {
		path := "gs://" + filepath.Join(gcs.Bucket, strings.TrimPrefix(a.Path, gcs.WorkDir))
		a.Path = path
		// Perhaps we should null the artifact dates
		snap[path] = a
	}
	logrus.Infof("%+v", snap)
	return &snap, nil
}