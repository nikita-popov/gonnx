package source_test

import (
	"testing"

	"github.com/nikita-popov/gonnx/internal/source"
)

func TestParseRef_Full(t *testing.T) {
	r, err := source.ParseRef(
		"git+https://github.com/example/repo.git?ref=v1.0&dir=models/resnet",
		"", "",
	)
	if err != nil {
		t.Fatal(err)
	}
	if r.RepoURL != "https://github.com/example/repo.git" {
		t.Errorf("RepoURL = %q", r.RepoURL)
	}
	if r.Ref != "v1.0" {
		t.Errorf("Ref = %q", r.Ref)
	}
	if r.Subdir != "models/resnet" {
		t.Errorf("Subdir = %q", r.Subdir)
	}
}

func TestParseRef_Defaults(t *testing.T) {
	r, err := source.ParseRef("https://github.com/example/repo.git", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if r.Ref != "master" {
		t.Errorf("default ref should be master, got %q", r.Ref)
	}
	if r.Subdir != "" {
		t.Errorf("subdir should be empty, got %q", r.Subdir)
	}
}

func TestParseRef_OverridesWin(t *testing.T) {
	r, err := source.ParseRef(
		"https://github.com/example/repo.git?ref=main&dir=old",
		"feature/x", "new/dir",
	)
	if err != nil {
		t.Fatal(err)
	}
	if r.Ref != "feature/x" {
		t.Errorf("Ref = %q, want feature/x", r.Ref)
	}
	if r.Subdir != "new/dir" {
		t.Errorf("Subdir = %q, want new/dir", r.Subdir)
	}
}

func TestParseRef_SSHScheme(t *testing.T) {
	r, err := source.ParseRef("git+ssh://git@github.com/example/repo.git", "main", "")
	if err != nil {
		t.Fatal(err)
	}
	if r.RepoURL != "ssh://git@github.com/example/repo.git" {
		t.Errorf("RepoURL = %q", r.RepoURL)
	}
}

func TestParseRef_MissingHost(t *testing.T) {
	_, err := source.ParseRef("not-a-url", "", "")
	if err == nil {
		t.Fatal("expected error for missing host")
	}
}

func TestRef_String(t *testing.T) {
	r := &source.Ref{
		RepoURL: "https://github.com/example/repo.git",
		Ref:     "master",
		Subdir:  "models/resnet50",
	}
	got := r.String()
	want := "https://github.com/example/repo.git?ref=master&dir=models/resnet50"
	if got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
}
