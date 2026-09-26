package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// pluginSpec mirrors only the fields of plugins/fanisi.spec.json this test
// checks. The generator's own schema validation lives with the generator.
type pluginSpec struct {
	CLI struct {
		Executable   string `json:"executable"`
		VersionProbe struct {
			Argv          []string `json:"argv"`
			RequireOutput string   `json:"require_output"`
		} `json:"version_probe"`
	} `json:"cli"`
	Tools []struct {
		Name   string   `json:"name"`
		Argv   []string `json:"argv"`
		Inputs []struct {
			Name     string   `json:"name"`
			Type     string   `json:"type"`
			Flag     string   `json:"flag"`
			Required bool     `json:"required"`
			Enum     []string `json:"enum"`
		} `json:"inputs"`
		Effects struct {
			ReadOnly    bool   `json:"read_only"`
			Destructive bool   `json:"destructive"`
			Network     bool   `json:"network"`
			Cost        string `json:"cost"`
		} `json:"effects"`
	} `json:"tools"`
}

func loadPluginSpec(t *testing.T) pluginSpec {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("plugins", "fanisi.spec.json"))
	if err != nil {
		t.Fatal(err)
	}
	var s pluginSpec
	dec := json.NewDecoder(bytes.NewReader(raw))
	if err := dec.Decode(&s); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestPluginSpecExposesOnlyReviewedComposeCommands(t *testing.T) {
	s := loadPluginSpec(t)
	if s.CLI.Executable != "fanisi" {
		t.Fatalf("executable = %q", s.CLI.Executable)
	}
	if strings.Join(s.CLI.VersionProbe.Argv, " ") != "compose --help" || !strings.Contains(composeUsageText, s.CLI.VersionProbe.RequireOutput) {
		t.Fatalf("version probe %q does not match compose usage", s.CLI.VersionProbe.RequireOutput)
	}
	want := []string{"catalog", "hash", "validate", "decide", "manifest", "dispatch", "status", "cancel"}
	var got []string
	for _, tool := range s.Tools {
		if len(tool.Argv) != 2 || tool.Argv[0] != "compose" || tool.Argv[1] != tool.Name {
			t.Fatalf("tool %s argv %q must be [compose %s]", tool.Name, tool.Argv, tool.Name)
		}
		got = append(got, tool.Name)
		for _, in := range tool.Inputs {
			if in.Flag == "" {
				t.Fatalf("tool %s input %s must be a flag", tool.Name, in.Name)
			}
			if in.Flag == "--command" {
				t.Fatalf("decide command backend must not be exposed")
			}
		}
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("tools = %v, want %v (no run, review or command backend)", got, want)
	}
	for _, tool := range s.Tools {
		if tool.Name == "dispatch" && (!tool.Effects.Destructive || !tool.Effects.Network || tool.Effects.Cost != "possible" || tool.Effects.ReadOnly) {
			t.Fatalf("dispatch effects understate delegate power: %+v", tool.Effects)
		}
	}
}

// TestPluginSpecFlagsAreAcceptedByCompose runs each declared tool through the
// real compose flag parser with every input set, using paths that do not
// exist. Any failure must come from loading those paths, never from an
// undefined flag.
func TestPluginSpecFlagsAreAcceptedByCompose(t *testing.T) {
	s := loadPluginSpec(t)
	for _, tool := range s.Tools {
		t.Run(tool.Name, func(t *testing.T) {
			missing := filepath.Join(t.TempDir(), "missing")
			args := []string{tool.Argv[1]}
			for _, in := range tool.Inputs {
				switch {
				case in.Type == "boolean":
					args = append(args, in.Flag)
				case len(in.Enum) > 0:
					args = append(args, in.Flag, in.Enum[0])
				default:
					args = append(args, in.Flag, filepath.Join(missing, in.Name))
				}
				if !strings.Contains(composeUsageLine(t, tool.Argv[1]), in.Flag) {
					t.Fatalf("compose %s usage does not document %s", tool.Argv[1], in.Flag)
				}
			}
			// status creates an empty journal layout and succeeds; every
			// other command fails on the missing paths.
			err := composeCommand(context.Background(), args)
			if err == nil && tool.Name != "status" {
				t.Fatalf("compose %v unexpectedly succeeded with missing paths", args)
			}
			if err != nil && (strings.Contains(err.Error(), "flag provided but not defined") || strings.Contains(err.Error(), "requires")) {
				t.Fatalf("compose %v rejected the declared flags: %v", args, err)
			}
		})
	}
}

func composeUsageLine(t *testing.T, sub string) string {
	t.Helper()
	for _, line := range strings.Split(composeUsageText, "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 3 && fields[0] == "fanisi" && fields[1] == "compose" && fields[2] == sub {
			return line
		}
	}
	t.Fatalf("no usage line for compose %s", sub)
	return ""
}
