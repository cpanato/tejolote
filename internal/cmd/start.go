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

package cmd

import (
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"chainguard.dev/apko/pkg/vcs"
	slsa "github.com/in-toto/in-toto-golang/in_toto/slsa_provenance/v0.2"
	"github.com/sirupsen/logrus"
	"sigs.k8s.io/release-utils/util"

	"github.com/puerco/tejolote/pkg/attestation"
	"github.com/puerco/tejolote/pkg/watcher"
	"github.com/spf13/cobra"
)

type startAttestationOptions struct {
	clone     bool
	repo      string
	repoPath  string
	pubsub    string
	vcsURL    string
	artifacts []string
}

func (opts startAttestationOptions) Validate() error {
	if opts.clone && opts.repo == "" {
		return errors.New("repository clone requested but no repository was specified")
	}

	if opts.clone && opts.repoPath == "" {
		return errors.New("repository clone requested but no repository path was specified")
	}
	return nil
}

func addStart(parentCmd *cobra.Command) {
	startAttestationOpts := startAttestationOptions{}
	var outputOps *outputOptions

	// Verb
	startCmd := &cobra.Command{
		Short:             "Start a partial document",
		Use:               "start",
		SilenceUsage:      false,
		PersistentPreRunE: initLogging,
	}

	// Noun
	startAttestationCmd := &cobra.Command{
		Short: "Attest to a build system run",
		Long: `tejolote start attestation
	
The start command of tejolte writes a partial attestation 
containing initial data that can be observed before launching a
build. The partial attestation is meant to be completed by
tejolote once it has finished observing a build run.

Whe starting an attestation, tejolote will snapshot the artifact
storage locations and retake them when finishing building the
provenance metadata. This allows it to "remember" the storage
states to notice new artifacts. By default tejolote will store the
storage state in a file with the same name as the partial
attestation but with ".storage-snap.json" appended.
	
	`,
		Use:               "attestation",
		SilenceUsage:      false,
		PersistentPreRunE: initLogging,
		RunE: func(cmd *cobra.Command, args []string) (err error) {
			if err := startAttestationOpts.Validate(); err != nil {
				return fmt.Errorf("validating options: %w", err)
			}

			if len(args) == 0 {
				return errors.New("build run spec URL not specified")
			}

			w, err := watcher.New(args[0])
			if err != nil {
				return fmt.Errorf("building watcher")
			}

			// Add artifact monitors to the watcher
			for _, uri := range startAttestationOpts.artifacts {
				if err := w.AddArtifactSource(uri); err != nil {
					return fmt.Errorf("adding artifacts source: %w", err)
				}
			}

			if err := w.Snap(); err != nil {
				return fmt.Errorf("snapshotting the artifact repositories: %w", err)
			}

			if outputOps.FinalSnapshotStatePath(outputOps.OutputPath) == "" {
				if len(w.Snapshots) > 0 {
					logrus.Warning("Not saving storage state but artifact sources defined")
				}
			} else {
				if err := w.SaveSnapshots(outputOps.FinalSnapshotStatePath(outputOps.OutputPath)); err != nil {
					return fmt.Errorf("saving storage snapshots: %w", err)
				}
			}

			att := attestation.New()
			predicate := attestation.NewSLSAPredicate()

			if startAttestationOpts.clone {
				// TODO: Implement
				return fmt.Errorf("repository cloning not yet implemented")
			}

			vcsURL := startAttestationOpts.vcsURL
			if vcsURL == "" {
				vcsURL, err = readVCSURL(*outputOps, startAttestationOpts)
				if err != nil {
					return fmt.Errorf("fetching VCS URL: %w", err)
				}
			}

			if vcsURL != "" {
				material := slsa.ProvenanceMaterial{
					URI:    vcsURL,
					Digest: map[string]string{},
				}
				if repoURL, repoDigest, ok := strings.Cut(vcsURL, "@"); ok {
					material.URI = repoURL
					material.Digest["sha1"] = repoDigest
				}
				predicate.Materials = append(predicate.Materials, material)
			}

			att.Predicate = predicate

			json, err := att.ToJSON()
			if err != nil {
				return fmt.Errorf("serializing attestation json: %w", err)
			}

			if outputOps.OutputPath == "" {
				fmt.Println(string(json))
			} else {
				os.WriteFile(outputOps.OutputPath, json, os.FileMode(0o644))
			}

			if startAttestationOpts.pubsub != "" {
				var sdata []byte
				if util.Exists(outputOps.FinalSnapshotStatePath(outputOps.OutputPath)) {
					sdata, err = os.ReadFile(outputOps.FinalSnapshotStatePath(outputOps.OutputPath))
					if err != nil {
						return fmt.Errorf("reading snapshot data: %w", err)
					}
				}
				message := watcher.StartMessage{
					SpecURL:      w.Builder.SpecURL,
					Attestation:  base64.StdEncoding.EncodeToString([]byte(json)),
					Artifacts:    startAttestationOpts.artifacts,
					ArtifactList: strings.Join(startAttestationOpts.artifacts, ","),
				}
				if sdata != nil {
					message.Snapshots = base64.StdEncoding.EncodeToString(sdata)
				}

				if err := w.PublishToTopic(startAttestationOpts.pubsub, message); err != nil {
					return fmt.Errorf("publishing message to pubsub topic: %w", err)
				}
			}

			return nil
		},
	}

	outputOps = addOutputFlags(startAttestationCmd)

	startAttestationCmd.PersistentFlags().StringVar(
		&startAttestationOpts.repo,
		"repository",
		"",
		"url of repository containing the main project source",
	)

	startAttestationCmd.PersistentFlags().StringVar(
		&startAttestationOpts.repoPath,
		"repo-path",
		".",
		"path to the main code repository (relative to workspace)",
	)

	startAttestationCmd.PersistentFlags().BoolVar(
		&startAttestationOpts.clone,
		"clone",
		false,
		"clone the repository",
	)

	startAttestationCmd.PersistentFlags().StringSliceVar(
		&startAttestationOpts.artifacts,
		"artifacts",
		[]string{},
		"artifact storage locations",
	)

	startAttestationCmd.PersistentFlags().StringVar(
		&startAttestationOpts.pubsub,
		"pubsub",
		"",
		"publish event to a pubsub topic",
	)

	startAttestationCmd.PersistentFlags().StringVar(
		&startAttestationOpts.vcsURL,
		"vcs-url",
		"",
		"VCS locator to add to SLSA materials (if emtpy will be probed)",
	)

	startCmd.AddCommand(startAttestationCmd)
	parentCmd.AddCommand(startCmd)
}

// readVCSURL checks the repository path to get the VCS url for the
// materials
func readVCSURL(outputOpts outputOptions, opts startAttestationOptions) (string, error) {
	if opts.repoPath == "" {
		return "", nil
	}

	repoPath := opts.repoPath

	// If its a relative URL, append the workspace
	if !strings.HasPrefix(opts.repoPath, string(filepath.Separator)) {
		repoPath = filepath.Join(outputOpts.Workspace, opts.repoPath)
	}

	repoPath, err := filepath.Abs(repoPath)
	if err != nil {
		return "", fmt.Errorf("resolving absolute path to repo: %w", err)
	}

	urlString, err := vcs.ProbeDirForVCSUrl(repoPath, repoPath)
	if err != nil {
		return "", fmt.Errorf("probing VCS URL: %w", err)
	}
	return urlString, nil
}
