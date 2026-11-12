package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestPairedCIIsManualAndRequiredForRelease(t *testing.T) {
	type step struct {
		ID  string `yaml:"id"`
		Run string `yaml:"run"`
	}
	type job struct {
		Name  string `yaml:"name"`
		If    string `yaml:"if"`
		Steps []step `yaml:"steps"`
	}
	readJobs := func(name string) map[string]job {
		t.Helper()
		data, err := os.ReadFile(filepath.Join(".github", "workflows", name+".yml"))
		if err != nil {
			t.Fatal(err)
		}
		var workflow struct {
			Jobs map[string]job `yaml:"jobs"`
		}
		if err := yaml.Unmarshal(data, &workflow); err != nil {
			t.Fatal(err)
		}
		return workflow.Jobs
	}

	ci := readJobs("ci")
	paired := ci["paired-contract"]
	if paired.If != "github.event_name == 'workflow_dispatch'" {
		t.Errorf("paired job condition = %q, want manual dispatch", paired.If)
	}
	if !slices.ContainsFunc(paired.Steps, func(candidate step) bool {
		return strings.TrimSpace(candidate.Run) == "make check-paired"
	}) {
		t.Error("paired job must run the local check-paired target")
	}

	release := readJobs("release")
	var sourceCheck string
	for _, step := range release["build"].Steps {
		if step.ID == "source" {
			sourceCheck = step.Run
			break
		}
	}
	for _, required := range []string{
		"release_gate.py source",
		"--ci-event workflow_dispatch",
		"--required-job '" + ci["build"].Name + "'",
		"--required-job '" + paired.Name + "'",
	} {
		if !strings.Contains(sourceCheck, required) {
			t.Errorf("release source check is missing %q", required)
		}
	}
}
