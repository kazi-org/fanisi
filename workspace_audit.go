package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
)

type FileIdentity struct {
	SHA256     string `json:"sha256"`
	Type       string `json:"type"`
	Mode       uint32 `json:"mode"`
	LinkTarget string `json:"link_target,omitempty"`
}

type ControllerArtifact struct {
	FileIdentity
	Baseline *FileIdentity `json:"baseline"`
}

type ControllerArtifactManifest struct {
	SchemaVersion int                           `json:"schema_version"`
	WorkspaceBase string                        `json:"workspace_base"`
	Artifacts     map[string]ControllerArtifact `json:"artifacts"`
}

func fileIdentity(path string) (FileIdentity, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return FileIdentity{}, err
	}
	result := FileIdentity{Mode: uint32(info.Mode().Perm())}
	switch {
	case info.Mode().IsRegular():
		raw, err := os.ReadFile(path)
		if err != nil {
			return result, err
		}
		result.Type = "regular"
		result.SHA256 = digest(raw)
	case info.Mode()&os.ModeSymlink != 0:
		target, err := os.Readlink(path)
		if err != nil {
			return result, err
		}
		result.Type = "symlink"
		result.LinkTarget = target
		result.SHA256 = digest([]byte(target))
	default:
		return result, fmt.Errorf("unsupported workspace file type: %s", path)
	}
	return result, nil
}

func workspaceInventory(root string) (map[string]FileIdentity, error) {
	result := map[string]FileIdentity{}
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if relative == ".git" {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.IsDir() {
			return nil
		}
		identity, err := fileIdentity(path)
		if err != nil {
			return err
		}
		result[filepath.ToSlash(relative)] = identity
		return nil
	})
	return result, err
}

func auditWorkspace(ctx context.Context, cfg Config, base string, baseline map[string]FileIdentity, artifacts map[string]ControllerArtifact) error {
	current, err := workspaceInventory(cfg.Workspace)
	if err != nil {
		return err
	}
	allowed := map[string]bool{}
	for _, p := range cfg.WritePaths {
		allowed[p] = true
	}
	for p, before := range baseline {
		after, exists := current[p]
		if allowed[p] {
			if exists && after.Type != "regular" {
				return fmt.Errorf("candidate changed file type: %s", p)
			}
			continue
		}
		if artifact, ok := artifacts[p]; ok {
			if artifact.Baseline == nil || *artifact.Baseline != before || !exists || artifact.FileIdentity != after {
				return fmt.Errorf("controller artifact baseline mismatch: %s", p)
			}
			continue
		}
		if !exists || before != after {
			return fmt.Errorf("unscoped workspace change: %s", p)
		}
	}
	for p, after := range current {
		if _, exists := baseline[p]; exists {
			continue
		}
		if allowed[p] {
			if after.Type != "regular" {
				return fmt.Errorf("candidate symlink or special file: %s", p)
			}
			continue
		}
		if artifact, ok := artifacts[p]; ok && artifact.Baseline == nil && artifact.FileIdentity == after {
			continue
		}
		return fmt.Errorf("unexpected workspace file, including ignored files: %s", p)
	}
	for p, artifact := range artifacts {
		if got, ok := current[p]; !ok || got != artifact.FileIdentity {
			return fmt.Errorf("controller artifact drift: %s", p)
		}
	}
	head, err := gitOutput(ctx, cfg.Workspace, "rev-parse", "HEAD")
	if err != nil {
		return err
	}
	if strings.TrimSpace(string(head)) != base {
		return errors.New("workspace HEAD changed")
	}
	return nil
}

func equalArtifacts(want, got map[string]ControllerArtifact) error {
	if !reflect.DeepEqual(want, got) {
		return errors.New("controller artifact changed during worker execution")
	}
	return nil
}
